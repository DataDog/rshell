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
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/DataDog/rshell/builtins"
)

const (
	MaxProgramBytes  = 256 << 10
	MaxRecordBytes   = 1 << 20
	MaxFields        = 16_384
	MaxVariableBytes = 1 << 20
	MaxPipeBytes     = 5 << 20
	maxFiniteFloat64 = 1.79769313486231570814527423731704357e+308
)

type valueKind int

const (
	valueString valueKind = iota
	valueNumber
	valueStrNum
	valueRegex
)

type value struct {
	kind    valueKind
	s       string
	n       float64
	pattern string
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

func regexValue(pattern string) value {
	return value{kind: valueRegex, pattern: pattern}
}

func (v value) String() string {
	switch v.kind {
	case valueNumber:
		return formatAwkNumber(v.n)
	case valueRegex:
		return v.pattern
	default:
		return v.s
	}
}

func formatAwkNumber(n float64) string {
	if n == 0 {
		n = 0
	}
	fixed := strconv.FormatFloat(n, 'f', -1, 64)
	for i := 0; i < len(fixed); i++ {
		if fixed[i] == '.' {
			return strconv.FormatFloat(n, 'g', 6, 64)
		}
	}
	return fixed
}

func (v value) Number() float64 {
	switch v.kind {
	case valueNumber, valueStrNum:
		return v.n
	case valueRegex:
		if n, ok := parseNumericPrefix(v.pattern); ok {
			return n
		}
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
	case valueRegex:
		return v.pattern != ""
	default:
		return v.s != ""
	}
}

func parseFullNumericString(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || n != n || n > maxFiniteFloat64 || n < -maxFiniteFloat64 {
		return 0, false
	}
	return n, true
}

func parseNumericPrefix(s string) (float64, bool) {
	prefix := numericPrefix(trimLeadingAwkSpace(s))
	if prefix == "" {
		return 0, false
	}
	n, err := strconv.ParseFloat(prefix, 64)
	return n, err == nil
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
	callCtx    *builtins.CallContext
	prog       *program
	vars       map[string]value
	arrays     map[string]map[string]value
	varSizes   map[string]int
	arraySizes map[arraySlot]int
	varBytes   int
	rangeOn    map[int]bool
	environSet bool
	frames     []callFrame
	ctx        context.Context
	pipes      map[string]*commandPipe
	pipeOrder  []string

	record   string
	fields   []string
	filename string
	nr       int
	fnr      int
	exitCode int
}

type arraySlot struct {
	name string
	key  string
}

type callFrame struct {
	locals map[string]*localVar
}

type commandPipe struct {
	command string
	name    string
	args    []string
	buf     bytes.Buffer
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
		callCtx:    callCtx,
		prog:       prog,
		vars:       make(map[string]value),
		arrays:     make(map[string]map[string]value),
		varSizes:   make(map[string]int),
		arraySizes: make(map[arraySlot]int),
		rangeOn:    make(map[int]bool),
		pipes:      make(map[string]*commandPipe),
	}
	rt.vars["FS"] = stringValue(" ")
	rt.vars["OFS"] = stringValue(" ")
	rt.vars["ORS"] = stringValue("\n")
	rt.vars["SUBSEP"] = stringValue("\034")
	rt.vars["RSTART"] = numberValue(0)
	rt.vars["RLENGTH"] = numberValue(-1)
	return rt
}

func (rt *runtime) run(ctx context.Context, files []string) builtins.Result {
	exited := false
	if err := rt.runRules(ctx, ruleBegin); err != nil {
		if code, ok := exitCodeFromError(err); ok {
			rt.exitCode = code
			exited = true
		} else {
			rt.callCtx.Errf("awk: %v\n", err)
			return builtins.Result{Code: 1}
		}
	}
	if !exited && rt.needsInput() {
		if len(files) == 0 {
			files = []string{"-"}
		}
		ranInput := false
		for _, file := range files {
			assigned, err := rt.applyOperandAssignment(file)
			if err != nil {
				rt.callCtx.Errf("awk: %v\n", err)
				return builtins.Result{Code: 1}
			}
			if assigned {
				continue
			}
			ranInput = true
			if err := rt.runFile(ctx, file); err != nil {
				if code, ok := exitCodeFromError(err); ok {
					rt.exitCode = code
					exited = true
					break
				}
				rt.callCtx.Errf("awk: %s: %v\n", file, err)
				return builtins.Result{Code: 1}
			}
		}
		if !ranInput && !exited {
			if err := rt.runFile(ctx, "-"); err != nil {
				if code, ok := exitCodeFromError(err); ok {
					rt.exitCode = code
				} else {
					rt.callCtx.Errf("awk: -: %v\n", err)
					return builtins.Result{Code: 1}
				}
			}
		}
	}
	if err := rt.runRules(ctx, ruleEnd); err != nil {
		if code, ok := exitCodeFromError(err); ok {
			rt.exitCode = code
		} else {
			rt.callCtx.Errf("awk: %v\n", err)
			return builtins.Result{Code: 1}
		}
	}
	if err := rt.closeAllCommandPipes(ctx); err != nil {
		rt.callCtx.Errf("awk: %v\n", err)
		return builtins.Result{Code: 1}
	}
	return builtins.Result{Code: normalizeAwkExitCode(rt.exitCode)}
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

func (rt *runtime) runFile(ctx context.Context, file string) error {
	rc, err := rt.openInput(ctx, file)
	if err != nil {
		return err
	}
	defer rc.Close()
	rt.filename = file
	rt.fnr = 0
	sc := bufio.NewScanner(rc)
	sc.Split(scanAwkRecord)
	sc.Buffer(make([]byte, 4096), MaxRecordBytes+1)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		rec := sc.Text()
		if len(rec) > MaxRecordBytes {
			return fmt.Errorf("record exceeds %d bytes", MaxRecordBytes)
		}
		if err := rt.setRecord(rec); err != nil {
			return err
		}
		rt.nr++
		rt.fnr++
		if err := rt.runRules(ctx, ruleNormal); err != nil {
			if errors.Is(err, errNextRecord) {
				continue
			}
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return nil
}

func scanAwkRecord(data []byte, atEOF bool) (int, []byte, error) {
	for i, b := range data {
		if b == '\n' {
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

func (rt *runtime) openInput(ctx context.Context, file string) (io.ReadCloser, error) {
	if file == "-" {
		if rt.callCtx.Stdin == nil {
			return io.NopCloser(strings.NewReader("")), nil
		}
		return io.NopCloser(rt.callCtx.Stdin), nil
	}
	f, err := rt.callCtx.OpenFile(ctx, file, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (rt *runtime) writeCommandPipe(ctx context.Context, target expr, out string) error {
	commandValue, err := rt.eval(target)
	if err != nil {
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
	if len(out) > MaxPipeBytes-pipe.buf.Len() {
		return fmt.Errorf("command pipe %q input exceeds %d bytes", command, MaxPipeBytes)
	}
	_, err = pipe.buf.WriteString(out)
	if err != nil {
		return err
	}
	return ctx.Err()
}

func (rt *runtime) commandPipe(command string) (*commandPipe, error) {
	if pipe, ok := rt.pipes[command]; ok {
		return pipe, nil
	}
	name, args, err := parseCommandPipe(command)
	if err != nil {
		return nil, err
	}
	if rt.callCtx.CommandAllowed != nil && !rt.callCtx.CommandAllowed(name) {
		return nil, fmt.Errorf("command pipe %q is not allowed", name)
	}
	pipe := &commandPipe{command: command, name: name, args: args}
	rt.pipes[command] = pipe
	rt.pipeOrder = append(rt.pipeOrder, command)
	return pipe, nil
}

func parseCommandPipe(command string) (string, []string, error) {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return "", nil, fmt.Errorf("expression for `|' redirection has null string value")
	}
	name := fields[0]
	if !validPipeCommandName(name) {
		return "", nil, fmt.Errorf("command pipe %q uses unsupported command name", command)
	}
	for _, field := range fields {
		if strings.ContainsRune(field, '\x00') || strings.ContainsRune(field, '\n') || strings.ContainsRune(field, '\r') {
			return "", nil, fmt.Errorf("command pipe %q uses unsupported shell syntax", command)
		}
		for _, ch := range field {
			if isCommandPipeShellSyntax(ch) {
				return "", nil, fmt.Errorf("command pipe %q uses unsupported shell syntax", command)
			}
		}
	}
	return name, fields[1:], nil
}

func validPipeCommandName(name string) bool {
	if name == "" {
		return false
	}
	for _, ch := range name {
		if ch >= 'a' && ch <= 'z' {
			continue
		}
		if ch >= 'A' && ch <= 'Z' {
			continue
		}
		if ch >= '0' && ch <= '9' {
			continue
		}
		if ch == '_' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

func isCommandPipeShellSyntax(ch rune) bool {
	switch ch {
	case '\'', '"', '\\', '`', '$', ';', '|', '&', '<', '>', '(', ')', '{', '}', '[', ']', '*', '?':
		return true
	default:
		return false
	}
}

func (rt *runtime) closeCommandPipe(ctx context.Context, command string) (uint8, bool, error) {
	pipe, ok := rt.pipes[command]
	if !ok {
		return 0, false, nil
	}
	delete(rt.pipes, command)
	rt.removeCommandPipeOrder(command)
	status, err := rt.runCommandPipe(ctx, pipe)
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
		_, _, err := rt.closeCommandPipe(ctx, command)
		if err != nil {
			return err
		}
	}
	return nil
}

func (rt *runtime) runCommandPipe(ctx context.Context, pipe *commandPipe) (uint8, error) {
	if rt.callCtx.RunCommandWithStdin == nil {
		return 127, fmt.Errorf("command pipes are not available")
	}
	dir := ""
	if rt.callCtx.WorkDir != nil {
		dir = rt.callCtx.WorkDir()
	}
	return rt.callCtx.RunCommandWithStdin(ctx, dir, pipe.name, pipe.args, bytes.NewReader(pipe.buf.Bytes()))
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
			if err := rt.printValues([]value{rt.field(0)}); err != nil {
				return err
			}
			continue
		}
		if err := rt.execStatements(ctx, r.action); err != nil {
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
	if rx, ok := x.(*regexExpr); ok {
		re, err := compileRegex(rx.pattern)
		if err != nil {
			return false, err
		}
		return re.MatchString(rt.record), nil
	}
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
	rt.record = rec
	fs := rt.getVar("FS").String()
	fields, err := splitAwkFields(rec, fs)
	if err != nil {
		return err
	}
	rt.fields = fields
	if len(rt.fields) > MaxFields {
		return fmt.Errorf("record has too many fields")
	}
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
	fieldCount := max(len(rt.fields), n)
	if err := validateRebuiltRecordSize(rt.fields, fieldCount, n, s, rt.getVar("OFS").String()); err != nil {
		return err
	}
	for len(rt.fields) < n {
		rt.fields = append(rt.fields, "")
	}
	rt.fields[n-1] = s
	return rt.rebuildRecordFromFields()
}

func (rt *runtime) setNF(n int) error {
	if n < 0 {
		return fmt.Errorf("invalid NF value")
	}
	if n > MaxFields {
		return fmt.Errorf("record has too many fields")
	}
	if err := validateRebuiltRecordSize(rt.fields, n, 0, "", rt.getVar("OFS").String()); err != nil {
		return err
	}
	if n < len(rt.fields) {
		rt.fields = rt.fields[:n]
	} else {
		for len(rt.fields) < n {
			rt.fields = append(rt.fields, "")
		}
	}
	return rt.rebuildRecordFromFields()
}

func splitAwkFields(s, fs string) ([]string, error) {
	if fs == " " {
		return splitAwkWhitespaceFields(s), nil
	}
	if err := validateFS(fs); err != nil {
		return nil, err
	}
	if s == "" {
		return nil, nil
	}
	if isSingleRune(fs) {
		return strings.Split(s, fs), nil
	}
	return splitAwkRegex(s, fs)
}

func splitAwkWhitespaceFields(rec string) []string {
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
			fields = append(fields, rec[start:i])
		}
	}
	return fields
}

func isAwkFieldBlank(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n'
}

func splitAwkChars(s string) []string {
	if s == "" {
		return nil
	}
	chars := make([]string, 0, len(s))
	for _, r := range s {
		chars = append(chars, string(r))
	}
	return chars
}

func splitAwkRegex(s, pattern string) ([]string, error) {
	if s == "" {
		return nil, nil
	}
	if pattern == "" {
		return splitAwkChars(s), nil
	}
	re, err := compileRegex(pattern)
	if err != nil {
		return nil, err
	}
	matches := re.FindAllStringIndex(s, -1)
	fields := make([]string, 0, len(matches)+1)
	last := 0
	for _, match := range matches {
		if match[0] == match[1] {
			continue
		}
		fields = append(fields, s[last:match[0]])
		last = match[1]
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
		return rt.isGlobalArray(root.globalArrayName)
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
		return unassignedValue()
	}
	switch name {
	case "NF":
		return numberValue(float64(len(rt.fields)))
	case "NR":
		return numberValue(float64(rt.nr))
	case "FNR":
		return numberValue(float64(rt.fnr))
	case "FILENAME":
		return stringValue(rt.filename)
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
		return rt.setNF(int(v.Number()))
	case "NR", "FNR", "FILENAME":
		return fmt.Errorf("assignment to %s is not supported", name)
	case "FS":
		if err := validateFS(v.String()); err != nil {
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
	rt.vars[name] = v
	return nil
}

func (rt *runtime) setLocalScalar(local *localVar, v value) error {
	size := len(v.String())
	if size > MaxVariableBytes {
		return fmt.Errorf("variable value exceeds %d bytes", MaxVariableBytes)
	}
	if rt.varBytes-local.valueSize+size > MaxVariableBytes {
		return fmt.Errorf("variable storage limit exceeded (%d bytes total)", rt.varBytes-local.valueSize+size)
	}
	rt.varBytes = rt.varBytes - local.valueSize + size
	local.valueSize = size
	local.value = v
	local.valueSet = true
	local.arrayAlias = nil
	local.globalArrayName = ""
	local.array = nil
	local.arraySizes = nil
	return nil
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
	if root.valueSet && root.array == nil {
		return nil, nil, "", true, fmt.Errorf("cannot use scalar %s as array", name)
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
	slot := arraySlot{name: name, key: key}
	old := rt.arraySizes[slot]
	if rt.varBytes-old+size > MaxVariableBytes {
		return fmt.Errorf("variable storage limit exceeded (%d bytes total)", rt.varBytes-old+size)
	}
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
	sortStringKeys(keys)
	return keys, nil
}

func sortStringKeys(keys []string) {
	for i := 1; i < len(keys); i++ {
		key := keys[i]
		j := i - 1
		for j >= 0 && keys[j] > key {
			keys[j+1] = keys[j]
			j--
		}
		keys[j+1] = key
	}
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

func validateFS(fs string) error {
	if fs == " " {
		return nil
	}
	if fs == "" {
		return fmt.Errorf("empty FS is not supported")
	}
	if isSingleRune(fs) {
		return nil
	}
	_, err := compileRegex(fs)
	if err != nil {
		return err
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
	re       *regexp.Regexp
	byteMode bool
}

func compileRegex(pattern string) (*awkRegex, error) {
	normalized, byteMode := normalizeAwkRegex(pattern)
	re, err := regexp.Compile(normalized)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression %q: %v", pattern, err)
	}
	re.Longest()
	return &awkRegex{re: re, byteMode: byteMode}, nil
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
	var b strings.Builder
	byteMode := awkRegexNeedsByteMode(pattern)
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		if ch != '\\' {
			if ch >= 0x80 {
				r, size := utf8.DecodeRuneInString(pattern[i:])
				if byteMode || (r == utf8.RuneError && size == 1) {
					for j := i; j < i+size; j++ {
						writeAwkRegexByteEscape(&b, pattern[j])
					}
					i += size - 1
					continue
				}
				b.WriteString(pattern[i : i+size])
				i += size - 1
				continue
			}
			b.WriteByte(ch)
			continue
		}
		if i+1 >= len(pattern) {
			b.WriteByte(ch)
			continue
		}
		if isOctalDigit(rune(pattern[i+1])) {
			value := 0
			for digits := 0; digits < 3 && i+1 < len(pattern) && isOctalDigit(rune(pattern[i+1])); digits++ {
				i++
				value = value*8 + int(pattern[i]-'0')
			}
			writeAwkRegexByteEscape(&b, byte(value))
			continue
		}
		i++
		writeAwkRegexEscape(&b, pattern[i])
	}
	return b.String(), byteMode
}

func awkRegexNeedsByteMode(pattern string) bool {
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		if ch == '\\' && i+1 < len(pattern) && isOctalDigit(rune(pattern[i+1])) {
			value := 0
			for digits := 0; digits < 3 && i+1 < len(pattern) && isOctalDigit(rune(pattern[i+1])); digits++ {
				i++
				value = value*8 + int(pattern[i]-'0')
			}
			if byte(value) >= 0x80 {
				return true
			}
			continue
		}
		if ch >= 0x80 {
			r, size := utf8.DecodeRuneInString(pattern[i:])
			if r == utf8.RuneError && size == 1 {
				return true
			}
			i += size - 1
		}
	}
	return false
}

func writeAwkRegexByteEscape(b *strings.Builder, value byte) {
	if value >= 0x80 {
		const hex = "0123456789abcdef"
		b.WriteString(`\x{`)
		b.WriteByte(hex[value>>4])
		b.WriteByte(hex[value&0x0f])
		b.WriteByte('}')
		return
	}
	b.WriteByte(value)
}

func encodeAwkRegexBytes(s string) (string, []int) {
	var b strings.Builder
	offsets := []int{0}
	for i := 0; i < len(s); i++ {
		before := b.Len()
		if s[i] >= 0x80 {
			b.WriteRune(rune(s[i]))
		} else {
			b.WriteByte(s[i])
		}
		for j := before + 1; j < b.Len(); j++ {
			offsets = append(offsets, i)
		}
		offsets = append(offsets, i+1)
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
	case '.', '[', ']', '(', ')', '{', '}', '*', '+', '?', '|', '^', '$', '\\':
		b.WriteByte('\\')
		b.WriteByte(esc)
	case 'w', 'W', 's', 'S':
		b.WriteByte('\\')
		b.WriteByte(esc)
	default:
		b.WriteByte(esc)
	}
}
