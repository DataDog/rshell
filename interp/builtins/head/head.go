// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package head implements the head builtin command.
//
// head — output the first part of files
//
// Usage: head [OPTION]... [FILE]...
//
// Print the first 10 lines of each FILE to standard output.
// With more than one FILE, precede each with a header giving the file name.
// With no FILE, or when FILE is -, read standard input.
//
// Accepted flags:
//
//	-n N, --lines=N
//	    Output the first N lines (default 10). A leading '+' (e.g. +5) is
//	    treated as a positive sign and is equivalent to plain 5.
//
//	-c N, --bytes=N
//	    Output the first N bytes instead of lines. A leading '+' is treated
//	    as a positive sign. If both -n and -c are specified, the last flag
//	    on the command line takes effect.
//
//	-q, --quiet, --silent
//	    Never print file name headers. --silent is an alias for --quiet.
//
//	-v, --verbose
//	    Always print file name headers, even when only one file is given.
//
//	-h, --help
//	    Print this usage message to stdout and exit 0.
//
// Exit codes:
//
//	0  All files processed successfully.
//	1  At least one error occurred (missing file, invalid argument, etc.).
//
// Memory safety:
//
//	Line mode uses a streaming scanner with a per-line cap of MaxLineBytes
//	(1 MiB). Lines that exceed this cap cause an error rather than an
//	unbounded allocation. Byte mode reads in fixed-size chunks; it never
//	allocates proportionally to user-supplied N. All loops check ctx.Err()
//	at each iteration to honour the shell's execution timeout and to support
//	graceful cancellation.
package head

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"strconv"

	"github.com/DataDog/rshell/interp/builtins"
)

// Cmd is the head builtin command descriptor.
var Cmd = builtins.Command{Name: "head", MakeFlags: registerFlags}

// MaxCount is the maximum accepted line or byte count. Values above this
// are clamped. This prevents huge theoretical allocations while remaining
// larger than any practical file.
const MaxCount = 1<<31 - 1 // 2 147 483 647

// MaxLineBytes is the per-line buffer cap for the line scanner. Lines
// longer than this are reported as an error instead of being buffered.
const MaxLineBytes = 1 << 20 // 1 MiB

// registerFlags registers all head flags on the framework-provided FlagSet and
// returns a bound handler whose flag variables are captured by closure. The
// framework calls Parse and passes positional arguments to the handler.
func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := fs.BoolP("help", "h", false, "print usage and exit")

	// quietFlag, silentFlag, and verboseFlag share a sequence counter so that
	// after parsing we can determine which of -q/--quiet/--silent/-v/--verbose
	// appeared last on the command line — the last flag wins, matching GNU head.
	// NoOptDefVal is set to "true" so pflag treats these as boolean flags that
	// can be given without a "=value" argument (e.g. "--quiet" not "--quiet=true").
	var headerSeq int
	quietFlag := newBoolSeqFlag(&headerSeq)
	silentFlag := newBoolSeqFlag(&headerSeq)
	verboseFlag := newBoolSeqFlag(&headerSeq)
	fs.VarPF(quietFlag, "quiet", "q", "never print file name headers").NoOptDefVal = "true"
	fs.VarPF(silentFlag, "silent", "", "alias for --quiet").NoOptDefVal = "true"
	fs.VarPF(verboseFlag, "verbose", "v", "always print file name headers").NoOptDefVal = "true"

	// linesFlag and bytesFlag share a sequence counter so that after parsing
	// we can compare their pos fields to determine which appeared last on the
	// command line. pflag calls Set() in parse order, so the last flag Set
	// gets the highest pos value — no raw arg scanning required.
	var modeSeq int
	linesFlag := newModeFlag(&modeSeq, "10")
	bytesFlag := newModeFlag(&modeSeq, "")
	fs.VarP(linesFlag, "lines", "n", "print the first N lines instead of the first 10")
	fs.VarP(bytesFlag, "bytes", "c", "print the first N bytes instead of lines")

	return func(ctx context.Context, callCtx *builtins.CallContext, files []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: head [OPTION]... [FILE]...\n")
			callCtx.Out("Print the first 10 lines of each FILE to standard output.\n")
			callCtx.Out("With no FILE, or when FILE is -, read standard input.\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}

		// Bytes mode wins if -c/--bytes was parsed after -n/--lines. When neither
		// is set both pos fields are 0 (false → line mode). When only one is set
		// the other stays 0, so the comparison selects correctly.
		useBytesMode := bytesFlag.pos > linesFlag.pos

		// Parse the count for the chosen mode.
		countStr := linesFlag.val
		modeLabel := "lines"
		if useBytesMode {
			countStr = bytesFlag.val
			modeLabel = "bytes"
		}

		count, ok := parseCount(countStr)
		if !ok {
			callCtx.Errf("head: invalid number of %s: %q\n", modeLabel, countStr)
			return builtins.Result{Code: 1}
		}

		// Default to stdin when no file arguments were given.
		if len(files) == 0 {
			files = []string{"-"}
		}

		// Header printing: the last of -q/--quiet/--silent and -v/--verbose wins
		// (matching GNU head). --silent is an alias for --quiet and shares the
		// same sequence counter. If none are specified, print headers only when
		// multiple files are given.
		lastQuietPos := max(quietFlag.pos, silentFlag.pos)
		var printHeaders bool
		switch {
		case verboseFlag.pos > lastQuietPos:
			printHeaders = true // -v was specified last
		case lastQuietPos > verboseFlag.pos:
			printHeaders = false // -q or --silent was specified last
		default:
			printHeaders = len(files) > 1 // neither: default multi-file behaviour
		}

		var failed bool
		var printedHeader bool
		for _, file := range files {
			if ctx.Err() != nil {
				break
			}
			if err := processFile(ctx, callCtx, file, printedHeader, printHeaders, useBytesMode, count); err != nil {
				name := file
				if file == "-" {
					name = "standard input"
				}
				callCtx.Errf("head: %s: %s\n", name, callCtx.PortableErr(err))
				failed = true
			} else if printHeaders {
				// A header was successfully printed for this file; subsequent
				// files should emit a blank-line separator before their header.
				printedHeader = true
			}
		}

		if failed {
			return builtins.Result{Code: 1}
		}
		return builtins.Result{}
	}
}

// processFile opens and processes one file (or stdin for "-").
// prevHeaderPrinted reports whether a header was already emitted for a previous
// file; when true, a blank-line separator is printed before this file's header.
func processFile(ctx context.Context, callCtx *builtins.CallContext, file string, prevHeaderPrinted bool, printHeaders, useBytesMode bool, count int64) error {
	var rc io.ReadCloser
	name := file
	if file == "-" {
		name = "standard input"
		// Print the header before the nil-stdin guard so that -v always
		// emits a header for stdin even when no input stream is present.
		if printHeaders {
			if prevHeaderPrinted {
				callCtx.Out("\n")
			}
			callCtx.Outf("==> %s <==\n", name)
		}
		if callCtx.Stdin == nil {
			return nil
		}
		rc = io.NopCloser(callCtx.Stdin)
	} else {
		f, err := callCtx.OpenFile(ctx, file, os.O_RDONLY, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		rc = f
		// Header is printed after a successful open so that a file that
		// cannot be opened produces no header (matches GNU head behaviour).
		if printHeaders {
			if prevHeaderPrinted {
				callCtx.Out("\n")
			}
			callCtx.Outf("==> %s <==\n", name)
		}
	}

	if useBytesMode {
		return readBytes(ctx, callCtx, rc, count)
	}
	return readLines(ctx, callCtx, rc, count)
}

// readLines writes the first count lines of r to callCtx.Stdout, preserving
// line endings exactly (including a missing final newline).
func readLines(ctx context.Context, callCtx *builtins.CallContext, r io.Reader, count int64) error {
	sc := bufio.NewScanner(r)
	buf := make([]byte, 4096)
	sc.Buffer(buf, MaxLineBytes)
	sc.Split(scanLinesPreservingNewline)

	var emitted int64
	for emitted < count && sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := callCtx.Stdout.Write(sc.Bytes()); err != nil {
			return err
		}
		emitted++
	}
	return sc.Err()
}

// readBytes writes the first count bytes of r to callCtx.Stdout. It reads
// in fixed-size chunks; the buffer is capped at chunkSize but shrunk to
// count when count is smaller, avoiding unnecessary allocation for small
// byte requests (e.g. head -c 5).
func readBytes(ctx context.Context, callCtx *builtins.CallContext, r io.Reader, count int64) error {
	if count == 0 {
		return nil
	}
	const chunkSize = 32 * 1024
	buf := make([]byte, min(int64(chunkSize), count))
	remaining := count
	for remaining > 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		toRead := min(int64(chunkSize), remaining)
		n, err := r.Read(buf[:toRead])
		if n > 0 {
			remaining -= int64(n)
			if _, werr := callCtx.Stdout.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// parseCount parses a line or byte count string. A leading '+' is
// accepted (treated as a positive sign by strconv.ParseInt, matching GNU
// head behavior). Returns (count, true) on success, (0, false) on failure.
func parseCount(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	if n > MaxCount {
		n = MaxCount
	}
	return n, true
}

// modeFlag is a pflag.Value implementation for -n/--lines and -c/--bytes.
// Two modeFlag values share a *seq counter; each call to Set increments
// the counter and records the new value in pos. After pflag.Parse, comparing
// pos fields reveals which flag appeared last on the command line — without
// scanning raw args or inspecting individual characters of flag tokens.
type modeFlag struct {
	val string
	seq *int // shared per-invocation counter; incremented on every Set call
	pos int  // counter value when Set was last called; 0 means never set
}

func newModeFlag(seq *int, defaultVal string) *modeFlag {
	return &modeFlag{val: defaultVal, seq: seq}
}

func (f *modeFlag) String() string { return f.val }
func (f *modeFlag) Set(s string) error {
	f.val = s
	*f.seq++
	f.pos = *f.seq
	return nil
}
func (f *modeFlag) Type() string { return "string" }

// boolSeqFlag is a pflag.Value implementation for boolean flags that share a
// sequence counter with other flags. After pflag.Parse, comparing the pos
// fields of flags that share a counter reveals which was specified last.
// This is used to implement last-flag-wins semantics for -q/--quiet/--silent
// versus -v/--verbose.
type boolSeqFlag struct {
	seq *int
	pos int
}

func newBoolSeqFlag(seq *int) *boolSeqFlag {
	return &boolSeqFlag{seq: seq}
}

func (f *boolSeqFlag) String() string { return "false" }
func (f *boolSeqFlag) Set(_ string) error {
	*f.seq++
	f.pos = *f.seq
	return nil
}
func (f *boolSeqFlag) Type() string     { return "bool" }
func (f *boolSeqFlag) IsBoolFlag() bool { return true }

// scanLinesPreservingNewline is a bufio.SplitFunc that includes the line
// terminator (\n) in the returned token. Unlike bufio.ScanLines, it does not
// strip \r\n or \n, so the caller reproduces the exact file content. If the
// file's last line has no terminator, the bare bytes are returned as the
// final token.
func scanLinesPreservingNewline(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		if b == '\n' {
			return i + 1, data[:i+1], nil
		}
	}
	if atEOF {
		// Last line has no trailing newline; return what we have.
		return len(data), data, nil
	}
	// Request more data.
	return 0, nil, nil
}
