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
//	    Output the last N lines (default 10). A leading '+' (e.g. +5) means
//	    output starting from line N (1-based offset from the beginning).
//
//	-c N, --bytes=N
//	    Output the last N bytes instead of lines. A leading '+' means
//	    output starting from byte N (1-based offset from the beginning).
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
// Memory safety and correctness:
//
//	Line mode uses a ring buffer of size min(N, MaxRingLines) slots. Each slot
//	holds one line; lines exceeding MaxLineBytes cause a scanner error. The
//	ring buffer's total memory footprint is additionally capped at MaxRingBytes
//	(64 MiB). If the input has more lines than the ring can hold and N exceeds
//	MaxRingLines, tail returns an error rather than silently truncating output.
//
//	Byte mode uses a circular buffer of size min(N, MaxBytesBuffer). If the
//	input exceeds MaxBytesBuffer bytes and N exceeds MaxBytesBuffer, tail
//	returns an error rather than silently returning fewer bytes than requested.
//
//	Offset (+N) modes stream without buffering. All loops check ctx.Err() at
//	each iteration to honour the shell's execution timeout.
//
// Infinite-stream protection:
//
//	Both last-N-lines and last-N-bytes modes must consume the entire input
//	before emitting output. For non-regular-file inputs (pipes, stdin,
//	character devices) without a context deadline, execution would hang
//	indefinitely. To bound this, tail returns an error once total bytes read
//	from such a source exceed MaxTotalReadBytes (256 MiB). Regular files are
//	not subject to this limit because the OS guarantees they are finite.
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

// MaxCount is the maximum accepted line or byte count. Values above this
// are clamped to prevent huge theoretical allocations.
const MaxCount = 1<<31 - 1 // 2 147 483 647

// MaxLineBytes is the per-line buffer cap for the line scanner.
const MaxLineBytes = 1 << 20 // 1 MiB

// MaxRingLines is the maximum number of lines held in the ring buffer.
const MaxRingLines = 100_000

// MaxRingBytes is the maximum total bytes the ring buffer may hold at any
// one time. Without this cap, MaxRingLines (100 000) × MaxLineBytes (1 MiB)
// yields a worst-case memory envelope of ~97.6 GiB. This constant reduces
// the bound to 64 MiB.
const MaxRingBytes = 64 << 20 // 64 MiB

// MaxBytesBuffer is the maximum size of the circular byte buffer used in
// last-N-bytes mode.
const MaxBytesBuffer = 32 << 20 // 32 MiB

// MaxTotalReadBytes is the maximum total bytes tail will consume from a
// single input source. Both last-N-lines and last-N-bytes modes must read
// the entire input before emitting output, so an infinite source without a
// context deadline would hang indefinitely. This limit bounds execution to a
// finite amount of work regardless of whether a timeout is configured.
const MaxTotalReadBytes = 256 << 20 // 256 MiB

// countMode holds the parsed value of a -n / -c argument.
type countMode struct {
	n      int64
	offset bool // true when the argument started with '+' (offset from start)
}

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
	fs.VarP(linesFlag, "lines", "n", "output the last N lines instead of the last 10")
	fs.VarP(bytesFlag, "bytes", "c", "output the last N bytes instead of lines")

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

	cm, ok := parseCount(countStr)
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

	var failed bool
	var headerPrinted bool
	for _, file := range files {
		if ctx.Err() != nil {
			break
		}
		if err := processFile(ctx, callCtx, file, &headerPrinted, printHeaders, useBytesMode, cm, *zeroTerminated); err != nil {
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
// headerPrinted tracks whether any header has been emitted so far; the blank
// separator line is printed only when a prior header was actually output
// (failed opens produce no header and must not cause a leading blank line).
func processFile(ctx context.Context, callCtx *builtins.CallContext, file string, headerPrinted *bool, printHeaders, useBytesMode bool, cm countMode, zeroTerm bool) error {
	var rc io.ReadCloser
	name := file
	if file == "-" {
		name = "standard input"
		// Print the header before the nil-stdin guard so that -v always
		// emits a header for stdin even when no input stream is present.
		if printHeaders {
			if *headerPrinted {
				callCtx.Out("\n")
			}
			callCtx.Outf("==> %s <==\n", name)
			*headerPrinted = true
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
		// cannot be opened produces no header (matches GNU tail behaviour).
		if printHeaders {
			if *headerPrinted {
				callCtx.Out("\n")
			}
			callCtx.Outf("==> %s <==\n", name)
			*headerPrinted = true
		}
	}

	// Detect regular files so that last-N modes can skip the infinite-stream
	// guard (MaxTotalReadBytes): regular files are finite by OS guarantee.
	type stater interface{ Stat() (os.FileInfo, error) }
	isRegularFile := false
	if sf, ok := rc.(stater); ok {
		if fi, statErr := sf.Stat(); statErr == nil {
			isRegularFile = fi.Mode().IsRegular()
		}
	}

	if useBytesMode {
		if cm.offset {
			return skipBytes(ctx, callCtx, rc, cm.n)
		}
		return readLastBytes(ctx, callCtx, rc, cm.n, isRegularFile)
	}
	if cm.offset {
		return skipLines(ctx, callCtx, rc, cm.n, zeroTerm)
	}
	return readLastLines(ctx, callCtx, rc, cm.n, zeroTerm, isRegularFile)
}

// readLastLines writes the last count lines of r to callCtx.Stdout.
// It uses a ring buffer of size min(count, MaxRingLines). If the input
// has more lines than MaxRingLines and count > MaxRingLines, an error is
// returned rather than silently truncating output.
// isRegularFile disables the MaxTotalReadBytes infinite-stream guard.
func readLastLines(ctx context.Context, callCtx *builtins.CallContext, r io.Reader, count int64, nullDelim, isRegularFile bool) error {
	if count == 0 {
		return nil
	}

	sc := bufio.NewScanner(r)
	buf := make([]byte, 4096)
	sc.Buffer(buf, MaxLineBytes)
	if nullDelim {
		sc.Split(scanNULPreservingNUL)
	} else {
		sc.Split(scanLinesPreservingNewline)
	}

	ringSize := int(min(count, int64(MaxRingLines)))
	ring := make([][]byte, ringSize)
	var ringHead int
	var ringCount int
	var ringBytes int64 // total bytes currently held in the ring buffer
	var totalRead int64 // total bytes consumed from input (infinite-stream guard)

	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		raw := sc.Bytes()
		totalRead += int64(len(raw))
		if !isRegularFile && totalRead > MaxTotalReadBytes {
			return errors.New("input too large: read limit exceeded")
		}
		cp := make([]byte, len(raw))
		copy(cp, raw)
		// When the ring is full, evict the oldest entry before writing.
		if ringCount == ringSize {
			// If count exceeds the ring capacity, we cannot deliver the full
			// requested window without silent truncation — return an error.
			if int64(ringSize) < count {
				return errors.New("input too large: line buffer limit exceeded")
			}
			ringBytes -= int64(len(ring[ringHead]))
		}
		ring[ringHead] = cp
		ringBytes += int64(len(cp))
		if ringBytes > MaxRingBytes {
			return errors.New("input too large: ring buffer memory limit exceeded")
		}
		ringHead = (ringHead + 1) % ringSize
		if ringCount < ringSize {
			ringCount++
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}

	// Emit ringCount lines in order starting from the oldest.
	start := (ringHead - ringCount + ringSize) % ringSize
	for i := 0; i < ringCount; i++ {
		if _, err := callCtx.Stdout.Write(ring[(start+i)%ringSize]); err != nil {
			return err
		}
	}
	return nil
}

// skipLines skips the first (n-1) lines of r and writes the rest to
// callCtx.Stdout. This implements the "+N" offset mode for -n.
func skipLines(ctx context.Context, callCtx *builtins.CallContext, r io.Reader, n int64, nullDelim bool) error {
	skipCount := max(n-1, 0)

	sc := bufio.NewScanner(r)
	buf := make([]byte, 4096)
	sc.Buffer(buf, MaxLineBytes)
	if nullDelim {
		sc.Split(scanNULPreservingNUL)
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

// readLastBytes writes the last count bytes of r to callCtx.Stdout.
// It reads the entire input into a circular buffer of size
// min(count, MaxBytesBuffer). If the input exceeds MaxBytesBuffer bytes and
// count > MaxBytesBuffer, an error is returned rather than silently returning
// fewer bytes than requested.
// isRegularFile disables the MaxTotalReadBytes infinite-stream guard.
func readLastBytes(ctx context.Context, callCtx *builtins.CallContext, r io.Reader, count int64, isRegularFile bool) error {
	if count == 0 {
		return nil
	}

	// Allocate the circular buffer eagerly. bufSize is capped at MaxBytesBuffer
	// (32 MiB), so this allocation is bounded regardless of the user-supplied
	// count value.
	bufSize := int(min(count, int64(MaxBytesBuffer)))
	circ := make([]byte, bufSize)
	var totalWritten int64

	tmp := make([]byte, 32*1024)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, readErr := r.Read(tmp)
		for i := 0; i < n; {
			pos := int(totalWritten % int64(bufSize))
			canWrite := min(bufSize-pos, n-i)
			copy(circ[pos:pos+canWrite], tmp[i:i+canWrite])
			totalWritten += int64(canWrite)
			i += canWrite
		}
		// If the circular buffer has wrapped and count exceeds the buffer
		// capacity, we cannot deliver the full requested window.
		if totalWritten > int64(bufSize) && count > int64(bufSize) {
			return errors.New("input too large: byte buffer limit exceeded")
		}
		if !isRegularFile && totalWritten > MaxTotalReadBytes {
			return errors.New("input too large: read limit exceeded")
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}

	if totalWritten == 0 {
		return nil
	}

	if totalWritten <= int64(bufSize) {
		_, err := callCtx.Stdout.Write(circ[:totalWritten])
		return err
	}

	// Circular buffer is full; emit older half then newer half.
	start := int(totalWritten % int64(bufSize))
	if _, err := callCtx.Stdout.Write(circ[start:]); err != nil {
		return err
	}
	_, err := callCtx.Stdout.Write(circ[:start])
	return err
}

// skipBytes skips the first (n-1) bytes of r and writes the rest to
// callCtx.Stdout. This implements the "+N" offset mode for -c.
func skipBytes(ctx context.Context, callCtx *builtins.CallContext, r io.Reader, n int64) error {
	skipCount := max(n-1, 0)

	buf := make([]byte, 32*1024)
	var skipped int64
	for skipped < skipCount {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		toRead := min(int64(len(buf)), skipCount-skipped)
		nRead, err := r.Read(buf[:toRead])
		skipped += int64(nRead)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}

	// Stream the rest.
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

// parseCount parses a line or byte count string for tail.
// A leading '+' activates offset mode (output starting from position N,
// 1-based). Without '+', the value is the number of trailing lines/bytes
// to output. GNU tail silently treats negative counts as their absolute value.
func parseCount(s string) (countMode, bool) {
	if s == "" {
		return countMode{}, false
	}
	isOffset := s[0] == '+'
	parseStr := s
	if isOffset {
		parseStr = s[1:]
	}
	n, err := strconv.ParseInt(parseStr, 10, 64)
	if err != nil {
		return countMode{}, false
	}
	// GNU tail silently treats negative counts as their absolute value.
	if n < 0 {
		n = -n
	}
	if n > MaxCount {
		n = MaxCount
	}
	return countMode{n: n, offset: isOffset}, true
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
	if _, ok := parseCount(s); !ok {
		return errors.New("invalid count")
	}
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
// strip \r\n or \n, preserving exact file content. A missing final newline is
// returned as the last token.
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

// scanNULPreservingNUL is a bufio.SplitFunc that splits on NUL bytes (\x00)
// and includes the NUL in the returned token, analogous to
// scanLinesPreservingNewline but for -z (--zero-terminated) mode.
func scanNULPreservingNUL(data []byte, atEOF bool) (advance int, token []byte, err error) {
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
