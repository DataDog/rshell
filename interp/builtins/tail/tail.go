// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package tail implements the tail builtin command.
//
// tail — output the last part of files
//
// Usage: tail [OPTION]... [FILE]...
//
// Print the last 10 lines of each FILE to standard output.
// With more than one FILE, precede each with a header giving the file name.
// With no FILE, or when FILE is -, read standard input.
//
// Accepted flags:
//
//	-n N, --lines=N
//	    Output the last N lines (default 10).  A leading '+' (e.g. +5) means
//	    "start from line 5" — skip the first N-1 lines and emit the rest.
//
//	-c N, --bytes=N
//	    Output the last N bytes instead of lines.  A leading '+' (e.g. +5)
//	    means "start from byte 5" — skip the first N-1 bytes and emit the rest.
//	    If both -n and -c are specified, the last flag on the command line
//	    takes effect.
//
//	-q, --quiet, --silent
//	    Never print file name headers. --silent is an alias for --quiet.
//
//	-v, --verbose
//	    Always print file name headers, even when only one file is given.
//
//	-z, --zero-terminated
//	    Use NUL as the line delimiter instead of newline.
//
//	-h, --help
//	    Print this usage message to stdout and exit 0.
//
// Rejected flags: -f, -F, --follow, --pid, --retry (not supported).
//
// Exit codes:
//
//	0  All files processed successfully.
//	1  At least one error occurred (missing file, invalid argument, etc.).
//
// Memory safety:
//
//	Line mode uses a ring buffer capped at MaxRingSize entries (1 M lines).
//	Each line token is capped at MaxLineBytes (1 MiB). Byte mode uses a
//	circular byte buffer capped at MaxByteBuffer (64 MiB). All loops check
//	ctx.Err() at each iteration to honour the shell's execution timeout and
//	support graceful cancellation.
package tail

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"strconv"

	"github.com/spf13/pflag"

	"github.com/DataDog/rshell/interp/builtins"
)

// Cmd is the tail builtin command descriptor.
var Cmd = builtins.Command{Name: "tail", Run: run}

// MaxCount is the maximum accepted line or byte count. Values above this are
// clamped to prevent huge theoretical allocations.
const MaxCount = 1<<31 - 1 // 2 147 483 647

// MaxLineBytes is the per-line buffer cap for the line scanner.
const MaxLineBytes = 1 << 20 // 1 MiB

// MaxRingSize is the maximum number of ring-buffer slots in line mode.
const MaxRingSize = 1 << 20 // 1 M entries

// MaxByteBuffer is the maximum circular byte buffer size in byte mode.
const MaxByteBuffer = 1 << 26 // 64 MiB

func run(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	fs := pflag.NewFlagSet("tail", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)

	help           := fs.BoolP("help", "h", false, "print usage and exit")
	zeroTerminated := fs.BoolP("zero-terminated", "z", false, "use NUL as line delimiter")

	// quietFlag, silentFlag, and verboseFlag share a sequence counter so that
	// after parsing we can tell which appeared last on the command line and
	// apply last-flag-wins semantics (e.g. "-q -v" should show headers).
	var headerSeq int
	quietFlag   := newHeaderFlag(&headerSeq)
	silentFlag  := newHeaderFlag(&headerSeq)
	verboseFlag := newHeaderFlag(&headerSeq)
	fs.VarP(quietFlag, "quiet", "q", "never print file name headers")
	fs.Var(silentFlag, "silent", "alias for --quiet")
	fs.VarP(verboseFlag, "verbose", "v", "always print file name headers")
	// Mark the header flags as boolean so pflag does not consume the next
	// positional argument as a value when the flag appears without "=…".
	fs.Lookup("quiet").NoOptDefVal = "true"
	fs.Lookup("silent").NoOptDefVal = "true"
	fs.Lookup("verbose").NoOptDefVal = "true"

	// linesFlag and bytesFlag share a sequence counter so that after parsing
	// we can compare their pos fields to determine which appeared last on the
	// command line. pflag calls Set() in parse order, so the last flag Set
	// gets the highest pos value.
	var modeSeq int
	linesFlag := newModeFlag(&modeSeq, "10")
	bytesFlag := newModeFlag(&modeSeq, "")
	fs.VarP(linesFlag, "lines", "n", "print the last N lines instead of the last 10")
	fs.VarP(bytesFlag, "bytes", "c", "print the last N bytes instead of lines")

	if err := fs.Parse(args); err != nil {
		callCtx.Errf("tail: %v\n", err)
		return builtins.Result{Code: 1}
	}

	if *help {
		callCtx.Out("Usage: tail [OPTION]... [FILE]...\n")
		callCtx.Out("Print the last 10 lines of each FILE to standard output.\n")
		callCtx.Out("With no FILE, or when FILE is -, read standard input.\n\n")
		fs.SetOutput(callCtx.Stdout)
		fs.PrintDefaults()
		return builtins.Result{}
	}

	// Bytes mode wins if -c/--bytes was parsed after -n/--lines.
	useBytesMode := bytesFlag.pos > linesFlag.pos

	countStr  := linesFlag.val
	modeLabel := "lines"
	if useBytesMode {
		countStr  = bytesFlag.val
		modeLabel = "bytes"
	}

	count, offsetMode, ok := parseCount(countStr)
	if !ok {
		callCtx.Errf("tail: invalid number of %s: %q\n", modeLabel, countStr)
		return builtins.Result{Code: 1}
	}

	files := fs.Args()
	if len(files) == 0 {
		files = []string{"-"}
	}

	// Determine header printing using last-flag-wins: the highest pos among
	// quiet/silent (suppress) vs verbose (force) controls the outcome.
	suppressPos := quietFlag.pos
	if silentFlag.pos > suppressPos {
		suppressPos = silentFlag.pos
	}
	printHeaders := len(files) > 1
	if verboseFlag.pos > suppressPos {
		printHeaders = true
	} else if suppressPos > verboseFlag.pos {
		printHeaders = false
	}

	delim := byte('\n')
	if *zeroTerminated {
		delim = 0
	}

	var failed bool
	for i, file := range files {
		if ctx.Err() != nil {
			break
		}
		if err := processFile(ctx, callCtx, file, i, printHeaders, useBytesMode, offsetMode, count, delim); err != nil {
			name := file
			if file == "-" {
				name = "standard input"
			}
			callCtx.Errf("tail: %s: %s\n", name, callCtx.PortableErr(err))
			failed = true
		}
	}

	if failed {
		return builtins.Result{Code: 1}
	}
	return builtins.Result{}
}

// processFile opens and processes one file (or stdin for "-").
func processFile(ctx context.Context, callCtx *builtins.CallContext, file string, idx int, printHeaders, useBytesMode, offsetMode bool, count int64, delim byte) error {
	var rc io.ReadCloser
	name := file
	if file == "-" {
		name = "standard input"
		if printHeaders {
			if idx > 0 {
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
		if printHeaders {
			if idx > 0 {
				callCtx.Out("\n")
			}
			callCtx.Outf("==> %s <==\n", name)
		}
	}

	if useBytesMode {
		if offsetMode {
			var skipCount int64
			if count > 0 {
				skipCount = count - 1
			}
			return tailBytesOffset(ctx, callCtx, rc, skipCount)
		}
		return tailBytes(ctx, callCtx, rc, count)
	}

	if offsetMode {
		var skipCount int64
		if count > 0 {
			skipCount = count - 1
		}
		return tailLinesOffset(ctx, callCtx, rc, skipCount, delim)
	}
	return tailLines(ctx, callCtx, rc, count, delim)
}

// tailLines reads r and emits the last count lines using a ring buffer.
func tailLines(ctx context.Context, callCtx *builtins.CallContext, r io.Reader, count int64, delim byte) error {
	if count == 0 {
		_, err := io.Copy(io.Discard, r)
		return err
	}

	ringCap := int(count)
	if int64(ringCap) > MaxRingSize {
		ringCap = MaxRingSize
	}

	ring := make([][]byte, ringCap)
	writePos := 0
	filled := 0

	sc := bufio.NewScanner(r)
	buf := make([]byte, 4096)
	sc.Buffer(buf, MaxLineBytes)
	if delim == 0 {
		sc.Split(scanNulTerminated)
	} else {
		sc.Split(scanLinesPreservingNewline)
	}

	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		token := sc.Bytes()
		cp := make([]byte, len(token))
		copy(cp, token)
		ring[writePos] = cp
		writePos = (writePos + 1) % ringCap
		if filled < ringCap {
			filled++
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}

	// Determine oldest-entry index and emit ring contents in order.
	startIdx := 0
	if filled == ringCap {
		startIdx = writePos
	}
	for i := 0; i < filled; i++ {
		idx := (startIdx + i) % ringCap
		if _, err := callCtx.Stdout.Write(ring[idx]); err != nil {
			return err
		}
	}
	return nil
}

// tailLinesOffset skips skipCount lines then emits the remainder.
func tailLinesOffset(ctx context.Context, callCtx *builtins.CallContext, r io.Reader, skipCount int64, delim byte) error {
	sc := bufio.NewScanner(r)
	buf := make([]byte, 4096)
	sc.Buffer(buf, MaxLineBytes)
	if delim == 0 {
		sc.Split(scanNulTerminated)
	} else {
		sc.Split(scanLinesPreservingNewline)
	}

	var skipped int64
	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if skipped < skipCount {
			skipped++
			continue
		}
		if _, err := callCtx.Stdout.Write(sc.Bytes()); err != nil {
			return err
		}
	}
	return sc.Err()
}

// tailBytes reads r and emits the last count bytes using a circular byte buffer.
func tailBytes(ctx context.Context, callCtx *builtins.CallContext, r io.Reader, count int64) error {
	if count == 0 {
		_, err := io.Copy(io.Discard, r)
		return err
	}

	bufCap := count
	if bufCap > MaxByteBuffer {
		bufCap = MaxByteBuffer
	}

	ring := make([]byte, bufCap)
	var totalWritten int64

	const chunkSize = 32 * 1024
	chunk := make([]byte, chunkSize)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := r.Read(chunk)
		if n > 0 {
			addBytesToRing(ring, chunk[:n], bufCap, &totalWritten)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
	}

	filled := totalWritten
	if filled > bufCap {
		filled = bufCap
	}

	if totalWritten <= bufCap {
		_, err := callCtx.Stdout.Write(ring[:filled])
		return err
	}

	// Ring is full; oldest byte is at totalWritten%bufCap.
	startPos := totalWritten % bufCap
	if _, err := callCtx.Stdout.Write(ring[startPos:]); err != nil {
		return err
	}
	if startPos > 0 {
		if _, err := callCtx.Stdout.Write(ring[:startPos]); err != nil {
			return err
		}
	}
	return nil
}

// addBytesToRing copies data into the circular byte ring buffer, wrapping as
// needed. totalWritten is updated by len(data).
func addBytesToRing(ring []byte, data []byte, cap int64, totalWritten *int64) {
	for len(data) > 0 {
		pos := *totalWritten % cap
		toEnd := cap - pos
		n := int64(len(data))
		if n > toEnd {
			n = toEnd
		}
		copy(ring[pos:pos+n], data[:n])
		data = data[n:]
		*totalWritten += n
	}
}

// tailBytesOffset skips skipCount bytes then emits the remainder.
func tailBytesOffset(ctx context.Context, callCtx *builtins.CallContext, r io.Reader, skipCount int64) error {
	const chunkSize = 32 * 1024
	buf := make([]byte, chunkSize)
	remaining := skipCount
	for remaining > 0 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		toRead := int64(chunkSize)
		if toRead > remaining {
			toRead = remaining
		}
		n, err := r.Read(buf[:toRead])
		remaining -= int64(n)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := r.Read(buf)
		if n > 0 {
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
}

// parseCount parses a tail count string and returns (count, offsetMode, ok).
// A leading '+' sets offsetMode; tail then starts from line/byte count instead
// of emitting the last count lines/bytes.
func parseCount(s string) (int64, bool, bool) {
	if s == "" {
		return 0, false, false
	}
	offsetMode := false
	rest := s
	if s[0] == '+' {
		offsetMode = true
		rest = s[1:]
		if rest == "" {
			return 0, false, false
		}
	}
	n, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || n < 0 {
		return 0, false, false
	}
	if n > MaxCount {
		n = MaxCount
	}
	return n, offsetMode, true
}

// modeFlag is a pflag.Value implementation for -n/--lines and -c/--bytes.
// Two modeFlag values share a *seq counter; each call to Set increments the
// counter and records the new value in pos. After pflag.Parse, comparing pos
// fields reveals which flag appeared last on the command line.
type modeFlag struct {
	val string
	seq *int
	pos int
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

// headerFlag is a pflag.Value implementation for -q/--quiet/--silent and
// -v/--verbose. Multiple headerFlag values share a *seq counter so that after
// pflag.Parse the one with the highest pos was set last on the command line
// and wins (last-flag-wins semantics).
type headerFlag struct {
	seq *int
	pos int
}

func newHeaderFlag(seq *int) *headerFlag { return &headerFlag{seq: seq} }

func (f *headerFlag) String() string   { return "false" }
func (f *headerFlag) Set(string) error { *f.seq++; f.pos = *f.seq; return nil }
func (f *headerFlag) Type() string     { return "bool" }
func (f *headerFlag) IsBoolFlag() bool { return true }

// scanLinesPreservingNewline is a bufio.SplitFunc that includes the line
// terminator (\n) in the returned token. Unlike bufio.ScanLines, it does not
// strip \r\n or \n, so the caller reproduces the exact file content.
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
		return len(data), data, nil
	}
	return 0, nil, nil
}

// scanNulTerminated is a bufio.SplitFunc that uses NUL (0x00) as the line
// delimiter. The NUL byte is included in the returned token, mirroring the
// behaviour of scanLinesPreservingNewline for newlines.
func scanNulTerminated(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		if b == 0 {
			return i + 1, data[:i+1], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}
