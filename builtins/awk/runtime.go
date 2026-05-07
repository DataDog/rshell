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

	record   string
	fields   []string
	filename string
	nr       int
	fnr      int
}

type arraySlot struct {
	name string
	key  string
}

func newRuntime(callCtx *builtins.CallContext, prog *program) *runtime {
	rt := &runtime{
		callCtx:    callCtx,
		prog:       prog,
		vars:       make(map[string]value),
		arrays:     make(map[string]map[string]value),
		varSizes:   make(map[string]int),
		arraySizes: make(map[arraySlot]int),
	}
	rt.vars["FS"] = stringValue(" ")
	rt.vars["OFS"] = stringValue(" ")
	rt.vars["ORS"] = stringValue("\n")
	return rt
}

func (rt *runtime) run(ctx context.Context, files []string) builtins.Result {
	if err := rt.runRules(ctx, ruleBegin); err != nil {
		rt.callCtx.Errf("awk: %v\n", err)
		return builtins.Result{Code: 1}
	}
	if rt.needsInput() {
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
				rt.callCtx.Errf("awk: %s: %v\n", file, err)
				return builtins.Result{Code: 1}
			}
		}
		if !ranInput {
			if err := rt.runFile(ctx, "-"); err != nil {
				rt.callCtx.Errf("awk: -: %v\n", err)
				return builtins.Result{Code: 1}
			}
		}
	}
	if err := rt.runRules(ctx, ruleEnd); err != nil {
		rt.callCtx.Errf("awk: %v\n", err)
		return builtins.Result{Code: 1}
	}
	return builtins.Result{}
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

func (rt *runtime) runRules(ctx context.Context, kind ruleKind) error {
	for _, r := range rt.prog.rules {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.kind != kind {
			continue
		}
		if kind == ruleNormal && r.pattern != nil {
			ok, err := rt.matchPattern(r.pattern)
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
		if err := rt.execStatements(r.action); err != nil {
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

func (rt *runtime) matchPattern(x expr) (bool, error) {
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
	rt.record = rec
	fs := rt.getVar("FS").String()
	if fs == " " {
		rt.fields = splitAwkWhitespaceFields(rec)
	} else {
		if err := validateFS(fs); err != nil {
			return err
		}
		if rec == "" {
			rt.fields = nil
		} else {
			rt.fields = strings.Split(rec, fs)
		}
	}
	if len(rt.fields) > MaxFields {
		return fmt.Errorf("record has too many fields")
	}
	return nil
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

func (rt *runtime) field(n int) value {
	if n == 0 {
		return inputStringValue(rt.record)
	}
	if n < 0 || n > len(rt.fields) {
		return stringValue("")
	}
	return inputStringValue(rt.fields[n-1])
}

func (rt *runtime) getVar(name string) value {
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
		return unassignedValue()
	}
}

func (rt *runtime) setVar(name string, v value) error {
	if rt.isArray(name) {
		return fmt.Errorf("cannot use array %s as scalar", name)
	}
	switch name {
	case "NF":
		return fmt.Errorf("assignment to NF is not supported")
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

func (rt *runtime) isArray(name string) bool {
	arr, ok := rt.arrays[name]
	return ok && arr != nil
}

func (rt *runtime) getArrayElem(name, key string) (value, error) {
	if err := rt.validateArrayName(name); err != nil {
		return value{}, err
	}
	if v, ok := rt.arrays[name][key]; ok {
		return v, nil
	}
	v := unassignedValue()
	if err := rt.setArrayElem(name, key, v); err != nil {
		return value{}, err
	}
	return v, nil
}

func (rt *runtime) setArrayElem(name, key string, v value) error {
	if err := rt.validateArrayName(name); err != nil {
		return err
	}
	size := len(key) + len(v.String())
	if size > MaxVariableBytes {
		return fmt.Errorf("array element exceeds %d bytes", MaxVariableBytes)
	}
	slot := arraySlot{name: name, key: key}
	old := rt.arraySizes[slot]
	if rt.varBytes-old+size > MaxVariableBytes {
		return fmt.Errorf("variable storage limit exceeded (%d bytes total)", rt.varBytes-old+size)
	}
	if rt.arrays[name] == nil {
		rt.arrays[name] = make(map[string]value)
	}
	rt.varBytes = rt.varBytes - old + size
	rt.arraySizes[slot] = size
	rt.arrays[name][key] = v
	return nil
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

func validateFS(fs string) error {
	if fs == " " {
		return nil
	}
	if fs == "" {
		return fmt.Errorf("empty FS is not supported")
	}
	r, size := utf8.DecodeRuneInString(fs)
	if r == utf8.RuneError && size == 0 {
		return fmt.Errorf("empty FS is not supported")
	}
	if size != len(fs) {
		return fmt.Errorf("multi-character and regex FS values are not supported")
	}
	return nil
}

func compileRegex(pattern string) (*regexp.Regexp, error) {
	normalized := normalizeAwkRegex(pattern)
	re, err := regexp.Compile(normalized)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression %q: %v", pattern, err)
	}
	return re, nil
}

func normalizeAwkRegex(pattern string) string {
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		ch := pattern[i]
		if ch != '\\' {
			b.WriteByte(ch)
			continue
		}
		if i+1 >= len(pattern) {
			b.WriteByte(ch)
			continue
		}
		i++
		writeAwkRegexEscape(&b, pattern[i])
	}
	return b.String()
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
