// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"bufio"
	"context"
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
		return strconv.FormatFloat(v.n, 'g', 6, 64)
	case valueRegex:
		return v.pattern
	default:
		return v.s
	}
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
	return n, err == nil
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
	callCtx  *builtins.CallContext
	prog     *program
	vars     map[string]value
	varSizes map[string]int
	varBytes int

	record   string
	fields   []string
	filename string
	nr       int
	fnr      int
}

func newRuntime(callCtx *builtins.CallContext, prog *program) *runtime {
	rt := &runtime{
		callCtx:  callCtx,
		prog:     prog,
		vars:     make(map[string]value),
		varSizes: make(map[string]int),
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
		for _, file := range files {
			if err := rt.runFile(ctx, file); err != nil {
				rt.callCtx.Errf("awk: %s: %v\n", file, err)
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
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return nil
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
		rt.fields = strings.Fields(rec)
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
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regular expression %q: %v", pattern, err)
	}
	return re, nil
}
