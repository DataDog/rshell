// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package builtins implements safe shell builtin commands.
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
//	Line mode uses a streaming scanner with a per-line cap of maxHeadLineBytes
//	(1 MiB). Lines that exceed this cap cause an error rather than an
//	unbounded allocation. Byte mode reads in fixed-size chunks; it never
//	allocates proportionally to user-supplied N. All loops check ctx.Err()
//	at each iteration to honour the shell's execution timeout and to support
//	graceful cancellation.

package builtins

import (
	"bufio"
	"context"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/pflag"
)

func init() {
	register("head", builtinHead)
}

// maxHeadCount is the maximum accepted line or byte count. Values above this
// are clamped. This prevents huge theoretical allocations while remaining
// larger than any practical file.
const maxHeadCount = 1<<31 - 1 // 2 147 483 647

// maxHeadLineBytes is the per-line buffer cap for the line scanner. Lines
// longer than this are reported as an error instead of being buffered.
const maxHeadLineBytes = 1 << 20 // 1 MiB

func builtinHead(ctx context.Context, callCtx *CallContext, args []string) Result {
	fs := pflag.NewFlagSet("head", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)

	help := fs.BoolP("help", "h", false, "print usage and exit")
	lines := fs.StringP("lines", "n", "10", "print the first N lines instead of the first 10")
	bytes_ := fs.StringP("bytes", "c", "", "print the first N bytes instead of lines")
	quiet := fs.BoolP("quiet", "q", false, "never print file name headers")
	_ = fs.Bool("silent", false, "alias for --quiet")
	verbose := fs.BoolP("verbose", "v", false, "always print file name headers")

	if err := fs.Parse(args); err != nil {
		callCtx.Errf("head: %v\n", err)
		return Result{Code: 1}
	}

	if *help {
		callCtx.Out("Usage: head [OPTION]... [FILE]...\n")
		callCtx.Out("Print the first 10 lines of each FILE to standard output.\n")
		callCtx.Out("With no FILE, or when FILE is -, read standard input.\n\n")
		fs.SetOutput(callCtx.Stdout)
		fs.PrintDefaults()
		return Result{}
	}

	// --silent is an alias for --quiet.
	if fs.Changed("silent") {
		*quiet = true
	}

	// Determine mode: lines vs bytes. When both flags are provided, the last
	// one on the command line wins (matches GNU head behavior). When neither
	// or only -n is provided, useBytesMode stays false (line mode is default).
	linesChanged := fs.Changed("lines")
	bytesChanged := fs.Changed("bytes")

	useBytesMode := false
	switch {
	case linesChanged && bytesChanged:
		useBytesMode = headBytesAppearsLast(args)
	case bytesChanged:
		useBytesMode = true
	// default: line mode (useBytesMode = false already)
	}

	// Parse the count for the chosen mode.
	countStr := *lines
	modeLabel := "lines"
	if useBytesMode {
		countStr = *bytes_
		modeLabel = "bytes"
	}

	count, ok := headParseCount(countStr)
	if !ok {
		callCtx.Errf("head: invalid number of %s: %q\n", modeLabel, countStr)
		return Result{Code: 1}
	}

	// Collect file arguments; default to stdin.
	files := fs.Args()
	if len(files) == 0 {
		files = []string{"-"}
	}

	// Header printing: on by default for multiple files, suppressed by -q,
	// forced for a single file by -v.
	printHeaders := len(files) > 1 || *verbose
	if *quiet {
		printHeaders = false
	}

	var failed bool
	for i, file := range files {
		if ctx.Err() != nil {
			break
		}
		if err := headProcessFile(ctx, callCtx, file, i, printHeaders, useBytesMode, count); err != nil {
			name := file
			if file == "-" {
				name = "(standard input)"
			}
			callCtx.Errf("head: %s: %s\n", name, callCtx.PortableErr(err))
			failed = true
		}
	}

	if failed {
		return Result{Code: 1}
	}
	return Result{}
}

// headProcessFile opens and processes one file (or stdin for "-").
func headProcessFile(ctx context.Context, callCtx *CallContext, file string, idx int, printHeaders, useBytesMode bool, count int64) error {
	var rc io.ReadCloser
	name := file
	if file == "-" {
		if callCtx.Stdin == nil {
			return nil
		}
		rc = io.NopCloser(callCtx.Stdin)
		name = "(standard input)"
	} else {
		f, err := callCtx.OpenFile(ctx, file, os.O_RDONLY, 0)
		if err != nil {
			return err
		}
		rc = f
	}
	defer rc.Close()

	if printHeaders {
		if idx > 0 {
			callCtx.Out("\n")
		}
		callCtx.Outf("==> %s <==\n", name)
	}

	if useBytesMode {
		return headBytes(ctx, callCtx, rc, count)
	}
	return headLines(ctx, callCtx, rc, count)
}

// headLines writes the first count lines of r to callCtx.Stdout, preserving
// line endings exactly (including a missing final newline).
func headLines(ctx context.Context, callCtx *CallContext, r io.Reader, count int64) error {
	sc := bufio.NewScanner(r)
	buf := make([]byte, 4096)
	sc.Buffer(buf, maxHeadLineBytes)
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

// headBytes writes the first count bytes of r to callCtx.Stdout. It reads
// in fixed-size chunks and never allocates proportionally to count.
func headBytes(ctx context.Context, callCtx *CallContext, r io.Reader, count int64) error {
	const chunkSize = 32 * 1024
	buf := make([]byte, chunkSize)
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
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// headParseCount parses a line or byte count string. A leading '+' is
// accepted (treated as a positive sign by strconv.ParseInt, matching GNU
// head behavior). Returns (count, true) on success, (0, false) on failure.
func headParseCount(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	if n > maxHeadCount {
		n = maxHeadCount
	}
	return n, true
}

// headBytesAppearsLast reports whether the last mode-selecting flag in args
// is -c/--bytes. This implements the GNU "last flag wins" behavior when both
// -n and -c appear on the same command line.
//
// lastBytes and lastLines are initialised to -1 ("not seen"). Any valid
// arg index is ≥ 0, so the comparison lastBytes > lastLines correctly
// selects the flag that appeared later, and returns false when neither (or
// only one) mode flag is present.
func headBytesAppearsLast(args []string) bool {
	lastBytes, lastLines := -1, -1
	for i, arg := range args {
		if arg == "--" {
			break
		}
		if headIsModeFlag(arg, 'c', "--bytes") {
			lastBytes = i
		} else if headIsModeFlag(arg, 'n', "--lines") {
			lastLines = i
		}
	}
	return lastBytes > lastLines
}

// headIsModeFlag reports whether arg is a short flag token for the given
// short rune (e.g. 'c') or a long flag token (e.g. "--bytes" / "--bytes=N").
// It matches: -X, -XN (value glued), --long, --long=N.
func headIsModeFlag(arg string, short byte, long string) bool {
	return arg == string([]byte{'-', short}) ||
		(len(arg) > 2 && arg[0] == '-' && arg[1] == short && arg[2] != '-') ||
		arg == long ||
		strings.HasPrefix(arg, long+"=")
}

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
