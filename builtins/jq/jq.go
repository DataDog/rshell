// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package jq implements the jq builtin command.
//
// jq — command-line JSON processor
//
// Usage: jq [OPTION]... FILTER [FILE]...
//
// Apply FILTER to each JSON value parsed from FILE(s) (or standard input
// when no FILE is given) and print the results.
//
// The filter language and output formatting follow the jq manual at
// https://jqlang.org/manual/. The expression engine is the fastjq library
// (Go module github.com/brianfloersch/fastjq, hosted internally at
// github.com/DataDog/fastjq) — a zero-allocation, pure-CPU jq engine that
// operates directly on JSON bytes. fastjq does not access the filesystem
// or the network; the only I/O performed by this builtin is reading input
// documents through the shell's sandboxed callCtx.OpenFile.
//
// Accepted flags:
//
//	-c, --compact-output    Compact output (single line per value).
//	-r, --raw-output        Print JSON-string outputs without surrounding quotes.
//	-j, --join-output       Like -r, but no newline between outputs.
//	-n, --null-input        Do not read input; run filter once with input=null.
//	-s, --slurp             Read all inputs into one array; run filter once.
//	-R, --raw-input         Each input line is a JSON string.
//	-a, --ascii-output      Escape non-ASCII characters as \uXXXX.
//	-S, --sort-keys         Sort object fields by key in output.
//	-e, --exit-status       Set exit code based on truthiness of outputs.
//	-h, --help              Print this usage message and exit.
//
// Exit codes:
//
//	0  Success.
//	1  Runtime error (unknown flag, file not found, invalid JSON, runtime
//	   filter error, or with -e all outputs were null/false/absent).
//	2  Usage error (missing FILTER, FILTER too large).
//	3  Compile error (FILTER could not be parsed).
//
// Line endings:
//
//	In --raw-input mode, lines are split on LF. Embedded CR before LF is
//	stripped (matching bufio.ScanLines and real jq). Lone CR (classic-Mac
//	convention) is treated as part of the line, matching real jq behaviour.
//
// Memory safety:
//
//	Per-source input is hard-capped at MaxStreamBytes (64 MiB). When --slurp
//	is given the same cap applies to the aggregate input across all files.
//	Each line in --raw-input mode is capped at MaxLineBytes (1 MiB). The
//	FILTER expression itself is capped at MaxFilterBytes (64 KiB). The JSON
//	re-emitter caps its recursion at maxEmitDepth (256) and its output size
//	at MaxStreamBytes. All loops check ctx.Err() at every iteration to
//	honour the shell's execution timeout and graceful cancellation. fastjq's
//	own fuzz suite guarantees the engine never panics on any byte input.
package jq

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"unicode/utf8"

	"github.com/brianfloersch/fastjq"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the jq builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "jq",
	Description: "command-line JSON processor",
	MakeFlags:   registerFlags,
}

// MaxStreamBytes caps the bytes read from any single input source (file
// or stdin). Larger inputs cause the command to fail with an error
// instead of allocating unbounded memory.
const MaxStreamBytes = 64 << 20 // 64 MiB

// MaxLineBytes caps a single line in --raw-input mode.
const MaxLineBytes = 1 << 20 // 1 MiB

// MaxFilterBytes caps the FILTER expression itself.
const MaxFilterBytes = 1 << 16 // 64 KiB

const readChunk = 32 * 1024

// options holds the parsed flag state.
type options struct {
	compact   bool
	rawOutput bool
	joinOut   bool
	nullInput bool
	slurp     bool
	rawInput  bool
	ascii     bool
	sortKeys  bool
	exitStat  bool
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := fs.BoolP("help", "h", false, "print usage and exit")
	compact := fs.BoolP("compact-output", "c", false, "compact instead of pretty-printed output")
	raw := fs.BoolP("raw-output", "r", false, "output strings without JSON quoting")
	join := fs.BoolP("join-output", "j", false, "like -r, but suppress trailing newlines")
	nullIn := fs.BoolP("null-input", "n", false, "do not read input; use null instead")
	slurp := fs.BoolP("slurp", "s", false, "read all inputs into an array; run filter once")
	rawIn := fs.BoolP("raw-input", "R", false, "each line of input is a JSON string")
	ascii := fs.BoolP("ascii-output", "a", false, "escape non-ASCII characters as \\uXXXX")
	sortK := fs.BoolP("sort-keys", "S", false, "sort object keys in output")
	exitSt := fs.BoolP("exit-status", "e", false, "set exit status based on output truthiness")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: jq [OPTION]... FILTER [FILE]...\n")
			callCtx.Out("Apply FILTER to JSON input from FILE(s) or standard input.\n")
			callCtx.Out("With no FILE, or when FILE is -, read standard input.\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}

		if len(args) == 0 {
			callCtx.Errf("jq: no filter given\n")
			return builtins.Result{Code: 2}
		}

		filter := args[0]
		files := args[1:]

		if len(filter) > MaxFilterBytes {
			callCtx.Errf("jq: filter too large (%d bytes, max %d)\n", len(filter), MaxFilterBytes)
			return builtins.Result{Code: 2}
		}

		// -j implies -r per jq manual.
		opts := options{
			compact:   *compact,
			rawOutput: *raw || *join,
			joinOut:   *join,
			nullInput: *nullIn,
			slurp:     *slurp,
			rawInput:  *rawIn,
			ascii:     *ascii,
			sortKeys:  *sortK,
			exitStat:  *exitSt,
		}

		prog, err := fastjq.Compile(filter)
		if err != nil {
			callCtx.Errf("jq: compile error: %s\n", err.Error())
			return builtins.Result{Code: 3}
		}

		st := &runState{ctx: ctx, callCtx: callCtx, opts: opts, prog: prog}

		if err := st.run(files); err != nil {
			if builtins.IsBrokenPipe(err) {
				// Match other builtins: silently terminate when the
				// downstream consumer closed the pipe.
				return st.finalResult()
			}
			if !errors.Is(err, errAlreadyReported) {
				callCtx.Errf("jq: %s\n", err.Error())
			}
			st.failed = true
		}

		return st.finalResult()
	}
}

// errAlreadyReported is a sentinel returned by helpers that have already
// written a specific error message to stderr. The top-level handler does
// not double-print such errors.
var errAlreadyReported = errors.New("error already reported")

// runState bundles the per-invocation execution context.
type runState struct {
	ctx     context.Context
	callCtx *builtins.CallContext
	opts    options
	prog    *fastjq.Program

	// failed is true when at least one runtime error occurred.
	failed bool

	// emittedTruthy reports whether at least one output was a value
	// other than null/false. Used to compute -e exit status.
	emittedTruthy bool

	// slurpTotal tracks the cumulative bytes already accumulated across
	// the slurp helpers so we can short-circuit before exceeding the cap.
	slurpTotal int64
}

func (s *runState) finalResult() builtins.Result {
	// -e: exit 1 when no output produced or every output was null/false.
	if s.failed || (s.opts.exitStat && !s.emittedTruthy) {
		return builtins.Result{Code: 1}
	}
	return builtins.Result{}
}

// run dispatches based on input mode flags.
func (s *runState) run(files []string) error {
	if s.opts.nullInput {
		return s.runOne([]byte("null"))
	}

	if len(files) == 0 {
		files = []string{"-"}
	}

	if s.opts.slurp && s.opts.rawInput {
		// Slurp + raw input: read all input as a single JSON string.
		return s.processRawSlurpFiles(files)
	}
	if s.opts.slurp {
		// Slurp: collect every JSON value from every file into one array.
		return s.processJSONSlurpFiles(files)
	}
	if s.opts.rawInput {
		// Raw input: each line is treated as a JSON string.
		for _, f := range files {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			if err := s.processRawLines(f); err != nil {
				return err
			}
		}
		return nil
	}
	// Default: stream JSON values from every file.
	for _, f := range files {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if err := s.processJSONStream(f); err != nil {
			return err
		}
	}
	return nil
}

// openSource returns a bounded reader for one input source.
// The returned closer must be called when the caller is finished.
func (s *runState) openSource(file string) (io.ReadCloser, error) {
	if file == "-" {
		if s.callCtx.Stdin == nil {
			return io.NopCloser(bytes.NewReader(nil)), nil
		}
		return io.NopCloser(s.callCtx.Stdin), nil
	}
	rc, err := s.callCtx.OpenFile(s.ctx, file, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	return rc, nil
}

// readAllBounded reads from rc until EOF or until the cap is exceeded.
// It is the caller's responsibility to close rc. Returns errCapExceeded
// when the cap fires, so callers can distinguish "input too large" from
// generic I/O errors.
func readAllBounded(ctx context.Context, rc io.Reader, limit int64) ([]byte, error) {
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, readChunk)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, err := rc.Read(chunk)
		if n > 0 {
			if int64(len(buf)+n) > limit {
				return nil, capExceededError(limit)
			}
			buf = append(buf, chunk[:n]...)
		}
		if errors.Is(err, io.EOF) {
			return buf, nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// decodeJSONValues reads whitespace-separated JSON values from one source
// and invokes fn for each. Used by both the streaming and slurp paths.
//
// The byte cap applies per-source. When --slurp is in effect, the caller
// is responsible for tracking the aggregate budget across files via
// runState.slurpTotal.
func (s *runState) decodeJSONValues(file string, fn func(raw []byte) error) error {
	rc, err := s.openSource(file)
	if err != nil {
		s.reportFileErr(file, err)
		return errAlreadyReported
	}
	defer rc.Close()

	dec := json.NewDecoder(&byteCountReader{r: rc, max: MaxStreamBytes})
	dec.UseNumber()
	for {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			s.reportInputErr(file, err)
			return errAlreadyReported
		}
		if err := fn(raw); err != nil {
			return err
		}
	}
}

// processJSONStream runs the filter once per JSON value in one source.
func (s *runState) processJSONStream(file string) error {
	return s.decodeJSONValues(file, func(raw []byte) error {
		return s.runOne([]byte(raw))
	})
}

// processJSONSlurpFiles reads every value from every file into one array,
// then runs the filter once. The aggregate input size across all files
// is capped at MaxStreamBytes so an attacker cannot supply N near-cap
// files to balloon transient memory.
func (s *runState) processJSONSlurpFiles(files []string) error {
	var arr [][]byte
	for _, file := range files {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		err := s.decodeJSONValues(file, func(raw []byte) error {
			cp := make([]byte, len(raw))
			copy(cp, raw)
			s.slurpTotal += int64(len(cp)) + 1 // +1 for separator/bracket overhead
			if s.slurpTotal > MaxStreamBytes {
				return capExceededError(MaxStreamBytes)
			}
			arr = append(arr, cp)
			return nil
		})
		if err != nil {
			if isCapExceeded(err) {
				s.reportInputErr(file, err)
				return errAlreadyReported
			}
			return err
		}
	}
	var buf bytes.Buffer
	buf.WriteByte('[')
	for i, v := range arr {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(v)
	}
	buf.WriteByte(']')
	return s.runOne(buf.Bytes())
}

// processRawLines reads one line at a time, packages each as a JSON
// string, and runs the filter on it.
//
// Line splitting follows bufio.ScanLines: LF terminates a line and a
// preceding CR is stripped. Lone CR is treated as part of the line,
// matching real jq's behaviour.
func (s *runState) processRawLines(file string) error {
	rc, err := s.openSource(file)
	if err != nil {
		s.reportFileErr(file, err)
		return errAlreadyReported
	}
	defer rc.Close()

	sc := bufio.NewScanner(&byteCountReader{r: rc, max: MaxStreamBytes})
	sc.Buffer(make([]byte, 4096), MaxLineBytes)
	for {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if !sc.Scan() {
			break
		}
		encoded := encodeJSONString(sc.Bytes())
		if err := s.runOne(encoded); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		s.reportInputErr(file, err)
		return errAlreadyReported
	}
	return nil
}

// processRawSlurpFiles reads all input bytes (across files) into one
// JSON string and runs the filter once. The aggregate cap is enforced
// during accumulation, not after, so over-large inputs short-circuit
// without ever fully materialising in memory.
func (s *runState) processRawSlurpFiles(files []string) error {
	var combined bytes.Buffer
	for _, file := range files {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		rc, err := s.openSource(file)
		if err != nil {
			s.reportFileErr(file, err)
			return errAlreadyReported
		}
		remaining := int64(MaxStreamBytes) - int64(combined.Len())
		data, rerr := readAllBounded(s.ctx, rc, remaining)
		rc.Close()
		if rerr != nil {
			s.reportInputErr(file, rerr)
			return errAlreadyReported
		}
		combined.Write(data)
	}
	encoded := encodeJSONString(combined.Bytes())
	return s.runOne(encoded)
}

// runOne runs the compiled filter on input and writes all results.
func (s *runState) runOne(input []byte) error {
	return s.prog.RunFunc(input, func(result []byte) error {
		if err := s.ctx.Err(); err != nil {
			return err
		}
		if !isNullOrFalse(result) {
			s.emittedTruthy = true
		}
		return s.writeResult(result)
	})
}

// writeResult formats one filter result according to the active flags
// and writes it to stdout.
func (s *runState) writeResult(raw []byte) error {
	formatted, err := s.formatValue(raw)
	if err != nil {
		return err
	}
	if _, err := s.callCtx.Stdout.Write(formatted); err != nil {
		return err
	}
	if !s.opts.joinOut {
		if _, err := s.callCtx.Stdout.Write([]byte{'\n'}); err != nil {
			return err
		}
	}
	return nil
}

// formatValue returns the bytes that represent one filter result.
//
//   - With --raw-output (or --join-output) on a JSON-string result, the
//     decoded string contents are returned (no quotes, escapes resolved).
//   - Otherwise the result is re-emitted as JSON with the active
//     compact / sort-keys / ascii options applied.
func (s *runState) formatValue(raw []byte) ([]byte, error) {
	if s.opts.rawOutput && len(raw) > 0 && raw[0] == '"' {
		var str string
		if err := json.Unmarshal(raw, &str); err == nil {
			return []byte(str), nil
		}
		// Fallthrough — not actually a string; emit as JSON.
	}
	// Pure pass-through for the most common case (compact, no ascii, no
	// sort-keys). fastjq already emits canonical compact JSON.
	if s.opts.compact && !s.opts.ascii && !s.opts.sortKeys {
		return raw, nil
	}
	return reformatJSON(s.ctx, raw, s.opts.compact, s.opts.ascii, s.opts.sortKeys)
}

// reportFileErr writes an "open failed" style message.
func (s *runState) reportFileErr(file string, err error) {
	name := file
	if file == "-" {
		name = "standard input"
	}
	s.callCtx.Errf("jq: %s: %s\n", name, s.callCtx.PortableErr(err))
}

// reportInputErr writes a "parse / read failed" style message.
func (s *runState) reportInputErr(file string, err error) {
	name := file
	if file == "-" {
		name = "<stdin>"
	}
	s.callCtx.Errf("jq: error reading %s: %s\n", name, err.Error())
}

// isNullOrFalse reports whether a fastjq output is the literal `null`
// or `false`. Used by --exit-status to compute exit code.
func isNullOrFalse(b []byte) bool {
	return bytes.Equal(b, []byte("null")) || bytes.Equal(b, []byte("false"))
}

// encodeJSONString produces the JSON-encoded form of an arbitrary byte
// slice (used for --raw-input). Invalid UTF-8 sequences are replaced
// with U+FFFD by Go's encoder, matching jq's behaviour for non-UTF-8
// raw input.
func encodeJSONString(b []byte) []byte {
	out, _ := json.Marshal(string(b))
	return out
}

// byteCountReader wraps an io.Reader and enforces a hard byte cap.
// When the cap is hit, Read returns errCapExceeded and truncates n so
// the consumer never sees bytes beyond the cap (i.e. the consumer's
// internal buffer cannot exceed max bytes either).
type byteCountReader struct {
	r   io.Reader
	n   int64
	max int64
}

func (c *byteCountReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.n > c.max {
		// Truncate so the caller never observes the overshoot bytes.
		over := c.n - c.max
		c.n = c.max
		n -= int(over)
		if n < 0 {
			n = 0
		}
		return n, capExceededError(c.max)
	}
	return n, err
}

// capExceededError builds the canonical "input exceeds N bytes" error.
// It wraps errCapExceeded so callers can identify the condition with
// errors.Is and produce a clearer user-facing message.
func capExceededError(max int64) error {
	return fmt.Errorf("%w: input exceeds %d bytes", errCapExceeded, max)
}

// errCapExceeded is the sentinel under capExceededError. Use isCapExceeded
// to test for it.
var errCapExceeded = errors.New("size cap exceeded")

func isCapExceeded(err error) bool {
	return errors.Is(err, errCapExceeded)
}

// maxEmitDepth bounds the recursion of the JSON re-emitter. JSON nested
// deeper than this is rejected rather than risking a runaway stack.
const maxEmitDepth = 256

// errEmitDepth is returned when JSON nesting exceeds maxEmitDepth.
var errEmitDepth = errors.New("json nesting too deep")

// reformatJSON re-emits raw with the active formatting options. raw
// must already be valid JSON (fastjq's output is always valid). The
// output is hard-capped at MaxStreamBytes; oversize results return an
// error rather than balloon memory.
func reformatJSON(ctx context.Context, raw []byte, compact, ascii, sortKeys bool) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out bytes.Buffer
	out.Grow(len(raw))
	indent := "  "
	if compact {
		indent = ""
	}
	e := &emitter{
		ctx:      ctx,
		out:      &out,
		sortKeys: sortKeys,
		ascii:    ascii,
		indent:   indent,
		maxBytes: MaxStreamBytes,
	}
	if err := e.emit(dec, "", 0); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// emitter renders one parsed JSON value with the requested formatting.
type emitter struct {
	ctx      context.Context
	out      *bytes.Buffer
	sortKeys bool
	ascii    bool
	indent   string // "" for compact, "  " for pretty
	maxBytes int    // hard cap on out.Len(); 0 disables the check
}

// guardWrites is called at the top of every container/value emit. It
// surfaces ctx.Err() and the output-size cap.
func (e *emitter) guardWrites() error {
	if err := e.ctx.Err(); err != nil {
		return err
	}
	if e.maxBytes > 0 && e.out.Len() > e.maxBytes {
		return capExceededError(int64(e.maxBytes))
	}
	return nil
}

// emit renders one value pulled from dec.
//
// prefix is the indentation string for the line that contains the
// value's opening character. It is not written by emit itself; it is
// used by container emitters to indent child lines and to align the
// closing bracket/brace. depth bounds recursion.
func (e *emitter) emit(dec *json.Decoder, prefix string, depth int) error {
	if err := e.guardWrites(); err != nil {
		return err
	}
	if depth > maxEmitDepth {
		return errEmitDepth
	}
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '[':
			return e.emitArray(dec, prefix, depth+1)
		case '{':
			return e.emitObject(dec, prefix, depth+1)
		default:
			return fmt.Errorf("unexpected delim %q", t)
		}
	case json.Number:
		e.out.WriteString(t.String())
	case string:
		e.emitString(t)
	case bool:
		if t {
			e.out.WriteString("true")
		} else {
			e.out.WriteString("false")
		}
	case nil:
		e.out.WriteString("null")
	default:
		return fmt.Errorf("unexpected token type %T", tok)
	}
	return nil
}

func (e *emitter) emitArray(dec *json.Decoder, prefix string, depth int) error {
	e.out.WriteByte('[')
	if !dec.More() {
		if _, err := dec.Token(); err != nil {
			return err
		}
		e.out.WriteByte(']')
		return nil
	}
	childPrefix := prefix + e.indent
	first := true
	for dec.More() {
		if err := e.guardWrites(); err != nil {
			return err
		}
		if !first {
			e.out.WriteByte(',')
		}
		first = false
		if e.indent != "" {
			e.out.WriteByte('\n')
			e.out.WriteString(childPrefix)
		}
		if err := e.emit(dec, childPrefix, depth); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	if e.indent != "" {
		e.out.WriteByte('\n')
		e.out.WriteString(prefix)
	}
	e.out.WriteByte(']')
	return nil
}

func (e *emitter) emitObject(dec *json.Decoder, prefix string, depth int) error {
	e.out.WriteByte('{')
	if !dec.More() {
		if _, err := dec.Token(); err != nil {
			return err
		}
		e.out.WriteByte('}')
		return nil
	}
	childPrefix := prefix + e.indent

	if e.sortKeys {
		type kv struct {
			k string
			v []byte
		}
		var pairs []kv
		for dec.More() {
			if err := e.guardWrites(); err != nil {
				return err
			}
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyTok.(string)
			if !ok {
				return fmt.Errorf("non-string object key")
			}
			var sub bytes.Buffer
			subE := &emitter{ctx: e.ctx, out: &sub, sortKeys: e.sortKeys, ascii: e.ascii, indent: e.indent, maxBytes: e.maxBytes}
			if err := subE.emit(dec, childPrefix, depth); err != nil {
				return err
			}
			pairs = append(pairs, kv{k: key, v: sub.Bytes()})
		}
		if _, err := dec.Token(); err != nil {
			return err
		}
		slices.SortFunc(pairs, func(a, b kv) int {
			switch {
			case a.k < b.k:
				return -1
			case a.k > b.k:
				return 1
			default:
				return 0
			}
		})
		for i, p := range pairs {
			if i > 0 {
				e.out.WriteByte(',')
			}
			if e.indent != "" {
				e.out.WriteByte('\n')
				e.out.WriteString(childPrefix)
			}
			e.emitString(p.k)
			e.out.WriteByte(':')
			if e.indent != "" {
				e.out.WriteByte(' ')
			}
			e.out.Write(p.v)
		}
	} else {
		first := true
		for dec.More() {
			if err := e.guardWrites(); err != nil {
				return err
			}
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyTok.(string)
			if !ok {
				return fmt.Errorf("non-string object key")
			}
			if !first {
				e.out.WriteByte(',')
			}
			first = false
			if e.indent != "" {
				e.out.WriteByte('\n')
				e.out.WriteString(childPrefix)
			}
			e.emitString(key)
			e.out.WriteByte(':')
			if e.indent != "" {
				e.out.WriteByte(' ')
			}
			if err := e.emit(dec, childPrefix, depth); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil {
			return err
		}
	}

	if e.indent != "" {
		e.out.WriteByte('\n')
		e.out.WriteString(prefix)
	}
	e.out.WriteByte('}')
	return nil
}

// emitString writes s as a JSON string literal. With ascii=true, every
// rune outside the 7-bit range is emitted as a \uXXXX (or surrogate-pair)
// escape. Control characters are always escaped.
func (e *emitter) emitString(s string) {
	e.out.WriteByte('"')
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch r {
		case '"':
			e.out.WriteString(`\"`)
		case '\\':
			e.out.WriteString(`\\`)
		case '\n':
			e.out.WriteString(`\n`)
		case '\r':
			e.out.WriteString(`\r`)
		case '\t':
			e.out.WriteString(`\t`)
		case '\b':
			e.out.WriteString(`\b`)
		case '\f':
			e.out.WriteString(`\f`)
		default:
			switch {
			case r < 0x20:
				fmt.Fprintf(e.out, `\u%04x`, r)
			case r < 0x80:
				e.out.WriteByte(byte(r))
			case e.ascii:
				if r > 0xFFFF {
					rr := r - 0x10000
					hi := 0xD800 + (rr >> 10)
					lo := 0xDC00 + (rr & 0x3FF)
					fmt.Fprintf(e.out, `\u%04x\u%04x`, hi, lo)
				} else {
					fmt.Fprintf(e.out, `\u%04x`, r)
				}
			default:
				e.out.WriteRune(r)
			}
		}
		i += size
	}
	e.out.WriteByte('"')
}
