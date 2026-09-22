// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package tee implements the tee builtin command.
//
// tee — read from standard input and write to standard output and files
//
// Usage: tee [OPTION]... [FILE]...
//
// Copy standard input to standard output, making a copy in each FILE
// operand. A FILE operand of "-" is opened as a literal file named "-"
// through the sandbox like any other operand — GNU tee treated "-" as an
// alias for stdout in coreutils 5.3.0 through 8.23, but that behavior was
// removed in 8.24 as mandated by POSIX, and this implementation matches
// current GNU tee rather than the historical behavior.
//
// tee mutates file content, so — like truncate, logrotate, rm, and
// systemctl — it is only available in remediation mode. This is enforced
// both at interpreter dispatch (RemediationOnly) and again inside the
// handler as defence in depth, per docs/RULES.md.
//
// Accepted flags:
//
//	-a, --append
//	    Append to the given FILEs, rather than overwriting them.
//
//	-h, --help
//	    Print usage to stdout and exit 0.
//
// Out of scope (not implemented; rejected as unknown flags):
//
//	-i, --ignore-interrupts   ignore SIGINT — rshell builtins do not
//	                          receive process signals the way a forked
//	                          tee(1) would, so this flag has no
//	                          meaningful effect to implement.
//	-p, --output-error[=MODE] behavior on write error / FIFO-aware
//	                          diagnostics — every FIFO write target is
//	                          already rejected by the sandbox (see
//	                          Special File Handling below), so the
//	                          "pipe error" modes GNU tee -p exists for
//	                          cannot occur here.
//
// File access:
//
//	Every FILE operand is opened via callCtx.OpenFile with O_WRONLY |
//	O_CREATE, plus O_APPEND (-a) or O_TRUNC (default). This routes through
//	the same AllowedPaths sandbox and remediation-mode gate that backs the
//	shell's own >/>> redirects and the truncate builtin: writes are
//	confined to :rw roots, a multiply linked (hard-linked) regular file is
//	rejected as a write target, and a non-regular target (FIFO, socket,
//	device) is rejected via an O_NONBLOCK-guarded open plus a post-open
//	fstat check — never blocking the shell waiting for a reader. See the
//	hard link and FIFO entries in AGENTS.md/docs/RULES.md for the shared
//	mechanism.
//
// Exit codes:
//
//	0  Standard input was fully copied to standard output and to every
//	   FILE operand without error.
//	1  Standard input could not be fully read, or at least one FILE
//	   operand could not be opened or written. tee continues copying to
//	   the remaining destinations after a single destination fails,
//	   matching GNU tee, so a failure on one FILE does not stop output to
//	   the others or to standard output.
//
// Memory safety:
//
//	Input is streamed from stdin in fixed-size chunks (teeBufSize, 32
//	KiB) and fanned out to standard output and every open FILE in the same
//	iteration — no buffering proportional to total input size. The read
//	loop checks ctx.Err() before every Read to honour the shell's
//	execution timeout and support graceful cancellation, and also returns
//	as soon as every destination (including standard output) has stopped
//	accepting writes, rather than draining a long-lived or infinite stdin
//	source to no purpose. The number of concurrently open file descriptors
//	is bounded by MaxFileOperands, checked before any FILE is opened, so a
//	glob or repeated operand cannot exhaust the embedding process's
//	descriptor table; a later duplicate name reuses its own file handle
//	rather than reopening (matching GNU tee's write-order and open-once
//	semantics).
package tee

import (
	"context"
	"errors"
	"io"
	iofs "io/fs"
	"os"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the tee builtin command descriptor.
var Cmd = builtins.Command{
	Name:            "tee",
	Description:     "read from standard input and write to standard output and files",
	MakeFlags:       registerFlags,
	RemediationOnly: true,
	// Preserve the historical read-only refusal wording; the dispatch gate
	// in interp emits this before flag parsing, and the in-handler check
	// repeats it as defence in depth.
	RemediationDeniedMessage: readOnlyMessage,
}

const readOnlyMessage = "tee: filesystem capability not available (remediation mode required)\n"

// teeBufSize is the fixed-size chunk used to stream stdin to stdout and
// every FILE destination. Bounded and independent of input size.
const teeBufSize = 32 * 1024

// MaxFileOperands is the maximum number of FILE operands accepted by a
// single tee invocation. Every accepted operand is opened and held open for
// the duration of the copy (see the package doc comment), so an unbounded
// operand count — easily reached through shell glob expansion — would open
// and hold an unbounded number of file descriptors, exhausting the
// embedding process's descriptor table and affecting unrelated concurrent
// work. Exceeding the limit rejects the entire command before any
// destination is opened, matching rm's MaxRemoveFiles precedent.
const MaxFileOperands = 1024

// dest groups a destination's writer with the name used in diagnostics.
type dest struct {
	name string
	w    io.Writer
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := fs.BoolP("help", "h", false, "print usage and exit")
	appendFlag := fs.BoolP("append", "a", false, "append to the given files, do not overwrite")

	return func(ctx context.Context, callCtx *builtins.CallContext, files []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: tee [OPTION]... [FILE]...\n")
			callCtx.Out("Copy standard input to standard output, making a copy in each FILE.\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}

		// Capability check after --help (matching cat/head's flag-parsing
		// order) but before opening anything: a write-capable builtin
		// invoked outside remediation mode must fail identically whether
		// or not FILE operands were supplied, rather than falling through
		// to a per-file "permission denied" from the sandbox.
		if !callCtx.RemediationMode {
			callCtx.Errf("%s", readOnlyMessage)
			return builtins.Result{Code: 1}
		}

		if len(files) > MaxFileOperands {
			callCtx.Errf("tee: too many operands (maximum %d)\n", MaxFileOperands)
			return builtins.Result{Code: 1}
		}

		flags := os.O_WRONLY | os.O_CREATE
		if *appendFlag {
			flags |= os.O_APPEND
		} else {
			flags |= os.O_TRUNC
		}

		dests := []dest{{name: "standard output", w: callCtx.Stdout}}
		var failed bool
		for _, file := range files {
			// "-" is a literal filename, not a stdout alias: GNU tee
			// dropped that historical special case in coreutils 8.24 (see
			// the package doc comment above). It goes through the same
			// sandbox path as any other operand.
			if err := rejectNonRegularTarget(ctx, callCtx, file); err != nil {
				callCtx.Errf("tee: %s: %s\n", file, callCtx.PortableErr(err))
				failed = true
				continue
			}
			f, err := callCtx.OpenFile(ctx, file, flags, 0666)
			if err != nil {
				callCtx.Errf("tee: %s: %s\n", file, callCtx.PortableErr(err))
				failed = true
				continue
			}
			// f must stay open for the whole copy below, which happens
			// after every destination has been opened; defer here (rather
			// than closing immediately) keeps it open until the handler
			// returns, which is exactly the lifetime tee needs.
			defer f.Close()
			dests = append(dests, dest{name: file, w: f})
		}

		if callCtx.Stdin != nil {
			if err := copyToAll(ctx, callCtx, callCtx.Stdin, dests); err != nil {
				failed = true
			}
		}

		if failed {
			return builtins.Result{Code: 1}
		}
		return builtins.Result{}
	}
}

// rejectNonRegularTarget prevents opening a non-regular file (FIFO, socket,
// device) as a write destination. Unlike Sandbox.Truncate, callCtx.OpenFile
// does not set O_NONBLOCK on its own write-open path, so an O_WRONLY open of
// a FIFO with no reader would block the shell indefinitely waiting for a
// connection. callCtx.StatFile is openat-based and never blocks, matching
// the interpreter's own rejectNonRegularRedirectTarget guard used for the >
// and >> redirects.
//
// ENOENT is ignored: O_CREATE will create a regular file for a missing
// target, and any other open failure surfaces from the subsequent OpenFile
// call. There is a TOCTOU window between Stat and Open; it is not a
// sandbox-escape risk because the sandbox enforces path containment
// atomically via openat, and the post-open link-count/regular-file checks in
// the sandbox's write-open path remain the authoritative guard against a
// swapped target — this check exists solely to avoid blocking on a FIFO
// before that guard can run.
func rejectNonRegularTarget(ctx context.Context, callCtx *builtins.CallContext, path string) error {
	info, err := callCtx.StatFile(ctx, path)
	if err != nil {
		return nil // ENOENT or other: let OpenFile surface the real error
	}
	if info.Mode()&iofs.ModeType == 0 {
		return nil // regular file
	}
	return &os.PathError{Op: "open", Path: path, Err: errNotRegularFile}
}

// errNotRegularFile is the sentinel wrapped by rejectNonRegularTarget's
// PathError. Its text matches the wording callCtx.PortableErr already
// produces for the sandbox's own post-open regular-file rejection (see
// writeopen.ErrNotRegularFile), so the user-visible message is identical
// whether tee is stopped by this pre-open stat check or — in the rarer case
// where a FIFO is swapped in between — by the sandbox's own guard.
var errNotRegularFile = errors.New("not a regular file")

// copyToAll streams src to every live destination in dests in fixed-size
// chunks, removing a destination from the active set the first time it
// returns a write error (matching GNU tee: a failing destination is
// reported once and dropped, while output continues to the rest). It
// returns a non-nil error if reading src failed or if any destination
// write failed at any point.
func copyToAll(ctx context.Context, callCtx *builtins.CallContext, src io.Reader, dests []dest) error {
	buf := make([]byte, teeBufSize)
	live := make([]bool, len(dests))
	liveCount := len(dests)
	for i := range live {
		live[i] = true
	}
	anyFailed := false

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, readErr := src.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			for i, d := range dests {
				if !live[i] {
					continue
				}
				if _, werr := d.w.Write(chunk); werr != nil {
					if builtins.IsBrokenPipe(werr) && d.name == "standard output" {
						// A broken stdout pipe is the normal way a
						// downstream consumer stops early (e.g. `tee
						// file | head -1`); matching cat's handling,
						// stop feeding it silently rather than
						// reporting an error for every remaining chunk.
						live[i] = false
						liveCount--
						continue
					}
					callCtx.Errf("tee: %s: %s\n", d.name, callCtx.PortableErr(werr))
					live[i] = false
					liveCount--
					anyFailed = true
				}
			}
			// Once every destination — including standard output — has
			// stopped accepting writes, there is nothing left to do with
			// further input. Returning here (rather than continuing to
			// drain src to EOF) prevents a long-lived or infinite stdin
			// source from being read forever after the last live
			// destination closes, e.g. `tee | head` once head has exited.
			if liveCount == 0 {
				break
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			callCtx.Errf("tee: read error: %s\n", callCtx.PortableErr(readErr))
			anyFailed = true
			break
		}
	}

	if anyFailed {
		return errCopyFailed
	}
	return nil
}

// errCopyFailed is a sentinel returned by copyToAll to signal that at least
// one destination write or the stdin read failed. Its text is never printed;
// the caller only checks err != nil, since each failure was already reported
// to stderr at the point it occurred (matching GNU tee's per-destination
// diagnostics instead of a single generic message).
var errCopyFailed = errors.New("tee: one or more destinations failed")
