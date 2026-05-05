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
	"math"
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
	// Bash documents `read [OPTION]... [NAME...]` and stops parsing flags
	// once the first NAME is seen — `read var -n 1` over `abc` assigns
	// var="abc" then errors on `-n` as an invalid identifier rather than
	// re-interpreting `-n 1` as a length flag. Disable pflag's default
	// interspersed-flag parsing so later -tokens remain NAMEs.
	fs.SetInterspersed(false)

	help := fs.BoolP("help", "h", false, "print usage and exit")
	raw := fs.BoolP("raw", "r", false, "do not interpret backslashes")
	prompt := fs.StringP("prompt", "p", "", "print PROMPT to stderr before reading")
	delim := fs.StringP("delim", "d", "", "use the first character of DELIM as the line terminator (empty = NUL)")
	timeoutStr := fs.StringP("timeout", "t", "", "time out after TIMEOUT seconds (decimal allowed)")

	// -n and -N must observe last-set-wins parse order to match bash:
	// `read -n 1 -N 2` reads 2 chars (-N is later) while `read -N 2 -n 1`
	// reads 1 char (-n is later). pflag tracks "was this flag set" via
	// Changed() but does not preserve relative parse order between two
	// flags, so we install a custom pflag.Value that records the order
	// each time Set() fires.
	nChars := -1
	nBytes := -1
	nCharsOrder := 0
	nBytesOrder := 0
	flagSeq := 0
	fs.VarP(&orderedIntValue{val: &nChars, order: &nCharsOrder, seq: &flagSeq}, "nchars", "n",
		fmt.Sprintf("return after reading at most NCHARS characters (max %d)", MaxReadBytes))
	fs.VarP(&orderedIntValue{val: &nBytes, order: &nBytesOrder, seq: &flagSeq}, "nbytes", "N",
		fmt.Sprintf("return after reading exactly NBYTES bytes (max %d), ignoring delimiters", MaxReadBytes))

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
			raw:         *raw,
			prompt:      *prompt,
			delim:       *delim,
			delimSet:    fs.Changed("delim"),
			nChars:      nChars,
			nBytes:      nBytes,
			nCharsOrder: nCharsOrder,
			nBytesOrder: nBytesOrder,
			timeoutStr:  *timeoutStr,
		})
	}
}

// orderedIntValue is a pflag.Value implementation that records, in
// addition to the parsed integer value, the relative parse-order
// position at which Set() was called. Sharing a single seq counter
// between two flags lets the handler determine which of `-n` / `-N`
// was specified last, which bash uses to break the tie when both
// length flags appear.
type orderedIntValue struct {
	val   *int
	order *int // updated to *seq on each Set; 0 means "not set"
	seq   *int // shared monotonic counter, incremented on each Set
}

func (v *orderedIntValue) String() string {
	if v.val == nil {
		return "-1"
	}
	return strconv.Itoa(*v.val)
}

func (v *orderedIntValue) Set(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil {
		return err
	}
	*v.val = n
	*v.seq++
	*v.order = *v.seq
	return nil
}

func (v *orderedIntValue) Type() string { return "int" }

type runOpts struct {
	raw         bool
	prompt      string
	delim       string
	delimSet    bool
	nChars      int
	nBytes      int
	nCharsOrder int
	nBytesOrder int
	timeoutStr  string
}

func run(ctx context.Context, c *builtins.CallContext, args []string, opt runOpts) builtins.Result {
	if c.SetVar == nil || c.GetVar == nil {
		c.Errf("read: variable access is not available in this context\n")
		return builtins.Result{Code: 2}
	}

	// Resolve which of -n / -N applies: bash uses last-set-wins when both
	// are present. If neither was set both order values are 0; the equal
	// comparison falls through to the "no length limit" path.
	nFixedMode := opt.nBytesOrder > opt.nCharsOrder
	switch {
	case nFixedMode:
		if opt.nBytes < 0 {
			c.Errf("read: -N: count must be non-negative\n")
			return builtins.Result{Code: 1}
		}
		if opt.nBytes > MaxReadBytes {
			c.Errf("read: -N: count exceeds maximum of %d\n", MaxReadBytes)
			return builtins.Result{Code: 1}
		}
	case opt.nCharsOrder > 0:
		if opt.nChars < 0 {
			c.Errf("read: -n: count must be non-negative\n")
			return builtins.Result{Code: 1}
		}
		if opt.nChars > MaxReadBytes {
			c.Errf("read: -n: count exceeds maximum of %d\n", MaxReadBytes)
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

	// Bash explicitly documents that `-t TIMEOUT` has no effect when
	// stdin is a regular file: regular-file reads don't block (the
	// kernel returns data or EOF immediately), so a timeout is
	// meaningless and bash skips its alarm path entirely. Setting up
	// context.WithTimeout here would cause `read -t 0.001 var <
	// bigfile` to spuriously return 142 with a partial assignment
	// when the timer expires while we're scanning a large line.
	// Detect the regular-file shape via fstat and skip the positive-
	// timeout setup; the syntactic validation of the -t value still
	// runs (matching bash, which rejects `read -t abc` even when
	// stdin is a regular file).
	stdinIsRegularFile := false
	if f, fok := c.Stdin.(*os.File); fok {
		if info, err := f.Stat(); err == nil && info.Mode().IsRegular() {
			stdinIsRegularFile = true
		}
	}

	// Resolve timeout. Bash treats -t 0 as a poll: returns 0 if input
	// is immediately available, non-zero otherwise, without waiting and
	// without consuming data. Positive timeouts wrap the read in a
	// context.WithTimeout (skipped for regular-file stdin per the
	// rule above). We also honour any deadline already on the parent
	// context (e.g. from interp.MaxExecutionTime or a CLI --timeout)
	// by propagating it to *os.File stdin via SetReadDeadline so a
	// blocking read syscall actually wakes up when the run as a
	// whole is cancelled, not only when -t fires.
	readCtx := ctx
	pollMode := false
	if opt.timeoutStr != "" {
		secs, ok := parseReadTimeout(opt.timeoutStr)
		if !ok {
			c.Errf("read: %s: invalid timeout specification\n", opt.timeoutStr)
			return builtins.Result{Code: 1}
		}
		switch {
		case secs == 0:
			pollMode = true
		case stdinIsRegularFile:
			// Bash: -t is ignored on regular files. Run the read
			// without a timeout-derived deadline.
		default:
			dur := time.Duration(secs * float64(time.Second))
			var cancel context.CancelFunc
			readCtx, cancel = context.WithTimeout(ctx, dur)
			defer cancel()
		}
	}
	// Best-effort kernel-level cancellation for blocking stdin reads.
	// Works for *os.File pipes (the typical runner-supplied stdin shape).
	// Two layers:
	//
	//   1. Kernel deadline: when readCtx has a deadline (-t or
	//      MaxExecutionTime), install it via SetReadDeadline so the
	//      Read syscall wakes up on timeout. We pull the deadline from
	//      readCtx rather than computing one with time.Now to avoid a
	//      second time source — context.WithTimeout's internal clock,
	//      surfaced via Deadline(), is authoritative. When both -t and
	//      an inherited deadline apply, Deadline() returns the earlier
	//      of the two, which is the correct fused deadline.
	//
	//   2. Watchdog goroutine: any cancellable readCtx (deadline OR
	//      bare parent.Cancel()) is mirrored to the kernel-level
	//      deadline by setting it to a past time on ctx.Done(), forcing
	//      a blocked Read to unblock immediately even when no timeout
	//      is configured.
	//
	// SetReadDeadline returns "file type does not support deadline" for
	// regular files and /dev/null. Those types don't block on Read, so
	// the failure is harmless. For pollable types where Read CAN block
	// (TTYs, sockets, named pipes via the runtime poller, etc.) the
	// call generally succeeds. When kernelCancel is established, we
	// stay on the direct read path — which crucially does NOT prefetch
	// past the consumer's request and therefore preserves bytes for
	// subsequent reads on the shared stdin (e.g. `while read line`).
	// Only when neither the *os.File assertion nor SetReadDeadline
	// support holds do we fall back to the request-response goroutine
	// path in readInput.
	kernelCancel := false
	if c.Stdin != nil && readCtx.Done() != nil {
		if f, fok := c.Stdin.(*os.File); fok {
			// Probe SetReadDeadline support by attempting to clear.
			// This is a no-op for descriptors that already have no
			// deadline and a quick failure for unsupported types.
			if err := f.SetReadDeadline(time.Time{}); err == nil {
				if dl, ok := readCtx.Deadline(); ok {
					_ = f.SetReadDeadline(dl)
				}
				stop := make(chan struct{})
				watchdogDone := make(chan struct{})
				go func() {
					defer close(watchdogDone)
					select {
					case <-readCtx.Done():
						// Past time; any future blocked Read on f
						// returns ErrDeadlineExceeded immediately.
						_ = f.SetReadDeadline(time.Unix(1, 0))
					case <-stop:
					}
				}()
				defer func() {
					close(stop)
					<-watchdogDone
					_ = f.SetReadDeadline(time.Time{})
				}()
				kernelCancel = true
			}
		}
	}

	// Bash validates identifiers per-assignment but with one special
	// case: if the FIRST NAME is invalid, the command aborts WITHOUT
	// reading any input, leaving the stream untouched for the next
	// read. Any subsequent invalid NAME (positions 2..n) only fires
	// after the read has already happened — earlier valid names are
	// assigned, then the error stops further assignment. Verified
	// empirically against bash 5.2.0:
	//   `printf 'a\nb\n' | { read 1bad; read next; }` → next="a"
	//     (first NAME invalid, no read consumed).
	//   `printf 'a b c\n' | { read x 2bad z; }` → x="a", error,
	//     z unset; next read sees the second record.
	// Run the upfront leading-name check before readInput so the
	// stream stays intact in the leading-invalid case; defer the
	// rest to the assignment loop below.
	noNames := len(args) == 0
	names := args
	if noNames {
		names = []string{"REPLY"}
	}
	if !noNames && !validVarName(names[0]) {
		c.Errf("read: `%s': not a valid identifier\n", names[0])
		return builtins.Result{Code: 1}
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

	// -N counts characters (not bytes) and ignores the delimiter. The
	// length-flag selection (last-set-wins between -n and -N) was
	// computed at the top of run() into nFixedMode.
	charLimit := -1
	switch {
	case nFixedMode:
		charLimit = opt.nBytes
	case opt.nCharsOrder > 0:
		charLimit = opt.nChars
	}
	// readInput needs the goroutine-based fallback only when the
	// context is cancellable AND we couldn't wire kernel-level
	// cancellation through SetReadDeadline above. Staying on the
	// direct Read path whenever possible is critical for correctness:
	// the goroutine path cannot avoid leaving a leaked goroutine
	// blocked inside Read on cancellation, which on a shared stdin
	// (the typical `while read line` shape) would consume bytes that
	// the next read iteration must observe. Kernel cancellation lets
	// us interrupt the blocked Read directly without prefetching.
	needsGoroutinePoll := readCtx.Done() != nil && !kernelCancel
	if c.Stdin == nil {
		// Nil stdin is treated as immediate EOF: fall through to the
		// assignment loop below so each NAME is cleared to "" before
		// the function returns 1, matching the EOF behaviour of bash
		// (and the explicit empty-line + EOF path further down).
		needsGoroutinePoll = false
	}
	line, eof, err := readInput(readCtx, c.Stdin, delim, opt.raw, charLimit, nFixedMode, needsGoroutinePoll)
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

	// On empty result + EOF or timeout, bash still assigns "" to every
	// requested NAME (clearing any prior value) before surfacing the
	// non-zero exit code. Falling through to the assignment loop with
	// the zero-valued `values` slice produces the same result for us.

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

	// Assign each variable in order. Bash validates identifiers
	// per-assignment: when an invalid name is encountered, the error
	// fires immediately and remaining variables are left untouched, but
	// any earlier valid names have already been assigned (and the read
	// itself has already consumed input). Match that behaviour.
	for i, name := range names {
		if !validVarName(name) {
			c.Errf("read: `%s': not a valid identifier\n", name)
			return builtins.Result{Code: 1}
		}
		if err := c.SetVar(name, values[i]); err != nil {
			c.Errf("read: %s\n", err)
			// Total-storage exhaustion is the same script-aborting
			// resource-cap violation that AST-level assignment treats
			// as fatal. Surface it via Result.Exiting so the runner
			// stops the script rather than continuing past the cap.
			if errors.Is(err, builtins.ErrVarStorageExceeded) {
				return builtins.Result{Code: 1, Exiting: true}
			}
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

// pollAvailable implements `read -t 0` semantics. Bash uses select(2)
// to report whether stdin is immediately readable without consuming
// any data; we use the platform-specific pollInputNonConsuming for
// the same effect on Unix, and fall back to a consume-based probe
// where that helper is unsupported.
//
// Returns:
//   - 0 — input is immediately available (data buffered to read).
//   - 1 — would block, no data buffered, or stdin is not pollable
//     (e.g. byte buffer). Bash uses exit code 1 for `-t 0` "no data
//     available", reserving 142 (128+SIGALRM) for the positive-
//     timeout case where an alarm actually fired.
//
// The fallback (Windows / non-File readers) consumes one byte on
// success — an intentional divergence from bash documented inline.
// Implementing non-consuming poll on Windows is a follow-up.
func pollAvailable(c *builtins.CallContext) builtins.Result {
	f, ok := c.Stdin.(*os.File)
	if !ok {
		return builtins.Result{Code: 1}
	}
	// Preferred path: non-consuming poll via the platform syscall.
	if avail, supported := pollInputNonConsuming(f.Fd()); supported {
		if avail {
			return builtins.Result{Code: 0}
		}
		return builtins.Result{Code: 1}
	}

	// Fallback: set a deadline in the past and try a one-byte read.
	// On success the byte is consumed; subsequent reads on the same
	// stream will not see it. Documented as a Windows-only divergence.
	// We use c.Now.Add(-time.Hour) to construct a deadline that is
	// reliably in the past while avoiding a direct time.Now call.
	//
	// IMPORTANT: SetReadDeadline can fail when the descriptor does
	// not support deadlines (e.g. a Windows console handle, some
	// character devices). If that happens we cannot safely call Read
	// — without an in-the-past deadline, Read would block indefinitely
	// when no input is buffered, which violates the documented `-t 0`
	// "poll, never wait" contract and would hang the script. Return
	// 1 (no data available) immediately in that case, matching the
	// same "not pollable" path taken when stdin isn't *os.File.
	pastDeadline := c.Now.Add(-time.Hour)
	if err := f.SetReadDeadline(pastDeadline); err != nil {
		return builtins.Result{Code: 1}
	}
	defer func() { _ = f.SetReadDeadline(time.Time{}) }()
	var probe [1]byte
	n, err := f.Read(probe[:])
	if errors.Is(err, io.EOF) || n == 1 {
		return builtins.Result{Code: 0}
	}
	return builtins.Result{Code: 1}
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

// parseReadTimeout parses bash's -t TIMEOUT syntax: a non-negative
// decimal number, optionally with a fractional part. Bash rejects
// scientific notation, NaN, Inf, and negatives — verified empirically
// by the "invalid timeout specification" error each of those produces.
// Returns the parsed seconds and ok=true on success, ok=false otherwise.
//
// The function also rejects values whose nanosecond representation
// would overflow time.Duration (a signed int64), so the conversion to
// time.Duration in the caller cannot wrap into a negative duration
// that would fire an immediate timeout.
func parseReadTimeout(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	// Bash accepts an optional leading `+` on -t TIMEOUT (verified
	// empirically: `read -t +1 v` and `read -t +0 v` both work in
	// bash 5.2). Strip it before the digit-only filter so the rest
	// of the parser doesn't have to special-case the sign. A bare
	// `+` (no digits after) is rejected by the seenDigit check.
	if len(s) > 0 && s[0] == '+' {
		s = s[1:]
	}
	if s == "" {
		return 0, false
	}
	// Accept only [0-9] and at most one '.', matching bash's parser
	// which excludes minus signs, exponents, and special tokens
	// (NaN, Inf, hex, etc.).
	seenDot := false
	seenDigit := false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			seenDigit = true
		case r == '.' && !seenDot:
			seenDot = true
		default:
			return 0, false
		}
	}
	if !seenDigit {
		return 0, false
	}
	secs, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	// Defensive: NaN/Inf are excluded by the char filter above, but
	// guard anyway in case the float parser would accept some
	// surprising input.
	if math.IsNaN(secs) || math.IsInf(secs, 0) {
		return 0, false
	}
	// Bound by what time.Duration can represent without wrapping.
	maxSecs := float64(math.MaxInt64) / float64(time.Second)
	if secs > maxSecs {
		return 0, false
	}
	return secs, true
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
func readInput(ctx context.Context, r io.Reader, delim rune, raw bool, charLimit int, ignoreDelim, useGoroutinePoll bool) (string, bool, error) {
	if r == nil {
		// No stdin (default runner configuration): treat as immediate
		// EOF. The caller's assignment loop will clear each requested
		// NAME to "", matching bash's behaviour when read hits EOF
		// before any data, and the empty-line + EOF return surfaces
		// exit code 1.
		return "", true, nil
	}
	var buf []byte

	// consumed counts every byte we read from r, including bytes that
	// are later discarded (line continuations, the backslash before an
	// escape, runes that exceed charLimit). It is the primary memory /
	// CPU bound for this builtin: an attacker-controlled stdin made of
	// repeated backslash-newline pairs would never grow buf and would
	// otherwise drain unbounded input. Capping consumed independently
	// of buf size guarantees the builtin always returns within
	// MaxReadBytes of input regardless of how much of that input ends
	// up assigned to a variable.
	//
	// The check uses `>` rather than `>=` so a record of exactly
	// MaxReadBytes data bytes can still observe its delimiter or EOF on
	// the next read attempt — only when consumed has already crossed
	// the cap (i.e. the previous byte put us at the boundary and we'd
	// be reading a non-terminator) does the error fire.
	consumed := 0

	var readByte func() (byte, error)
	if useGoroutinePoll {
		// Goroutine-based fallback used only when the context is
		// cancellable AND kernel-level cancellation via
		// SetReadDeadline was unavailable (e.g. non-*os.File stdin or
		// a specialised character device that rejects deadlines).
		//
		// IMPORTANT: this path uses a request-response pattern, not a
		// prefetch loop. A naive prefetch would call r.Read in a tight
		// loop and queue bytes ahead of the consumer; on a shared
		// stdin (the typical `while read line` shape) the producer
		// would consume bytes past the delimiter and the next read
		// iteration would see EOF or skip data. Instead, the producer
		// performs exactly one r.Read per request from the consumer
		// and sends the result on an unbuffered channel — bytes are
		// never read speculatively.
		//
		// Trade-off: if Read is stuck in the kernel when ctx fires,
		// the producer can't be force-cancelled. It will eventually
		// unblock when stdin produces data or closes; AT MOST ONE
		// byte is lost in that scenario (the byte the producer was
		// reading at the moment of cancellation). The leaked
		// goroutine exits via the stop signal once Read returns.
		type byteResult struct {
			b   byte
			err error
		}
		reqCh := make(chan struct{})
		resCh := make(chan byteResult)
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			var rbuf [1]byte
			// savedErr defers an error that arrived in the same Read
			// call as a successful byte (n=1 with err != nil — io.Reader
			// is allowed to return data and io.EOF together). The byte
			// is delivered this turn; the error surfaces on the next
			// readByte request, mirroring the direct-read path's
			// behaviour of returning the byte first and the EOF on the
			// subsequent Read.
			var savedErr error
			for {
				select {
				case <-stop:
					return
				case _, ok := <-reqCh:
					if !ok {
						return
					}
				}
				if savedErr != nil {
					select {
					case resCh <- byteResult{0, savedErr}:
					case <-stop:
					}
					return
				}
				var b byte
				var sendErr error
				gotData := false
				for {
					n, err := r.Read(rbuf[:])
					if n == 1 {
						b = rbuf[0]
						gotData = true
						if err != nil {
							savedErr = err
						}
						break
					}
					if err != nil {
						sendErr = err
						break
					}
					// n=0, err=nil per io.Reader contract: retry.
					// Check stop so cancellation isn't ignored if a
					// misbehaving reader spins this loop.
					select {
					case <-stop:
						return
					default:
					}
				}
				select {
				case resCh <- byteResult{b, sendErr}:
				case <-stop:
					return
				}
				if !gotData && sendErr != nil {
					// Pure error result delivered; nothing more to do.
					return
				}
			}
		}()
		readByte = func() (byte, error) {
			if consumed > MaxReadBytes {
				return 0, fmt.Errorf("input exceeds maximum of %d bytes", MaxReadBytes)
			}
			select {
			case reqCh <- struct{}{}:
			case <-ctx.Done():
				return 0, ctx.Err()
			}
			select {
			case res := <-resCh:
				if res.err != nil {
					return 0, res.err
				}
				consumed++
				return res.b, nil
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}
	} else {
		// Direct path: either no deadline at all, or SetReadDeadline
		// has installed a kernel-level deadline that will surface as
		// os.ErrDeadlineExceeded from r.Read. ctx.Err() between
		// retries handles the io.Reader-contract case of n=0,err=nil.
		one := make([]byte, 1)
		readByte = func() (byte, error) {
			if consumed > MaxReadBytes {
				return 0, fmt.Errorf("input exceeds maximum of %d bytes", MaxReadBytes)
			}
			for {
				if err := ctx.Err(); err != nil {
					return 0, err
				}
				n, err := r.Read(one)
				if n == 1 {
					consumed++
					return one[0], nil
				}
				if err != nil {
					return 0, err
				}
				// n == 0 && err == nil: io.Reader contract permits this; retry.
			}
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

		// Bash strips embedded NUL bytes from the assigned value. The
		// only case where NUL is meaningful is as the delimiter
		// (`read -d ''`), and that's handled by the delim check above.
		// In any other configuration — including -N mode with -d '' —
		// bash discards NULs without counting them toward charLimit.
		if rn == 0 {
			continue
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
			// Backslash-newline is always a line continuation in non-raw
			// mode, regardless of the active delimiter or whether -N is
			// in effect (verified empirically against bash 5.3). With a
			// custom -d delimiter, `\<delim>` (where delim != '\n') is
			// instead an escape that preserves the literal delimiter:
			// `printf 'a\,b,c' | read -d , x` assigns `a,b`. Both the
			// continuation and the escape branches drop the backslash;
			// the difference is whether the next rune is appended.
			if nrn == '\n' {
				continue
			}
			// NUL bytes are always stripped from the assigned value
			// (the general "bash silently discards NULs unless they
			// are the delimiter, and even with `-d ''` does not
			// store them" rule from earlier in the loop). For a
			// `\<NUL>` pair under `-d ''` this means we drop both
			// the backslash and the NUL and keep reading — matching
			// bash, which treats `\<NUL>` as if the backslash
			// merely escapes a NUL that gets stripped.
			if nrn == 0 {
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
