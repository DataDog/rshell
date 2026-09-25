// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"

	"mvdan.cc/sh/v3/syntax"
)

// redirectFilePerm is the permission bits used when creating redirect target
// files. 0666 lets the process umask determine the final mode (open(2) applies
// mode & ~umask), matching bash's >FILE behaviour and Sandbox.Truncate.
// Hardcoding 0644 here would instead bake in the result for umask 022 only,
// producing a non-group-writable file on hosts with a 002 umask.
// On Windows there is no umask; Go maps the mode to the read-only attribute
// (perm&0200), which both 0644 and 0666 set, so this value is inert there.
const redirectFilePerm os.FileMode = 0666

// statFileMode returns the fs.FileMode for path via the sandbox.
// When r.sandbox is nil, it returns os.ErrNotExist so the caller skips the
// type-check — a nil sandbox routes all opens through sandbox.Open(nil),
// which returns ErrPermission immediately, so a FIFO can never actually block.
func (r *Runner) statFileMode(path string) (fs.FileMode, error) {
	if r.sandbox == nil {
		return 0, os.ErrNotExist
	}
	info, err := r.sandbox.Stat(path, r.Dir)
	if err != nil {
		return 0, err
	}
	return info.Mode(), nil
}

// rejectNonRegularRedirectTarget prevents opening a non-regular file (FIFO,
// socket, device) as a write redirect target. Opening a FIFO with O_WRONLY
// blocks until a reader connects, which would hang the script before context
// cancellation fires. Sandbox.Stat is openat-based and never blocks.
//
// /dev/null is handled by the io.Discard fast path and never reaches here.
// ENOENT is ignored — O_CREATE will create a regular file; other open
// failures surface from the subsequent Open call. When r.sandbox is nil,
// the guard is skipped.
//
// There is a TOCTOU window between Stat and Open; it is not a sandbox-escape
// risk because the sandbox enforces path containment atomically via openat.
func (r *Runner) rejectNonRegularRedirectTarget(path string) error {
	mode, err := r.statFileMode(path)
	if err != nil {
		return nil // ENOENT or other: let Open surface the real error
	}
	if mode&fs.ModeType == 0 {
		return nil // regular file
	}
	werr := fmt.Errorf("open %s: not a regular file", path)
	r.errf("%v\n", werr)
	return werr
}

// withElevatedRedirectOpen runs fn (the redirect's type-check and open,
// together) inside the corresponding elevate() window when
// r.pendingElevatedRedirect is set, and directly at the caller's current
// privilege otherwise.
//
// fn must be the entire privileged operation: both the special-file check
// (rejectNonRegularRedirectTarget) and the open(2) call. Running the check
// unprivileged while the open runs elevated would defeat the check for a
// root-only target: rejectNonRegularRedirectTarget's unprivileged Stat can
// only fail closed (permission denied), which it treats as inconclusive and
// defers to Open, but Open then runs elevated and can reach and block
// indefinitely on a root-owned FIFO — ignoring context cancellation, since
// the sandbox's write-open path issues a plain blocking open(2). Elevating
// the check together with the open closes that gap: whatever privilege
// level actually performs the open also performs the type check first, so
// the check's Stat sees exactly what the open would see.
//
// Elevation is otherwise scoped as tightly as possible: fn must only be the
// type-check-and-open pair, never earlier expansion steps. In particular
// r.literal(rd.Word) (resolving the redirect target itself) can run a
// command substitution, and a substituted command must never INHERIT
// elevation intended for a specific, operator-authorized "sudo <name>"
// command merely by appearing inside that command's own redirect target.
// r.pendingElevatedRedirect is never set while a command substitution
// subshell runs (subshell() does not copy it — see (*Runner).subshell), so
// the substitution's own dispatch always re-derives this field from its own
// authorization rather than inheriting the caller's: a substituted command
// with no "sudo" marker of its own runs unprivileged, exactly the scenario
// this scoping exists to guarantee, while a substituted command that DOES
// carry its own "sudo <name>" marker independently elevates on its own
// merits through this identical mechanism, in its own subshell.
func (r *Runner) withElevatedRedirectOpen(ctx context.Context, fn func() (io.ReadWriteCloser, error)) (io.ReadWriteCloser, error) {
	name := r.pendingElevatedRedirect
	if name == "" {
		return fn()
	}
	var f io.ReadWriteCloser
	var fnErr error
	if err := r.elevate(ctx, name, func() {
		f, fnErr = fn()
	}); err != nil {
		// fn may have already run and opened f before the ElevateFunc
		// implementation failed to restore privileges on its way out (its
		// documented contract is to restore privileges "including when run
		// fails", but a failure in that unwind step is exactly what this
		// err represents). Close it rather than leaking the descriptor.
		if f != nil {
			f.Close()
		}
		// A failure at this layer means the ElevateFunc implementation
		// itself malfunctioned (e.g. setresuid failed) — not an ordinary
		// sandbox/policy rejection, which r.open and
		// rejectNonRegularRedirectTarget already print via r.errf and
		// leave non-fatal (exit 1, script continues). Mark it fatal,
		// matching the identical failure mode for an elevated command's
		// own dispatch in call() ("elevating %s: %w"), so the caller
		// cannot be left with an unexplained bare exit 1 and the script
		// aborts instead of running further statements against a
		// privilege boundary that just proved itself unreliable.
		werr := fmt.Errorf("elevating redirect for %s: %w", name, err)
		r.exit.fatal(werr)
		return nil, werr
	}
	return f, fnErr
}

// openWriteRedirect opens arg for writing and assigns it to *orig.
// Used by >, >|, and >> in remediation mode.
func (r *Runner) openWriteRedirect(ctx context.Context, op syntax.RedirOperator, arg string, orig *io.Writer) (io.Closer, error) {
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if op == syntax.AppOut {
		flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}
	f, err := r.withElevatedRedirectOpen(ctx, func() (io.ReadWriteCloser, error) {
		if err := r.rejectNonRegularRedirectTarget(arg); err != nil {
			return nil, err
		}
		return r.open(ctx, arg, flags, redirectFilePerm, true)
	})
	if err != nil {
		return nil, err
	}
	*orig = f
	return f, nil
}

// openWriteAllRedirect opens arg for writing and assigns it to both stdout and
// stderr. Used by &> and &>> in remediation mode.
func (r *Runner) openWriteAllRedirect(ctx context.Context, op syntax.RedirOperator, arg string) (io.Closer, error) {
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if op == syntax.AppAll {
		flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
	}
	f, err := r.withElevatedRedirectOpen(ctx, func() (io.ReadWriteCloser, error) {
		if err := r.rejectNonRegularRedirectTarget(arg); err != nil {
			return nil, err
		}
		return r.open(ctx, arg, flags, redirectFilePerm, true)
	})
	if err != nil {
		return nil, err
	}
	r.stdout = f
	r.stderr = f
	return f, nil
}
