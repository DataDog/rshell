// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/rshell/builtins"
)

// runtime is the per-invocation interpreter state.
type runtime struct {
	callCtx *builtins.CallContext

	// Globals (scalars and arrays). Awk has no scope distinction.
	globals map[string]awkValue
	arrays  map[string]map[string]awkValue

	// Special variables, cached for hot-path access.
	nr  int64
	nf  int64
	fnr int64
	fs  string // input field separator (literal or regex source)
	ofs string
	ors string
	rs  string // input record separator (single character)

	// fsRe holds the compiled regex for FS when FS is a regex (length > 1).
	fsRe *regexp.Regexp

	filename string
	subsep   string
	convFmt  string
	ofmt     string
	rstart   int64
	rlength  int64

	// Current record.
	record string
	fields []string // fields[0] is the whole record; fields[i] is $i

	// Array iteration order tracking.
	rng *deterministicRand

	// Cumulative bytes read across all non-regular-file inputs.
	totalReadBytes int64

	// rangeStates tracks per-rule "in range" state for range patterns.
	rangeStates map[int]bool

	// callDepth bounds runtime recursion in execStmt/evalExpr to prevent
	// stack-overflow via deeply nested constructs that survived parsing.
	callDepth int
}

// maxRuntimeDepth caps execution recursion. Defense-in-depth alongside
// parser-side maxParseDepth.
const maxRuntimeDepth = 1024

func newRuntime(callCtx *builtins.CallContext) *runtime {
	return &runtime{
		callCtx:     callCtx,
		globals:     make(map[string]awkValue),
		arrays:      make(map[string]map[string]awkValue),
		fs:          " ",
		ofs:         " ",
		ors:         "\n",
		rs:          "\n",
		subsep:      "\x1c",
		convFmt:     "%.6g",
		ofmt:        "%.6g",
		rng:         newDeterministicRand(0),
		rangeStates: make(map[int]bool),
	}
}

// setFS validates and stores the input field separator.
func (r *runtime) setFS(s string) error {
	if len(s) > MaxStringBytes {
		return errors.New("FS too long")
	}
	// Expand backslash escapes that gawk/mawk accept on the command line.
	switch s {
	case `\t`:
		s = "\t"
	case `\n`:
		s = "\n"
	case `\r`:
		s = "\r"
	}
	r.fs = s
	r.fsRe = nil
	if len(s) > 1 {
		re, err := regexp.Compile(s)
		if err != nil {
			return fmt.Errorf("invalid regex: %v", err)
		}
		r.fsRe = re
	}
	return nil
}

// applyVarAssignment handles -v var=value. The value is treated as a string
// literal (escapes are NOT expanded, matching POSIX) — but this is awk-style:
// it is treated as a string-number, so numeric coercion works naturally.
func (r *runtime) applyVarAssignment(s string) error {
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return fmt.Errorf("expected NAME=VALUE, got %q", s)
	}
	name := s[:eq]
	if !isValidVarName(name) {
		return fmt.Errorf("invalid variable name %q", name)
	}
	val := s[eq+1:]
	if reason, blocked := blockedNames[name]; blocked {
		return errors.New(reason)
	}
	switch name {
	case "FS":
		return r.setFS(val)
	case "OFS":
		r.ofs = val
	case "ORS":
		if len(val) > MaxStringBytes {
			return errors.New("ORS too long")
		}
		r.ors = val
	case "RS":
		if len(val) > 1 {
			return errors.New("multi-character RS not supported")
		}
		r.rs = val
	case "SUBSEP":
		r.subsep = val
	case "CONVFMT":
		r.convFmt = val
	case "OFMT":
		r.ofmt = val
	case "NR":
		r.nr = floatToInt64Safe(parseAwkNumber(val))
	case "FNR":
		r.fnr = floatToInt64Safe(parseAwkNumber(val))
	case "NF":
		return r.storeScalar("NF", strNumValue(val))
	case "RSTART":
		r.rstart = floatToInt64Safe(parseAwkNumber(val))
	case "RLENGTH":
		r.rlength = floatToInt64Safe(parseAwkNumber(val))
	default:
		r.globals[name] = strNumValue(val)
	}
	return nil
}

func isValidVarName(s string) bool {
	if s == "" {
		return false
	}
	if !isIdentStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isIdentCont(s[i]) {
			return false
		}
	}
	return true
}

// isArgvAssignment reports whether a positional argument is an awk variable
// assignment of the form NAME=value, where NAME is a valid awk identifier.
// Such arguments are treated as assignments (not filenames) by GNU awk.
func isArgvAssignment(s string) bool {
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return false
	}
	return isValidVarName(s[:eq])
}

// run is the main driver: BEGIN blocks, then each input file, then END.
func run(ctx context.Context, r *runtime, prog *program, files []string) (uint8, error) {
	// Run BEGIN blocks first.
	for _, rule := range prog.rules {
		if _, isBegin := rule.pat.(*beginPattern); !isBegin {
			continue
		}
		if rule.action == nil {
			continue
		}
		if err := r.execBlock(ctx, rule.action); err != nil {
			return finalizeAfterUnwind(ctx, r, prog, err)
		}
	}

	// We must read input whenever there is any non-BEGIN rule (including END,
	// which depends on NR/$0 reflecting the consumed input).
	needsInput := false
	for _, rule := range prog.rules {
		if _, isBegin := rule.pat.(*beginPattern); !isBegin {
			needsInput = true
			break
		}
	}

	if needsInput {
		if len(files) == 0 {
			files = []string{"-"}
		}
		for _, file := range files {
			if ctx.Err() != nil {
				return 1, ctx.Err()
			}
			// GNU awk treats positional arguments of the form var=value as
			// variable assignments rather than filenames. Apply them between
			// files so they take effect for subsequent input files.
			if isArgvAssignment(file) {
				if err := r.applyVarAssignment(file); err != nil {
					return 1, fmt.Errorf("awk: argv assignment %q: %w", file, err)
				}
				continue
			}
			if err := r.processFile(ctx, prog, file); err != nil {
				return finalizeAfterUnwind(ctx, r, prog, err)
			}
		}
	}

	if err := runEnd(ctx, r, prog); err != nil {
		if ee, ok := err.(*exitSignal); ok {
			return ee.code, nil
		}
		return 1, err
	}
	return 0, nil
}

// finalizeAfterUnwind handles the propagation of an error returned from a
// BEGIN block or a main-rule loop. If the error is an exitSignal, END blocks
// still run; if END itself signals exit, that code wins; otherwise the
// outer signal's code is returned. Any other error is returned as-is.
func finalizeAfterUnwind(ctx context.Context, r *runtime, prog *program, err error) (uint8, error) {
	ee, ok := err.(*exitSignal)
	if !ok {
		return 1, err
	}
	endErr := runEnd(ctx, r, prog)
	if endErr == nil {
		return ee.code, nil
	}
	if eee, ok := endErr.(*exitSignal); ok {
		return eee.code, nil
	}
	return 1, endErr
}

// runEnd executes END blocks.
func runEnd(ctx context.Context, r *runtime, prog *program) error {
	for _, rule := range prog.rules {
		if _, isEnd := rule.pat.(*endPattern); !isEnd {
			continue
		}
		if rule.action == nil {
			continue
		}
		if err := r.execBlock(ctx, rule.action); err != nil {
			return err
		}
	}
	return nil
}

// processFile reads a file (or stdin when name == "-"), splits it into
// records, and applies the program rules to each record.
func (r *runtime) processFile(ctx context.Context, prog *program, name string) error {
	rc, isRegular, err := r.openInput(ctx, name)
	if err != nil {
		// Per awk convention, a missing file is an error but we continue
		// to other files. Handled by callers; we return immediately here.
		return fmt.Errorf("%s: %s", displayName(name), r.callCtx.PortableErr(err))
	}
	if rc == nil {
		// stdin not available; treat as empty.
		return nil
	}
	defer rc.Close()
	if name == "-" {
		r.filename = "-"
	} else {
		r.filename = name
	}
	r.fnr = 0

	reader := io.Reader(rc)
	if !isRegular {
		reader = &capReader{r: rc, remaining: MaxTotalReadBytes - r.totalReadBytes, total: &r.totalReadBytes}
	}

	sc := bufio.NewScanner(reader)
	sc.Buffer(make([]byte, 4096), MaxRecordBytes+1)
	// Use a dynamic split function that reads r.rs on every call so that
	// assigning RS inside a rule takes effect for subsequent records.
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		return makeSplitFunc(r.rs)(data, atEOF)
	})

	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.nr++
		r.fnr++
		if err := r.setRecord(sc.Text()); err != nil {
			return err
		}
		if err := r.applyRules(ctx, prog); err != nil {
			if errors.Is(err, errNextRecord) {
				continue
			}
			return err
		}
	}
	if scErr := sc.Err(); scErr != nil {
		if errors.Is(scErr, bufio.ErrTooLong) {
			return fmt.Errorf("%s: record exceeds maximum size of %d bytes", displayName(name), MaxRecordBytes)
		}
		return fmt.Errorf("%s: %s", displayName(name), r.callCtx.PortableErr(scErr))
	}
	return nil
}

// errNextRecord is a sentinel returned by `next` to skip remaining patterns
// for the current record.
var errNextRecord = errors.New("next")

// errBreak / errContinue are loop-control sentinels.
var (
	errBreak    = errors.New("break")
	errContinue = errors.New("continue")
)

// exitSignal is returned by `exit` to unwind back to the driver.
type exitSignal struct{ code uint8 }

func (e *exitSignal) Error() string { return "exit" }

// applyRules iterates the rules in order, evaluating patterns and running
// actions when they match.
func (r *runtime) applyRules(ctx context.Context, prog *program) error {
	for i, rule := range prog.rules {
		switch rule.pat.(type) {
		case *beginPattern, *endPattern:
			continue
		}
		matched, err := r.evalPattern(i, rule.pat)
		if err != nil {
			return err
		}
		if !matched {
			continue
		}
		if rule.action == nil {
			// Default action: print $0.
			if err := r.printLine([]string{r.record}); err != nil {
				return err
			}
		} else {
			if err := r.execBlock(ctx, rule.action); err != nil {
				return err
			}
		}
	}
	return nil
}

// evalPattern returns whether the rule's pattern matches the current record.
func (r *runtime) evalPattern(idx int, p pattern) (bool, error) {
	switch v := p.(type) {
	case *alwaysPattern:
		return true, nil
	case *regexPattern:
		return v.re.MatchString(r.record), nil
	case *exprPattern:
		val, err := r.evalExpr(v.e)
		if err != nil {
			return false, err
		}
		return val.isTrue(), nil
	case *rangePattern:
		inRange := r.rangeStates[idx]
		if !inRange {
			matched, err := r.evalPatternSingle(v.start)
			if err != nil {
				return false, err
			}
			if matched {
				r.rangeStates[idx] = true
				inRange = true
			}
		}
		if inRange {
			matched, err := r.evalPatternSingle(v.end)
			if err != nil {
				return false, err
			}
			if matched {
				r.rangeStates[idx] = false
			}
			return true, nil
		}
		return false, nil
	}
	return false, fmt.Errorf("internal: unknown pattern type %T", p)
}

// evalPatternSingle evaluates a non-range pattern subexpression for use
// inside a rangePattern.
func (r *runtime) evalPatternSingle(p pattern) (bool, error) {
	switch v := p.(type) {
	case *regexPattern:
		return v.re.MatchString(r.record), nil
	case *exprPattern:
		val, err := r.evalExpr(v.e)
		if err != nil {
			return false, err
		}
		return val.isTrue(), nil
	case *alwaysPattern:
		return true, nil
	}
	return false, fmt.Errorf("internal: unknown pattern type %T inside range", p)
}

// setRecord assigns $0 (and consequently splits into fields).
func (r *runtime) setRecord(rec string) error {
	if len(rec) > MaxRecordBytes {
		return fmt.Errorf("record exceeds maximum size of %d bytes", MaxRecordBytes)
	}
	r.record = rec
	r.fields = r.splitFields(rec)
	r.nf = int64(len(r.fields))
	return nil
}

// splitFields splits the current record into fields per FS.
func (r *runtime) splitFields(rec string) []string {
	if rec == "" {
		return nil
	}
	if r.fsRe != nil {
		return r.fsRe.Split(rec, -1)
	}
	switch {
	case r.fs == " ":
		// Default: split on runs of whitespace, leading/trailing trimmed.
		return strings.Fields(rec)
	case r.fs == "":
		// Empty FS: each character is a field.
		out := make([]string, 0, len(rec))
		for _, ch := range rec {
			out = append(out, string(ch))
		}
		return out
	case r.fs == "\t":
		return strings.Split(rec, "\t")
	default:
		// Single character or fixed string.
		return strings.Split(rec, r.fs)
	}
}

// rebuildRecord recomputes $0 from the current fields slice using OFS. Caps
// the result at MaxRecordBytes so a script cannot grow $0 unboundedly via
// repeated field assignments.
func (r *runtime) rebuildRecord() error {
	rec := strings.Join(r.fields, r.ofs)
	if len(rec) > MaxRecordBytes {
		return fmt.Errorf("rebuilt record exceeds maximum size %d", MaxRecordBytes)
	}
	r.record = rec
	return nil
}

// getField returns the value of $i (i >= 0).
func (r *runtime) getField(i int) string {
	if i == 0 {
		return r.record
	}
	if i < 0 {
		return ""
	}
	if i > len(r.fields) {
		return ""
	}
	return r.fields[i-1]
}

// setField assigns to $i, growing the field slice if necessary.
func (r *runtime) setField(i int, val string) error {
	if i < 0 {
		return errors.New("cannot assign to negative field")
	}
	if i > MaxFields {
		return fmt.Errorf("field index %d exceeds maximum %d", i, MaxFields)
	}
	if i == 0 {
		if len(val) > MaxRecordBytes {
			return fmt.Errorf("$0 assignment exceeds maximum record size %d", MaxRecordBytes)
		}
		r.record = val
		r.fields = r.splitFields(val)
		r.nf = int64(len(r.fields))
		return nil
	}
	for len(r.fields) < i {
		r.fields = append(r.fields, "")
	}
	r.fields[i-1] = val
	r.nf = int64(len(r.fields))
	return r.rebuildRecord()
}

// openInput opens a file or returns stdin. The bool indicates whether the
// reader corresponds to a regular file (so the totalReadBytes cap can be
// skipped). Non-regular sources (FIFOs, /dev/zero, /proc/* streams, stdin) get
// the cap to bound infinite reads.
func (r *runtime) openInput(ctx context.Context, name string) (io.ReadCloser, bool, error) {
	if name == "-" {
		if r.callCtx.Stdin == nil {
			return nil, false, nil
		}
		return io.NopCloser(r.callCtx.Stdin), false, nil
	}
	regular := false
	if r.callCtx.StatFile != nil {
		if info, err := r.callCtx.StatFile(ctx, name); err == nil {
			regular = info.Mode().IsRegular()
		}
	}
	rwc, err := r.callCtx.OpenFile(ctx, name, os.O_RDONLY, 0)
	if err != nil {
		return nil, false, err
	}
	return rwc, regular, nil
}

// printLine writes the joined args followed by ORS. Returns the first write
// error so callers (including the BEGIN/END drivers) can surface broken-pipe
// or other I/O failures.
func (r *runtime) printLine(parts []string) error {
	switch len(parts) {
	case 0:
		_, err := io.WriteString(r.callCtx.Stdout, r.ors)
		return err
	case 1:
		if _, err := io.WriteString(r.callCtx.Stdout, parts[0]); err != nil {
			return err
		}
	default:
		if _, err := io.WriteString(r.callCtx.Stdout, strings.Join(parts, r.ofs)); err != nil {
			return err
		}
	}
	_, err := io.WriteString(r.callCtx.Stdout, r.ors)
	return err
}

// execBlock runs a block of statements.
func (r *runtime) execBlock(ctx context.Context, b *blockStmt) error {
	for _, s := range b.body {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := r.execStmt(ctx, s); err != nil {
			return err
		}
	}
	return nil
}

// execStmt executes a single statement.
func (r *runtime) execStmt(ctx context.Context, s stmt) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	r.callDepth++
	defer func() { r.callDepth-- }()
	if r.callDepth > maxRuntimeDepth {
		return fmt.Errorf("execution recursion exceeded %d", maxRuntimeDepth)
	}
	switch v := s.(type) {
	case *blockStmt:
		return r.execBlock(ctx, v)
	case *exprStmt:
		_, err := r.evalExpr(v.expr)
		return err
	case *printStmt:
		return r.execPrint(v.args)
	case *printfStmt:
		return r.execPrintf(v.args)
	case *ifStmt:
		c, err := r.evalExpr(v.cond)
		if err != nil {
			return err
		}
		if c.isTrue() {
			return r.execStmt(ctx, v.then)
		}
		if v.else_ != nil {
			return r.execStmt(ctx, v.else_)
		}
		return nil
	case *whileStmt:
		iter := 0
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			iter++
			if iter > MaxLoopIterations {
				return fmt.Errorf("loop iteration limit (%d) exceeded", MaxLoopIterations)
			}
			c, err := r.evalExpr(v.cond)
			if err != nil {
				return err
			}
			if !c.isTrue() {
				return nil
			}
			if err := r.execStmt(ctx, v.body); err != nil {
				if errors.Is(err, errBreak) {
					return nil
				}
				if errors.Is(err, errContinue) {
					continue
				}
				return err
			}
		}
	case *doWhileStmt:
		iter := 0
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			iter++
			if iter > MaxLoopIterations {
				return fmt.Errorf("loop iteration limit (%d) exceeded", MaxLoopIterations)
			}
			if err := r.execStmt(ctx, v.body); err != nil {
				if errors.Is(err, errBreak) {
					return nil
				}
				if !errors.Is(err, errContinue) {
					return err
				}
			}
			c, err := r.evalExpr(v.cond)
			if err != nil {
				return err
			}
			if !c.isTrue() {
				return nil
			}
		}
	case *forStmt:
		if v.init != nil {
			if err := r.execStmt(ctx, v.init); err != nil {
				return err
			}
		}
		iter := 0
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			iter++
			if iter > MaxLoopIterations {
				return fmt.Errorf("loop iteration limit (%d) exceeded", MaxLoopIterations)
			}
			if v.cond != nil {
				c, err := r.evalExpr(v.cond)
				if err != nil {
					return err
				}
				if !c.isTrue() {
					return nil
				}
			}
			if err := r.execStmt(ctx, v.body); err != nil {
				if errors.Is(err, errBreak) {
					return nil
				}
				if !errors.Is(err, errContinue) {
					return err
				}
			}
			if v.post != nil {
				if err := r.execStmt(ctx, v.post); err != nil {
					return err
				}
			}
		}
	case *forInStmt:
		arr := r.arrays[v.arrayVar]
		if arr == nil {
			return nil
		}
		// Snapshot and sort keys for deterministic iteration order.
		// Awk does not mandate a specific order, but deterministic output
		// for the same input is required by repo convention.
		keys := make([]string, 0, len(arr))
		for k := range arr {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r.globals[v.loopVar] = strNumValue(k)
			if err := r.execStmt(ctx, v.body); err != nil {
				if errors.Is(err, errBreak) {
					return nil
				}
				if errors.Is(err, errContinue) {
					continue
				}
				return err
			}
		}
		return nil
	case *breakStmt:
		return errBreak
	case *continueStmt:
		return errContinue
	case *nextStmt:
		return errNextRecord
	case *exitStmt:
		var code uint8
		if v.code != nil {
			val, err := r.evalExpr(v.code)
			if err != nil {
				return err
			}
			n := floatToInt64Safe(val.toNumber())
			if n < 0 {
				n = 0
			}
			code = uint8(n & 0xFF)
		}
		return &exitSignal{code: code}
	case *deleteStmt:
		if v.indices == nil {
			delete(r.arrays, v.arrayVar)
			return nil
		}
		key, err := r.indexKey(v.indices)
		if err != nil {
			return err
		}
		if arr := r.arrays[v.arrayVar]; arr != nil {
			delete(arr, key)
		}
		return nil
	}
	return fmt.Errorf("internal: unknown statement type %T", s)
}

// execPrint writes the print statement.
func (r *runtime) execPrint(args []expr) error {
	if len(args) == 0 {
		return r.printLine([]string{r.record})
	}
	parts := make([]string, len(args))
	for i, a := range args {
		v, err := r.evalExpr(a)
		if err != nil {
			return err
		}
		// For numbers in print, awk uses OFMT (not CONVFMT).
		parts[i] = r.printableString(v)
	}
	return r.printLine(parts)
}

// printableString formats a value the way `print` would: numbers via OFMT,
// strings as-is.
func (r *runtime) printableString(v awkValue) string {
	if v.kind == valNum {
		return formatNumber(v.f, r.ofmt)
	}
	return v.toString(r.ofmt)
}

// execPrintf writes the printf statement.
func (r *runtime) execPrintf(args []expr) error {
	if len(args) == 0 {
		return errors.New("printf: no format")
	}
	fmtVal, err := r.evalExpr(args[0])
	if err != nil {
		return err
	}
	values := make([]awkValue, len(args)-1)
	for i, a := range args[1:] {
		v, err := r.evalExpr(a)
		if err != nil {
			return err
		}
		values[i] = v
	}
	out, err := awkSprintf(fmtVal.toString(r.convFmt), values, r.convFmt)
	if err != nil {
		return err
	}
	if len(out) > MaxStringBytes {
		return fmt.Errorf("printf output exceeds maximum string length %d", MaxStringBytes)
	}
	_, werr := io.WriteString(r.callCtx.Stdout, out)
	return werr
}

// indexKey builds the SUBSEP-joined string key for arr[i,j,...].
func (r *runtime) indexKey(indices []expr) (string, error) {
	parts := make([]string, len(indices))
	for i, e := range indices {
		v, err := r.evalExpr(e)
		if err != nil {
			return "", err
		}
		parts[i] = v.toString(r.convFmt)
	}
	return strings.Join(parts, r.subsep), nil
}

// =====================================================================
// Expression evaluation.
// =====================================================================

func (r *runtime) evalExpr(e expr) (awkValue, error) {
	r.callDepth++
	defer func() { r.callDepth-- }()
	if r.callDepth > maxRuntimeDepth {
		return uninitValue, fmt.Errorf("expression recursion exceeded %d", maxRuntimeDepth)
	}
	switch v := e.(type) {
	case *numExpr:
		return numValue(v.val), nil
	case *strExpr:
		return strValue(v.val), nil
	case *regexExpr:
		// Bare regex acts as ($0 ~ /re/).
		if v.re.MatchString(r.record) {
			return numValue(1), nil
		}
		return numValue(0), nil
	case *identExpr:
		return r.lookupScalar(v.name), nil
	case *fieldExpr:
		idxVal, err := r.evalExpr(v.index)
		if err != nil {
			return uninitValue, err
		}
		idx := floatToInt64Safe(idxVal.toNumber())
		if idx < 0 || idx > MaxFields {
			return strValue(""), nil
		}
		return strNumValue(r.getField(int(idx))), nil
	case *indexExpr:
		key, err := r.indexKey(v.indices)
		if err != nil {
			return uninitValue, err
		}
		arr := r.arrays[v.name]
		if arr == nil {
			arr = make(map[string]awkValue)
			r.arrays[v.name] = arr
		}
		val, ok := arr[key]
		if !ok {
			// Reading a missing element creates it (awk semantics: array membership
			// is established on first read, not just on write).
			if len(arr) >= MaxArrayEntries {
				return uninitValue, fmt.Errorf("array exceeds maximum entry count %d", MaxArrayEntries)
			}
			arr[key] = uninitValue
			return uninitValue, nil
		}
		return val, nil
	case *unaryExpr:
		return r.evalUnary(v)
	case *binaryExpr:
		return r.evalBinary(v)
	case *concatExpr:
		var sb strings.Builder
		for _, p := range v.parts {
			val, err := r.evalExpr(p)
			if err != nil {
				return uninitValue, err
			}
			sb.WriteString(val.toString(r.convFmt))
			if sb.Len() > MaxStringBytes {
				return uninitValue, fmt.Errorf("string length exceeds maximum %d", MaxStringBytes)
			}
		}
		return strValue(sb.String()), nil
	case *assignExpr:
		return r.evalAssign(v)
	case *incrExpr:
		return r.evalIncr(v)
	case *condExpr:
		c, err := r.evalExpr(v.cond)
		if err != nil {
			return uninitValue, err
		}
		if c.isTrue() {
			return r.evalExpr(v.then)
		}
		return r.evalExpr(v.else_)
	case *callExpr:
		return r.evalCall(v)
	case *inExpr:
		return r.evalIn(v)
	case *matchExpr:
		return r.evalMatch(v)
	}
	return uninitValue, fmt.Errorf("internal: unknown expression type %T", e)
}

func (r *runtime) lookupScalar(name string) awkValue {
	switch name {
	case "NR":
		return numValue(float64(r.nr))
	case "NF":
		return numValue(float64(r.nf))
	case "FNR":
		return numValue(float64(r.fnr))
	case "FS":
		return strValue(r.fs)
	case "OFS":
		return strValue(r.ofs)
	case "ORS":
		return strValue(r.ors)
	case "RS":
		return strValue(r.rs)
	case "FILENAME":
		return strValue(r.filename)
	case "SUBSEP":
		return strValue(r.subsep)
	case "CONVFMT":
		return strValue(r.convFmt)
	case "OFMT":
		return strValue(r.ofmt)
	case "RSTART":
		return numValue(float64(r.rstart))
	case "RLENGTH":
		return numValue(float64(r.rlength))
	}
	return r.globals[name]
}

func (r *runtime) storeScalar(name string, v awkValue) error {
	switch name {
	case "NR":
		r.nr = floatToInt64Safe(v.toNumber())
		return nil
	case "NF":
		nf := floatToInt64Safe(v.toNumber())
		if nf < 0 {
			nf = 0
		}
		if nf > MaxFields {
			return fmt.Errorf("NF exceeds maximum %d", MaxFields)
		}
		r.nf = nf
		switch {
		case int(r.nf) < len(r.fields):
			r.fields = r.fields[:r.nf]
		case int(r.nf) > len(r.fields):
			for int64(len(r.fields)) < r.nf {
				r.fields = append(r.fields, "")
			}
		}
		return r.rebuildRecord()
	case "FNR":
		r.fnr = floatToInt64Safe(v.toNumber())
		return nil
	case "FS":
		return r.setFS(v.toString(r.convFmt))
	case "OFS":
		r.ofs = v.toString(r.convFmt)
		return nil
	case "ORS":
		s := v.toString(r.convFmt)
		if len(s) > MaxStringBytes {
			return errors.New("ORS too long")
		}
		r.ors = s
		return nil
	case "RS":
		s := v.toString(r.convFmt)
		if len(s) > 1 {
			return errors.New("multi-character RS not supported")
		}
		r.rs = s
		return nil
	case "FILENAME":
		r.filename = v.toString(r.convFmt)
		return nil
	case "SUBSEP":
		r.subsep = v.toString(r.convFmt)
		return nil
	case "CONVFMT":
		r.convFmt = v.toString(r.convFmt)
		return nil
	case "OFMT":
		r.ofmt = v.toString(r.convFmt)
		return nil
	case "RSTART":
		r.rstart = floatToInt64Safe(v.toNumber())
		return nil
	case "RLENGTH":
		r.rlength = floatToInt64Safe(v.toNumber())
		return nil
	}
	r.globals[name] = v
	return nil
}

func (r *runtime) evalUnary(v *unaryExpr) (awkValue, error) {
	x, err := r.evalExpr(v.operand)
	if err != nil {
		return uninitValue, err
	}
	switch v.op {
	case tkPlus:
		return numValue(x.toNumber()), nil
	case tkMinus:
		return numValue(-x.toNumber()), nil
	case tkNot:
		if x.isTrue() {
			return numValue(0), nil
		}
		return numValue(1), nil
	}
	return uninitValue, fmt.Errorf("internal: unknown unary op %s", tokenName(v.op))
}

func (r *runtime) evalBinary(v *binaryExpr) (awkValue, error) {
	switch v.op {
	case tkAnd:
		l, err := r.evalExpr(v.left)
		if err != nil {
			return uninitValue, err
		}
		if !l.isTrue() {
			return numValue(0), nil
		}
		rr, err := r.evalExpr(v.right)
		if err != nil {
			return uninitValue, err
		}
		if rr.isTrue() {
			return numValue(1), nil
		}
		return numValue(0), nil
	case tkOr:
		l, err := r.evalExpr(v.left)
		if err != nil {
			return uninitValue, err
		}
		if l.isTrue() {
			return numValue(1), nil
		}
		rr, err := r.evalExpr(v.right)
		if err != nil {
			return uninitValue, err
		}
		if rr.isTrue() {
			return numValue(1), nil
		}
		return numValue(0), nil
	}
	l, err := r.evalExpr(v.left)
	if err != nil {
		return uninitValue, err
	}
	rr, err := r.evalExpr(v.right)
	if err != nil {
		return uninitValue, err
	}
	switch v.op {
	case tkPlus:
		return numValue(l.toNumber() + rr.toNumber()), nil
	case tkMinus:
		return numValue(l.toNumber() - rr.toNumber()), nil
	case tkStar:
		return numValue(l.toNumber() * rr.toNumber()), nil
	case tkSlash:
		rv := rr.toNumber()
		if rv == 0 {
			return uninitValue, errors.New("division by zero")
		}
		return numValue(l.toNumber() / rv), nil
	case tkPercent:
		rv := rr.toNumber()
		if rv == 0 {
			return uninitValue, errors.New("division by zero in %")
		}
		return numValue(math.Mod(l.toNumber(), rv)), nil
	case tkCaret:
		return numValue(math.Pow(l.toNumber(), rr.toNumber())), nil
	case tkEq, tkNe, tkLt, tkLe, tkGt, tkGe:
		return r.compare(v.op, l, rr), nil
	}
	return uninitValue, fmt.Errorf("internal: unknown binary op %s", tokenName(v.op))
}

// compare implements awk's mixed string/number comparison.
// Both numeric: compare as numbers.
// Both string: compare lexicographically.
// Mixed (string vs number): string-numbers are compared as numbers if they
// look numeric; otherwise compared as strings.
func (r *runtime) compare(op tokenKind, a, b awkValue) awkValue {
	asNum := false
	if a.kind == valNum && b.kind == valNum {
		asNum = true
	} else if a.kind == valNum && b.kind == valStrNum {
		asNum = looksNumeric(b.s)
	} else if a.kind == valNum && b.kind == valUninit {
		asNum = true
	} else if a.kind == valStrNum && b.kind == valNum {
		asNum = looksNumeric(a.s)
	} else if a.kind == valUninit && b.kind == valNum {
		asNum = true
	} else if a.kind == valStrNum && b.kind == valStrNum {
		asNum = looksNumeric(a.s) && looksNumeric(b.s)
	} else if a.kind == valNum && b.kind == valStr {
		asNum = false
	} else if a.kind == valStr && b.kind == valNum {
		asNum = false
	}
	var cmp int
	if asNum {
		af := a.toNumber()
		bf := b.toNumber()
		switch {
		case af < bf:
			cmp = -1
		case af > bf:
			cmp = 1
		}
	} else {
		as := a.toString(r.convFmt)
		bs := b.toString(r.convFmt)
		cmp = strings.Compare(as, bs)
	}
	var ok bool
	switch op {
	case tkEq:
		ok = cmp == 0
	case tkNe:
		ok = cmp != 0
	case tkLt:
		ok = cmp < 0
	case tkLe:
		ok = cmp <= 0
	case tkGt:
		ok = cmp > 0
	case tkGe:
		ok = cmp >= 0
	}
	if ok {
		return numValue(1)
	}
	return numValue(0)
}

func (r *runtime) evalAssign(v *assignExpr) (awkValue, error) {
	rhs, err := r.evalExpr(v.right)
	if err != nil {
		return uninitValue, err
	}
	if v.op == tkAssign {
		return r.assignLValue(v.left, rhs)
	}
	cur, err := r.evalExpr(v.left)
	if err != nil {
		return uninitValue, err
	}
	a := cur.toNumber()
	b := rhs.toNumber()
	var newF float64
	switch v.op {
	case tkAddAssign:
		newF = a + b
	case tkSubAssign:
		newF = a - b
	case tkMulAssign:
		newF = a * b
	case tkDivAssign:
		if b == 0 {
			return uninitValue, errors.New("division by zero")
		}
		newF = a / b
	case tkModAssign:
		if b == 0 {
			return uninitValue, errors.New("division by zero in %=")
		}
		newF = math.Mod(a, b)
	case tkPowAssign:
		newF = math.Pow(a, b)
	default:
		return uninitValue, fmt.Errorf("internal: unknown compound assign %s", tokenName(v.op))
	}
	return r.assignLValue(v.left, numValue(newF))
}

// assignLValue writes a value to an l-value (identifier, array element, field).
func (r *runtime) assignLValue(l expr, val awkValue) (awkValue, error) {
	switch lv := l.(type) {
	case *identExpr:
		if err := r.storeScalar(lv.name, val); err != nil {
			return uninitValue, err
		}
		return val, nil
	case *indexExpr:
		key, err := r.indexKey(lv.indices)
		if err != nil {
			return uninitValue, err
		}
		arr := r.arrays[lv.name]
		if arr == nil {
			arr = make(map[string]awkValue)
			r.arrays[lv.name] = arr
		}
		if _, exists := arr[key]; !exists {
			if len(arr) >= MaxArrayEntries {
				return uninitValue, fmt.Errorf("array %q exceeds maximum entries %d", lv.name, MaxArrayEntries)
			}
		}
		arr[key] = val
		return val, nil
	case *fieldExpr:
		idxVal, err := r.evalExpr(lv.index)
		if err != nil {
			return uninitValue, err
		}
		s := val.toString(r.convFmt)
		idx := floatToInt64Safe(idxVal.toNumber())
		if idx < 0 || idx > MaxFields {
			return uninitValue, fmt.Errorf("field index %d out of range", idx)
		}
		if err := r.setField(int(idx), s); err != nil {
			return uninitValue, err
		}
		return strValue(s), nil
	}
	return uninitValue, errors.New("invalid assignment target")
}

func (r *runtime) evalIncr(v *incrExpr) (awkValue, error) {
	cur, err := r.evalExpr(v.expr)
	if err != nil {
		return uninitValue, err
	}
	x := cur.toNumber()
	delta := 1.0
	if v.op == tkDec {
		delta = -1.0
	}
	newVal := numValue(x + delta)
	if _, err := r.assignLValue(v.expr, newVal); err != nil {
		return uninitValue, err
	}
	if v.post {
		return numValue(x), nil
	}
	return newVal, nil
}

func (r *runtime) evalIn(v *inExpr) (awkValue, error) {
	arr := r.arrays[v.arrayVar]
	if arr == nil {
		return numValue(0), nil
	}
	key, err := r.indexKey(v.keys)
	if err != nil {
		return uninitValue, err
	}
	if _, ok := arr[key]; ok {
		return numValue(1), nil
	}
	return numValue(0), nil
}

func (r *runtime) evalMatch(v *matchExpr) (awkValue, error) {
	leftVal, err := r.evalExpr(v.left)
	if err != nil {
		return uninitValue, err
	}
	subject := leftVal.toString(r.convFmt)
	var re *regexp.Regexp
	if v.re != nil {
		re = v.re
	} else {
		rv, err := r.evalExpr(v.right)
		if err != nil {
			return uninitValue, err
		}
		re, err = compileERE(rv.toString(r.convFmt))
		if err != nil {
			return uninitValue, fmt.Errorf("invalid regex: %v", err)
		}
	}
	matched := re.MatchString(subject)
	if v.negate {
		matched = !matched
	}
	if matched {
		return numValue(1), nil
	}
	return numValue(0), nil
}

// =====================================================================
// Helpers.
// =====================================================================

// makeSplitFunc returns a bufio.SplitFunc that splits on the given record
// separator. RS == "\n" uses the standard line-splitter (preserving \r\n
// passthrough behaviour). RS == "" turns on paragraph mode (we do NOT
// implement paragraph mode in v1; treat as \n). Single-char RS is the
// general case.
func makeSplitFunc(rs string) bufio.SplitFunc {
	if rs == "" || rs == "\n" {
		return splitLines
	}
	sep := rs[0]
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		for i, b := range data {
			if b == sep {
				return i + 1, data[:i], nil
			}
		}
		if atEOF {
			if len(data) == 0 {
				return 0, nil, nil
			}
			return len(data), data, nil
		}
		return 0, nil, nil
	}
}

// splitLines is bufio.ScanLines but tolerant to \r\n.
func splitLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		if b == '\n' {
			tok := data[:i]
			if len(tok) > 0 && tok[len(tok)-1] == '\r' {
				tok = tok[:len(tok)-1]
			}
			return i + 1, tok, nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// displayName converts "-" to "standard input" for diagnostics.
func displayName(name string) string {
	if name == "-" {
		return "standard input"
	}
	return name
}

// capReader limits total reads from non-regular-file inputs to keep awk from
// consuming an infinite source unboundedly.
type capReader struct {
	r         io.Reader
	remaining int64
	total     *int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.remaining <= 0 {
		return 0, fmt.Errorf("input exceeds maximum read of %d bytes", MaxTotalReadBytes)
	}
	if int64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.r.Read(p)
	c.remaining -= int64(n)
	*c.total += int64(n)
	return n, err
}

// =====================================================================
// Deterministic RNG for srand/rand reproducibility.
// =====================================================================

type deterministicRand struct {
	state uint64
	seed  int64
}

func newDeterministicRand(seed int64) *deterministicRand {
	r := &deterministicRand{}
	r.setSeed(seed)
	return r
}

func (d *deterministicRand) setSeed(seed int64) {
	d.seed = seed
	if seed == 0 {
		d.state = 0x9E3779B97F4A7C15
	} else {
		d.state = uint64(seed)
	}
}

func (d *deterministicRand) next() float64 {
	// xorshift64*.
	d.state ^= d.state >> 12
	d.state ^= d.state << 25
	d.state ^= d.state >> 27
	v := d.state * 2685821657736338717
	// Take the high 53 bits and divide by 2^53.
	return float64(v>>11) / (1 << 53)
}

// itoa wraps strconv.Itoa.
func itoa(n int) string { return strconv.Itoa(n) }

// floatToInt64Safe converts a float to int64, mapping NaN to 0 and clamping
// out-of-range or ±Inf values to the int64 boundaries. This avoids the
// implementation-defined behaviour of a direct int64() cast on NaN/Inf.
func floatToInt64Safe(f float64) int64 {
	if math.IsNaN(f) {
		return 0
	}
	if f >= float64(math.MaxInt64) {
		return math.MaxInt64
	}
	if f <= float64(math.MinInt64) {
		return math.MinInt64
	}
	return int64(f)
}
