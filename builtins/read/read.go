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

	readCtx := ctx
	if opt.timeoutStr != "" {
		secs, err := strconv.ParseFloat(opt.timeoutStr, 64)
		if err != nil || secs < 0 {
			c.Errf("read: -t: invalid timeout %q\n", opt.timeoutStr)
			return builtins.Result{Code: 1}
		}
		dur := time.Duration(secs * float64(time.Second))
		var cancel context.CancelFunc
		readCtx, cancel = context.WithTimeout(ctx, dur)
		defer cancel()
		// Best-effort kernel-level read deadline: works for *os.File pipes,
		// which is the typical stdin shape produced by the runner. Other
		// readers fall back to context-driven cancellation between bytes.
		if f, ok := c.Stdin.(*os.File); ok {
			_ = f.SetReadDeadline(time.Now().Add(dur))
			defer func() { _ = f.SetReadDeadline(time.Time{}) }()
		}
	}

	names := args
	if len(names) == 0 {
		names = []string{"REPLY"}
	}
	for _, n := range names {
		if !validVarName(n) {
			c.Errf("read: %s: not a valid identifier\n", n)
			return builtins.Result{Code: 1}
		}
	}

	// Bash only displays the prompt when stdin is a terminal. In this
	// non-interactive shell stdin is always a pipe, so the prompt is
	// effectively suppressed; the check is here for parity in case stdin
	// is ever wired to a TTY by a calling embedder.
	if opt.prompt != "" && stdinIsTTY(c.Stdin) {
		fmt.Fprint(c.Stderr, opt.prompt)
	}

	if c.Stdin == nil {
		return builtins.Result{Code: 1}
	}
	line, eof, err := readInput(readCtx, c.Stdin, delim, opt.raw, opt.nChars, opt.nBytes)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
			// Bash convention for read -t timeout: 128 + SIGALRM (14) = 142.
			return builtins.Result{Code: 142}
		}
		c.Errf("read: %s\n", err)
		return builtins.Result{Code: 1}
	}

	// Bash returns 1 with no assignment when EOF is hit before any data.
	if eof && line == "" {
		return builtins.Result{Code: 1}
	}

	ifs := " \t\n"
	if v, ok := c.GetVar("IFS"); ok {
		ifs = v
	}
	fields := splitIFS(line, ifs, len(names))

	for i, name := range names {
		var val string
		if i < len(fields) {
			val = fields[i]
		}
		if err := c.SetVar(name, val); err != nil {
			c.Errf("read: %s\n", err)
			return builtins.Result{Code: 1}
		}
	}

	if eof {
		return builtins.Result{Code: 1}
	}
	return builtins.Result{}
}

// stdinIsTTY reports whether r is an *os.File backed by a character
// device (terminal). False for pipes, regular files, byte buffers, etc.
func stdinIsTTY(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
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

// readInput reads from r byte-by-byte until one of:
//   - the delimiter rune is encountered (default and -n modes)
//   - nChars characters have been read (-n mode)
//   - nBytes bytes have been read (-N mode)
//   - EOF
//   - context cancellation or timeout
//   - MaxReadBytes total bytes (hard memory cap)
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
func readInput(ctx context.Context, r io.Reader, delim rune, raw bool, nChars, nBytes int) (string, bool, error) {
	var buf []byte
	bytes := 0
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

	// -N (byte) mode: read up to nBytes raw bytes, ignoring delimiter and escapes.
	if nBytes >= 0 {
		for bytes < nBytes && bytes < MaxReadBytes {
			b, err := readByte()
			if errors.Is(err, io.EOF) {
				return string(buf), true, nil
			}
			if err != nil {
				return string(buf), false, err
			}
			buf = append(buf, b)
			bytes++
		}
		return string(buf), false, nil
	}

	// Default and -n modes: rune-by-rune with delimiter and escape handling.
	runes := 0
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

	for {
		if bytes >= MaxReadBytes {
			return string(buf), false, fmt.Errorf("input exceeds maximum of %d bytes", MaxReadBytes)
		}
		if nChars >= 0 && runes >= nChars {
			return string(buf), false, nil
		}

		rn, rb, err := readRune()
		if errors.Is(err, io.EOF) {
			if len(rb) > 0 {
				buf = append(buf, rb...)
				bytes += len(rb)
			}
			return string(buf), true, nil
		}
		if err != nil {
			return string(buf), false, err
		}

		if rn == delim {
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
			if nrn == delim {
				// Line continuation: discard both, keep reading.
				bytes += len(rb) + len(nrb)
				continue
			}
			// Escape: drop the backslash, keep the next rune.
			buf = append(buf, nrb...)
			bytes += len(rb) + len(nrb)
			runes++
			continue
		}

		buf = append(buf, rb...)
		bytes += len(rb)
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
