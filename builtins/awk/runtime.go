// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"regexp/syntax"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/DataDog/rshell/builtins"
)

const (
	MaxProgramBytes                    = 256 << 10
	MaxProgramFiles                    = 64
	MaxRecordBytes                     = 1 << 20
	MaxFields                          = 16_384
	MaxVariableBytes                   = 1 << 20
	MaxRegexBytes                      = 64 << 10
	MaxPipeBytes                       = 5 << 20
	MaxStdoutBytes                     = 10 << 20
	MaxRedirections                    = 64
	maxCommandPipeStmtSuffixEntries    = MaxProgramBytes
	maxCommandPipeRuleSuffixEntries    = 3 * (MaxProgramBytes + 1)
	maxCommandPipeFunctionTouchEntries = MaxProgramBytes
	maxStatementExecutions             = 1 << 20
	maxLoopIterations                  = 1 << 20
	maxFunctionCalls                   = 1 << 20
	maxInputRecords                    = 1 << 20
	maxMainRuleEvaluations             = 1 << 20
	maxExpressionEvaluations           = 1 << 22
	maxStringProcessingBytes           = 64 * MaxVariableBytes
	maxInputBytes                      = maxStringProcessingBytes
	maxRegexCacheEntries               = 64
	maxRegexCacheBytes                 = MaxProgramBytes
	maxFunctionDepth                   = 256
)

var (
	errTooManyFields      = errors.New("too many fields")
	errInputBytesExceeded = errors.New("input byte limit exceeded")
)

type valueKind int

const (
	valueString valueKind = iota
	valueNumber
	valueStrNum
)

type value struct {
	kind valueKind
	s    string
	n    float64
}

func stringValue(s string) value {
	return value{kind: valueString, s: s}
}

func inputStringValue(s string) value {
	if n, ok := parseFullNumericString(s); ok {
		return value{kind: valueStrNum, s: s, n: n}
	}
	return value{kind: valueString, s: s}
}

func unassignedValue() value {
	return value{kind: valueStrNum, s: "", n: 0}
}

func numberValue(n float64) value {
	return value{kind: valueNumber, n: n}
}

func (v value) String() string {
	switch v.kind {
	case valueNumber:
		return formatAwkNumber(v.n)
	default:
		return v.s
	}
}

func formatAwkNumber(n float64) string {
	if special, ok := formatAwkSpecialNumber(n, false); ok {
		return special
	}
	if n == 0 {
		n = 0
	}
	fixed := strconv.FormatFloat(n, 'f', -1, 64)
	for i := 0; i < len(fixed); i++ {
		if fixed[i] == '.' {
			return strconv.FormatFloat(n, 'g', 6, 64)
		}
	}
	return strconv.FormatFloat(n, 'f', 0, 64)
}

func (v value) Number() float64 {
	switch v.kind {
	case valueNumber, valueStrNum:
		return v.n
	default:
		if n, ok := parseNumericPrefix(v.s); ok {
			return n
		}
	}
	return 0
}

func (v value) Bool() bool {
	switch v.kind {
	case valueNumber:
		return v.n != 0
	case valueStrNum:
		return v.n != 0
	default:
		return v.s != ""
	}
}

func cloneStoredString(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	b.WriteString(s)
	return b.String()
}

func cloneStoredValue(v value) value {
	if v.kind != valueNumber {
		v.s = cloneStoredString(v.s)
	}
	return v
}

func parseFullNumericString(s string) (float64, bool) {
	trimmed := strings.Trim(s, " \t\n\r\f\v")
	if trimmed == "" || numericPrefix(trimmed) != trimmed {
		return 0, false
	}
	return parseAwkFloat(trimmed)
}

func parseNumericPrefix(s string) (float64, bool) {
	prefix := numericPrefix(trimLeadingAwkSpace(s))
	if prefix == "" {
		return 0, false
	}
	return parseAwkFloat(prefix)
}

func parseAwkFloat(s string) (float64, bool) {
	n, err := strconv.ParseFloat(s, 64)
	return n, err == nil || errors.Is(err, strconv.ErrRange)
}

func formatAwkSpecialNumber(n float64, upper bool) (string, bool) {
	var text string
	switch {
	case math.IsInf(n, 1):
		text = "+inf"
	case math.IsInf(n, -1):
		text = "-inf"
	case math.IsNaN(n):
		text = "+nan"
		if math.Signbit(n) {
			text = "-nan"
		}
	default:
		return "", false
	}
	if upper {
		text = strings.ToUpper(text)
	}
	return text, true
}

func trimLeadingAwkSpace(s string) string {
	for len(s) > 0 {
		switch s[0] {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			s = s[1:]
		default:
			return s
		}
	}
	return s
}

func numericPrefix(s string) string {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			digits++
		}
	}
	if digits == 0 {
		return ""
	}
	end := i
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		expDigits := 0
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
			expDigits++
		}
		if expDigits > 0 {
			end = j
		}
	}
	return s[:end]
}

type runtime struct {
	callCtx          *builtins.CallContext
	prog             *program
	vars             map[string]value
	arrays           map[string]map[string]value
	varSizes         map[string]int
	arraySizes       map[arraySlot]int
	varBytes         int
	callArgBytes     int
	exprTempBytes    int
	rangeOn          map[int]bool
	environSet       bool
	functionDepth    int
	functionCalls    int
	stmtExecutions   int
	loopIterations   int
	inputRecords     int
	inputBytes       int
	mainRuleEvals    int
	exprEvaluations  int
	stringWorkBytes  int
	regexCache       map[regexCacheKey]*awkRegex
	regexCacheOrder  []regexCacheKey
	regexCacheBytes  int
	frames           []callFrame
	ctx              context.Context
	futureStmts      stmtFuture
	pipes            map[string]*commandPipe
	flushedPipes     map[string]uint8
	pipeOrder        []string
	lookaheadUsage   commandPipeLookaheadUsage
	lookaheadLimits  commandPipeLookaheadUsage
	stdoutBuf        bytes.Buffer
	stdoutMu         sync.Mutex
	stdoutBytes      int
	inputArgs        []string
	inputIndex       int
	mainInput        *recordSource
	mainHadInput     bool
	mainUsedStdin    bool
	mainDefaultStdin bool
	fileInputs       map[string]*recordSource
	failedFileInputs map[string]bool
	commandInputs    map[string]*commandInputPipe
	redirections     int
	redirectionBytes int
	redirectPayload  int
	commandEnvBytes  int

	record   string
	fields   []string
	filename value
	nf       value
	nr       value
	fnr      value
	exitCode int
}

type arraySlot struct {
	name string
	key  string
}

type regexCacheKey struct {
	pattern    string
	ignoreCase bool
}

type callFrame struct {
	locals map[string]*localVar
}

type commandPipe struct {
	command   string
	buf       bytes.Buffer
	env       []string
	envBytes  int
	lookahead commandPipeLookaheadCache
}

// commandPipeLookaheadCache has at most one entry per parsed statement
// position, rule suffix, and user function. Each cache is program-bounded, and
// runtime-wide accounting prevents active pipes from multiplying those bounds.
type commandPipeLookaheadCache struct {
	stmtSuffixes    map[stmtSuffixKey]commandPipeAction
	ruleSuffixes    [ruleEnd + 1][]commandPipeAction
	functionTouches map[string]bool
	usage           commandPipeLookaheadUsage
}

type commandPipeLookaheadUsage struct {
	stmtSuffixes    int
	ruleSuffixes    int
	functionTouches int
}

type stmtSuffixKey struct {
	first  *stmt
	length int
}

func (c *commandPipeLookaheadCache) clear() {
	*c = commandPipeLookaheadCache{}
}

func (u *commandPipeLookaheadUsage) add(delta commandPipeLookaheadUsage) {
	u.stmtSuffixes += delta.stmtSuffixes
	u.ruleSuffixes += delta.ruleSuffixes
	u.functionTouches += delta.functionTouches
}

func (u *commandPipeLookaheadUsage) subtract(delta commandPipeLookaheadUsage) {
	u.stmtSuffixes -= delta.stmtSuffixes
	u.ruleSuffixes -= delta.ruleSuffixes
	u.functionTouches -= delta.functionTouches
}

func (rt *runtime) reserveCommandPipeLookahead(pipe *commandPipe, delta commandPipeLookaheadUsage) error {
	if delta.stmtSuffixes > rt.lookaheadLimits.stmtSuffixes-rt.lookaheadUsage.stmtSuffixes {
		return commandPipeLookaheadLimitError("statement suffix", rt.lookaheadLimits.stmtSuffixes)
	}
	if delta.ruleSuffixes > rt.lookaheadLimits.ruleSuffixes-rt.lookaheadUsage.ruleSuffixes {
		return commandPipeLookaheadLimitError("rule suffix", rt.lookaheadLimits.ruleSuffixes)
	}
	if delta.functionTouches > rt.lookaheadLimits.functionTouches-rt.lookaheadUsage.functionTouches {
		return commandPipeLookaheadLimitError("function touch", rt.lookaheadLimits.functionTouches)
	}
	rt.lookaheadUsage.add(delta)
	pipe.lookahead.usage.add(delta)
	return nil
}

func commandPipeLookaheadLimitError(kind string, limit int) error {
	return fmt.Errorf("command pipe %s lookahead cache exceeds %d entries", kind, limit)
}

func (rt *runtime) releaseCommandPipeLookahead(pipe *commandPipe, delta commandPipeLookaheadUsage) {
	rt.lookaheadUsage.subtract(delta)
	pipe.lookahead.usage.subtract(delta)
}

func (rt *runtime) clearCommandPipeLookahead(pipe *commandPipe) {
	rt.lookaheadUsage.subtract(pipe.lookahead.usage)
	pipe.lookahead.clear()
}

func (rt *runtime) clearCommandPipeLookaheadCaches() {
	for _, pipe := range rt.pipes {
		rt.clearCommandPipeLookahead(pipe)
	}
}

type commandInputPipe struct {
	source       *recordSource
	status       uint8
	payloadBytes int
}

type recordSource struct {
	name           string
	rc             io.ReadCloser
	sc             *bufio.Scanner
	rt             *runtime
	rs             string
	recordAdvance  int
	recordBuffered int
	closeOnce      sync.Once
	asyncRead      bool
	interruptRead  func() bool
	restoreRead    func()
}

type localVar struct {
	value           value
	valueSize       int
	valueSet        bool
	array           map[string]value
	arraySizes      map[string]int
	arrayAlias      *localVar
	globalArrayName string
}

func newRuntime(callCtx *builtins.CallContext, prog *program) *runtime {
	rt := &runtime{
		callCtx:      callCtx,
		prog:         prog,
		filename:     stringValue(""),
		nf:           numberValue(0),
		nr:           numberValue(0),
		fnr:          numberValue(0),
		regexCache:   make(map[regexCacheKey]*awkRegex),
		vars:         make(map[string]value),
		arrays:       make(map[string]map[string]value),
		varSizes:     make(map[string]int),
		arraySizes:   make(map[arraySlot]int),
		rangeOn:      make(map[int]bool),
		pipes:        make(map[string]*commandPipe),
		flushedPipes: make(map[string]uint8),
		lookaheadLimits: commandPipeLookaheadUsage{
			stmtSuffixes:    maxCommandPipeStmtSuffixEntries,
			ruleSuffixes:    maxCommandPipeRuleSuffixEntries,
			functionTouches: maxCommandPipeFunctionTouchEntries,
		},
		fileInputs:       make(map[string]*recordSource),
		failedFileInputs: make(map[string]bool),
		commandInputs:    make(map[string]*commandInputPipe),
	}
	rt.vars["FS"] = stringValue(" ")
	rt.vars["RS"] = stringValue("\n")
	rt.vars["OFS"] = stringValue(" ")
	rt.vars["ORS"] = stringValue("\n")
	rt.vars["SUBSEP"] = stringValue("\034")
	rt.vars["RSTART"] = numberValue(0)
	rt.vars["RLENGTH"] = numberValue(-1)
	return rt
}

func (rt *runtime) run(ctx context.Context, files []string) builtins.Result {
	rt.inputArgs = append([]string{}, files...)
	defer rt.closeAllInputs()
	defer rt.clearCommandPipeLookaheadCaches()
	exited := false
	if err := rt.runRules(ctx, ruleBegin); err != nil {
		if code, ok := exitCodeFromError(err); ok {
			rt.exitCode = code
			exited = true
		} else {
			return rt.errorResult(err)
		}
	}
	if !exited && rt.needsInput() {
		for {
			rec, ok, err := rt.readMainRecord(ctx)
			if err != nil {
				if code, ok := exitCodeFromError(err); ok {
					rt.exitCode = code
					exited = true
					break
				}
				return rt.errorResult(err)
			}
			if !ok {
				break
			}
			if err := rt.setRecord(rec); err != nil {
				return rt.errorResult(err)
			}
			if err := rt.runRules(ctx, ruleNormal); err != nil {
				if errors.Is(err, errNextRecord) {
					continue
				}
				if code, ok := exitCodeFromError(err); ok {
					rt.exitCode = code
					exited = true
					break
				}
				return rt.errorResult(err)
			}
		}
	}
	if err := rt.runRules(ctx, ruleEnd); err != nil {
		if code, ok := exitCodeFromError(err); ok {
			rt.exitCode = code
		} else {
			return rt.errorResult(err)
		}
	}
	if err := rt.closeAllCommandPipes(ctx); err != nil {
		return rt.errorResult(err)
	}
	rt.flushStdoutBuffer()
	return builtins.Result{Code: normalizeAwkExitCode(rt.exitCode)}
}

func (rt *runtime) errorResult(err error) builtins.Result {
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		rt.flushStdoutBuffer()
	}
	rt.callCtx.Errf("awk: %v\n", err)
	code := uint8(1)
	if isFatalError(err) {
		code = 2
	}
	return builtins.Result{Code: code}
}

func isFatalError(err error) bool {
	const prefix = "fatal: "
	msg := err.Error()
	return len(msg) >= len(prefix) && msg[:len(prefix)] == prefix
}

func exitCodeFromError(err error) (int, bool) {
	exit, ok := err.(*exitError)
	if ok {
		return exit.code, true
	}
	return 0, false
}

func normalizeAwkExitCode(code int) uint8 {
	code %= 256
	if code < 0 {
		code += 256
	}
	return uint8(code)
}

func (rt *runtime) ensureEnviron() {
	if rt.environSet {
		return
	}
	rt.environSet = true
	elems := make(map[string]value)
	if rt.callCtx.Env != nil {
		rt.callCtx.Env(func(name, value string) bool {
			elems[name] = inputStringValue(value)
			return true
		})
	}
	rt.arrays["ENVIRON"] = elems
}

func (rt *runtime) applyOperandAssignment(arg string) (bool, error) {
	name, value, ok := strings.Cut(arg, "=")
	if !ok || !validIdentifierName(name) {
		return false, nil
	}
	if !validVarName(name) {
		return true, fmt.Errorf("invalid variable assignment %q", arg)
	}
	if err := rt.setVar(name, inputStringValue(DecodeAwkEscapes(value))); err != nil {
		return true, err
	}
	return true, nil
}

func (rt *runtime) needsInput() bool {
	for _, r := range rt.prog.rules {
		if r.kind == ruleNormal || r.kind == ruleEnd {
			return true
		}
	}
	return false
}

func (rt *runtime) readMainRecord(ctx context.Context) (string, bool, error) {
	for {
		if rt.mainInput == nil {
			ok, err := rt.openNextMainInput(ctx)
			if err != nil || !ok {
				return "", false, err
			}
		}
		rec, ok, err := rt.mainInput.readRecord(ctx)
		if err != nil {
			return "", false, fmt.Errorf("%s: %v", rt.mainInput.name, err)
		}
		if ok {
			if rt.inputRecords >= maxInputRecords {
				return "", false, fmt.Errorf("input record limit exceeded (maximum %d)", maxInputRecords)
			}
			rt.inputRecords++
			if err := rt.setVar("NR", numberValue(rt.nr.Number()+1)); err != nil {
				return "", false, err
			}
			if err := rt.setVar("FNR", numberValue(rt.fnr.Number()+1)); err != nil {
				return "", false, err
			}
			return rec, true, nil
		}
		rt.mainInput.close()
		rt.mainInput = nil
	}
}

func (rt *runtime) openNextMainInput(ctx context.Context) (bool, error) {
	for rt.inputIndex < len(rt.inputArgs) {
		arg := rt.inputArgs[rt.inputIndex]
		rt.inputIndex++
		assigned, err := rt.applyOperandAssignment(arg)
		if err != nil {
			return false, err
		}
		if assigned {
			continue
		}
		return rt.openMainInput(ctx, arg)
	}
	if !rt.mainHadInput && !rt.mainDefaultStdin {
		rt.mainDefaultStdin = true
		return rt.openMainInput(ctx, "-")
	}
	return false, nil
}

func (rt *runtime) openMainInput(ctx context.Context, file string) (bool, error) {
	src, err := rt.openRecordSource(ctx, file)
	if err != nil {
		return false, fmt.Errorf("fatal: cannot open file `%s' for reading: %v", file, err)
	}
	rt.mainHadInput = true
	if file == "-" {
		rt.mainUsedStdin = true
	}
	if err := rt.setVar("FILENAME", stringValue(file)); err != nil {
		src.close()
		return false, err
	}
	if err := rt.setVar("FNR", numberValue(0)); err != nil {
		src.close()
		return false, err
	}
	rt.mainInput = src
	return true, nil
}

// newRecordSource keeps at most one read in flight so context cancellation can
// return even when the underlying reader is blocked. As with the read builtin's
// fallback path, a reader that cannot be interrupted may retain that one
// goroutine until it eventually returns; no speculative record is started.
func (rt *runtime) newRecordSource(name string, rc io.ReadCloser) *recordSource {
	src := rt.newRecordSourceBase(name, rc)
	src.asyncRead = true
	return src
}

func (rt *runtime) newBufferedRecordSource(name string, rc io.ReadCloser) *recordSource {
	return rt.newRecordSourceBase(name, rc)
}

func (rt *runtime) newRecordSourceBase(name string, rc io.ReadCloser) *recordSource {
	src := &recordSource{name: name, rc: rc, rt: rt}
	sc := bufio.NewScanner(rc)
	sc.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if len(data) > src.recordBuffered {
			src.recordBuffered = len(data)
		}
		advance, token, err := scanAwkRecord(data, atEOF, src.rs)
		if err == nil && token != nil {
			src.recordAdvance = advance
		}
		return advance, token, err
	})
	sc.Buffer(make([]byte, 4096), MaxRecordBytes+utf8.UTFMax)
	src.sc = sc
	return src
}

func (src *recordSource) recordSeparator() string {
	return src.rt.getVar("RS").String()
}

func (src *recordSource) readRecord(ctx context.Context) (string, bool, error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	src.rs = src.recordSeparator()
	if src.asyncRead && ctx.Done() != nil {
		result := make(chan recordReadResult, 1)
		go func() {
			result <- src.scanRecord()
		}()
		select {
		case scanned := <-result:
			if err := ctx.Err(); err != nil {
				return "", false, err
			}
			return src.finishRecordRead(scanned)
		case <-ctx.Done():
			restore := false
			if src.interruptRead != nil {
				restore = src.interruptRead()
			}
			src.closeAsync()
			if restore && src.restoreRead != nil {
				go func() {
					<-result
					src.restoreRead()
				}()
			}
			return "", false, ctx.Err()
		}
	}
	scanned := src.scanRecord()
	if err := ctx.Err(); err != nil {
		return "", false, err
	}
	return src.finishRecordRead(scanned)
}

type recordReadResult struct {
	record  string
	advance int
	ok      bool
	err     error
}

func (src *recordSource) scanRecord() recordReadResult {
	src.recordAdvance = 0
	src.recordBuffered = 0
	if !src.sc.Scan() {
		return recordReadResult{advance: src.recordBuffered, err: src.sc.Err()}
	}
	rec := src.sc.Text()
	if len(rec) > MaxRecordBytes {
		return recordReadResult{advance: src.recordAdvance, err: fmt.Errorf("record exceeds %d bytes", MaxRecordBytes)}
	}
	return recordReadResult{record: rec, advance: src.recordAdvance, ok: true}
}

func (src *recordSource) finishRecordRead(scanned recordReadResult) (string, bool, error) {
	if err := src.rt.chargeInputBytes(scanned.advance); err != nil {
		return "", false, err
	}
	if scanned.err != nil || !scanned.ok {
		return scanned.record, scanned.ok, scanned.err
	}
	return scanned.record, true, nil
}

func (rt *runtime) chargeInputBytes(n int) error {
	if n > maxInputBytes || rt.inputBytes > maxInputBytes-n {
		return fmt.Errorf("%w (maximum %d bytes)", errInputBytesExceeded, maxInputBytes)
	}
	rt.inputBytes += n
	return nil
}

func (src *recordSource) close() {
	if src != nil && src.rc != nil {
		src.closeOnce.Do(func() {
			src.rc.Close() //nolint:errcheck
		})
	}
}

func (src *recordSource) closeAsync() {
	if src != nil && src.rc != nil {
		src.closeOnce.Do(func() {
			go func() {
				src.rc.Close() //nolint:errcheck
			}()
		})
	}
}

func scanAwkRecord(data []byte, atEOF bool, rs string) (int, []byte, error) {
	if err := validateRS(rs); err != nil {
		return 0, nil, err
	}
	sep := []byte(rs)
	if i := bytes.Index(data, sep); i >= 0 {
		return i + len(sep), data[:i], nil
	}
	if atEOF {
		if len(data) == 0 {
			return 0, nil, nil
		}
		return len(data), data, nil
	}
	return 0, nil, nil
}

func (rt *runtime) openRecordSource(ctx context.Context, file string) (*recordSource, error) {
	if file == "-" {
		if rt.callCtx.Stdin == nil {
			return rt.newBufferedRecordSource(file, io.NopCloser(strings.NewReader(""))), nil
		}
		src := rt.newRecordSource(file, io.NopCloser(rt.callCtx.Stdin))
		if setter, ok := rt.callCtx.Stdin.(interface {
			SetReadDeadline(time.Time) error
		}); ok {
			src.interruptRead = func() bool {
				return setter.SetReadDeadline(time.Unix(1, 0)) == nil
			}
			src.restoreRead = func() {
				_ = setter.SetReadDeadline(time.Time{})
			}
		}
		return src, nil
	}
	f, err := rt.openLegacyRecordInput(ctx, file)
	if err != nil {
		return nil, err
	}
	return rt.newRecordSource(file, f), nil
}

func (rt *runtime) openLegacyRecordInput(ctx context.Context, file string) (io.ReadCloser, error) {
	f, err := rt.callCtx.OpenFile(ctx, file, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (rt *runtime) writeCommandPipe(ctx context.Context, target expr, out string) error {
	budget := expressionTemporaryBudget{rt: rt}
	if err := budget.retainString(out); err != nil {
		return err
	}
	defer budget.release()
	commandValue, err := rt.eval(target)
	if err != nil {
		return err
	}
	if err := budget.retainValue(commandValue); err != nil {
		return err
	}
	command := commandValue.String()
	if command == "" {
		return fmt.Errorf("expression for `|' redirection has null string value")
	}
	pipe, err := rt.commandPipe(command)
	if err != nil {
		return err
	}
	if len(out) > MaxPipeBytes-rt.redirectPayload {
		return fmt.Errorf("command pipe input storage exceeds %d bytes", MaxPipeBytes)
	}
	if _, err := pipe.buf.WriteString(out); err != nil {
		return err
	}
	rt.redirectPayload += len(out)
	return ctx.Err()
}

func (rt *runtime) commandPipe(command string) (*commandPipe, error) {
	if pipe, ok := rt.pipes[command]; ok {
		return pipe, nil
	}
	if _, ok := rt.flushedPipes[command]; ok {
		delete(rt.flushedPipes, command)
		rt.releaseRedirection(command)
	}
	if err := rt.reserveRedirection(command); err != nil {
		return nil, err
	}
	env, envBytes, err := rt.commandEnvironment()
	if err != nil {
		rt.releaseRedirection(command)
		return nil, err
	}
	if envBytes > MaxVariableBytes-rt.commandEnvBytes {
		rt.releaseRedirection(command)
		return nil, fmt.Errorf("command pipe environment storage exceeds %d bytes", MaxVariableBytes)
	}
	pipe := &commandPipe{command: command, env: env, envBytes: envBytes}
	rt.pipes[command] = pipe
	rt.pipeOrder = append(rt.pipeOrder, command)
	rt.commandEnvBytes += envBytes
	return pipe, nil
}

func (rt *runtime) closeCommandPipe(ctx context.Context, command string, flushStdoutBefore bool) (uint8, bool, error) {
	pipe, ok := rt.pipes[command]
	if !ok {
		if status, ok := rt.flushedPipes[command]; ok {
			delete(rt.flushedPipes, command)
			rt.releaseRedirection(command)
			return status, true, nil
		}
		return 0, false, nil
	}
	delete(rt.pipes, command)
	rt.removeCommandPipeOrder(command)
	rt.clearCommandPipeLookahead(pipe)
	if flushStdoutBefore {
		rt.flushStdoutBuffer()
	}
	status, err := rt.runCommandPipe(ctx, pipe)
	rt.releaseRedirection(command)
	rt.redirectPayload -= pipe.buf.Len()
	rt.commandEnvBytes -= pipe.envBytes
	return status, true, err
}

func (rt *runtime) removeCommandPipeOrder(command string) {
	for i, candidate := range rt.pipeOrder {
		if candidate == command {
			copy(rt.pipeOrder[i:], rt.pipeOrder[i+1:])
			rt.pipeOrder = rt.pipeOrder[:len(rt.pipeOrder)-1]
			return
		}
	}
}

func (rt *runtime) closeAllCommandPipes(ctx context.Context) error {
	for len(rt.pipeOrder) > 0 {
		command := rt.pipeOrder[0]
		_, _, err := rt.closeCommandPipe(ctx, command, false)
		if err != nil {
			return err
		}
	}
	return nil
}

func (rt *runtime) flushCommandPipesForStdout(ctx context.Context, remaining stmtFuture) error {
	for _, command := range append([]string(nil), rt.pipeOrder...) {
		pipe := rt.pipes[command]
		if pipe != nil {
			action, err := rt.commandPipeNextAction(pipe, remaining)
			if err != nil {
				return err
			}
			if action != commandPipeActionNone {
				continue
			}
		}
		status, ok, err := rt.closeCommandPipe(ctx, command, false)
		if err != nil {
			return err
		}
		if ok {
			if err := rt.reserveRedirection(command); err != nil {
				return err
			}
			rt.flushedPipes[command] = status
		}
	}
	return nil
}

func (rt *runtime) shouldBufferStdoutForPipes(remaining stmtFuture) (bool, error) {
	for _, command := range rt.pipeOrder {
		pipe := rt.pipes[command]
		if pipe != nil {
			action, err := rt.commandPipeNextAction(pipe, remaining)
			if err != nil {
				return false, err
			}
			if action != commandPipeActionNone {
				return true, nil
			}
		}
	}
	return false, nil
}

func (rt *runtime) commandPipeNextAction(pipe *commandPipe, future stmtFuture) (commandPipeAction, error) {
	for {
		action, err := rt.cachedStmtsCommandPipeAction(pipe, future.stmts)
		if err != nil {
			return commandPipeActionNone, err
		}
		if action != commandPipeActionNone {
			return action, nil
		}
		if future.rules != nil {
			action, err = rt.ruleFutureCommandPipeAction(pipe, *future.rules)
			if err != nil {
				return commandPipeActionNone, err
			}
			if action != commandPipeActionNone {
				return action, nil
			}
		}
		if future.next == nil {
			return commandPipeActionNone, nil
		}
		future = *future.next
	}
}

func (rt *runtime) cachedStmtsCommandPipeAction(pipe *commandPipe, stmts []stmt) (commandPipeAction, error) {
	if len(stmts) == 0 {
		return commandPipeActionNone, nil
	}
	key := stmtSuffixKey{first: &stmts[0], length: len(stmts)}
	if action, ok := pipe.lookahead.stmtSuffixes[key]; ok {
		return action, nil
	}

	firstCached := len(stmts)
	action := commandPipeActionNone
	for i := 1; i < len(stmts); i++ {
		key = stmtSuffixKey{first: &stmts[i], length: len(stmts) - i}
		if cached, ok := pipe.lookahead.stmtSuffixes[key]; ok {
			firstCached = i
			action = cached
			break
		}
	}
	delta := commandPipeLookaheadUsage{stmtSuffixes: firstCached}
	if err := rt.reserveCommandPipeLookahead(pipe, delta); err != nil {
		return commandPipeActionNone, err
	}
	if err := rt.ensureCommandPipeFunctionTouches(pipe); err != nil {
		rt.releaseCommandPipeLookahead(pipe, delta)
		return commandPipeActionNone, err
	}
	resolveUserFunction := func(name string) commandPipeAction {
		if pipe.lookahead.functionTouches[name] {
			return commandPipeActionWrite
		}
		return commandPipeActionNone
	}
	if pipe.lookahead.stmtSuffixes == nil {
		pipe.lookahead.stmtSuffixes = make(map[stmtSuffixKey]commandPipeAction)
	}
	for i := firstCached - 1; i >= 0; i-- {
		if current := rt.stmtCommandPipeAction(pipe.command, stmts[i], resolveUserFunction); current != commandPipeActionNone {
			action = current
		}
		key = stmtSuffixKey{first: &stmts[i], length: len(stmts) - i}
		pipe.lookahead.stmtSuffixes[key] = action
	}
	return action, nil
}

func (rt *runtime) ensureCommandPipeFunctionTouches(pipe *commandPipe) error {
	if pipe.lookahead.functionTouches != nil {
		return nil
	}
	touches := make(map[string]bool)
	callers := make(map[string][]string, len(rt.prog.functions))
	queue := make([]string, 0, len(rt.prog.functions))
	for name, fn := range rt.prog.functions {
		caller := name
		resolveUserFunction := func(callee string) commandPipeAction {
			callers[callee] = append(callers[callee], caller)
			return commandPipeActionNone
		}
		if rt.stmtsCommandPipeAction(pipe.command, fn.body, resolveUserFunction) == commandPipeActionNone {
			continue
		}
		touches[name] = true
		queue = append(queue, name)
	}
	for i := 0; i < len(queue); i++ {
		for _, caller := range callers[queue[i]] {
			if touches[caller] {
				continue
			}
			touches[caller] = true
			queue = append(queue, caller)
		}
	}
	if err := rt.reserveCommandPipeLookahead(pipe, commandPipeLookaheadUsage{functionTouches: len(touches)}); err != nil {
		return err
	}
	pipe.lookahead.functionTouches = touches
	return nil
}

func (rt *runtime) ruleFutureCommandPipeAction(pipe *commandPipe, future ruleFutureCursor) (commandPipeAction, error) {
	action, err := rt.ruleActionsCommandPipeAction(pipe, future.kind, future.nextRule)
	if err != nil || action != commandPipeActionNone {
		return action, err
	}
	switch future.kind {
	case ruleBegin:
		action, err = rt.ruleActionsCommandPipeAction(pipe, ruleNormal, 0)
		if err != nil || action != commandPipeActionNone {
			return action, err
		}
		return rt.ruleActionsCommandPipeAction(pipe, ruleEnd, 0)
	case ruleNormal:
		action, err = rt.ruleActionsCommandPipeAction(pipe, ruleNormal, 0)
		if err != nil || action != commandPipeActionNone {
			return action, err
		}
		return rt.ruleActionsCommandPipeAction(pipe, ruleEnd, 0)
	default:
		return commandPipeActionNone, nil
	}
}

func (rt *runtime) ruleActionsCommandPipeAction(pipe *commandPipe, kind ruleKind, start int) (commandPipeAction, error) {
	if kind < ruleNormal || kind > ruleEnd || start >= len(rt.prog.rules) {
		return commandPipeActionNone, nil
	}
	suffixes := pipe.lookahead.ruleSuffixes[kind]
	if suffixes == nil {
		delta := commandPipeLookaheadUsage{ruleSuffixes: len(rt.prog.rules) + 1}
		if err := rt.reserveCommandPipeLookahead(pipe, delta); err != nil {
			return commandPipeActionNone, err
		}
		suffixes = make([]commandPipeAction, len(rt.prog.rules)+1)
		for i := len(rt.prog.rules) - 1; i >= 0; i-- {
			suffixes[i] = suffixes[i+1]
			r := &rt.prog.rules[i]
			if r.kind != kind || r.action == nil {
				continue
			}
			action, err := rt.cachedStmtsCommandPipeAction(pipe, r.action)
			if err != nil {
				rt.releaseCommandPipeLookahead(pipe, delta)
				return commandPipeActionNone, err
			}
			if action != commandPipeActionNone {
				suffixes[i] = action
			}
		}
		pipe.lookahead.ruleSuffixes[kind] = suffixes
	}
	if start < 0 {
		start = 0
	}
	return suffixes[start], nil
}

type commandPipeAction uint8

type commandPipeFunctionResolver func(string) commandPipeAction

const (
	commandPipeActionNone commandPipeAction = iota
	commandPipeActionWrite
	commandPipeActionClose
)

func (rt *runtime) stmtsCommandPipeAction(command string, stmts []stmt, resolveUserFunction commandPipeFunctionResolver) commandPipeAction {
	for _, st := range stmts {
		if action := rt.stmtCommandPipeAction(command, st, resolveUserFunction); action != commandPipeActionNone {
			return action
		}
	}
	return commandPipeActionNone
}

func (rt *runtime) stmtCommandPipeAction(command string, st stmt, resolveUserFunction commandPipeFunctionResolver) commandPipeAction {
	switch s := st.(type) {
	case *printStmt:
		if action := rt.exprsCommandPipeAction(command, s.args, resolveUserFunction); action != commandPipeActionNone {
			return action
		}
		return pipeExprCommandPipeAction(s.pipe, command)
	case *printfStmt:
		if action := rt.exprsCommandPipeAction(command, s.args, resolveUserFunction); action != commandPipeActionNone {
			return action
		}
		return pipeExprCommandPipeAction(s.pipe, command)
	case *ifStmt:
		if action := rt.exprCommandPipeAction(command, s.cond, resolveUserFunction); action != commandPipeActionNone {
			return action
		}
		return mergeBranchCommandPipeAction(
			rt.stmtsCommandPipeAction(command, s.thenStmts, resolveUserFunction),
			rt.stmtsCommandPipeAction(command, s.elseStmts, resolveUserFunction),
		)
	case *forInStmt:
		return rt.stmtsCommandPipeAction(command, s.body, resolveUserFunction)
	case *forStmt:
		forParts := []expr{s.init, s.cond, s.post}
		if action := rt.exprsCommandPipeAction(command, forParts, resolveUserFunction); action != commandPipeActionNone {
			return action
		}
		return rt.stmtsCommandPipeAction(command, s.body, resolveUserFunction)
	case *whileStmt:
		if action := rt.exprCommandPipeAction(command, s.cond, resolveUserFunction); action != commandPipeActionNone {
			return action
		}
		return rt.stmtsCommandPipeAction(command, s.body, resolveUserFunction)
	case *deleteStmt:
		return rt.exprsCommandPipeAction(command, s.indices, resolveUserFunction)
	case *exitStmt:
		return rt.exprCommandPipeAction(command, s.status, resolveUserFunction)
	case *returnStmt:
		return rt.exprCommandPipeAction(command, s.value, resolveUserFunction)
	case *exprStmt:
		return rt.exprCommandPipeAction(command, s.x, resolveUserFunction)
	default:
		return commandPipeActionNone
	}
}

func mergeBranchCommandPipeAction(left, right commandPipeAction) commandPipeAction {
	if left == commandPipeActionWrite || right == commandPipeActionWrite {
		return commandPipeActionWrite
	}
	if left == commandPipeActionClose || right == commandPipeActionClose {
		return commandPipeActionClose
	}
	return commandPipeActionNone
}

func pipeExprCommandPipeAction(pipe expr, command string) commandPipeAction {
	if pipe == nil {
		return commandPipeActionNone
	}
	if static, ok := staticStringExpr(pipe); ok {
		if static == command {
			return commandPipeActionWrite
		}
		return commandPipeActionNone
	}
	return commandPipeActionWrite
}

func (rt *runtime) exprsCommandPipeAction(command string, exprs []expr, resolveUserFunction commandPipeFunctionResolver) commandPipeAction {
	for _, x := range exprs {
		if action := rt.exprCommandPipeAction(command, x, resolveUserFunction); action != commandPipeActionNone {
			return action
		}
	}
	return commandPipeActionNone
}

func (rt *runtime) exprCommandPipeAction(command string, x expr, resolveUserFunction commandPipeFunctionResolver) commandPipeAction {
	if x == nil {
		return commandPipeActionNone
	}
	switch e := x.(type) {
	case *arrayRefExpr:
		return rt.exprsCommandPipeAction(command, e.indices, resolveUserFunction)
	case *compositeExpr:
		return rt.exprsCommandPipeAction(command, e.parts, resolveUserFunction)
	case *fieldExpr:
		return rt.exprCommandPipeAction(command, e.index, resolveUserFunction)
	case *groupedExpr:
		return rt.exprCommandPipeAction(command, e.x, resolveUserFunction)
	case *unaryExpr:
		return rt.exprCommandPipeAction(command, e.x, resolveUserFunction)
	case *binaryExpr:
		if action := rt.exprCommandPipeAction(command, e.left, resolveUserFunction); action != commandPipeActionNone {
			return action
		}
		return rt.exprCommandPipeAction(command, e.right, resolveUserFunction)
	case *ternaryExpr:
		if action := rt.exprCommandPipeAction(command, e.cond, resolveUserFunction); action != commandPipeActionNone {
			return action
		}
		return mergeBranchCommandPipeAction(
			rt.exprCommandPipeAction(command, e.then, resolveUserFunction),
			rt.exprCommandPipeAction(command, e.els, resolveUserFunction),
		)
	case *assignExpr:
		if action := rt.exprCommandPipeAction(command, e.left, resolveUserFunction); action != commandPipeActionNone {
			return action
		}
		return rt.exprCommandPipeAction(command, e.right, resolveUserFunction)
	case *incDecExpr:
		return rt.exprCommandPipeAction(command, e.x, resolveUserFunction)
	case *callExpr:
		if action := rt.exprsCommandPipeAction(command, e.args, resolveUserFunction); action != commandPipeActionNone {
			return action
		}
		if e.name == "close" && len(e.args) == 1 {
			if static, ok := staticStringExpr(e.args[0]); ok {
				if static == command {
					return commandPipeActionClose
				}
				return commandPipeActionNone
			}
			return commandPipeActionClose
		}
		if _, ok := rt.prog.functions[e.name]; ok && resolveUserFunction != nil {
			return resolveUserFunction(e.name)
		}
	case *getlineExpr:
		if action := rt.exprCommandPipeAction(command, e.target, resolveUserFunction); action != commandPipeActionNone {
			return action
		}
		return rt.exprCommandPipeAction(command, e.source, resolveUserFunction)
	}
	return commandPipeActionNone
}

func staticStringExpr(x expr) (string, bool) {
	switch e := x.(type) {
	case *stringExpr:
		return e.value, true
	case *groupedExpr:
		return staticStringExpr(e.x)
	default:
		return "", false
	}
}

func (rt *runtime) runCommandPipe(ctx context.Context, pipe *commandPipe) (uint8, error) {
	if rt.callCtx.RunScriptWithStdin == nil {
		return 127, fmt.Errorf("command pipes are not available")
	}
	dir := ""
	if rt.callCtx.WorkDir != nil {
		dir = rt.callCtx.WorkDir()
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stdout := &commandStdoutWriter{rt: rt, cancel: cancel}
	status, err := rt.callCtx.RunScriptWithStdin(childCtx, dir, pipe.command, pipe.env, bytes.NewReader(pipe.buf.Bytes()), stdout)
	if limitErr := stdout.limitError(); limitErr != nil {
		return status, limitErr
	}
	return status, err
}

func (rt *runtime) writeStdoutString(ctx context.Context, s string, remaining stmtFuture) error {
	if s != "" {
		if err := rt.reserveStdout(len(s)); err != nil {
			return err
		}
		buffer, err := rt.shouldBufferStdoutForPipes(remaining)
		if err != nil {
			return err
		}
		if buffer {
			if len(s) > MaxPipeBytes-rt.stdoutBuf.Len() {
				return fmt.Errorf("buffered output exceeds %d bytes", MaxPipeBytes)
			}
			_, err = rt.stdoutBuf.WriteString(s)
			if err != nil {
				return err
			}
			return ctx.Err()
		}
		if err := rt.flushCommandPipesForStdout(ctx, remaining); err != nil {
			return err
		}
		rt.flushStdoutBuffer()
	}
	rt.callCtx.Out(s)
	return nil
}

func (rt *runtime) reserveStdout(n int) error {
	rt.stdoutMu.Lock()
	defer rt.stdoutMu.Unlock()
	if n > MaxStdoutBytes-rt.stdoutBytes {
		return fmt.Errorf("stdout output exceeds %d bytes", MaxStdoutBytes)
	}
	rt.stdoutBytes += n
	return nil
}

type commandStdoutWriter struct {
	rt     *runtime
	cancel func()
	err    error
}

func (w *commandStdoutWriter) Write(p []byte) (int, error) {
	w.rt.stdoutMu.Lock()
	if w.err != nil {
		w.rt.stdoutMu.Unlock()
		return len(p), nil
	}
	if len(p) > MaxStdoutBytes-w.rt.stdoutBytes {
		w.err = fmt.Errorf("stdout output exceeds %d bytes", MaxStdoutBytes)
		w.rt.stdoutMu.Unlock()
		w.cancel()
		return len(p), nil
	}
	w.rt.stdoutBytes += len(p)
	n, err := w.rt.callCtx.Stdout.Write(p)
	w.rt.stdoutMu.Unlock()
	return n, err
}

func (w *commandStdoutWriter) limitError() error {
	w.rt.stdoutMu.Lock()
	defer w.rt.stdoutMu.Unlock()
	return w.err
}

func (rt *runtime) flushStdoutBuffer() {
	if rt.stdoutBuf.Len() == 0 {
		return
	}
	rt.callCtx.Out(rt.stdoutBuf.String())
	rt.stdoutBuf.Reset()
}

func (rt *runtime) getlineFileRecord(ctx context.Context, name string) (string, int, error) {
	src, ok := rt.fileInputs[name]
	if !ok {
		opened, err := rt.openFileInput(ctx, name)
		if err != nil {
			return "", 0, err
		}
		if opened == nil {
			return "", -1, nil
		}
		src = opened
	}
	return rt.getlineRecord(ctx, src)
}

func (rt *runtime) getlineRecord(ctx context.Context, src *recordSource) (string, int, error) {
	rec, ok, err := src.readRecord(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", 0, ctxErr
		}
		if errors.Is(err, errInputBytesExceeded) {
			return "", 0, err
		}
		rt.setErrno(err)
		return "", -1, nil
	}
	if !ok {
		return "", 0, nil
	}
	return rec, 1, nil
}

func (rt *runtime) openFileInput(ctx context.Context, name string) (*recordSource, error) {
	if name == "" {
		return nil, fmt.Errorf("fatal: expression for `<' redirection has null string value")
	}
	if !rt.failedFileInputs[name] {
		if err := rt.reserveRedirection(name); err != nil {
			return nil, err
		}
	}
	src, err := rt.openRecordSource(ctx, name)
	if err != nil {
		rt.failedFileInputs[name] = true
		rt.setErrno(err)
		return nil, nil
	}
	rt.fileInputs[name] = src
	delete(rt.failedFileInputs, name)
	return src, nil
}

func (rt *runtime) getlineCommandRecord(ctx context.Context, command string) (string, int, error) {
	pipe, ok := rt.commandInputs[command]
	if !ok {
		opened, err := rt.openCommandInput(ctx, command)
		if err != nil {
			return "", 0, err
		}
		pipe = opened
	}
	return rt.getlineRecord(ctx, pipe.source)
}

func (rt *runtime) openCommandInput(ctx context.Context, command string) (*commandInputPipe, error) {
	if command == "" {
		return nil, fmt.Errorf("fatal: expression for `|' redirection has null string value")
	}
	if rt.callCtx.RunScriptWithStdin == nil {
		return nil, fmt.Errorf("command pipes are not available")
	}
	if err := rt.reserveRedirection(command); err != nil {
		return nil, err
	}
	keepRedirection := false
	defer func() {
		if !keepRedirection {
			rt.releaseRedirection(command)
		}
	}()
	dir := ""
	if rt.callCtx.WorkDir != nil {
		dir = rt.callCtx.WorkDir()
	}
	var out limitedBuffer
	out.max = MaxPipeBytes - rt.redirectPayload
	env, _, err := rt.commandEnvironment()
	if err != nil {
		return nil, err
	}
	status, err := rt.callCtx.RunScriptWithStdin(ctx, dir, command, env, rt.commandInputStdin(), &out)
	if out.err != nil {
		return nil, fmt.Errorf("command pipe output storage exceeds %d bytes", MaxPipeBytes)
	}
	if err != nil {
		return nil, err
	}
	pipe := &commandInputPipe{
		source:       rt.newBufferedRecordSource(command, io.NopCloser(bytes.NewReader(out.buf.Bytes()))),
		status:       status,
		payloadBytes: out.buf.Len(),
	}
	rt.commandInputs[command] = pipe
	rt.redirectPayload += out.buf.Len()
	keepRedirection = true
	return pipe, nil
}

func (rt *runtime) commandInputStdin() io.Reader {
	if rt.callCtx.Stdin != nil && !rt.mainUsedStdin {
		return rt.callCtx.Stdin
	}
	return strings.NewReader("")
}

func (rt *runtime) commandEnvironment() ([]string, int, error) {
	rt.ensureEnviron()
	elems := rt.arrays["ENVIRON"]
	env := make([]string, 0, len(elems))
	bytesUsed := 0
	for name, value := range elems {
		entry := name + "=" + value.String()
		if len(entry) > MaxVariableBytes-bytesUsed {
			return nil, 0, fmt.Errorf("command environment exceeds %d bytes", MaxVariableBytes)
		}
		bytesUsed += len(entry)
		env = append(env, entry)
	}
	sortStringKeys(env, false)
	return env, bytesUsed, nil
}

type limitedBuffer struct {
	buf bytes.Buffer
	max int
	err error
}

func (w *limitedBuffer) Write(p []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if len(p) > w.max-w.buf.Len() {
		remaining := w.max - w.buf.Len()
		if remaining > 0 {
			_, _ = w.buf.Write(p[:remaining])
		}
		w.err = fmt.Errorf("command pipe output exceeds %d bytes", w.max)
		return len(p), w.err
	}
	n, err := w.buf.Write(p)
	if err != nil {
		w.err = err
	}
	return n, err
}

func (rt *runtime) closeCommandInput(command string) (uint8, bool, error) {
	pipe, ok := rt.commandInputs[command]
	if !ok {
		return 0, false, nil
	}
	pipe.source.close()
	delete(rt.commandInputs, command)
	rt.releaseRedirection(command)
	rt.redirectPayload -= pipe.payloadBytes
	return pipe.status, true, nil
}

func (rt *runtime) closeInputFile(name string) (int, bool) {
	if src, ok := rt.fileInputs[name]; ok {
		src.close()
		delete(rt.fileInputs, name)
		rt.releaseRedirection(name)
		return 0, true
	}
	if rt.failedFileInputs[name] {
		delete(rt.failedFileInputs, name)
		rt.releaseRedirection(name)
		return -1, true
	}
	return 0, false
}

func (rt *runtime) closeAllInputs() {
	if rt.mainInput != nil {
		rt.mainInput.close()
		rt.mainInput = nil
	}
	for name, src := range rt.fileInputs {
		src.close()
		delete(rt.fileInputs, name)
		rt.releaseRedirection(name)
	}
	for command, pipe := range rt.commandInputs {
		pipe.source.close()
		delete(rt.commandInputs, command)
		rt.releaseRedirection(command)
		rt.redirectPayload -= pipe.payloadBytes
	}
	for name := range rt.failedFileInputs {
		delete(rt.failedFileInputs, name)
		rt.releaseRedirection(name)
	}
}

func (rt *runtime) reserveRedirection(name string) error {
	if rt.redirections >= MaxRedirections {
		return fmt.Errorf("too many tracked redirections (maximum %d)", MaxRedirections)
	}
	if len(name) > MaxVariableBytes-rt.redirectionBytes {
		return fmt.Errorf("redirection name storage limit exceeded (%d bytes total)", rt.redirectionBytes+len(name))
	}
	rt.redirections++
	rt.redirectionBytes += len(name)
	return nil
}

func (rt *runtime) releaseRedirection(name string) {
	rt.redirections--
	rt.redirectionBytes -= len(name)
}

func (rt *runtime) setErrno(err error) {
	if err == nil {
		return
	}
	msg := err.Error()
	if rt.callCtx.PortableErr != nil {
		msg = rt.callCtx.PortableErr(err)
	}
	if len(msg) > 0 && msg[0] >= 'a' && msg[0] <= 'z' {
		msg = string(msg[0]-'a'+'A') + msg[1:]
	}
	_ = rt.setVar("ERRNO", stringValue(msg))
}

func (rt *runtime) setErrnoString(msg string) {
	_ = rt.setVar("ERRNO", stringValue(msg))
}

func (rt *runtime) chargeMainRuleEvaluation() error {
	if rt.mainRuleEvals >= maxMainRuleEvaluations {
		return fmt.Errorf("main-input rule evaluation limit exceeded (maximum %d)", maxMainRuleEvaluations)
	}
	rt.mainRuleEvals++
	return nil
}

func (rt *runtime) runRules(ctx context.Context, kind ruleKind) error {
	prevCtx := rt.ctx
	rt.ctx = ctx
	defer func() { rt.ctx = prevCtx }()
	for i := range rt.prog.rules {
		r := &rt.prog.rules[i]
		if err := ctx.Err(); err != nil {
			return err
		}
		if kind == ruleNormal {
			if err := rt.chargeMainRuleEvaluation(); err != nil {
				return err
			}
		}
		if r.kind != kind {
			continue
		}
		if kind == ruleNormal && r.pattern != nil {
			ok, err := rt.matchPattern(i, r.pattern)
			if err != nil {
				return err
			}
			if !ok {
				continue
			}
		}
		if r.action == nil {
			v := rt.field(0)
			if err := rt.chargeStringValue(v); err != nil {
				return err
			}
			out, err := rt.formatPrintValues([]value{v})
			if err != nil {
				return err
			}
			if err := rt.chargeStringProcessing(len(out)); err != nil {
				return err
			}
			if err := rt.writeStdoutString(ctx, out, rt.ruleFuture(kind, i+1)); err != nil {
				return err
			}
			continue
		}
		if err := rt.execStatementsWithFuture(ctx, r.action, rt.ruleFuture(kind, i+1)); err != nil {
			if errors.Is(err, errNextRecord) {
				if kind == ruleNormal {
					return err
				}
				return fmt.Errorf("next is not allowed in BEGIN or END")
			}
			return err
		}
	}
	return nil
}

func (rt *runtime) ruleFuture(kind ruleKind, nextRule int) stmtFuture {
	return stmtFuture{rules: &ruleFutureCursor{kind: kind, nextRule: nextRule}}
}

func (rt *runtime) matchPattern(ruleIndex int, x expr) (bool, error) {
	if rx, ok := x.(*rangeExpr); ok {
		return rt.matchRangePattern(ruleIndex, rx)
	}
	return rt.matchSimplePattern(x)
}

func (rt *runtime) matchRangePattern(ruleIndex int, x *rangeExpr) (bool, error) {
	if rt.rangeOn[ruleIndex] {
		end, err := rt.matchSimplePattern(x.end)
		if err != nil {
			return false, err
		}
		if end {
			rt.rangeOn[ruleIndex] = false
		}
		return true, nil
	}
	start, err := rt.matchSimplePattern(x.start)
	if err != nil {
		return false, err
	}
	if !start {
		return false, nil
	}
	end, err := rt.matchSimplePattern(x.end)
	if err != nil {
		return false, err
	}
	if !end {
		rt.rangeOn[ruleIndex] = true
	}
	return true, nil
}

func (rt *runtime) matchSimplePattern(x expr) (bool, error) {
	v, err := rt.eval(x)
	if err != nil {
		return false, err
	}
	return v.Bool(), nil
}

func (rt *runtime) setRecord(rec string) error {
	if err := validateRecordSize(rec); err != nil {
		return err
	}
	rec = cloneStoredString(rec)
	rt.record = rec
	fs := rt.getVar("FS").String()
	fields, err := rt.splitAwkFields(rec, fs)
	if err != nil {
		if errors.Is(err, errTooManyFields) {
			return fmt.Errorf("record has too many fields")
		}
		return err
	}
	for i, field := range fields {
		fields[i] = cloneStoredString(field)
	}
	rt.fields = fields
	if len(rt.fields) > MaxFields {
		return fmt.Errorf("record has too many fields")
	}
	rt.setComputedNF(len(rt.fields))
	return nil
}

func validateRecordSize(rec string) error {
	if len(rec) > MaxRecordBytes {
		return fmt.Errorf("record exceeds %d bytes", MaxRecordBytes)
	}
	return nil
}

func validateRebuiltRecordSize(fields []string, fieldCount, replacementIndex int, replacement, ofs string) error {
	total := 0
	for i := 0; i < fieldCount; i++ {
		if i > 0 {
			total += len(ofs)
			if total > MaxRecordBytes {
				return fmt.Errorf("record exceeds %d bytes", MaxRecordBytes)
			}
		}
		field := ""
		if i < len(fields) {
			field = fields[i]
		}
		if replacementIndex == i+1 {
			field = replacement
		}
		total += len(field)
		if total > MaxRecordBytes {
			return fmt.Errorf("record exceeds %d bytes", MaxRecordBytes)
		}
	}
	return nil
}

func (rt *runtime) rebuildRecordFromFields() error {
	ofs := rt.getVar("OFS").String()
	if err := validateRebuiltRecordSize(rt.fields, len(rt.fields), 0, "", ofs); err != nil {
		return err
	}
	rt.record = strings.Join(rt.fields, ofs)
	for i, field := range rt.fields {
		rt.fields[i] = cloneStoredString(field)
	}
	return nil
}

func (rt *runtime) setField(n int, v value) error {
	if n < 0 {
		return fmt.Errorf("invalid field index")
	}
	if n == 0 {
		return rt.setRecord(v.String())
	}
	if n > MaxFields {
		return fmt.Errorf("record has too many fields")
	}
	s := v.String()
	oldCount := len(rt.fields)
	fieldCount := max(len(rt.fields), n)
	if err := validateRebuiltRecordSize(rt.fields, fieldCount, n, s, rt.getVar("OFS").String()); err != nil {
		return err
	}
	for len(rt.fields) < n {
		rt.fields = append(rt.fields, "")
	}
	rt.fields[n-1] = s
	if err := rt.rebuildRecordFromFields(); err != nil {
		return err
	}
	if n > oldCount {
		rt.setComputedNF(len(rt.fields))
	}
	return nil
}

func (rt *runtime) setNF(v value) error {
	n := int(v.Number())
	if n < 0 {
		return fmt.Errorf("invalid NF value")
	}
	if n > MaxFields {
		return fmt.Errorf("record has too many fields")
	}
	if err := validateRebuiltRecordSize(rt.fields, n, 0, "", rt.getVar("OFS").String()); err != nil {
		return err
	}
	size := len(v.String())
	if size > MaxVariableBytes {
		return fmt.Errorf("variable value exceeds %d bytes", MaxVariableBytes)
	}
	old := rt.varSizes["NF"]
	if rt.varBytes-old+size > MaxVariableBytes {
		return fmt.Errorf("variable storage limit exceeded (%d bytes total)", rt.varBytes-old+size)
	}
	stored := cloneStoredValue(v)
	if n < len(rt.fields) {
		rt.fields = rt.fields[:n]
	} else {
		for len(rt.fields) < n {
			rt.fields = append(rt.fields, "")
		}
	}
	if err := rt.rebuildRecordFromFields(); err != nil {
		return err
	}
	rt.varBytes = rt.varBytes - old + size
	rt.varSizes["NF"] = size
	rt.nf = stored
	return nil
}

func (rt *runtime) setComputedNF(n int) {
	old := rt.varSizes["NF"]
	rt.varBytes -= old
	delete(rt.varSizes, "NF")
	rt.nf = numberValue(float64(n))
}

func (rt *runtime) splitAwkFields(s, fs string) ([]string, error) {
	if fs == " " {
		return splitAwkWhitespaceFields(s)
	}
	if err := rt.validateFS(fs); err != nil {
		return nil, err
	}
	if s == "" {
		return nil, nil
	}
	if isSingleRune(fs) {
		parts := strings.SplitN(s, fs, MaxFields+1)
		if len(parts) > MaxFields {
			return nil, errTooManyFields
		}
		return parts, nil
	}
	return rt.splitAwkRegex(s, fs)
}

func splitAwkWhitespaceFields(rec string) ([]string, error) {
	var fields []string
	for i := 0; i < len(rec); {
		for i < len(rec) && isAwkFieldBlank(rec[i]) {
			i++
		}
		start := i
		for i < len(rec) && !isAwkFieldBlank(rec[i]) {
			i++
		}
		if start < i {
			if len(fields) >= MaxFields {
				return nil, errTooManyFields
			}
			fields = append(fields, rec[start:i])
		}
	}
	return fields, nil
}

func isAwkFieldBlank(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n'
}

func splitAwkChars(s string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	chars := make([]string, 0, min(len(s), MaxFields))
	for len(s) > 0 {
		if len(chars) >= MaxFields {
			return nil, errTooManyFields
		}
		_, size := utf8.DecodeRuneInString(s)
		chars = append(chars, s[:size])
		s = s[size:]
	}
	return chars, nil
}

func (rt *runtime) splitAwkRegex(s, pattern string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	if pattern == "" {
		return splitAwkChars(s)
	}
	re, err := rt.compileRegex(pattern)
	if err != nil {
		return nil, err
	}
	searchText := s
	var offsets []int
	if re.byteMode {
		searchText, offsets = encodeAwkRegexBytes(s)
	}
	fields := make([]string, 0, min(len(s), MaxFields))
	last := 0
	search := 0
	iterations := 0
	for search <= len(searchText) {
		if rt.ctx != nil && iterations%256 == 0 {
			if err := rt.ctx.Err(); err != nil {
				return nil, err
			}
		}
		iterations++
		matcher := re.re
		if search > 0 {
			matcher = re.afterStart
		}
		match := matcher.FindStringIndex(searchText[search:])
		if match == nil {
			break
		}
		rawStart, rawEnd := search+match[0], search+match[1]
		start, end := rawStart, rawEnd
		if re.byteMode {
			start, end = offsets[rawStart], offsets[rawEnd]
		}
		if start == end {
			if end == len(s) {
				break
			}
			_, size := utf8.DecodeRuneInString(searchText[rawEnd:])
			search = rawEnd + size
			continue
		}
		if len(fields) >= MaxFields-1 {
			return nil, errTooManyFields
		}
		fields = append(fields, s[last:start])
		last = end
		search = rawEnd
	}
	if len(fields) == 0 {
		return []string{s}, nil
	}
	fields = append(fields, s[last:])
	return fields, nil
}

func (rt *runtime) field(n int) value {
	if n == 0 {
		return inputStringValue(rt.record)
	}
	if n < 0 || n > len(rt.fields) {
		return stringValue("")
	}
	return inputStringValue(rt.fields[n-1])
}

func (rt *runtime) currentFrame() *callFrame {
	if len(rt.frames) == 0 {
		return nil
	}
	return &rt.frames[len(rt.frames)-1]
}

func (rt *runtime) lookupLocal(name string) *localVar {
	frame := rt.currentFrame()
	if frame == nil {
		return nil
	}
	return frame.locals[name]
}

func rootLocalVar(v *localVar) *localVar {
	for v != nil && v.arrayAlias != nil {
		v = v.arrayAlias
	}
	return v
}

func (rt *runtime) localIsArray(v *localVar) bool {
	root := rootLocalVar(v)
	if root == nil {
		return false
	}
	if root.globalArrayName != "" {
		return rt.isGlobalArray(root.globalArrayName) || isBuiltinArrayName(root.globalArrayName)
	}
	return root.array != nil
}

func (rt *runtime) getVar(name string) value {
	if local := rt.lookupLocal(name); local != nil {
		root := rootLocalVar(local)
		if rt.localIsArray(root) {
			return unassignedValue()
		}
		if local.valueSet {
			return local.value
		}
		rt.markLocalScalarRead(local)
		return unassignedValue()
	}
	switch name {
	case "NF":
		return rt.nf
	case "NR":
		return rt.nr
	case "FNR":
		return rt.fnr
	case "FILENAME":
		return rt.filename
	default:
		if v, ok := rt.vars[name]; ok {
			return v
		}
		if isBuiltinArrayName(name) {
			return unassignedValue()
		}
		v := unassignedValue()
		rt.vars[name] = v
		return v
	}
}

func (rt *runtime) setVar(name string, v value) error {
	if local := rt.lookupLocal(name); local != nil {
		root := rootLocalVar(local)
		if rt.localIsArray(root) {
			return fmt.Errorf("cannot use array %s as scalar", name)
		}
		return rt.setLocalScalar(local, v)
	}
	if rt.isArray(name) {
		return fmt.Errorf("cannot use array %s as scalar", name)
	}
	if isBuiltinArrayName(name) {
		return fmt.Errorf("cannot use array %s as scalar", name)
	}
	switch name {
	case "NF":
		return rt.setNF(v)
	case "FS":
		if err := rt.validateFS(v.String()); err != nil {
			return err
		}
	case "RS":
		if err := validateRS(v.String()); err != nil {
			return err
		}
	}
	size := len(v.String())
	if size > MaxVariableBytes {
		return fmt.Errorf("variable value exceeds %d bytes", MaxVariableBytes)
	}
	old := rt.varSizes[name]
	if rt.varBytes-old+size > MaxVariableBytes {
		return fmt.Errorf("variable storage limit exceeded (%d bytes total)", rt.varBytes-old+size)
	}
	rt.varBytes = rt.varBytes - old + size
	rt.varSizes[name] = size
	stored := cloneStoredValue(v)
	switch name {
	case "NR":
		rt.nr = stored
	case "FNR":
		rt.fnr = stored
	case "FILENAME":
		rt.filename = stored
	default:
		rt.vars[name] = stored
	}
	return nil
}

func (rt *runtime) setLocalScalar(local *localVar, v value) error {
	root := rootLocalVar(local)
	size := len(v.String())
	if size > MaxVariableBytes {
		return fmt.Errorf("variable value exceeds %d bytes", MaxVariableBytes)
	}
	if rt.varBytes-local.valueSize+size > MaxVariableBytes {
		return fmt.Errorf("variable storage limit exceeded (%d bytes total)", rt.varBytes-local.valueSize+size)
	}
	rt.varBytes = rt.varBytes - local.valueSize + size
	local.valueSize = size
	local.value = cloneStoredValue(v)
	local.valueSet = true
	if root != nil && root != local && !rt.localIsArray(root) {
		root.valueSet = true
		if root.globalArrayName != "" {
			rt.markGlobalScalarName(root.globalArrayName)
		}
	}
	local.arrayAlias = nil
	local.globalArrayName = ""
	local.array = nil
	local.arraySizes = nil
	return nil
}

func (rt *runtime) markLocalScalarRead(local *localVar) {
	root := rootLocalVar(local)
	if root == nil || rt.localIsArray(root) {
		return
	}
	root.value = unassignedValue()
	root.valueSet = true
	if root.globalArrayName != "" {
		rt.markGlobalScalarName(root.globalArrayName)
	}
}

func (rt *runtime) isArray(name string) bool {
	if local := rt.lookupLocal(name); local != nil {
		return rt.localIsArray(local)
	}
	return rt.isGlobalArray(name)
}

func (rt *runtime) isGlobalArray(name string) bool {
	_, ok := rt.arrays[name]
	return ok
}

func (rt *runtime) localArrayStorage(name string, create bool) (map[string]value, *localVar, string, bool, error) {
	local := rt.lookupLocal(name)
	if local == nil {
		return nil, nil, "", false, nil
	}
	root := rootLocalVar(local)
	if root.valueSet && root.array == nil {
		return nil, nil, "", true, fmt.Errorf("cannot use scalar %s as array", name)
	}
	if root.globalArrayName != "" {
		actual := root.globalArrayName
		rt.ensureBuiltinArray(actual)
		if err := rt.validateArrayName(actual); err != nil {
			return nil, nil, "", true, err
		}
		if create || rt.arrays[actual] != nil {
			rt.markArrayName(actual)
		}
		return rt.arrays[actual], root, actual, true, nil
	}
	if root.array == nil && create {
		root.array = make(map[string]value)
		root.arraySizes = make(map[string]int)
	}
	return root.array, root, "", true, nil
}

func (rt *runtime) ensureLocalArray(name string) (map[string]value, *localVar, string, bool, error) {
	elems, local, globalName, handled, err := rt.localArrayStorage(name, true)
	if handled || err != nil {
		return elems, local, globalName, handled, err
	}
	rt.ensureBuiltinArray(name)
	if err := rt.validateArrayName(name); err != nil {
		return nil, nil, "", false, err
	}
	rt.markArrayName(name)
	return rt.arrays[name], nil, name, false, nil
}

func (rt *runtime) getArrayElem(name, key string) (value, error) {
	elems, local, globalName, handled, err := rt.ensureLocalArray(name)
	if err != nil {
		return value{}, err
	}
	if v, ok := elems[key]; ok {
		return v, nil
	}
	v := unassignedValue()
	if handled {
		if err := rt.setLocalArrayElem(local, globalName, key, v); err != nil {
			return value{}, err
		}
		return v, nil
	}
	if err := rt.setGlobalArrayElem(name, key, v); err != nil {
		return value{}, err
	}
	return v, nil
}

func (rt *runtime) hasArrayElem(name, key string) (bool, error) {
	elems, _, _, handled, err := rt.localArrayStorage(name, true)
	if err != nil {
		return false, err
	}
	if !handled {
		rt.ensureBuiltinArray(name)
		if err := rt.validateArrayName(name); err != nil {
			return false, err
		}
		rt.markArrayName(name)
		elems = rt.arrays[name]
	}
	_, ok := elems[key]
	return ok, nil
}

func (rt *runtime) setArrayElem(name, key string, v value) error {
	_, local, globalName, handled, err := rt.ensureLocalArray(name)
	if err != nil {
		return err
	}
	if handled {
		return rt.setLocalArrayElem(local, globalName, key, v)
	}
	return rt.setGlobalArrayElem(name, key, v)
}

func (rt *runtime) setGlobalArrayElem(name, key string, v value) error {
	rt.ensureBuiltinArray(name)
	if err := rt.validateArrayName(name); err != nil {
		return err
	}
	rt.markArrayName(name)
	size := len(key) + len(v.String())
	if size > MaxVariableBytes {
		return fmt.Errorf("array element exceeds %d bytes", MaxVariableBytes)
	}
	old := rt.arraySizes[arraySlot{name: name, key: key}]
	if rt.varBytes-old+size > MaxVariableBytes {
		return fmt.Errorf("variable storage limit exceeded (%d bytes total)", rt.varBytes-old+size)
	}
	key = cloneStoredString(key)
	v = cloneStoredValue(v)
	slot := arraySlot{name: name, key: key}
	rt.varBytes = rt.varBytes - old + size
	rt.arraySizes[slot] = size
	rt.arrays[name][key] = v
	return nil
}

func (rt *runtime) setLocalArrayElem(local *localVar, globalName, key string, v value) error {
	if globalName != "" {
		return rt.setGlobalArrayElem(globalName, key, v)
	}
	root := rootLocalVar(local)
	if root.array == nil {
		root.array = make(map[string]value)
		root.arraySizes = make(map[string]int)
	}
	size := len(key) + len(v.String())
	if size > MaxVariableBytes {
		return fmt.Errorf("array element exceeds %d bytes", MaxVariableBytes)
	}
	old := root.arraySizes[key]
	if rt.varBytes-old+size > MaxVariableBytes {
		return fmt.Errorf("variable storage limit exceeded (%d bytes total)", rt.varBytes-old+size)
	}
	key = cloneStoredString(key)
	v = cloneStoredValue(v)
	rt.varBytes = rt.varBytes - old + size
	root.arraySizes[key] = size
	root.array[key] = v
	return nil
}

func (rt *runtime) replaceArray(name string, elems map[string]value) error {
	if err := rt.deleteArray(name); err != nil {
		return err
	}
	for key, v := range elems {
		if err := rt.setArrayElem(name, key, v); err != nil {
			return err
		}
	}
	return nil
}

func (rt *runtime) deleteArrayElem(name, key string) error {
	elems, local, globalName, handled, err := rt.ensureLocalArray(name)
	if err != nil {
		return err
	}
	if handled {
		if globalName != "" {
			return rt.deleteGlobalArrayElem(globalName, key)
		}
		root := rootLocalVar(local)
		if root.array == nil {
			return nil
		}
		if old := root.arraySizes[key]; old > 0 {
			rt.varBytes -= old
			if rt.varBytes < 0 {
				rt.varBytes = 0
			}
		}
		delete(root.arraySizes, key)
		delete(elems, key)
		return nil
	}
	return rt.deleteGlobalArrayElem(name, key)
}

func (rt *runtime) deleteGlobalArrayElem(name, key string) error {
	rt.ensureBuiltinArray(name)
	if err := rt.validateArrayName(name); err != nil {
		return err
	}
	rt.markArrayName(name)
	slot := arraySlot{name: name, key: key}
	if old := rt.arraySizes[slot]; old > 0 {
		rt.varBytes -= old
		if rt.varBytes < 0 {
			rt.varBytes = 0
		}
	}
	delete(rt.arraySizes, slot)
	delete(rt.arrays[name], key)
	return nil
}

func (rt *runtime) deleteArray(name string) error {
	_, local, globalName, handled, err := rt.ensureLocalArray(name)
	if err != nil {
		return err
	}
	if handled {
		if globalName != "" {
			return rt.deleteGlobalArray(globalName)
		}
		root := rootLocalVar(local)
		for _, size := range root.arraySizes {
			rt.varBytes -= size
		}
		if rt.varBytes < 0 {
			rt.varBytes = 0
		}
		root.array = make(map[string]value)
		root.arraySizes = make(map[string]int)
		root.valueSet = false
		root.valueSize = 0
		return nil
	}
	return rt.deleteGlobalArray(name)
}

func (rt *runtime) deleteGlobalArray(name string) error {
	rt.ensureBuiltinArray(name)
	if err := rt.validateArrayName(name); err != nil {
		return err
	}
	rt.markArrayName(name)
	for slot, size := range rt.arraySizes {
		if slot.name != name {
			continue
		}
		rt.varBytes -= size
		delete(rt.arraySizes, slot)
	}
	if rt.varBytes < 0 {
		rt.varBytes = 0
	}
	rt.arrays[name] = make(map[string]value)
	return nil
}

func (rt *runtime) arrayKeys(name string) ([]string, error) {
	return rt.arrayKeysSorted(name, false)
}

func (rt *runtime) arrayKeysSorted(name string, ignoreCase bool) ([]string, error) {
	elems, _, _, handled, err := rt.localArrayStorage(name, true)
	if err != nil {
		return nil, err
	}
	if !handled {
		rt.ensureBuiltinArray(name)
		if err := rt.validateArrayName(name); err != nil {
			return nil, err
		}
		rt.markArrayName(name)
		elems = rt.arrays[name]
	}
	keys := make([]string, 0, len(elems))
	for key := range elems {
		keys = append(keys, key)
	}
	sortStringKeys(keys, ignoreCase)
	return keys, nil
}

func sortStringKeys(keys []string, ignoreCase bool) {
	slices.SortFunc(keys, func(left, right string) int {
		return compareAwkSortKeys(left, right, ignoreCase)
	})
}

func compareAwkSortKeys(left, right string, ignoreCase bool) int {
	compareLeft := left
	compareRight := right
	if ignoreCase {
		compareLeft = mapAwkCase(left, strings.ToLower)
		compareRight = mapAwkCase(right, strings.ToLower)
	}
	if compareLeft < compareRight {
		return -1
	}
	if compareLeft > compareRight {
		return 1
	}
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}

func (rt *runtime) ensureBuiltinArray(name string) {
	if name == "ENVIRON" {
		rt.ensureEnviron()
	}
}

func (rt *runtime) markArrayName(name string) {
	if rt.arrays[name] == nil {
		rt.arrays[name] = make(map[string]value)
	}
}

func (rt *runtime) validateArrayName(name string) error {
	if isBuiltinScalarName(name) {
		return fmt.Errorf("cannot use scalar %s as array", name)
	}
	if _, ok := rt.vars[name]; ok {
		return fmt.Errorf("cannot use scalar %s as array", name)
	}
	return nil
}

func (rt *runtime) markGlobalScalarName(name string) {
	if _, ok := rt.vars[name]; !ok {
		rt.vars[name] = unassignedValue()
		rt.varSizes[name] = 0
	}
}

func isBuiltinScalarName(name string) bool {
	switch name {
	case "NF", "NR", "FNR", "FILENAME":
		return true
	default:
		return false
	}
}

func isBuiltinArrayName(name string) bool {
	return name == "ENVIRON"
}

func isReservedAwkVariableName(name string) bool {
	return isBuiltinScalarName(name) || isBuiltinArrayName(name) || isWritableSpecialScalarName(name)
}

func isWritableSpecialScalarName(name string) bool {
	switch name {
	case "FS", "RS", "OFS", "ORS", "SUBSEP", "RSTART", "RLENGTH", "IGNORECASE":
		return true
	default:
		return false
	}
}

func (rt *runtime) validateFS(fs string) error {
	if fs == " " {
		return nil
	}
	if fs == "" {
		return fmt.Errorf("empty FS is not supported")
	}
	if isSingleRune(fs) {
		return nil
	}
	_, err := rt.compileRegex(fs)
	if err != nil {
		return err
	}
	return nil
}

func validateRS(rs string) error {
	if rs == "" {
		return fmt.Errorf("empty RS is not supported")
	}
	if !isSingleRune(rs) {
		return fmt.Errorf("multi-character RS is not supported")
	}
	return nil
}

func isSingleRune(s string) bool {
	if s == "" {
		return false
	}
	_, size := utf8.DecodeRuneInString(s)
	return size == len(s)
}

type awkRegex struct {
	re         *regexp.Regexp
	afterStart *regexp.Regexp
	byteMode   bool
}

func (rt *runtime) compileRegex(pattern string) (*awkRegex, error) {
	key := regexCacheKey{pattern: pattern, ignoreCase: rt.ignoreCase()}
	if re, ok := rt.regexCache[key]; ok {
		return re, nil
	}
	re, err := compileRegexWithOptions(pattern, key.ignoreCase)
	if err != nil {
		return nil, err
	}
	rt.rememberRegex(key, re)
	return re, nil
}

func (rt *runtime) rememberRegex(key regexCacheKey, re *awkRegex) {
	if len(key.pattern) > maxRegexCacheBytes {
		return
	}
	key.pattern = cloneStoredString(key.pattern)
	for len(rt.regexCacheOrder) >= maxRegexCacheEntries || rt.regexCacheBytes+len(key.pattern) > maxRegexCacheBytes {
		oldest := rt.regexCacheOrder[0]
		rt.regexCacheOrder = rt.regexCacheOrder[1:]
		delete(rt.regexCache, oldest)
		rt.regexCacheBytes -= len(oldest.pattern)
	}
	rt.regexCache[key] = re
	rt.regexCacheOrder = append(rt.regexCacheOrder, key)
	rt.regexCacheBytes += len(key.pattern)
}

func (rt *runtime) ignoreCase() bool {
	return rt.getVar("IGNORECASE").Bool()
}

func (rt *runtime) indexString(haystack, needle string) (int, error) {
	if needle == "" {
		return 1, nil
	}
	if !rt.ignoreCase() {
		pos := strings.Index(haystack, needle)
		if pos < 0 {
			return 0, nil
		}
		return runeLen(haystack[:pos]) + 1, nil
	}
	foldedHaystack := mapAwkCase(haystack, strings.ToLower)
	foldedNeedle := mapAwkCase(needle, strings.ToLower)
	pos := strings.Index(foldedHaystack, foldedNeedle)
	if pos < 0 {
		return 0, nil
	}
	return runeIndexForByteOffset(foldedHaystack, pos) + 1, nil
}

func compileRegex(pattern string) (*awkRegex, error) {
	return compileRegexWithOptions(pattern, false)
}

func compileRegexWithOptions(pattern string, ignoreCase bool) (*awkRegex, error) {
	if len(pattern) > MaxRegexBytes {
		return nil, fmt.Errorf("regular expression exceeds %d bytes", MaxRegexBytes)
	}
	normalized, byteMode := normalizeAwkRegex(pattern)
	if ignoreCase {
		normalized = "(?i:" + normalized + ")"
	}
	re, err := regexp.Compile(normalized)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression %q: %v", pattern, err)
	}
	re.Longest()
	afterStart, err := compileRegexAfterStart(normalized, re)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression %q: %v", pattern, err)
	}
	return &awkRegex{re: re, afterStart: afterStart, byteMode: byteMode}, nil
}

func compileRegexAfterStart(pattern string, original *regexp.Regexp) (*regexp.Regexp, error) {
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, err
	}
	if !disableBeginText(parsed) {
		return original, nil
	}
	afterStart, err := regexp.Compile(parsed.String())
	if err != nil {
		return nil, err
	}
	afterStart.Longest()
	return afterStart, nil
}

func disableBeginText(re *syntax.Regexp) bool {
	if re.Op == syntax.OpBeginText {
		*re = syntax.Regexp{Op: syntax.OpNoMatch}
		return true
	}
	changed := false
	for _, sub := range re.Sub {
		changed = disableBeginText(sub) || changed
	}
	return changed
}

func (re *awkRegex) MatchString(s string) bool {
	if !re.byteMode {
		return re.re.MatchString(s)
	}
	encoded, _ := encodeAwkRegexBytes(s)
	return re.re.MatchString(encoded)
}

func (re *awkRegex) FindStringIndex(s string) []int {
	if !re.byteMode {
		return re.re.FindStringIndex(s)
	}
	encoded, offsets := encodeAwkRegexBytes(s)
	loc := re.re.FindStringIndex(encoded)
	if loc == nil {
		return nil
	}
	return []int{offsets[loc[0]], offsets[loc[1]]}
}

func (re *awkRegex) FindStringRuneIndex(s string) []int {
	loc := re.FindStringIndex(s)
	if loc == nil {
		return nil
	}
	if !re.byteMode {
		return []int{runeLen(s[:loc[0]]), runeLen(s[:loc[1]])}
	}
	start, end := runeRangeForByteRange(s, loc[0], loc[1])
	return []int{start, end}
}

func (re *awkRegex) FindAllStringIndex(s string, n int) [][]int {
	if !re.byteMode {
		return re.re.FindAllStringIndex(s, n)
	}
	encoded, offsets := encodeAwkRegexBytes(s)
	matches := re.re.FindAllStringIndex(encoded, n)
	for _, loc := range matches {
		loc[0] = offsets[loc[0]]
		loc[1] = offsets[loc[1]]
	}
	return matches
}

func (re *awkRegex) FindStringSubmatchIndex(s string) []int {
	loc := re.FindAllStringSubmatchIndex(s, 1)
	if len(loc) == 0 {
		return nil
	}
	return loc[0]
}

func (re *awkRegex) FindAllStringSubmatchIndex(s string, n int) [][]int {
	if !re.byteMode {
		return re.re.FindAllStringSubmatchIndex(s, n)
	}
	encoded, offsets := encodeAwkRegexBytes(s)
	matches := re.re.FindAllStringSubmatchIndex(encoded, n)
	for _, locs := range matches {
		for i := 0; i+1 < len(locs); i += 2 {
			if locs[i] < 0 {
				continue
			}
			locs[i] = offsets[locs[i]]
			locs[i+1] = offsets[locs[i+1]]
		}
	}
	return matches
}

func runeRangeForByteRange(s string, startByte, endByte int) (int, int) {
	if startByte < 0 {
		startByte = 0
	}
	if startByte > len(s) {
		startByte = len(s)
	}
	if endByte < startByte {
		endByte = startByte
	}
	if endByte > len(s) {
		endByte = len(s)
	}
	if startByte == endByte {
		idx := runeIndexForByteOffset(s, startByte)
		return idx, idx
	}
	return runeIndexForByteOffset(s, startByte), runeIndexAfterByteOffset(s, endByte)
}

func runeIndexForByteOffset(s string, offset int) int {
	if offset <= 0 {
		return 0
	}
	runeIndex := 0
	for i := 0; i < len(s); runeIndex++ {
		_, size := utf8.DecodeRuneInString(s[i:])
		next := i + size
		if offset < next {
			return runeIndex
		}
		if offset == next {
			return runeIndex + 1
		}
		i = next
	}
	return runeIndex
}

func runeIndexAfterByteOffset(s string, offset int) int {
	if offset <= 0 {
		return 0
	}
	runeIndex := 0
	for i := 0; i < len(s); runeIndex++ {
		_, size := utf8.DecodeRuneInString(s[i:])
		next := i + size
		if offset <= next {
			return runeIndex + 1
		}
		i = next
	}
	return runeIndex
}

func normalizeAwkRegex(pattern string) (string, bool) {
	var decoded strings.Builder
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		if ch != '\\' {
			decoded.WriteByte(ch)
			continue
		}
		if i+1 >= len(pattern) {
			decoded.WriteByte(ch)
			continue
		}
		if isOctalDigit(rune(pattern[i+1])) {
			value := 0
			for digits := 0; digits < 3 && i+1 < len(pattern) && isOctalDigit(rune(pattern[i+1])); digits++ {
				i++
				value = value*8 + int(pattern[i]-'0')
			}
			decoded.WriteByte(byte(value))
			continue
		}
		i++
		writeAwkRegexEscape(&decoded, pattern[i])
	}

	var normalized strings.Builder
	byteMode := false
	decodedPattern := decoded.String()
	for i := 0; i < len(decodedPattern); {
		r, size := utf8.DecodeRuneInString(decodedPattern[i:])
		if r == utf8.RuneError && size == 1 {
			writeAwkRegexByteMarker(&normalized, decodedPattern[i])
			byteMode = true
			i++
			continue
		}
		normalized.WriteString(decodedPattern[i : i+size])
		i += size
	}
	return normalized.String(), byteMode
}

// Private-use runes keep byte-mode values outside Unicode case-fold pairs.
const awkRegexByteRuneBase = '\ue000'

func writeAwkRegexByteMarker(b *strings.Builder, value byte) {
	b.WriteRune(awkRegexByteRuneBase + rune(value))
}

func encodeAwkRegexBytes(s string) (string, []int) {
	var b strings.Builder
	offsets := []int{0}
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		before := b.Len()
		if r == utf8.RuneError && size == 1 {
			writeAwkRegexByteMarker(&b, s[i])
			for j := before + 1; j < b.Len(); j++ {
				offsets = append(offsets, i)
			}
			offsets = append(offsets, i+1)
			i++
			continue
		}
		b.WriteString(s[i : i+size])
		for j := 1; j <= size; j++ {
			offsets = append(offsets, i+j)
		}
		i += size
	}
	return b.String(), offsets
}

func writeAwkRegexEscape(b *strings.Builder, esc byte) {
	switch esc {
	case 'n':
		b.WriteString(`\n`)
	case 't':
		b.WriteString(`\t`)
	case 'r':
		b.WriteString(`\r`)
	case 'b':
		b.WriteString(`\x08`)
	case 'f':
		b.WriteString(`\f`)
	case 'a':
		b.WriteString(`\x07`)
	case 'v':
		b.WriteString(`\x0b`)
	case '.', '[', ']', '(', ')', '{', '}', '*', '+', '?', '|', '^', '$', '-', '\\':
		b.WriteByte('\\')
		b.WriteByte(esc)
	case 'w', 'W', 's', 'S':
		b.WriteByte('\\')
		b.WriteByte(esc)
	default:
		b.WriteByte(esc)
	}
}
