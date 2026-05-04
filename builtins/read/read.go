// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package read implements the `read` shell builtin: it consumes a line
// (or bounded chunk) from standard input, applies POSIX field-splitting
// based on IFS, and assigns the resulting fields to one or more shell
// variables in the calling shell's scope.
package read

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/DataDog/rshell/builtins"
)

// MaxReadBytes bounds the number of input bytes that read will buffer
// for a single invocation. It also caps the values accepted for -n and
// -N at flag-parse time, so a script cannot ask read to allocate an
// arbitrary amount of memory.
const MaxReadBytes = 1 << 20 // 1 MiB

// Cmd is the read builtin command.
var Cmd = builtins.Command{
	Name:        "read",
	Description: "read a line from standard input and assign to shell variables",
	MakeFlags:   registerFlags,
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := fs.Bool("help", false, "print usage and exit")
	raw := fs.BoolP("raw", "r", false, "do not interpret backslashes")
	prompt := fs.StringP("prompt", "p", "", "print PROMPT to stderr before reading")
	delim := fs.StringP("delim", "d", "", "use the first character of DELIM as the line terminator (empty = NUL)")
	nChars := fs.IntP("nchars", "n", -1, fmt.Sprintf("return after reading at most NCHARS characters (max %d)", MaxReadBytes))
	nBytes := fs.IntP("nbytes", "N", -1, fmt.Sprintf("return after reading exactly NBYTES bytes (max %d), ignoring delimiters", MaxReadBytes))
	timeoutStr := fs.StringP("timeout", "t", "", "time out after TIMEOUT seconds (decimal allowed)")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: read [OPTION]... [NAME...]\n")
			callCtx.Out("Read one line from standard input and assign each field to a shell\n")
			callCtx.Out("variable named by NAME, splitting on the characters in IFS. With no\n")
			callCtx.Out("NAME, the line is assigned to REPLY.\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}
		return run(ctx, callCtx, args, runOpts{
			raw:        *raw,
			prompt:     *prompt,
			delim:      *delim,
			delimSet:   fs.Changed("delim"),
			nChars:     *nChars,
			nBytes:     *nBytes,
			timeoutStr: *timeoutStr,
		})
	}
}

type runOpts struct {
	raw        bool
	prompt     string
	delim      string
	delimSet   bool
	nChars     int
	nBytes     int
	timeoutStr string
}

func run(ctx context.Context, c *builtins.CallContext, args []string, opt runOpts) builtins.Result {
	if c.SetVar == nil || c.GetVar == nil {
		c.Errf("read: variable access is not available in this context\n")
		return builtins.Result{Code: 2}
	}

	if opt.nChars != -1 {
		if opt.nChars < 0 {
			c.Errf("read: -n: count must be non-negative\n")
			return builtins.Result{Code: 1}
		}
		if opt.nChars > MaxReadBytes {
			c.Errf("read: -n: count exceeds maximum of %d\n", MaxReadBytes)
			return builtins.Result{Code: 1}
		}
	}
	if opt.nBytes != -1 {
		if opt.nBytes < 0 {
			c.Errf("read: -N: count must be non-negative\n")
			return builtins.Result{Code: 1}
		}
		if opt.nBytes > MaxReadBytes {
			c.Errf("read: -N: count exceeds maximum of %d\n", MaxReadBytes)
			return builtins.Result{Code: 1}
		}
	}

	var delim rune = '\n'
	if opt.delimSet {
		if opt.delim == "" {
			delim = 0
		} else {
			delim, _ = utf8.DecodeRuneInString(opt.delim)
		}
	}

	// Resolve timeout. Bash treats -t 0 as a poll: returns 0 if input
	// is immediately available, non-zero otherwise, without waiting and
	// without consuming data. Positive timeouts wrap the read in a
	// context.WithTimeout and propagate the deadline to *os.File via
	// SetReadDeadline so a blocked syscall actually wakes up.
	readCtx := ctx
	pollMode := false
	if opt.timeoutStr != "" {
		secs, err := strconv.ParseFloat(opt.timeoutStr, 64)
		if err != nil || secs < 0 {
			c.Errf("read: -t: invalid timeout %q\n", opt.timeoutStr)
			return builtins.Result{Code: 1}
		}
		if secs == 0 {
			pollMode = true
		} else {
			dur := time.Duration(secs * float64(time.Second))
			var cancel context.CancelFunc
			readCtx, cancel = context.WithTimeout(ctx, dur)
			defer cancel()
			// Best-effort kernel-level read deadline: works for *os.File pipes,
			// which is the typical stdin shape produced by the runner. Other
			// readers fall back to context-driven cancellation between bytes.
			// We pull the deadline from readCtx rather than computing one with
			// time.Now to avoid a second time source: builtins read time only
			// via callCtx.Now (script-start reference) or, for monotonic
			// "from-now" deadlines like this one, via context.WithTimeout's
			// internal clock surfaced through ctx.Deadline().
			if dl, ok := readCtx.Deadline(); ok {
				if f, fok := c.Stdin.(*os.File); fok {
					_ = f.SetReadDeadline(dl)
					defer func() { _ = f.SetReadDeadline(time.Time{}) }()
				}
			}
		}
	}

	noNames := len(args) == 0
	names := args
	if noNames {
		names = []string{"REPLY"}
	}
	for _, n := range names {
		if !validVarName(n) {
			c.Errf("read: %s: not a valid identifier\n", n)
			return builtins.Result{Code: 1}
		}
	}

	// Poll mode (-t 0) returns immediately: 0 if input is available,
	// 142 otherwise. Performed before the prompt and before any read
	// attempt to match bash, which neither prints nor consumes in this
	// case.
	if pollMode {
		return pollAvailable(c)
	}

	// Bash only displays the -p prompt when stdin is an actual terminal,
	// not just any character device (so /dev/null does not trigger it).
	if opt.prompt != "" && stdinIsTerminal(c.Stdin) {
		fmt.Fprint(c.Stderr, opt.prompt)
	}

	if c.Stdin == nil {
		return builtins.Result{Code: 1}
	}

	// -N counts characters (not bytes) and ignores the delimiter. Otherwise
	// the read loop is identical to the default/-n path including escape
	// handling, so we collapse the modes into one helper that takes a
	// character limit and a "ignore delimiter" flag.
	nFixedMode := opt.nBytes >= 0
	charLimit := -1
	switch {
	case nFixedMode:
		charLimit = opt.nBytes
	case opt.nChars >= 0:
		charLimit = opt.nChars
	}
	line, eof, err := readInput(readCtx, c.Stdin, delim, opt.raw, charLimit, nFixedMode)
	timedOut := false
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
			// Bash convention for read -t timeout: 128 + SIGALRM (14) = 142.
			// Bash still assigns any partially-read data on timeout, so we
			// fall through to the assignment path and surface the 142 exit
			// code at the end. Empty-line + timeout is handled below.
			timedOut = true
		} else {
			c.Errf("read: %s\n", err)
			return builtins.Result{Code: 1}
		}
	}

	// Empty result: bash returns 1 on EOF, 142 on timeout, neither
	// performs any assignment.
	if line == "" && (eof || timedOut) {
		if timedOut {
			return builtins.Result{Code: 142}
		}
		return builtins.Result{Code: 1}
	}

	// Determine field values for each variable. Three cases, each matching
	// bash exactly:
	//   1. No NAMEs — raw line is assigned to REPLY, no IFS splitting and
	//      no leading/trailing IFS-whitespace stripping.
	//   2. -N mode (with NAMEs) — the entire read goes to the first NAME;
	//      remaining NAMEs are empty. IFS splitting is skipped.
	//   3. Default / -n mode — POSIX IFS field-splitting across NAMEs.
	values := make([]string, len(names))
	switch {
	case noNames:
		values[0] = line
	case nFixedMode:
		values[0] = line
	default:
		ifs := " \t\n"
		if v, ok := c.GetVar("IFS"); ok {
			ifs = v
		}
		fields := splitIFS(line, ifs, len(names))
		copy(values, fields)
	}

	for i, name := range names {
		if err := c.SetVar(name, values[i]); err != nil {
			c.Errf("read: %s\n", err)
			return builtins.Result{Code: 1}
		}
	}

	if timedOut {
		return builtins.Result{Code: 142}
	}
	if eof {
		return builtins.Result{Code: 1}
	}
	return builtins.Result{}
}

// pollAvailable implements `read -t 0` semantics. Bash returns success
// when a select(2) on stdin reports the descriptor is immediately
// readable (data available or EOF), and 142 otherwise. Without
// requiring the OS-specific select syscall here, we approximate the
// check by setting an in-the-past read deadline on stdin and
// attempting a one-byte read: a successful read (or EOF) means data is
// available; a deadline-exceeded error means the descriptor would have
// blocked.
//
// Two intentional divergences from bash:
//   - On success the byte read is consumed (bash leaves the stream
//     untouched). This matters for callers that follow `read -t 0` with
//     another `read` on the same stream; in practice `read -t 0` is
//     used as a one-shot availability probe.
//   - For non-*os.File readers (e.g. byte buffers in tests) we cannot
//     set a deadline, so we report not-available (142) rather than
//     guess.
func pollAvailable(c *builtins.CallContext) builtins.Result {
	f, ok := c.Stdin.(*os.File)
	if !ok {
		return builtins.Result{Code: 142}
	}
	// Use c.Now (script-start reference) minus an hour to construct a
	// deadline that is reliably in the past, avoiding a direct time.Now
	// call. SetReadDeadline with a past time makes Read return
	// immediately: with data if any is buffered, with
	// os.ErrDeadlineExceeded if the descriptor would have blocked.
	pastDeadline := c.Now.Add(-time.Hour)
	_ = f.SetReadDeadline(pastDeadline)
	defer func() { _ = f.SetReadDeadline(time.Time{}) }()
	var probe [1]byte
	n, err := f.Read(probe[:])
	if errors.Is(err, io.EOF) || n == 1 {
		return builtins.Result{Code: 0}
	}
	return builtins.Result{Code: 142}
}

// stdinIsTerminal reports whether r is an *os.File whose descriptor
// refers to an actual terminal (TTY), as determined by the platform's
// isatty equivalent. Returns false for pipes, regular files, /dev/null,
// and other non-terminal character devices.
func stdinIsTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// validVarName reports whether name is a valid POSIX shell identifier:
// non-empty, starts with a letter or underscore, then letters, digits,
// or underscores.
func validVarName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		first := i == 0
		switch {
		case r == '_':
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case !first && r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// readInput reads from r rune-by-rune until one of:
//   - the delimiter rune is encountered (and ignoreDelim is false)
//   - charLimit characters have been read (when charLimit >= 0)
//   - EOF
//   - context cancellation or timeout
//   - the next character would push the output buffer past MaxReadBytes
//
// The returned line excludes the trailing delimiter. eof reports whether
// the underlying reader reached EOF.
//
// In non-raw mode (raw=false), backslash sequences are interpreted:
//   - "\<delim>" is a line continuation: both runes are dropped and reading
//     continues on the next physical line.
//   - "\<X>" for any other X reduces to X (the backslash is removed, X
//     is appended verbatim and counts as one character).
//
// Reads happen one byte at a time. This is slow but correct: a buffered
// reader would consume bytes past the delimiter and prevent subsequent
// reads from the same underlying stream from observing them.
//
// The MaxReadBytes cap is checked just before each append so a value or
// line exactly at the cap (e.g. read -n 1048576 over 1 MiB of ASCII)
// succeeds; only a write that would exceed the cap is rejected.
func readInput(ctx context.Context, r io.Reader, delim rune, raw bool, charLimit int, ignoreDelim bool) (string, bool, error) {
	var buf []byte
	one := make([]byte, 1)

	readByte := func() (byte, error) {
		for {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			n, err := r.Read(one)
			if n == 1 {
				return one[0], nil
			}
			if err != nil {
				return 0, err
			}
			// n == 0 && err == nil: io.Reader contract permits this; retry.
		}
	}

	readRune := func() (rune, []byte, error) {
		b, err := readByte()
		if err != nil {
			return 0, nil, err
		}
		if b < utf8.RuneSelf {
			return rune(b), []byte{b}, nil
		}
		rb := []byte{b}
		for !utf8.FullRune(rb) && len(rb) < utf8.UTFMax {
			b2, err := readByte()
			if err != nil {
				rn, _ := utf8.DecodeRune(rb)
				return rn, rb, err
			}
			rb = append(rb, b2)
		}
		rn, _ := utf8.DecodeRune(rb)
		return rn, rb, nil
	}

	tryAppend := func(rb []byte) error {
		if len(buf)+len(rb) > MaxReadBytes {
			return fmt.Errorf("input exceeds maximum of %d bytes", MaxReadBytes)
		}
		buf = append(buf, rb...)
		return nil
	}

	runes := 0
	for {
		if charLimit >= 0 && runes >= charLimit {
			return string(buf), false, nil
		}

		rn, rb, err := readRune()
		if errors.Is(err, io.EOF) {
			if len(rb) > 0 {
				if aerr := tryAppend(rb); aerr != nil {
					return string(buf), false, aerr
				}
			}
			return string(buf), true, nil
		}
		if err != nil {
			return string(buf), false, err
		}

		if !ignoreDelim && rn == delim {
			return string(buf), false, nil
		}

		if !raw && rn == '\\' {
			nrn, nrb, nerr := readRune()
			if errors.Is(nerr, io.EOF) {
				// Trailing backslash with no escapee: bash drops the
				// backslash and treats input as terminated by EOF.
				return string(buf), true, nil
			}
			if nerr != nil {
				return string(buf), false, nerr
			}
			if !ignoreDelim && nrn == delim {
				// Line continuation: discard both, keep reading.
				continue
			}
			// Escape: drop the backslash, keep the next rune.
			if aerr := tryAppend(nrb); aerr != nil {
				return string(buf), false, aerr
			}
			runes++
			continue
		}

		if aerr := tryAppend(rb); aerr != nil {
			return string(buf), false, aerr
		}
		runes++
	}
}

// splitIFS splits s into exactly n fields using POSIX read-style field
// splitting based on IFS:
//
//   - Whitespace IFS chars (space, tab, newline) coalesce: a run of
//     consecutive IFS-whitespace counts as one separator.
//   - Non-whitespace IFS chars do not coalesce: each occurrence introduces
//     a separate (possibly empty) field.
//   - Leading and trailing IFS-whitespace is stripped from the input.
//   - When the input has more fields than n, the n-th field absorbs the
//     remainder of the input verbatim, with only trailing IFS-whitespace
//     stripped.
//   - A single trailing non-whitespace IFS character is treated as the
//     start of an empty trailing field that bash silently discards. We
//     recognise this when the absorbed last field contains exactly one
//     non-whitespace IFS character and that character is at the end —
//     in which case it is stripped. With two or more non-whitespace IFS
//     characters in the last field, none are stripped (matches bash:
//     `IFS=: read a b` of `a:b:` → b="b"; of `a:b::` → b="b::").
//   - When the input has fewer fields than n, the missing fields are "".
func splitIFS(s, ifs string, n int) []string {
	if n <= 0 {
		return nil
	}
	isWS := func(r rune) bool { return r == ' ' || r == '\t' || r == '\n' }
	inIFS := func(r rune) bool { return strings.ContainsRune(ifs, r) }
	inIFSWS := func(r rune) bool { return inIFS(r) && isWS(r) }
	inIFSNonWS := func(r rune) bool { return inIFS(r) && !isWS(r) }

	s = trimLeadingFunc(s, inIFSWS)

	fields := make([]string, 0, n)
	for len(fields) < n-1 && s != "" {
		// Read one field: until the first IFS character.
		i := 0
		for i < len(s) {
			r, size := utf8.DecodeRuneInString(s[i:])
			if inIFS(r) {
				break
			}
			i += size
		}
		fields = append(fields, s[:i])
		s = s[i:]

		// Consume one separator: optional ifs-ws + at most one ifs-non-ws + optional ifs-ws.
		s = trimLeadingFunc(s, inIFSWS)
		if s != "" {
			r, size := utf8.DecodeRuneInString(s)
			if inIFSNonWS(r) {
				s = s[size:]
				s = trimLeadingFunc(s, inIFSWS)
			}
		}
	}

	// Last field absorbs the remainder with trailing IFS-whitespace stripped.
	s = trimTrailingFunc(s, inIFSWS)

	// Strip a lone trailing non-ws IFS character that represents an empty
	// trailing field bash drops. Only applies when the absorbed string has
	// exactly one such character; multiple non-ws IFS chars indicate the
	// absorbed remainder spans multiple separator-bounded sub-fields and
	// must be preserved verbatim.
	nonWSIFSCount := 0
	for _, r := range s {
		if inIFSNonWS(r) {
			nonWSIFSCount++
		}
	}
	if nonWSIFSCount == 1 {
		if r, size := utf8.DecodeLastRuneInString(s); inIFSNonWS(r) {
			s = s[:len(s)-size]
		}
	}

	fields = append(fields, s)

	for len(fields) < n {
		fields = append(fields, "")
	}
	return fields
}

func trimLeadingFunc(s string, pred func(rune) bool) string {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if !pred(r) {
			return s[i:]
		}
		i += size
	}
	return ""
}

func trimTrailingFunc(s string, pred func(rune) bool) string {
	for len(s) > 0 {
		r, size := utf8.DecodeLastRuneInString(s)
		if !pred(r) {
			return s
		}
		s = s[:len(s)-size]
	}
	return s
}
