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
	"unicode"
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
	maxFileOpenAttempts                = 1 << 10
	minRegexCompileWork                = 1 << 10
	maxInputBytes                      = maxStringProcessingBytes
	maxRegexCacheEntries               = 64
	maxRegexCacheBytes                 = MaxProgramBytes
	maxFunctionDepth                   = 256
	awkBreakableSpaceClass             = `\x{20}\x{1680}\x{2000}-\x{2006}\x{2008}-\x{200a}\x{205f}\x{3000}`
	awkNoBreakSpaceClass               = `\x{a0}\x{2007}\x{202f}`
	awkLowercaseTitleClass             = `\x{1c5}\x{1c8}\x{1cb}\x{1f2}`
	awkUnicode151LetterClass           = `\x{2ebf0}-\x{2ee5d}`
	awkUnicode151SymbolClass           = `\x{2ffc}-\x{2fff}\x{31ef}`
)

var (
	errTooManyFields        = errors.New("too many fields")
	errInputBytesExceeded   = errors.New("input byte limit exceeded")
	awkNonASCIIDigitClass   = awkUnicodeRangeClassExcluding(unicode.Nd, unicode.ASCII_Hex_Digit)
	awkOtherAlphabeticClass = awkUnicodeRangeClass(unicode.Other_Alphabetic)
	awkOtherLowercaseClass  = awkUnicodeRangeClass(unicode.Other_Lowercase)
	awkOtherUppercaseClass  = awkUnicodeRangeClass(unicode.Other_Uppercase)
	awkPunctuationClass     = awkUnicodeRangeClassExcluding(unicode.P, unicode.Other_Alphabetic) +
		awkUnicodeRangeClassExcluding(unicode.S, unicode.Other_Alphabetic) +
		awkUnicodeRangeClassExcluding(unicode.M, unicode.Other_Alphabetic) +
		awkUnicodeRangeClassExcluding(unicode.No, unicode.Other_Alphabetic) +
		awkUnicodeRangeClassExcluding(unicode.Cf, unicode.Other_Alphabetic) +
		`\p{Co}` + awkNoBreakSpaceClass
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
	formatted, _ := formatAwkNumberWithFormat(n, "%.6g")
	return formatted
}

func formatAwkNumberWithFormat(n float64, format string) (string, error) {
	if n == 0 {
		n = 0
	}
	if _, special := formatAwkSpecialNumber(n, false); !special {
		fixed := strconv.FormatFloat(n, 'f', -1, 64)
		if !strings.ContainsRune(fixed, '.') {
			return strconv.FormatFloat(n, 'f', 0, 64), nil
		}
	}
	return formatPrintf(format, []value{numberValue(n)})
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
	switch strings.ToLower(s) {
	case "+inf":
		n, _ := strconv.ParseFloat("+Inf", 64)
		return n, true
	case "-inf":
		n, _ := strconv.ParseFloat("-Inf", 64)
		return n, true
	case "+nan", "-nan":
		n, _ := strconv.ParseFloat("NaN", 64)
		if s[0] == '-' {
			n = -n
		}
		return n, true
	}
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
	special := strings.Trim(s, " \t\n\r\f\v")
	switch strings.ToLower(special) {
	case "+inf", "-inf", "+nan", "-nan":
		return special
	}

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
	flushedPipes     map[string]flushedCommandPipe
	pipeOrder        []string
	redirectionOrder int
	lookaheadUsage   commandPipeLookaheadUsage
	lookaheadLimits  commandPipeLookaheadUsage
	stdoutBuf        bytes.Buffer
	stdoutMu         sync.Mutex
	stdoutBytes      int
	redirectMu       sync.Mutex
	inputArgs        []string
	inputIndex       int
	mainInput        *recordSource
	mainHadInput     bool
	mainUsedStdin    bool
	mainDefaultStdin bool
	fileInputs       map[string]*recordSource
	failedFileInputs map[string]bool
	fileOpenAttempts int
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
	command       string
	buf           bytes.Buffer
	env           []string
	envBytes      int
	creationOrder int
	lookahead     commandPipeLookaheadCache
}

type flushedCommandPipe struct {
	status        uint8
	creationOrder int
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
	source        *recordSource
	cancel        context.CancelFunc
	done          chan struct{}
	status        uint8
	err           error
	payloadBytes  int
	creationOrder int
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
		flushedPipes: make(map[string]flushedCommandPipe),
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
	rt.vars["OFMT"] = stringValue("%.6g")
	rt.vars["CONVFMT"] = stringValue("%.6g")
	rt.vars["SUBSEP"] = stringValue("\034")
	rt.vars["RSTART"] = numberValue(0)
	rt.vars["RLENGTH"] = numberValue(0)
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
			return rt.errorResult(ctx, err)
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
				return rt.errorResult(ctx, err)
			}
			if !ok {
				break
			}
			if err := rt.setRecord(rec); err != nil {
				return rt.errorResult(ctx, err)
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
				return rt.errorResult(ctx, err)
			}
		}
	}
	if err := rt.runRules(ctx, ruleEnd); err != nil {
		if code, ok := exitCodeFromError(err); ok {
			rt.exitCode = code
		} else {
			return rt.errorResult(ctx, err)
		}
	}
	if err := rt.closeAllCommandPipes(ctx); err != nil {
		return rt.errorResult(ctx, err)
	}
	if err := rt.closeAllCommandInputs(); err != nil {
		return rt.errorResult(ctx, err)
	}
	rt.flushStdoutBuffer()
	return builtins.Result{Code: normalizeAwkExitCode(rt.exitCode)}
}

func (rt *runtime) errorResult(ctx context.Context, err error) builtins.Result {
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		rt.flushStdoutBuffer()
		_ = rt.closeAllCommandPipes(ctx)
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
			return "", false, fmt.Errorf("%s: %w", rt.mainInput.name, err)
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
	if err := rt.chargeFileOpenAttempt(file); err != nil {
		return false, err
	}
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
	if !rt.mainDefaultStdin {
		if err := rt.setVar("FNR", numberValue(0)); err != nil {
			src.close()
			return false, err
		}
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
		stdin := rt.callCtx.Stdin
		// Normal cleanup must not close caller-owned stdin; cancellation may.
		src := rt.newRecordSource(file, io.NopCloser(stdin))
		closer, _ := stdin.(interface{ Close() error })
		setter, _ := stdin.(interface {
			SetReadDeadline(time.Time) error
		})
		if setter != nil || closer != nil {
			src.interruptRead = func() bool {
				if setter != nil && setter.SetReadDeadline(time.Unix(1, 0)) == nil {
					return true
				}
				if closer != nil {
					_ = closer.Close()
				}
				return false
			}
		}
		if setter != nil {
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
	command, err := budget.convfmtString(commandValue)
	if err != nil {
		return err
	}
	if command == "" {
		return fmt.Errorf("fatal: expression for `|' redirection has null string value")
	}
	pipe, err := rt.commandPipe(command)
	if err != nil {
		return err
	}
	if !rt.reserveRedirectPayload(len(out)) {
		return fmt.Errorf("command pipe input storage exceeds %d bytes", MaxPipeBytes)
	}
	n, err := pipe.buf.WriteString(out)
	if n < len(out) {
		rt.releaseRedirectPayload(len(out) - n)
	}
	if err != nil {
		return err
	}
	return ctx.Err()
}

func (rt *runtime) commandPipe(command string) (*commandPipe, error) {
	if pipe, ok := rt.pipes[command]; ok {
		return pipe, nil
	}
	flushed, wasFlushed := rt.flushedPipes[command]
	if wasFlushed {
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
	creationOrder := flushed.creationOrder
	if !wasFlushed {
		creationOrder = rt.nextCommandRedirectionOrder()
	}
	pipe := &commandPipe{command: command, env: env, envBytes: envBytes, creationOrder: creationOrder}
	rt.pipes[command] = pipe
	rt.pipeOrder = append(rt.pipeOrder, command)
	rt.commandEnvBytes += envBytes
	return pipe, nil
}

func (rt *runtime) closeCommandPipe(ctx context.Context, command string, flushStdoutBefore bool) (uint8, bool, error) {
	pipe, ok := rt.pipes[command]
	if !ok {
		if flushed, ok := rt.flushedPipes[command]; ok {
			delete(rt.flushedPipes, command)
			rt.releaseRedirection(command)
			return flushed.status, true, nil
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
	rt.releaseRedirectPayload(pipe.buf.Len())
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
			rt.flushedPipes[command] = flushedCommandPipe{status: status, creationOrder: pipe.creationOrder}
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
	if err := rt.chargeFileOpenAttempt(name); err != nil {
		return nil, err
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

func (rt *runtime) chargeFileOpenAttempt(name string) error {
	if name == "-" {
		return nil
	}
	if rt.fileOpenAttempts >= maxFileOpenAttempts {
		return fmt.Errorf("file open attempt limit exceeded (maximum %d)", maxFileOpenAttempts)
	}
	rt.fileOpenAttempts++
	return nil
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
	rec, status, err := rt.getlineRecord(ctx, pipe.source)
	if err != nil {
		return "", 0, err
	}
	if _, childErr, done := pipe.result(); done && childErr != nil {
		_, _, closeErr := rt.closeCommandInput(command)
		return "", 0, closeErr
	}
	return rec, status, nil
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
	env, _, err := rt.commandEnvironment()
	if err != nil {
		return nil, err
	}
	childCtx, cancel := context.WithCancel(ctx)
	reader, writer := io.Pipe()
	pipe := &commandInputPipe{
		cancel:        cancel,
		done:          make(chan struct{}),
		creationOrder: rt.nextCommandRedirectionOrder(),
	}
	pipe.source = rt.newRecordSource(command, reader)
	pipe.source.interruptRead = func() bool {
		cancel()
		return false
	}
	rt.commandInputs[command] = pipe
	keepRedirection = true
	stdin := rt.commandInputStdin()
	go func() {
		defer cancel()
		stdout := &commandInputWriter{rt: rt, pipe: pipe, writer: writer, cancel: cancel}
		status, err := rt.callCtx.RunScriptWithStdin(childCtx, dir, command, env, stdin, stdout)
		if limitErr := stdout.limitError(); limitErr != nil {
			err = limitErr
		}
		pipe.status = status
		pipe.err = err
		close(pipe.done)
		_ = writer.CloseWithError(err)
	}()
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
		// GNU awk exports numeric-only ENVIRON values as empty strings.
		entry := name + "=" + value.s
		if len(entry) > MaxVariableBytes-bytesUsed {
			return nil, 0, fmt.Errorf("command environment exceeds %d bytes", MaxVariableBytes)
		}
		bytesUsed += len(entry)
		env = append(env, entry)
	}
	if err := rt.chargeStringSort(len(env), bytesUsed); err != nil {
		return nil, 0, err
	}
	sortStringKeys(env, false)
	return env, bytesUsed, nil
}

type commandInputWriter struct {
	rt     *runtime
	pipe   *commandInputPipe
	writer *io.PipeWriter
	cancel context.CancelFunc
	err    error
}

func (w *commandInputWriter) Write(p []byte) (int, error) {
	w.rt.redirectMu.Lock()
	if w.err != nil {
		err := w.err
		w.rt.redirectMu.Unlock()
		return 0, err
	}
	if len(p) > MaxPipeBytes-w.rt.redirectPayload {
		w.err = fmt.Errorf("command pipe output storage exceeds %d bytes", MaxPipeBytes)
		err := w.err
		w.rt.redirectMu.Unlock()
		w.cancel()
		return len(p), err
	}
	w.rt.redirectPayload += len(p)
	w.pipe.payloadBytes += len(p)
	w.rt.redirectMu.Unlock()
	n, err := w.writer.Write(p)
	if n < len(p) {
		released := len(p) - n
		w.rt.redirectMu.Lock()
		w.pipe.payloadBytes -= released
		w.rt.redirectPayload -= released
		w.rt.redirectMu.Unlock()
	}
	return n, err
}

func (w *commandInputWriter) limitError() error {
	w.rt.redirectMu.Lock()
	defer w.rt.redirectMu.Unlock()
	return w.err
}

func (pipe *commandInputPipe) result() (uint8, error, bool) {
	select {
	case <-pipe.done:
		return pipe.status, pipe.err, true
	default:
		return 0, nil, false
	}
}

func (rt *runtime) closeCommandInput(command string) (uint8, bool, error) {
	pipe, ok := rt.commandInputs[command]
	if !ok {
		return 0, false, nil
	}
	_, _, completed := pipe.result()
	pipe.source.close()
	if !completed {
		pipe.cancel()
	}
	<-pipe.done
	delete(rt.commandInputs, command)
	rt.releaseRedirection(command)
	rt.releaseCommandInputPayload(pipe)
	if !completed && (errors.Is(pipe.err, context.Canceled) || errors.Is(pipe.err, io.ErrClosedPipe)) {
		return pipe.status, true, nil
	}
	return pipe.status, true, pipe.err
}

func (rt *runtime) nextCommandRedirectionOrder() int {
	rt.redirectionOrder++
	return rt.redirectionOrder
}

func (rt *runtime) commandOutputCreationOrder(command string) (int, bool) {
	if pipe, ok := rt.pipes[command]; ok {
		return pipe.creationOrder, true
	}
	if pipe, ok := rt.flushedPipes[command]; ok {
		return pipe.creationOrder, true
	}
	return 0, false
}

func (rt *runtime) closeCommandRedirection(ctx context.Context, command string, flushStdoutBefore bool) (uint8, bool, error) {
	outputOrder, hasOutput := rt.commandOutputCreationOrder(command)
	input, hasInput := rt.commandInputs[command]
	if hasOutput && (!hasInput || outputOrder > input.creationOrder) {
		return rt.closeCommandPipe(ctx, command, flushStdoutBefore)
	}
	if hasInput {
		return rt.closeCommandInput(command)
	}
	return 0, false, nil
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
	for command := range rt.commandInputs {
		_, _, _ = rt.closeCommandInput(command)
	}
	for name := range rt.failedFileInputs {
		delete(rt.failedFileInputs, name)
		rt.releaseRedirection(name)
	}
}

func (rt *runtime) closeAllCommandInputs() error {
	for command := range rt.commandInputs {
		_, _, err := rt.closeCommandInput(command)
		if err != nil {
			return err
		}
	}
	return nil
}

func (rt *runtime) reserveRedirectPayload(n int) bool {
	rt.redirectMu.Lock()
	defer rt.redirectMu.Unlock()
	if n > MaxPipeBytes-rt.redirectPayload {
		return false
	}
	rt.redirectPayload += n
	return true
}

func (rt *runtime) releaseCommandInputPayload(pipe *commandInputPipe) {
	rt.redirectMu.Lock()
	defer rt.redirectMu.Unlock()
	rt.redirectPayload -= pipe.payloadBytes
	pipe.payloadBytes = 0
}

func (rt *runtime) releaseRedirectPayload(n int) {
	rt.redirectMu.Lock()
	defer rt.redirectMu.Unlock()
	rt.redirectPayload -= n
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
				return fmt.Errorf("fatal: next is not allowed in BEGIN or END")
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

func validateRebuiltRecordSize(fields []string, fieldCount, replacementIndex int, replacement, ofs string) (int, error) {
	total := 0
	for i := 0; i < fieldCount; i++ {
		if i > 0 {
			total += len(ofs)
			if total > MaxRecordBytes {
				return 0, fmt.Errorf("record exceeds %d bytes", MaxRecordBytes)
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
			return 0, fmt.Errorf("record exceeds %d bytes", MaxRecordBytes)
		}
	}
	return total, nil
}

func (rt *runtime) rebuildRecordFromFields() {
	ofs := rt.getVar("OFS").String()
	rt.record = strings.Join(rt.fields, ofs)
	for i, field := range rt.fields {
		rt.fields[i] = cloneStoredString(field)
	}
}

func (rt *runtime) setField(n int, v value) error {
	if n < 0 {
		return fmt.Errorf("invalid field index")
	}
	if n > MaxFields {
		return fmt.Errorf("record has too many fields")
	}
	s, err := rt.conversionString(v, "CONVFMT")
	if err != nil {
		return err
	}
	if n == 0 {
		return rt.setRecord(s)
	}
	oldCount := len(rt.fields)
	fieldCount := max(len(rt.fields), n)
	recordSize, err := validateRebuiltRecordSize(rt.fields, fieldCount, n, s, rt.getVar("OFS").String())
	if err != nil {
		return err
	}
	if err := rt.chargeStringProcessing(max(recordSize, fieldCount)); err != nil {
		return err
	}
	for len(rt.fields) < n {
		rt.fields = append(rt.fields, "")
	}
	rt.fields[n-1] = s
	rt.rebuildRecordFromFields()
	if n > oldCount {
		rt.setComputedNF(len(rt.fields))
	}
	return nil
}

func (rt *runtime) setNF(v value) error {
	n := int(v.Number())
	if n < 0 {
		return fmt.Errorf("fatal: invalid NF value")
	}
	if n > MaxFields {
		return fmt.Errorf("record has too many fields")
	}
	recordSize, err := validateRebuiltRecordSize(rt.fields, n, 0, "", rt.getVar("OFS").String())
	if err != nil {
		return err
	}
	if err := rt.chargeStringProcessing(max(recordSize, n)); err != nil {
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
	rt.rebuildRecordFromFields()
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
	if re.continuation == nil {
		return splitAwkRegexMatches(s, re.re.FindAllStringIndex(s, MaxFields+1))
	}
	fields := make([]string, 0, min(len(s), MaxFields))
	last := 0
	search := 0
	leadingEmptyAdvance := -1
	iterations := 0
	for search <= len(s) {
		if rt.ctx != nil && iterations%256 == 0 {
			if err := rt.ctx.Err(); err != nil {
				return nil, err
			}
		}
		iterations++
		match := findAwkRegexFrom(re, s, search, false)
		if match == nil {
			break
		}
		start, end := match[0], match[1]
		if start == end {
			leadingEmpty := search == 0 && start == 0
			if !leadingEmpty {
				leadingEmptyAdvance = -1
			}
			if end == len(s) {
				break
			}
			_, size := utf8.DecodeRuneInString(s[end:])
			search = end + size
			if leadingEmpty {
				continued := findAwkRegexFrom(re, s[:search], search, false)
				if continued == nil || continued[0] != search || continued[1] != search {
					leadingEmptyAdvance = search
				}
			}
			continue
		}
		if leadingEmptyAdvance >= 0 {
			last = leadingEmptyAdvance
			leadingEmptyAdvance = -1
		}
		if len(fields) >= MaxFields-1 {
			return nil, errTooManyFields
		}
		fields = append(fields, s[last:start])
		last = end
		search = end
	}
	if len(fields) == 0 {
		if leadingEmptyAdvance >= 0 && leadingEmptyAdvance < len(s) {
			return []string{s[leadingEmptyAdvance:]}, nil
		}
		return []string{s}, nil
	}
	fields = append(fields, s[last:])
	return fields, nil
}

func splitAwkRegexMatches(s string, matches [][]int) ([]string, error) {
	fields := make([]string, 0, min(len(matches)+1, MaxFields))
	last := 0
	for _, match := range matches {
		start, end := match[0], match[1]
		if start == end {
			continue
		}
		if len(fields) >= MaxFields-1 {
			return nil, errTooManyFields
		}
		fields = append(fields, s[last:start])
		last = end
	}
	if len(fields) == 0 {
		return []string{s}, nil
	}
	return append(fields, s[last:]), nil
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

func (rt *runtime) conversionString(v value, formatVar string) (string, error) {
	if v.kind != valueNumber {
		return v.s, nil
	}
	return formatAwkNumberWithFormat(v.n, rt.getVar(formatVar).String())
}

func (rt *runtime) setVar(name string, v value) error {
	if local := rt.lookupLocal(name); local != nil {
		root := rootLocalVar(local)
		if rt.localIsArray(root) {
			return fmt.Errorf("fatal: cannot use array %s as scalar", name)
		}
		return rt.setLocalScalar(local, v)
	}
	if rt.isArray(name) {
		return fmt.Errorf("fatal: cannot use array %s as scalar", name)
	}
	if isBuiltinArrayName(name) {
		return fmt.Errorf("fatal: cannot use array %s as scalar", name)
	}
	switch name {
	case "NF":
		return rt.setNF(v)
	case "FS", "RS", "OFS", "ORS", "SUBSEP":
		s, err := rt.conversionString(v, "CONVFMT")
		if err != nil {
			return err
		}
		v = stringValue(s)
	case "OFMT", "CONVFMT":
		if v.kind == valueNumber {
			s, err := rt.conversionString(v, "CONVFMT")
			if err != nil {
				return err
			}
			// Keep the original number while materializing its string facet.
			v = value{kind: valueStrNum, s: s, n: v.n}
		}
	}
	switch name {
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
		return nil, nil, "", true, fmt.Errorf("fatal: cannot use scalar %s as array", name)
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

func (rt *runtime) arrayStorage(name string) (map[string]value, error) {
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
	return elems, nil
}

func (rt *runtime) arrayLen(name string) (int, error) {
	elems, err := rt.arrayStorage(name)
	if err != nil {
		return 0, err
	}
	return len(elems), nil
}

func (rt *runtime) arrayKeysSorted(name string, ignoreCase bool) ([]string, error) {
	elems, err := rt.arrayStorage(name)
	if err != nil {
		return nil, err
	}
	if err := rt.chargeArraySort(elems); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(elems))
	for key := range elems {
		keys = append(keys, key)
	}
	sortStringKeys(keys, ignoreCase)
	return keys, nil
}

func (rt *runtime) chargeArraySort(elems map[string]value) error {
	remaining := maxStringProcessingBytes - rt.stringWorkBytes
	bytesUsed := 0
	for key := range elems {
		keyBytes := max(1, len(key))
		if keyBytes > remaining-bytesUsed {
			return rt.chargeStringProcessing(remaining + 1)
		}
		bytesUsed += keyBytes
	}
	return rt.chargeStringSort(len(elems), bytesUsed)
}

func (rt *runtime) chargeStringSort(count, bytesUsed int) error {
	levels := 1
	for n := count; n > 1; n = n/2 + n%2 {
		levels++
	}
	remaining := maxStringProcessingBytes - rt.stringWorkBytes
	if bytesUsed > remaining/levels {
		return rt.chargeStringProcessing(remaining + 1)
	}
	return rt.chargeStringProcessing(bytesUsed * levels)
}

func sortStringKeys(keys []string, ignoreCase bool) {
	slices.SortFunc(keys, func(left, right string) int {
		return compareAwkSortKeys(left, right, ignoreCase)
	})
}

func compareAwkSortKeys(left, right string, ignoreCase bool) int {
	if ignoreCase {
		if cmp := compareAwkStringsIgnoreCase(left, right); cmp != 0 {
			return cmp
		}
	}
	return strings.Compare(left, right)
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
		return fmt.Errorf("fatal: cannot use scalar %s as array", name)
	}
	if _, ok := rt.vars[name]; ok {
		return fmt.Errorf("fatal: cannot use scalar %s as array", name)
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
	case "FS", "RS", "OFS", "ORS", "OFMT", "CONVFMT", "SUBSEP", "RSTART", "RLENGTH", "IGNORECASE":
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
	re                   *regexp.Regexp
	continuation         *regexp.Regexp
	submatchContinuation *regexp.Regexp
	byteMode             bool
}

func (rt *runtime) compileRegex(pattern string) (*awkRegex, error) {
	key := regexCacheKey{pattern: pattern, ignoreCase: rt.ignoreCase()}
	if re, ok := rt.regexCache[key]; ok {
		return re, nil
	}
	if err := rt.chargeStringProcessing(max(minRegexCompileWork, len(pattern))); err != nil {
		return nil, err
	}
	re, err := compileRegexWithOptions(pattern, key.ignoreCase)
	if err != nil {
		const invalidPrefix = "invalid regular expression"
		msg := err.Error()
		if len(msg) >= len(invalidPrefix) && msg[:len(invalidPrefix)] == invalidPrefix {
			return nil, fmt.Errorf("fatal: %w", err)
		}
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
	normalized, byteMode, ok := normalizeAwkRegexWithOptions(pattern, ignoreCase)
	if !ok {
		return nil, fmt.Errorf("regular expression exceeds %d bytes", MaxRegexBytes)
	}
	if ignoreCase {
		var err error
		normalized, ok, err = normalizeAwkRegexIgnoreCase(normalized)
		if err != nil {
			return nil, fmt.Errorf("invalid regular expression %q: %v", pattern, err)
		}
		if !ok {
			return nil, fmt.Errorf("regular expression exceeds %d bytes", MaxRegexBytes)
		}
	}
	normalized = "(?s:" + normalized + ")"
	if ignoreCase {
		normalized = "(?i:" + normalized + ")"
	}
	re, err := regexp.Compile(normalized)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression %q: %v", pattern, err)
	}
	re.Longest()
	continuation, err := compileAwkContinuationRegex(normalized, false)
	if err != nil && byteMode {
		return nil, fmt.Errorf("invalid regular expression %q: %v", pattern, err)
	}
	submatchContinuation, _ := compileAwkContinuationRegex(normalized, true)
	if submatchContinuation == nil && byteMode && re.NumSubexp() <= maxSubstitutionMatchIndices/2 {
		return nil, fmt.Errorf("invalid regular expression %q: capture nesting is too deep", pattern)
	}
	return &awkRegex{
		re:                   re,
		continuation:         continuation,
		submatchContinuation: submatchContinuation,
		byteMode:             byteMode,
	}, nil
}

func compileAwkContinuationRegex(pattern string, keepCaptures bool) (*regexp.Regexp, error) {
	parsed, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, err
	}
	if !keepCaptures {
		stripAwkRegexCaptures(parsed)
	}
	continuation := &syntax.Regexp{
		Op: syntax.OpConcat,
		Sub: []*syntax.Regexp{
			{Op: syntax.OpAnyChar},
			parsed,
		},
	}
	re, err := regexp.Compile(continuation.String())
	if err != nil {
		return nil, err
	}
	re.Longest()
	return re, nil
}

func stripAwkRegexCaptures(re *syntax.Regexp) {
	for re.Op == syntax.OpCapture {
		*re = *re.Sub[0]
	}
	for _, sub := range re.Sub {
		stripAwkRegexCaptures(sub)
	}
}

var awkRegexCUTF8FoldPartitions = [][][]rune{
	{{'I', 'i', 'ı'}},
	{{'K', 'k'}, {'K'}},
	{{'Å', 'å'}, {'Å'}},
	{{'Θ', 'θ', 'ϑ'}, {'ϴ'}},
	{{'Ω', 'ω'}, {'Ω'}},
}

var awkRegexCUTF8AffectedRunes = []rune{
	'I', 'K', 'i', 'k', 'Å', 'å', 'ı', 'Θ', 'Ω', 'θ', 'ω', 'ϑ', 'ϴ', 'Ω', 'K', 'Å',
}

func normalizeAwkRegexIgnoreCase(pattern string) (string, bool, error) {
	var normalized strings.Builder
	normalized.Grow(len(pattern))
	write := func(text string) bool {
		if len(text) > MaxRegexBytes-normalized.Len() {
			return false
		}
		normalized.WriteString(text)
		return true
	}

	for i := 0; i < len(pattern); {
		if pattern[i] == '\\' {
			end := min(i+2, len(pattern))
			if end < len(pattern) && pattern[end] == '{' && strings.ContainsRune("xNpP", rune(pattern[i+1])) {
				if close := strings.IndexByte(pattern[end+1:], '}'); close >= 0 {
					end += close + 2
				}
			}
			if !write(pattern[i:end]) {
				return "", false, nil
			}
			i = end
			continue
		}
		if pattern[i] == '(' {
			if end := awkRegexGroupHeaderEnd(pattern, i); end >= 0 {
				if !write(pattern[i:end]) {
					return "", false, nil
				}
				i = end
				continue
			}
		}
		if pattern[i] == '[' {
			end := awkRegexBracketEnd(pattern, i)
			if end < 0 {
				if !write(pattern[i:]) {
					return "", false, nil
				}
				break
			}
			class, changed, err := foldAwkRegexClassCUTF8(pattern[i:end])
			if err != nil {
				return "", false, err
			}
			if changed {
				class = "(?-i:" + class + ")"
			}
			if !write(class) {
				return "", false, nil
			}
			i = end
			continue
		}

		r, size := utf8.DecodeRuneInString(pattern[i:])
		replacement, replace := awkRegexCUTF8Literal(r)
		if replace {
			if !write("(?-i:" + replacement + ")") {
				return "", false, nil
			}
		} else if !write(pattern[i : i+size]) {
			return "", false, nil
		}
		i += size
	}
	return normalized.String(), true, nil
}

func awkRegexBracketEnd(pattern string, start int) int {
	i := start + 1
	if i < len(pattern) && pattern[i] == '^' {
		i++
	}
	if i < len(pattern) && pattern[i] == ']' {
		i++
	}
	for ; i < len(pattern); i++ {
		if pattern[i] == '\\' {
			i++
			continue
		}
		if pattern[i] == ']' {
			return i + 1
		}
	}
	return -1
}

func awkRegexGroupHeaderEnd(pattern string, start int) int {
	if start+2 >= len(pattern) || pattern[start+1] != '?' {
		return -1
	}
	i := start + 2
	if pattern[i] == 'P' && i+1 < len(pattern) && pattern[i+1] == '<' {
		i++
	}
	if pattern[i] == '<' {
		if end := strings.IndexByte(pattern[i+1:], '>'); end >= 0 {
			return i + end + 2
		}
		return -1
	}
	flagsStart := i
	for i < len(pattern) && strings.ContainsRune("imsU-", rune(pattern[i])) {
		i++
	}
	if i < len(pattern) && (pattern[i] == ':' || pattern[i] == ')') && (i > flagsStart || pattern[i] == ':') {
		return i + 1
	}
	return -1
}

func awkRegexCUTF8Literal(r rune) (string, bool) {
	switch r {
	case 'I', 'i', 'ı':
		return `[Iiı]`, true
	case 'K', 'k':
		return `[Kk]`, true
	case 'K':
		return `[K]`, true
	case 'Å', 'å':
		return `[Åå]`, true
	case 'Å':
		return `[Å]`, true
	case 'Θ', 'θ', 'ϑ':
		return `[Θθϑ]`, true
	case 'ϴ':
		return `[ϴ]`, true
	case 'Ω', 'ω':
		return `[Ωω]`, true
	case 'Ω':
		return `[Ω]`, true
	default:
		return "", false
	}
}

func foldAwkRegexClassCUTF8(class string) (string, bool, error) {
	originalExpr, err := syntax.Parse(class, syntax.Perl)
	if err != nil {
		return "", false, err
	}
	original, ok := awkRegexRuneRanges(originalExpr)
	if !ok {
		return "", false, fmt.Errorf("invalid character class %q", class)
	}
	foldedExpr, err := syntax.Parse("(?i:"+class+")", syntax.Perl)
	if err != nil {
		return "", false, err
	}
	folded, ok := awkRegexRuneRanges(foldedExpr)
	if !ok {
		return "", false, fmt.Errorf("invalid character class %q", class)
	}
	goFolded := slices.Clone(folded)

	negated := len(class) > 2 && class[1] == '^'
	folded = removeAwkRegexCUTF8AffectedRunes(folded)
	for _, partition := range awkRegexCUTF8FoldPartitions {
		for _, group := range partition {
			accepted := negated
			for _, r := range group {
				contains := awkRegexRuneRangesContain(original, r)
				if negated && !contains {
					accepted = false
					break
				}
				if !negated && contains {
					accepted = true
					break
				}
			}
			if accepted {
				for _, r := range group {
					folded = append(folded, r, r)
				}
			}
		}
	}
	folded = cleanAwkRegexRuneRanges(folded)
	if slices.Equal(folded, goFolded) {
		return class, false, nil
	}
	return (&syntax.Regexp{Op: syntax.OpCharClass, Rune: folded}).String(), true, nil
}

func awkRegexRuneRanges(re *syntax.Regexp) ([]rune, bool) {
	switch re.Op {
	case syntax.OpNoMatch:
		return nil, true
	case syntax.OpLiteral:
		if len(re.Rune) != 1 {
			return nil, false
		}
		ranges := []rune{re.Rune[0], re.Rune[0]}
		if re.Flags&syntax.FoldCase != 0 {
			for r := unicode.SimpleFold(re.Rune[0]); r != re.Rune[0]; r = unicode.SimpleFold(r) {
				ranges = append(ranges, r, r)
			}
		}
		return cleanAwkRegexRuneRanges(ranges), true
	case syntax.OpCharClass:
		return slices.Clone(re.Rune), true
	case syntax.OpAnyCharNotNL:
		return []rune{0, '\n' - 1, '\n' + 1, unicode.MaxRune}, true
	case syntax.OpAnyChar:
		return []rune{0, unicode.MaxRune}, true
	default:
		return nil, false
	}
}

func awkRegexRuneRangesContain(ranges []rune, target rune) bool {
	for i := 0; i < len(ranges); i += 2 {
		if target < ranges[i] {
			return false
		}
		if target <= ranges[i+1] {
			return true
		}
	}
	return false
}

func removeAwkRegexCUTF8AffectedRunes(ranges []rune) []rune {
	result := make([]rune, 0, len(ranges))
	for i := 0; i < len(ranges); i += 2 {
		start, end := ranges[i], ranges[i+1]
		for _, excluded := range awkRegexCUTF8AffectedRunes {
			if excluded < start {
				continue
			}
			if excluded > end {
				break
			}
			if start < excluded {
				result = append(result, start, excluded-1)
			}
			start = excluded + 1
		}
		if start <= end {
			result = append(result, start, end)
		}
	}
	return result
}

func cleanAwkRegexRuneRanges(ranges []rune) []rune {
	pairs := make([][2]rune, 0, len(ranges)/2)
	for i := 0; i < len(ranges); i += 2 {
		pairs = append(pairs, [2]rune{ranges[i], ranges[i+1]})
	}
	slices.SortFunc(pairs, func(left, right [2]rune) int {
		switch {
		case left[0] < right[0]:
			return -1
		case left[0] > right[0]:
			return 1
		case left[1] < right[1]:
			return -1
		case left[1] > right[1]:
			return 1
		default:
			return 0
		}
	})
	result := make([]rune, 0, len(ranges))
	for _, pair := range pairs {
		if len(result) == 0 || pair[0] > result[len(result)-1]+1 {
			result = append(result, pair[0], pair[1])
			continue
		}
		if pair[1] > result[len(result)-1] {
			result[len(result)-1] = pair[1]
		}
	}
	return result
}

func (re *awkRegex) MatchString(s string) bool {
	return re.FindStringIndex(s) != nil
}

func (re *awkRegex) FindStringIndex(s string) []int {
	if !re.byteMode {
		return re.re.FindStringIndex(s)
	}
	return findAwkRegexIndex(re.re, s, false)
}

func (re *awkRegex) FindAllStringIndex(s string, n int) [][]int {
	if !re.byteMode {
		return re.re.FindAllStringIndex(s, n)
	}
	return findAllAwkRegexMatches(re, s, n, false)
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
	return findAllAwkRegexMatches(re, s, n, true)
}

type awkRegexRuneReader struct {
	prefix    rune
	text      string
	index     int
	hasPrefix bool
}

func (r *awkRegexRuneReader) ReadRune() (rune, int, error) {
	if r.hasPrefix {
		r.hasPrefix = false
		return r.prefix, 1, nil
	}
	if r.index >= len(r.text) {
		return 0, 0, io.EOF
	}
	decoded, size := utf8.DecodeRuneInString(r.text[r.index:])
	if decoded == utf8.RuneError && size == 1 {
		decoded = awkRegexByteRuneBase + rune(r.text[r.index])
	}
	r.index += size
	return decoded, size, nil
}

func findAwkRegexIndex(re *regexp.Regexp, s string, submatches bool) []int {
	return findAwkRegexIndexWithReader(re, &awkRegexRuneReader{text: s}, submatches)
}

func findAwkRegexIndexWithReader(re *regexp.Regexp, reader *awkRegexRuneReader, submatches bool) []int {
	if submatches {
		return re.FindReaderSubmatchIndex(reader)
	}
	return re.FindReaderIndex(reader)
}

func findAllAwkRegexMatches(re *awkRegex, s string, n int, submatches bool) [][]int {
	return findAllAwkRegexMatchesWithAdvance(re, s, n, submatches, false, true)
}

func findAllAwkSubstitutionMatches(re *awkRegex, s string, n int, submatches, skipAdjacentEmpty bool) [][]int {
	if re.continuation == nil || submatches && re.submatchContinuation == nil {
		if submatches {
			return re.FindAllStringSubmatchIndex(s, n)
		}
		return re.FindAllStringIndex(s, n)
	}
	return findAllAwkRegexMatchesWithAdvance(re, s, n, submatches, true, skipAdjacentEmpty)
}

func findAllAwkRegexMatchesWithAdvance(re *awkRegex, s string, n int, submatches, bytewise, skipAdjacentEmpty bool) [][]int {
	if n == 0 {
		return nil
	}
	var matches [][]int
	search := 0
	previousEnd := -1
	for search <= len(s) && (n < 0 || len(matches) < n) {
		var loc []int
		if bytewise {
			loc = findAwkRegexFromByte(re, s, search, submatches)
		} else {
			loc = findAwkRegexFrom(re, s, search, submatches)
		}
		if loc == nil {
			break
		}
		accept := true
		if loc[1] == search {
			if skipAdjacentEmpty && loc[0] == previousEnd {
				accept = false
			}
			if search == len(s) {
				search = len(s) + 1
			} else if bytewise {
				search++
			} else {
				_, size := utf8.DecodeRuneInString(s[search:])
				search += size
			}
		} else {
			search = loc[1]
		}
		previousEnd = loc[1]
		if accept {
			matches = append(matches, loc)
		}
	}
	return matches
}

func findAwkRegexFromByte(re *awkRegex, s string, search int, submatches bool) []int {
	if search == 0 {
		if submatches {
			return re.FindStringSubmatchIndex(s)
		}
		return re.FindStringIndex(s)
	}
	context := rune(s[search-1])
	if s[search-1] >= 0x80 {
		context = awkRegexByteRuneBase + rune(s[search-1])
	}
	reader := &awkRegexRuneReader{
		prefix:    context,
		text:      s[search:],
		hasPrefix: true,
	}
	continuation := re.continuation
	if submatches {
		continuation = re.submatchContinuation
	}
	loc := findAwkRegexIndexWithReader(continuation, reader, submatches)
	if loc == nil {
		return nil
	}
	dotSize := 1
	if loc[0] > 0 {
		_, dotSize = utf8.DecodeRuneInString(s[search+loc[0]-1:])
	}
	start := search + loc[0] + dotSize - 1
	end := search + loc[1] - 1
	if !submatches {
		return []int{start, end}
	}
	result := make([]int, len(loc))
	result[0], result[1] = start, end
	for i := 2; i < len(loc); i++ {
		if loc[i] >= 0 {
			result[i] = search + loc[i] - 1
		} else {
			result[i] = -1
		}
	}
	return result
}

func findAwkRegexFrom(re *awkRegex, s string, search int, submatches bool) []int {
	if search == 0 {
		if re.byteMode {
			return findAwkRegexIndex(re.re, s, submatches)
		}
		if submatches {
			return re.re.FindStringSubmatchIndex(s)
		}
		return re.re.FindStringIndex(s)
	}
	previousSize := previousAwkRuneSize(s, search)
	base := search - previousSize
	var loc []int
	continuation := re.continuation
	if submatches && re.submatchContinuation != nil {
		continuation = re.submatchContinuation
	}
	continuationSubmatches := submatches && re.submatchContinuation != nil
	if re.byteMode {
		loc = findAwkRegexIndex(continuation, s[base:], continuationSubmatches)
	} else if continuationSubmatches {
		loc = continuation.FindStringSubmatchIndex(s[base:])
	} else {
		loc = continuation.FindStringIndex(s[base:])
	}
	if loc == nil {
		return nil
	}
	_, logicalStartSize := utf8.DecodeRuneInString(s[base+loc[0]:])
	start := base + loc[0] + logicalStartSize
	end := base + loc[1]
	if !submatches {
		return []int{start, end}
	}
	result := make([]int, len(loc))
	result[0], result[1] = start, end
	for i := 2; i < len(loc); i++ {
		if loc[i] >= 0 {
			result[i] = base + loc[i]
		} else {
			result[i] = -1
		}
	}
	return result
}

func previousAwkRuneSize(s string, end int) int {
	for size := min(utf8.UTFMax, end); size > 1; size-- {
		_, decodedSize := utf8.DecodeRuneInString(s[end-size : end])
		if decodedSize == size {
			return size
		}
	}
	return 1
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

func normalizeAwkRegex(pattern string) (string, bool, bool) {
	return normalizeAwkRegexWithOptions(pattern, false)
}

func normalizeAwkRegexWithOptions(pattern string, ignoreCase bool) (string, bool, bool) {
	const (
		intervalNone = iota
		intervalLowerStart
		intervalLower
		intervalComma
		intervalUpper
	)
	var decoded strings.Builder
	inClass := false
	classStart := false
	classAtomStart := -1
	classBracketPending := false
	var classSubexpression byte
	classSubexpressionEnd := false
	intervalState := intervalNone
	intervalStart := -1
	intervalOperandStart := -1
	intervalNested := false
	intervalOperandless := false
	expandedRepeatWork := 0
	skipDecodedByte := false
	lastAtomStart := -1
	lastQuantifiedStart := -1
	var groupStarts []int
	var last byte
	consume := func(ch byte) {
		position := decoded.Len()
		wasInClass := inClass
		if inClass {
			switch {
			case classSubexpression != 0:
				if ch == ']' && classSubexpressionEnd {
					classSubexpression = 0
					classSubexpressionEnd = false
				} else {
					classSubexpressionEnd = ch == classSubexpression
				}
				classBracketPending = false
				classStart = false
			case classBracketPending && (ch == ':' || ch == '.' || ch == '='):
				classSubexpression = ch
				classSubexpressionEnd = true
				classBracketPending = false
				classStart = false
			case ch == ']' && !classStart:
				inClass = false
				classBracketPending = false
			case classStart && ch != '^':
				classStart = false
				classBracketPending = ch == '['
			default:
				classBracketPending = ch == '['
			}
		} else if ch == '[' {
			inClass = true
			classStart = true
			classAtomStart = position
		}
		completedInterval := false
		if wasInClass || inClass {
			intervalState = intervalNone
			intervalOperandless = false
		} else {
			switch intervalState {
			case intervalNone:
				if ch == '{' {
					switch {
					case awkRegexCanRepeat(last):
						intervalState = intervalLowerStart
						intervalStart = position
						intervalOperandStart = lastAtomStart
						intervalNested = false
						intervalOperandless = false
					case lastQuantifiedStart >= 0 && (last == '*' || last == '+' || last == '?'):
						intervalState = intervalLowerStart
						intervalStart = position
						intervalOperandStart = lastQuantifiedStart
						intervalNested = true
						intervalOperandless = false
					default:
						intervalState = intervalLowerStart
						intervalStart = position
						intervalOperandStart = -1
						intervalNested = false
						intervalOperandless = true
					}
				}
			case intervalLowerStart:
				if isDigit(rune(ch)) {
					intervalState = intervalLower
				} else {
					intervalState = intervalNone
					intervalNested = false
					intervalOperandless = false
				}
			case intervalLower:
				switch {
				case isDigit(rune(ch)):
				case ch == ',':
					intervalState = intervalComma
				case ch == '}':
					intervalState = intervalNone
					completedInterval = true
				default:
					intervalState = intervalNone
					intervalNested = false
					intervalOperandless = false
				}
			case intervalComma:
				switch {
				case isDigit(rune(ch)):
					intervalState = intervalUpper
				case ch == '}':
					intervalState = intervalNone
					completedInterval = true
				default:
					intervalState = intervalNone
					intervalNested = false
					intervalOperandless = false
				}
			case intervalUpper:
				if !isDigit(rune(ch)) {
					intervalState = intervalNone
					completedInterval = ch == '}'
					if !completedInterval {
						intervalNested = false
						intervalOperandless = false
					}
				}
			}
		}
		if completedInterval {
			text := decoded.String()
			if intervalOperandless {
				escaped := text[:intervalStart] + `\` + text[intervalStart:] + `\`
				decoded.Reset()
				decoded.WriteString(escaped)
				lastAtomStart = decoded.Len() - 1
				lastQuantifiedStart = -1
				intervalOperandless = false
				last = 'a'
				return
			}
			lower, upper, unbounded, validInterval := parseAwkInterval(text[intervalStart+1:])
			largeInterval := validInterval && (lower > 1000 || !unbounded && upper > 1000)
			atomWork := 0
			if intervalOperandStart >= 0 {
				atomWork = max(1, intervalStart-intervalOperandStart)
			}
			repeatWork := lower
			if !unbounded {
				repeatWork = upper
			}
			if largeInterval && atomWork > 0 && repeatWork <= (MaxRegexBytes-expandedRepeatWork)/atomWork {
				work := repeatWork * atomWork
				atom := text[intervalOperandStart:intervalStart]
				expanded, ok := expandAwkInterval(atom, lower, upper, unbounded, MaxRegexBytes-len(text[:intervalOperandStart]), ignoreCase)
				if ok {
					rewritten := text[:intervalOperandStart] + expanded
					decoded.Reset()
					decoded.WriteString(rewritten)
					expandedRepeatWork += work
					skipDecodedByte = true
				}
			}
			if !skipDecodedByte && intervalNested {
				nested := text[:intervalOperandStart] + "(?:" +
					text[intervalOperandStart:intervalStart] + ")" + text[intervalStart:]
				decoded.Reset()
				decoded.WriteString(nested)
			}
			lastAtomStart = intervalOperandStart
			lastQuantifiedStart = intervalOperandStart
			intervalNested = false
			intervalOperandless = false
			last = '*'
		} else {
			if wasInClass && !inClass {
				lastAtomStart = classAtomStart
				lastQuantifiedStart = -1
			} else if !wasInClass && !inClass && intervalState == intervalNone {
				switch ch {
				case '(':
					groupStarts = append(groupStarts, position)
					lastAtomStart = -1
					lastQuantifiedStart = -1
				case ')':
					if len(groupStarts) > 0 {
						lastAtomStart = groupStarts[len(groupStarts)-1]
						groupStarts = groupStarts[:len(groupStarts)-1]
					}
					lastQuantifiedStart = -1
				case '|', '^', '$':
					lastAtomStart = -1
					lastQuantifiedStart = -1
				case '*', '+', '?', '{', '}':
				default:
					if ch&0xc0 != 0x80 {
						lastAtomStart = position
						lastQuantifiedStart = -1
					}
				}
			}
			last = ch
		}
	}
	writeDecoded := func(ch byte, escapeOperandless bool) {
		skipDecodedByte = false
		previousWasQuantifier := last == '*' || last == '+' || last == '?'
		re2GroupPrefix := !escapeOperandless && last == '(' && ch == '?'
		quantifier := !inClass && (ch == '*' || ch == '+' || ch == '?') && !re2GroupPrefix
		if quantifier && previousWasQuantifier && lastQuantifiedStart >= 0 {
			text := decoded.String()
			decoded.Reset()
			decoded.WriteString(text[:lastQuantifiedStart])
			decoded.WriteString("(?:")
			decoded.WriteString(text[lastQuantifiedStart:])
			decoded.WriteByte(')')
			lastAtomStart = lastQuantifiedStart
			consume(ch)
			lastQuantifiedStart = lastAtomStart
		} else if quantifier && !awkRegexCanRepeat(last) {
			decoded.WriteByte('\\')
			intervalState = intervalNone
			last = 'a'
		} else {
			consume(ch)
			if quantifier {
				lastQuantifiedStart = lastAtomStart
			}
		}
		if !skipDecodedByte {
			decoded.WriteByte(ch)
		}
	}
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		if ch != '\\' {
			writeDecoded(ch, false)
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
			writeDecoded(byte(value), true)
			continue
		}
		if value, size, ok := decodeAwkHexEscape(pattern[i+1:]); ok {
			i += size
			writeDecoded(value, true)
			continue
		}
		i++
		lastAtomStart = decoded.Len()
		writeAwkRegexEscape(&decoded, pattern[i], inClass)
		intervalState = intervalNone
		intervalOperandless = false
		if inClass {
			classStart = false
			classBracketPending = false
			classSubexpressionEnd = false
		} else {
			last = 'a'
		}
	}

	var normalized strings.Builder
	byteMode := false
	decodedPattern, ok := expandAwkPOSIXClasses(decoded.String(), MaxRegexBytes, ignoreCase)
	if !ok {
		return "", false, false
	}
	decodedPattern = normalizeAwkRegexIntervals(decodedPattern)
	for i := 0; i < len(decodedPattern); {
		r, size := utf8.DecodeRuneInString(decodedPattern[i:])
		if r == utf8.RuneError && size == 1 {
			marker := fmt.Sprintf(`\x{%x}`, awkRegexByteRuneBase+rune(decodedPattern[i]))
			if len(marker) > MaxRegexBytes-normalized.Len() {
				return "", false, false
			}
			normalized.WriteString(marker)
			byteMode = true
			i++
			continue
		}
		if size > MaxRegexBytes-normalized.Len() {
			return "", false, false
		}
		normalized.WriteString(decodedPattern[i : i+size])
		i += size
	}
	return normalized.String(), byteMode, true
}

func awkRegexCanRepeat(last byte) bool {
	switch last {
	case 0, '(', '|', '^', '$', '*', '+', '?':
		return false
	default:
		return true
	}
}

func normalizeAwkRegexIntervals(pattern string) string {
	var normalized strings.Builder
	normalized.Grow(len(pattern))
	writeInterval := func(lower, upper int, unbounded bool) {
		var digits [32]byte
		normalized.WriteByte('{')
		normalized.Write(strconv.AppendInt(digits[:0], int64(lower), 10))
		switch {
		case unbounded:
			normalized.WriteByte(',')
		case upper != lower:
			normalized.WriteByte(',')
			normalized.Write(strconv.AppendInt(digits[:0], int64(upper), 10))
		}
		normalized.WriteByte('}')
	}

	for i := 0; i < len(pattern); {
		switch pattern[i] {
		case '\\':
			end := min(i+2, len(pattern))
			if end < len(pattern) && pattern[end] == '{' && strings.ContainsRune("xNpP", rune(pattern[i+1])) {
				if close := strings.IndexByte(pattern[end+1:], '}'); close >= 0 {
					end += close + 2
				}
			}
			normalized.WriteString(pattern[i:end])
			i = end
		case '[':
			end := awkRegexBracketEnd(pattern, i)
			if end < 0 {
				normalized.WriteString(pattern[i:])
				return normalized.String()
			}
			normalized.WriteString(pattern[i:end])
			i = end
		case '{':
			close := strings.IndexByte(pattern[i+1:], '}')
			if close < 0 {
				normalized.WriteByte(pattern[i])
				i++
				continue
			}
			end := i + close + 1
			lower, upper, unbounded, ok := parseAwkInterval(pattern[i+1 : end])
			if !ok {
				normalized.WriteString(pattern[i : end+1])
			} else {
				writeInterval(lower, upper, unbounded)
			}
			i = end + 1
		default:
			normalized.WriteByte(pattern[i])
			i++
		}
	}
	return normalized.String()
}

func parseAwkInterval(body string) (int, int, bool, bool) {
	lowerText, upperText, hasComma := strings.Cut(body, ",")
	lower, ok := parseAwkRepeatCount(lowerText)
	if !ok {
		return 0, 0, false, false
	}
	if !hasComma {
		return lower, lower, false, true
	}
	if upperText == "" {
		return lower, 0, true, true
	}
	upper, ok := parseAwkRepeatCount(upperText)
	if !ok || upper < lower {
		return 0, 0, false, false
	}
	return lower, upper, false, true
}

func parseAwkRepeatCount(text string) (int, bool) {
	if text == "" {
		return 0, false
	}
	const saturated = MaxRegexBytes + 1
	value := 0
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return 0, false
		}
		digit := int(text[i] - '0')
		if value > (saturated-digit)/10 {
			value = saturated
		} else {
			value = value*10 + digit
		}
	}
	return value, true
}

func expandAwkInterval(atom string, lower, upper int, unbounded bool, maxBytes int, ignoreCase bool) (string, bool) {
	const maxRE2Repeat = 1000
	captureless, ok := capturelessAwkRegex(atom, ignoreCase)
	if !ok {
		return "", false
	}
	var expanded strings.Builder
	appendText := func(text string) bool {
		if len(text) > maxBytes-expanded.Len() {
			return false
		}
		expanded.WriteString(text)
		return true
	}
	appendRepeats := func(count int, optional bool) bool {
		for count > 0 {
			chunk := min(count, maxRE2Repeat)
			repetition := fmt.Sprintf("{%d}", chunk)
			if optional {
				repetition = fmt.Sprintf("{0,%d}", chunk)
			}
			if !appendText("(?:") || !appendText(captureless) || !appendText(")") || !appendText(repetition) {
				return false
			}
			count -= chunk
		}
		return true
	}
	appendAtom := func(quantifier string) bool {
		return appendText("(?:") && appendText(atom) && appendText(")") && appendText(quantifier)
	}

	if !appendText("(?:") {
		return "", false
	}
	if unbounded {
		if !appendRepeats(lower-1, false) || !appendAtom("+") {
			return "", false
		}
	} else if lower == 0 {
		if !appendText("(?:") || !appendRepeats(upper-1, true) || !appendAtom("") || !appendText(")?") {
			return "", false
		}
	} else if !appendRepeats(lower-1, false) || !appendRepeats(upper-lower, true) || !appendAtom("") {
		return "", false
	}
	if !appendText(")") {
		return "", false
	}
	return expanded.String(), true
}

func capturelessAwkRegex(pattern string, ignoreCase bool) (string, bool) {
	expanded, ok := expandAwkPOSIXClasses(pattern, MaxRegexBytes, ignoreCase)
	if !ok {
		return "", false
	}
	parsed, err := syntax.Parse(expanded, syntax.Perl)
	if err != nil {
		return "", false
	}
	stripAwkRegexCaptures(parsed)
	return parsed.String(), true
}

func expandAwkPOSIXClasses(pattern string, maxBytes int, ignoreCase bool) (string, bool) {
	if len(pattern) > maxBytes {
		return "", false
	}
	if !strings.Contains(pattern, "[:") && !strings.Contains(pattern, "[.") && !strings.Contains(pattern, "[=") {
		return pattern, true
	}
	var expanded strings.Builder
	expanded.Grow(len(pattern))
	writeString := func(text string) bool {
		if len(text) > maxBytes-expanded.Len() {
			return false
		}
		expanded.WriteString(text)
		return true
	}
	writeByte := func(ch byte) bool {
		if expanded.Len() >= maxBytes {
			return false
		}
		expanded.WriteByte(ch)
		return true
	}
	inClass := false
	classStart := false
	for i := 0; i < len(pattern); {
		if pattern[i] == '\\' {
			if !writeByte(pattern[i]) {
				return "", false
			}
			i++
			if i < len(pattern) {
				if !writeByte(pattern[i]) {
					return "", false
				}
				i++
			}
			if inClass {
				classStart = false
			}
			continue
		}
		if !inClass {
			if !writeByte(pattern[i]) {
				return "", false
			}
			if pattern[i] == '[' {
				inClass = true
				classStart = true
			}
			i++
			continue
		}
		if i+2 <= len(pattern) && pattern[i] == '[' {
			kind := pattern[i+1]
			if kind == ':' || kind == '.' || kind == '=' {
				endMarker := string([]byte{kind, ']'})
				if end := strings.Index(pattern[i+2:], endMarker); end >= 0 {
					name := pattern[i+2 : i+2+end]
					replacement, ok := unicodeAwkPOSIXClass(name, ignoreCase)
					if kind != ':' {
						replacement, ok = awkBracketElement(name)
					}
					if ok {
						if !writeString(replacement) {
							return "", false
						}
						i += end + 4
						classStart = false
						continue
					}
				}
			}
		}
		ch := pattern[i]
		if !writeByte(ch) {
			return "", false
		}
		i++
		if ch == ']' && !classStart {
			inClass = false
			continue
		}
		if classStart && ch != '^' {
			classStart = false
		}
	}
	return expanded.String(), true
}

func unicodeAwkPOSIXClass(name string, ignoreCase bool) (string, bool) {
	if ignoreCase && (name == "lower" || name == "upper") {
		name = "alpha"
	}
	switch name {
	case "alpha":
		return `\p{L}\p{Nl}` + awkNonASCIIDigitClass + awkOtherAlphabeticClass + awkUnicode151LetterClass, true
	case "alnum":
		return `\p{L}\p{Nl}\p{Nd}` + awkOtherAlphabeticClass + awkUnicode151LetterClass, true
	case "lower":
		return `\p{Ll}` + awkLowercaseTitleClass + awkOtherLowercaseClass, true
	case "upper":
		return `\p{Lu}\p{Lt}` + awkOtherUppercaseClass, true
	case "blank":
		return `\t` + awkBreakableSpaceClass, true
	case "space":
		return `\t\n\v\f\r` + awkBreakableSpaceClass + `\p{Zl}\p{Zp}`, true
	case "graph":
		return `\p{L}\p{M}\p{N}\p{P}\p{S}\p{Cf}\p{Co}` + awkNoBreakSpaceClass + awkUnicode151LetterClass + awkUnicode151SymbolClass, true
	case "print":
		return `\p{L}\p{M}\p{N}\p{P}\p{S}\p{Zs}\p{Cf}\p{Co}` + awkUnicode151LetterClass + awkUnicode151SymbolClass, true
	case "punct":
		return awkPunctuationClass + awkUnicode151SymbolClass, true
	case "cntrl":
		return `\p{Cc}\p{Zl}\p{Zp}`, true
	default:
		return "", false
	}
}

func awkUnicodeRangeClass(table *unicode.RangeTable) string {
	return awkUnicodeRangeClassExcluding(table, nil)
}

func awkUnicodeRangeClassExcluding(table, excluded *unicode.RangeTable) string {
	var class strings.Builder
	writeRune := func(r uint32) {
		class.WriteString(`\x{`)
		class.WriteString(strconv.FormatUint(uint64(r), 16))
		class.WriteByte('}')
	}
	var runStart, runEnd uint32
	haveRun := false
	flushRun := func() {
		if !haveRun {
			return
		}
		writeRune(runStart)
		if runEnd != runStart {
			class.WriteByte('-')
			writeRune(runEnd)
		}
		haveRun = false
	}
	visitRune := func(current uint32) {
		if excluded != nil && unicode.Is(excluded, rune(current)) {
			flushRun()
			return
		}
		if haveRun && current == runEnd+1 {
			runEnd = current
			return
		}
		flushRun()
		runStart, runEnd, haveRun = current, current, true
	}
	writeRange := func(lo, hi, stride uint32) {
		for current := lo; ; current += stride {
			visitRune(current)
			if hi-current < stride {
				return
			}
		}
	}
	for _, r := range table.R16 {
		writeRange(uint32(r.Lo), uint32(r.Hi), uint32(r.Stride))
	}
	for _, r := range table.R32 {
		writeRange(r.Lo, r.Hi, r.Stride)
	}
	flushRun()
	return class.String()
}

func awkBracketElement(element string) (string, bool) {
	r, size := utf8.DecodeRuneInString(element)
	if size == 0 || size != len(element) || (r == utf8.RuneError && size == 1) {
		return "", false
	}
	if strings.ContainsRune(`\]-^`, r) {
		return `\` + element, true
	}
	return element, true
}

// Surrogate code points cannot occur in valid UTF-8 and have no case folds.
const awkRegexByteRuneBase rune = 0xd800

func writeAwkRegexEscape(b *strings.Builder, esc byte, inClass bool) {
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
	case 'w':
		if inClass {
			b.WriteByte(esc)
		} else {
			b.WriteString(`[[:alnum:]_]`)
		}
	case 'W':
		if inClass {
			b.WriteByte(esc)
		} else {
			b.WriteString(`[^[:alnum:]_]`)
		}
	case 's':
		if inClass {
			b.WriteByte(esc)
		} else {
			b.WriteString(`[[:space:]]`)
		}
	case 'S':
		if inClass {
			b.WriteByte(esc)
		} else {
			b.WriteString(`[^[:space:]]`)
		}
	case 'y', 'B', '<', '>':
		if inClass {
			if esc == 'y' {
				esc = 'b'
			}
			b.WriteByte(esc)
		} else if esc == 'B' {
			b.WriteString(`\B`)
		} else {
			b.WriteString(`\b`)
		}
	case '`', '\'':
		if inClass {
			b.WriteByte(esc)
		} else if esc == '`' {
			b.WriteString(`\A`)
		} else {
			b.WriteString(`\z`)
		}
	default:
		b.WriteByte(esc)
	}
}
