// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awkcore

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Limits for safety.
const (
	MaxArraySize      = 100000
	MaxStringLen      = 1 << 20 // 1 MiB
	MaxLoopIterations = 10000000
	MaxCallDepth      = 100
	MaxOutputBytes    = 1 << 20 // 1 MiB
	MaxRegexCache     = 1000
	MaxSprintfWidth   = 1000
	MaxProgFileSize   = 10 << 20 // 10 MiB
)

// RuntimeError is returned when awk encounters a runtime error.
type RuntimeError struct {
	msg      string
	exitCode uint8
	isExit   bool
}

func (e *RuntimeError) Error() string   { return e.msg }
func (e *RuntimeError) ExitCode() uint8 { return e.exitCode }
func (e *RuntimeError) IsExit() bool    { return e.isExit }

// Control flow signals.
type breakSignal struct{}
type continueSignal struct{}
type nextSignal struct{}
type exitSignal struct{ code uint8 }
type returnSignal struct{ val value }

func (breakSignal) Error() string    { return "break" }
func (continueSignal) Error() string { return "continue" }
func (nextSignal) Error() string     { return "next" }
func (e exitSignal) Error() string   { return fmt.Sprintf("exit %d", e.code) }
func (returnSignal) Error() string   { return "return" }

// value represents an awk value (number or string, lazily converted).
type value struct {
	str    string
	num    float64
	isNum  bool
	strSet bool
}

var zeroValue = value{}

func numVal(n float64) value {
	return value{num: n, isNum: true}
}

func strVal(s string) value {
	return value{str: s, strSet: true}
}

func (v value) toNum() float64 {
	if v.isNum {
		return v.num
	}
	s := strings.TrimSpace(v.str)
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func (v value) toStr() string {
	if v.strSet {
		return v.str
	}
	if v.isNum {
		if v.num == float64(int64(v.num)) && !math.IsInf(v.num, 0) {
			return strconv.FormatInt(int64(v.num), 10)
		}
		return strconv.FormatFloat(v.num, 'g', 6, 64)
	}
	return ""
}

func (v value) toBool() bool {
	if v.isNum {
		return v.num != 0
	}
	return v.str != ""
}

// Interpreter executes an awk program.
type Interpreter struct {
	prog   *Program
	stdout io.Writer
	stderr io.Writer

	globals    map[string]value
	arrays     map[string]map[string]value
	fields     []string
	record     string
	rng        *rand.Rand
	exitCode   uint8
	callDepth  int
	outBytes   int
	regexCache map[string]*regexp.Regexp

	// range pattern states: true when active.
	rangeActive []bool
}

// NewInterpreter creates a new awk interpreter.
func NewInterpreter(prog *Program, stdout, stderr io.Writer) *Interpreter {
	interp := &Interpreter{
		prog:       prog,
		stdout:     stdout,
		stderr:     stderr,
		globals:    make(map[string]value),
		arrays:     make(map[string]map[string]value),
		rng:        rand.New(rand.NewSource(0)),
		regexCache: make(map[string]*regexp.Regexp),
	}
	// Initialize built-in variables.
	interp.globals["FS"] = strVal(" ")
	interp.globals["RS"] = strVal("\n")
	interp.globals["OFS"] = strVal(" ")
	interp.globals["ORS"] = strVal("\n")
	interp.globals["NR"] = numVal(0)
	interp.globals["NF"] = numVal(0)
	interp.globals["FNR"] = numVal(0)
	interp.globals["FILENAME"] = strVal("")
	interp.globals["SUBSEP"] = strVal("\x1c")
	interp.globals["RSTART"] = numVal(0)
	interp.globals["RLENGTH"] = numVal(-1)
	interp.rangeActive = make([]bool, len(prog.Rules))

	// ARGC/ARGV are set externally.
	return interp
}

// SetFS sets the field separator.
func (interp *Interpreter) SetFS(fs string) {
	interp.globals["FS"] = strVal(fs)
}

// SetVar sets a global variable.
func (interp *Interpreter) SetVar(name, val string) {
	interp.globals[name] = strVal(val)
}

// SetFilename sets the FILENAME variable.
func (interp *Interpreter) SetFilename(name string) {
	interp.globals["FILENAME"] = strVal(name)
}

// ResetFNR resets the per-file record counter.
func (interp *Interpreter) ResetFNR() {
	interp.globals["FNR"] = numVal(0)
}

// ExitCode returns the program's exit code.
func (interp *Interpreter) ExitCode() uint8 {
	return interp.exitCode
}

// ExecBegin executes all BEGIN blocks.
func (interp *Interpreter) ExecBegin(ctx context.Context) error {
	for _, action := range interp.prog.Begin {
		if err := interp.execStmts(ctx, action.Stmts); err != nil {
			return interp.handleError(err)
		}
	}
	return nil
}

// ExecEnd executes all END blocks.
func (interp *Interpreter) ExecEnd(ctx context.Context) error {
	for _, action := range interp.prog.End {
		if err := interp.execStmts(ctx, action.Stmts); err != nil {
			return interp.handleError(err)
		}
	}
	return nil
}

// ExecLine processes one input line.
func (interp *Interpreter) ExecLine(ctx context.Context, line string) error {
	// Update NR and FNR.
	interp.globals["NR"] = numVal(interp.globals["NR"].toNum() + 1)
	interp.globals["FNR"] = numVal(interp.globals["FNR"].toNum() + 1)

	interp.setRecord(line)

	for i, rule := range interp.prog.Rules {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		matched, err := interp.matchPattern(ctx, rule.Pattern, i)
		if err != nil {
			return interp.handleError(err)
		}
		if !matched {
			continue
		}
		if rule.Action != nil {
			if err := interp.execStmts(ctx, rule.Action.Stmts); err != nil {
				return interp.handleError(err)
			}
		} else {
			// Default action: print $0.
			interp.writeOut(interp.record + interp.globals["ORS"].toStr())
		}
	}
	return nil
}

func (interp *Interpreter) setRecord(line string) {
	interp.record = line
	interp.globals["$0"] = strVal(line)
	interp.splitRecord()
}

func (interp *Interpreter) splitRecord() {
	fs := interp.globals["FS"].toStr()
	parts := splitFields(interp.record, fs)
	interp.fields = parts
	interp.globals["NF"] = numVal(float64(len(parts)))
}

func (interp *Interpreter) matchPattern(ctx context.Context, pat Pattern, ruleIdx int) (bool, error) {
	if pat == nil {
		return true, nil
	}
	switch p := pat.(type) {
	case *ExprPattern:
		if re, ok := p.Expr.(*RegexLit); ok {
			return interp.matchRegex(re.Val, interp.record)
		}
		v, err := interp.eval(ctx, p.Expr)
		if err != nil {
			return false, err
		}
		return v.toBool(), nil
	case *RangePattern:
		if !interp.rangeActive[ruleIdx] {
			v, err := interp.eval(ctx, p.Begin)
			if err != nil {
				return false, err
			}
			if v.toBool() {
				interp.rangeActive[ruleIdx] = true
				return true, nil
			}
			return false, nil
		}
		v, err := interp.eval(ctx, p.End)
		if err != nil {
			return false, err
		}
		if v.toBool() {
			interp.rangeActive[ruleIdx] = false
		}
		return true, nil
	}
	return false, nil
}

func (interp *Interpreter) handleError(err error) error {
	if err == nil {
		return nil
	}
	switch e := err.(type) {
	case exitSignal:
		interp.exitCode = e.code
		return &RuntimeError{msg: e.Error(), exitCode: e.code, isExit: true}
	case nextSignal:
		return nil
	case breakSignal, continueSignal:
		return nil
	case returnSignal:
		return nil
	case *RuntimeError:
		return e
	}
	return &RuntimeError{msg: err.Error(), exitCode: 1}
}

func (interp *Interpreter) execStmts(ctx context.Context, stmts []Stmt) error {
	for _, stmt := range stmts {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := interp.execStmt(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (interp *Interpreter) execStmt(ctx context.Context, stmt Stmt) error {
	switch s := stmt.(type) {
	case *ExprStmt:
		_, err := interp.eval(ctx, s.Expr)
		return err
	case *PrintStmt:
		return interp.execPrint(ctx, s)
	case *PrintfStmt:
		return interp.execPrintf(ctx, s)
	case *IfStmt:
		return interp.execIf(ctx, s)
	case *WhileStmt:
		return interp.execWhile(ctx, s)
	case *DoWhileStmt:
		return interp.execDoWhile(ctx, s)
	case *ForStmt:
		return interp.execFor(ctx, s)
	case *ForInStmt:
		return interp.execForIn(ctx, s)
	case *BreakStmt:
		return breakSignal{}
	case *ContinueStmt:
		return continueSignal{}
	case *NextStmt:
		return nextSignal{}
	case *ExitStmt:
		return interp.execExit(ctx, s)
	case *DeleteStmt:
		return interp.execDelete(ctx, s)
	case *BlockStmt:
		return interp.execStmts(ctx, s.Stmts)
	case *ReturnStmt:
		return interp.execReturn(ctx, s)
	}
	return nil
}

func (interp *Interpreter) execPrint(ctx context.Context, s *PrintStmt) error {
	if s.Dest != nil {
		return &RuntimeError{msg: "output redirection is not supported (blocked for safety)", exitCode: 1}
	}
	ofs := interp.globals["OFS"].toStr()
	ors := interp.globals["ORS"].toStr()
	if len(s.Args) == 0 {
		interp.writeOut(interp.record + ors)
		return nil
	}
	var parts []string
	for _, arg := range s.Args {
		v, err := interp.eval(ctx, arg)
		if err != nil {
			return err
		}
		parts = append(parts, v.toStr())
	}
	interp.writeOut(strings.Join(parts, ofs) + ors)
	return nil
}

func (interp *Interpreter) execPrintf(ctx context.Context, s *PrintfStmt) error {
	if s.Dest != nil {
		return &RuntimeError{msg: "output redirection is not supported (blocked for safety)", exitCode: 1}
	}
	if len(s.Args) == 0 {
		return nil
	}
	var vals []value
	for _, arg := range s.Args {
		v, err := interp.eval(ctx, arg)
		if err != nil {
			return err
		}
		vals = append(vals, v)
	}
	result := awkSprintf(vals)
	interp.writeOut(result)
	return nil
}

func (interp *Interpreter) execIf(ctx context.Context, s *IfStmt) error {
	v, err := interp.eval(ctx, s.Cond)
	if err != nil {
		return err
	}
	if v.toBool() {
		return interp.execStmts(ctx, s.Then)
	}
	if s.Else != nil {
		return interp.execStmts(ctx, s.Else)
	}
	return nil
}

func (interp *Interpreter) execWhile(ctx context.Context, s *WhileStmt) error {
	for i := 0; i < MaxLoopIterations; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		v, err := interp.eval(ctx, s.Cond)
		if err != nil {
			return err
		}
		if !v.toBool() {
			break
		}
		if err := interp.execStmts(ctx, s.Body); err != nil {
			if _, ok := err.(breakSignal); ok {
				break
			}
			if _, ok := err.(continueSignal); ok {
				continue
			}
			return err
		}
	}
	return nil
}

func (interp *Interpreter) execDoWhile(ctx context.Context, s *DoWhileStmt) error {
	for i := 0; i < MaxLoopIterations; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := interp.execStmts(ctx, s.Body); err != nil {
			if _, ok := err.(breakSignal); ok {
				break
			}
			if _, ok := err.(continueSignal); ok {
				// fall through to condition
			} else {
				return err
			}
		}
		v, err := interp.eval(ctx, s.Cond)
		if err != nil {
			return err
		}
		if !v.toBool() {
			break
		}
	}
	return nil
}

func (interp *Interpreter) execFor(ctx context.Context, s *ForStmt) error {
	if s.Init != nil {
		if err := interp.execStmt(ctx, s.Init); err != nil {
			return err
		}
	}
	for i := 0; i < MaxLoopIterations; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if s.Cond != nil {
			v, err := interp.eval(ctx, s.Cond)
			if err != nil {
				return err
			}
			if !v.toBool() {
				break
			}
		}
		if err := interp.execStmts(ctx, s.Body); err != nil {
			if _, ok := err.(breakSignal); ok {
				break
			}
			if _, ok := err.(continueSignal); ok {
				// fall through to post
			} else {
				return err
			}
		}
		if s.Post != nil {
			if err := interp.execStmt(ctx, s.Post); err != nil {
				return err
			}
		}
	}
	return nil
}

func (interp *Interpreter) execForIn(ctx context.Context, s *ForInStmt) error {
	arr := interp.arrays[s.Array]
	if arr == nil {
		return nil
	}
	keys := make([]string, 0, len(arr))
	for k := range arr {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if i >= MaxLoopIterations {
			break
		}
		interp.globals[s.Var] = strVal(k)
		if err := interp.execStmts(ctx, s.Body); err != nil {
			if _, ok := err.(breakSignal); ok {
				break
			}
			if _, ok := err.(continueSignal); ok {
				continue
			}
			return err
		}
	}
	return nil
}

func (interp *Interpreter) execExit(ctx context.Context, s *ExitStmt) error {
	code := uint8(0)
	if s.Value != nil {
		v, err := interp.eval(ctx, s.Value)
		if err != nil {
			return err
		}
		n := v.toNum()
		if n < 0 {
			n = 0
		} else if n > 255 {
			n = 255
		}
		code = uint8(n)
	}
	return exitSignal{code: code}
}

func (interp *Interpreter) execDelete(ctx context.Context, s *DeleteStmt) error {
	if s.Index == nil {
		delete(interp.arrays, s.Array)
		return nil
	}
	arr := interp.arrays[s.Array]
	if arr == nil {
		return nil
	}
	key, err := interp.buildArrayKey(ctx, s.Index)
	if err != nil {
		return err
	}
	delete(arr, key)
	return nil
}

func (interp *Interpreter) execReturn(ctx context.Context, s *ReturnStmt) error {
	var v value
	if s.Value != nil {
		var err error
		v, err = interp.eval(ctx, s.Value)
		if err != nil {
			return err
		}
	}
	return returnSignal{val: v}
}

// eval evaluates an expression.
func (interp *Interpreter) eval(ctx context.Context, expr Expr) (value, error) {
	if ctx.Err() != nil {
		return zeroValue, ctx.Err()
	}
	switch e := expr.(type) {
	case *NumberLit:
		f, _ := strconv.ParseFloat(e.Val, 64)
		return numVal(f), nil
	case *StringLit:
		return strVal(e.Val), nil
	case *RegexLit:
		matched, err := interp.matchRegex(e.Val, interp.record)
		if err != nil {
			return zeroValue, err
		}
		if matched {
			return numVal(1), nil
		}
		return numVal(0), nil
	case *VarExpr:
		return interp.getVar(e.Name), nil
	case *FieldExpr:
		return interp.evalField(ctx, e)
	case *ArrayExpr:
		return interp.evalArray(ctx, e)
	case *UnaryExpr:
		return interp.evalUnary(ctx, e)
	case *BinaryExpr:
		return interp.evalBinary(ctx, e)
	case *TernaryExpr:
		return interp.evalTernary(ctx, e)
	case *AssignExpr:
		return interp.evalAssign(ctx, e)
	case *IncrDecrExpr:
		return interp.evalIncrDecr(ctx, e)
	case *ConcatExpr:
		return interp.evalConcat(ctx, e)
	case *MatchExpr:
		return interp.evalMatch(ctx, e)
	case *InExpr:
		return interp.evalIn(ctx, e)
	case *CallExpr:
		return interp.evalCall(ctx, e)
	case *GetlineExpr:
		return zeroValue, &RuntimeError{msg: "getline is not supported in this context", exitCode: 1}
	}
	return zeroValue, nil
}

func (interp *Interpreter) getVar(name string) value {
	if v, ok := interp.globals[name]; ok {
		return v
	}
	return zeroValue
}

func (interp *Interpreter) evalField(ctx context.Context, e *FieldExpr) (value, error) {
	v, err := interp.eval(ctx, e.Index)
	if err != nil {
		return zeroValue, err
	}
	idx := int(v.toNum())
	if idx == 0 {
		return strVal(interp.record), nil
	}
	if idx < 0 || idx > len(interp.fields) {
		return strVal(""), nil
	}
	return strVal(interp.fields[idx-1]), nil
}

func (interp *Interpreter) evalArray(ctx context.Context, e *ArrayExpr) (value, error) {
	key, err := interp.buildArrayKey(ctx, e.Index)
	if err != nil {
		return zeroValue, err
	}
	arr := interp.arrays[e.Name]
	if arr == nil {
		return zeroValue, nil
	}
	return arr[key], nil
}

func (interp *Interpreter) evalUnary(ctx context.Context, e *UnaryExpr) (value, error) {
	v, err := interp.eval(ctx, e.Expr)
	if err != nil {
		return zeroValue, err
	}
	switch e.Op {
	case tokNOT:
		if v.toBool() {
			return numVal(0), nil
		}
		return numVal(1), nil
	case tokMINUS:
		return numVal(-v.toNum()), nil
	}
	return v, nil
}

func (interp *Interpreter) evalBinary(ctx context.Context, e *BinaryExpr) (value, error) {
	// Short-circuit for logical operators.
	if e.Op == tokAND {
		left, err := interp.eval(ctx, e.Left)
		if err != nil {
			return zeroValue, err
		}
		if !left.toBool() {
			return numVal(0), nil
		}
		right, err := interp.eval(ctx, e.Right)
		if err != nil {
			return zeroValue, err
		}
		if right.toBool() {
			return numVal(1), nil
		}
		return numVal(0), nil
	}
	if e.Op == tokOR {
		left, err := interp.eval(ctx, e.Left)
		if err != nil {
			return zeroValue, err
		}
		if left.toBool() {
			return numVal(1), nil
		}
		right, err := interp.eval(ctx, e.Right)
		if err != nil {
			return zeroValue, err
		}
		if right.toBool() {
			return numVal(1), nil
		}
		return numVal(0), nil
	}

	left, err := interp.eval(ctx, e.Left)
	if err != nil {
		return zeroValue, err
	}
	right, err := interp.eval(ctx, e.Right)
	if err != nil {
		return zeroValue, err
	}

	switch e.Op {
	case tokPLUS:
		return numVal(left.toNum() + right.toNum()), nil
	case tokMINUS:
		return numVal(left.toNum() - right.toNum()), nil
	case tokSTAR:
		return numVal(left.toNum() * right.toNum()), nil
	case tokSLASH:
		d := right.toNum()
		if d == 0 {
			return zeroValue, &RuntimeError{msg: "division by zero", exitCode: 2}
		}
		return numVal(left.toNum() / d), nil
	case tokPERCENT:
		d := right.toNum()
		if d == 0 {
			return zeroValue, &RuntimeError{msg: "division by zero", exitCode: 2}
		}
		return numVal(math.Mod(left.toNum(), d)), nil
	case tokPOWER:
		return numVal(math.Pow(left.toNum(), right.toNum())), nil
	case tokLT:
		return boolToNum(compareValues(left, right) < 0), nil
	case tokLE:
		return boolToNum(compareValues(left, right) <= 0), nil
	case tokGT:
		return boolToNum(compareValues(left, right) > 0), nil
	case tokGE:
		return boolToNum(compareValues(left, right) >= 0), nil
	case tokEQ:
		return boolToNum(compareValues(left, right) == 0), nil
	case tokNE:
		return boolToNum(compareValues(left, right) != 0), nil
	}
	return zeroValue, nil
}

func compareValues(a, b value) int {
	// If both are numeric, compare as numbers.
	aNum, aIsNum := tryParseNum(a)
	bNum, bIsNum := tryParseNum(b)
	if aIsNum && bIsNum {
		if aNum < bNum {
			return -1
		}
		if aNum > bNum {
			return 1
		}
		return 0
	}
	// Otherwise compare as strings.
	as := a.toStr()
	bs := b.toStr()
	if as < bs {
		return -1
	}
	if as > bs {
		return 1
	}
	return 0
}

func tryParseNum(v value) (float64, bool) {
	if v.isNum {
		return v.num, true
	}
	if v.strSet {
		s := strings.TrimSpace(v.str)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, true
}

func boolToNum(b bool) value {
	if b {
		return numVal(1)
	}
	return numVal(0)
}

func (interp *Interpreter) evalTernary(ctx context.Context, e *TernaryExpr) (value, error) {
	v, err := interp.eval(ctx, e.Cond)
	if err != nil {
		return zeroValue, err
	}
	if v.toBool() {
		return interp.eval(ctx, e.Then)
	}
	return interp.eval(ctx, e.Else)
}

func (interp *Interpreter) evalAssign(ctx context.Context, e *AssignExpr) (value, error) {
	val, err := interp.eval(ctx, e.Value)
	if err != nil {
		return zeroValue, err
	}

	if e.Op != tokASSIGN {
		old, err := interp.eval(ctx, e.Target)
		if err != nil {
			return zeroValue, err
		}
		switch e.Op {
		case tokPLUSASSIGN:
			val = numVal(old.toNum() + val.toNum())
		case tokMINUSASSIGN:
			val = numVal(old.toNum() - val.toNum())
		case tokSTARASSIGN:
			val = numVal(old.toNum() * val.toNum())
		case tokSLASHASSIGN:
			d := val.toNum()
			if d == 0 {
				return zeroValue, &RuntimeError{msg: "division by zero", exitCode: 2}
			}
			val = numVal(old.toNum() / d)
		case tokPERCENTASSIGN:
			d := val.toNum()
			if d == 0 {
				return zeroValue, &RuntimeError{msg: "division by zero", exitCode: 2}
			}
			val = numVal(math.Mod(old.toNum(), d))
		case tokPOWERASSIGN:
			val = numVal(math.Pow(old.toNum(), val.toNum()))
		}
	}

	interp.assignTo(ctx, e.Target, val)
	return val, nil
}

func (interp *Interpreter) assignTo(ctx context.Context, target Expr, val value) {
	switch t := target.(type) {
	case *VarExpr:
		interp.globals[t.Name] = val
	case *FieldExpr:
		v, _ := interp.eval(ctx, t.Index)
		idx := int(v.toNum())
		interp.setField(idx, val.toStr())
	case *ArrayExpr:
		key, _ := interp.buildArrayKey(ctx, t.Index)
		arr := interp.arrays[t.Name]
		if arr == nil {
			arr = make(map[string]value)
			interp.arrays[t.Name] = arr
		}
		if len(arr) >= MaxArraySize {
			return
		}
		arr[key] = val
	}
}

func (interp *Interpreter) setField(idx int, val string) {
	if idx == 0 {
		interp.setRecord(val)
		return
	}
	if idx < 0 || idx > MaxArraySize {
		return
	}
	for len(interp.fields) < idx {
		interp.fields = append(interp.fields, "")
	}
	interp.fields[idx-1] = val
	interp.globals["NF"] = numVal(float64(len(interp.fields)))
	interp.rebuildRecord()
}

func (interp *Interpreter) rebuildRecord() {
	ofs := interp.globals["OFS"].toStr()
	interp.record = strings.Join(interp.fields, ofs)
	interp.globals["$0"] = strVal(interp.record)
}

func (interp *Interpreter) evalIncrDecr(ctx context.Context, e *IncrDecrExpr) (value, error) {
	old, err := interp.eval(ctx, e.Expr)
	if err != nil {
		return zeroValue, err
	}
	n := old.toNum()
	var newVal float64
	if e.Op == tokINCR {
		newVal = n + 1
	} else {
		newVal = n - 1
	}
	interp.assignTo(ctx, e.Expr, numVal(newVal))
	if e.Pre {
		return numVal(newVal), nil
	}
	return numVal(n), nil
}

func (interp *Interpreter) evalConcat(ctx context.Context, e *ConcatExpr) (value, error) {
	left, err := interp.eval(ctx, e.Left)
	if err != nil {
		return zeroValue, err
	}
	right, err := interp.eval(ctx, e.Right)
	if err != nil {
		return zeroValue, err
	}
	result := left.toStr() + right.toStr()
	if len(result) > MaxStringLen {
		result = result[:MaxStringLen]
	}
	return strVal(result), nil
}

func (interp *Interpreter) evalMatch(ctx context.Context, e *MatchExpr) (value, error) {
	left, err := interp.eval(ctx, e.Expr)
	if err != nil {
		return zeroValue, err
	}
	pattern := interp.regexPattern(ctx, e.Regex)
	matched, err := interp.matchRegex(pattern, left.toStr())
	if err != nil {
		return zeroValue, err
	}
	if e.Not {
		matched = !matched
	}
	return boolToNum(matched), nil
}

func (interp *Interpreter) evalIn(ctx context.Context, e *InExpr) (value, error) {
	key, err := interp.buildArrayKey(ctx, e.Index)
	if err != nil {
		return zeroValue, err
	}
	arr := interp.arrays[e.Array]
	if arr == nil {
		return numVal(0), nil
	}
	if _, ok := arr[key]; ok {
		return numVal(1), nil
	}
	return numVal(0), nil
}

func (interp *Interpreter) evalCall(ctx context.Context, e *CallExpr) (value, error) {
	// Check for built-in functions first.
	if v, err, handled := interp.callBuiltin(ctx, e); handled {
		return v, err
	}

	// User-defined function.
	fn, ok := interp.prog.Funcs[e.Name]
	if !ok {
		return zeroValue, &RuntimeError{
			msg:      fmt.Sprintf("undefined function: %s", e.Name),
			exitCode: 1,
		}
	}

	interp.callDepth++
	if interp.callDepth > MaxCallDepth {
		interp.callDepth--
		return zeroValue, &RuntimeError{msg: "call depth exceeded", exitCode: 1}
	}
	defer func() { interp.callDepth-- }()

	// Save and restore globals that are parameters.
	saved := make(map[string]value)
	savedArrays := make(map[string]map[string]value)
	for _, p := range fn.Params {
		if v, ok := interp.globals[p]; ok {
			saved[p] = v
		}
		if a, ok := interp.arrays[p]; ok {
			savedArrays[p] = a
		}
		delete(interp.globals, p)
		delete(interp.arrays, p)
	}
	defer func() {
		for _, p := range fn.Params {
			delete(interp.globals, p)
			delete(interp.arrays, p)
		}
		for k, v := range saved {
			interp.globals[k] = v
		}
		for k, v := range savedArrays {
			interp.arrays[k] = v
		}
	}()

	// Assign parameter values.
	for i, p := range fn.Params {
		if i < len(e.Args) {
			v, err := interp.eval(ctx, e.Args[i])
			if err != nil {
				return zeroValue, err
			}
			interp.globals[p] = v
		}
	}

	// Execute body.
	err := interp.execStmts(ctx, fn.Body)
	if err != nil {
		if rs, ok := err.(returnSignal); ok {
			return rs.val, nil
		}
		return zeroValue, err
	}
	return zeroValue, nil
}

func (interp *Interpreter) callBuiltin(ctx context.Context, e *CallExpr) (value, error, bool) {
	evalArgs := func() ([]value, error) {
		vals := make([]value, len(e.Args))
		for i, a := range e.Args {
			v, err := interp.eval(ctx, a)
			if err != nil {
				return nil, err
			}
			vals[i] = v
		}
		return vals, nil
	}

	switch e.Name {
	case "length":
		if len(e.Args) == 0 {
			return numVal(float64(len(interp.record))), nil, true
		}
		// Check if argument is an array name.
		if ve, ok := e.Args[0].(*VarExpr); ok {
			if arr, ok := interp.arrays[ve.Name]; ok {
				return numVal(float64(len(arr))), nil, true
			}
		}
		args, err := evalArgs()
		if err != nil {
			return zeroValue, err, true
		}
		return numVal(float64(utf8.RuneCountInString(args[0].toStr()))), nil, true

	case "substr":
		args, err := evalArgs()
		if err != nil {
			return zeroValue, err, true
		}
		if len(args) < 2 {
			return zeroValue, &RuntimeError{msg: "substr: too few arguments", exitCode: 1}, true
		}
		s := args[0].toStr()
		start := int(args[1].toNum()) - 1
		runes := []rune(s)
		if start < 0 {
			start = 0
		}
		if start >= len(runes) {
			return strVal(""), nil, true
		}
		if len(args) >= 3 {
			length := int(args[2].toNum())
			if length < 0 {
				length = 0
			}
			end := start + length
			if end > len(runes) {
				end = len(runes)
			}
			return strVal(string(runes[start:end])), nil, true
		}
		return strVal(string(runes[start:])), nil, true

	case "index":
		args, err := evalArgs()
		if err != nil {
			return zeroValue, err, true
		}
		if len(args) < 2 {
			return zeroValue, &RuntimeError{msg: "index: too few arguments", exitCode: 1}, true
		}
		idx := strings.Index(args[0].toStr(), args[1].toStr())
		return numVal(float64(idx + 1)), nil, true

	case "split":
		if len(e.Args) < 2 {
			return zeroValue, &RuntimeError{msg: "split: too few arguments", exitCode: 1}, true
		}
		strArg, err := interp.eval(ctx, e.Args[0])
		if err != nil {
			return zeroValue, err, true
		}
		arrayExpr, ok := e.Args[1].(*VarExpr)
		if !ok {
			return zeroValue, &RuntimeError{msg: "split: second argument must be an array", exitCode: 1}, true
		}
		var fs string
		if len(e.Args) >= 3 {
			fsVal, err := interp.eval(ctx, e.Args[2])
			if err != nil {
				return zeroValue, err, true
			}
			fs = fsVal.toStr()
		} else {
			fs = interp.globals["FS"].toStr()
		}
		parts := splitFields(strArg.toStr(), fs)
		arr := make(map[string]value)
		for i, p := range parts {
			if i >= MaxArraySize {
				break
			}
			arr[strconv.Itoa(i+1)] = strVal(p)
		}
		interp.arrays[arrayExpr.Name] = arr
		return numVal(float64(len(parts))), nil, true

	case "sub":
		return interp.callSub(ctx, e, false)
	case "gsub":
		return interp.callSub(ctx, e, true)

	case "match":
		if len(e.Args) < 2 {
			return zeroValue, &RuntimeError{msg: "match: too few arguments", exitCode: 1}, true
		}
		strArg, err := interp.eval(ctx, e.Args[0])
		if err != nil {
			return zeroValue, err, true
		}
		pattern := interp.regexPattern(ctx, e.Args[1])
		re, err := interp.compileRegex(pattern)
		if err != nil {
			return zeroValue, &RuntimeError{msg: fmt.Sprintf("match: %s", err), exitCode: 1}, true
		}
		loc := re.FindStringIndex(strArg.toStr())
		if loc == nil {
			interp.globals["RSTART"] = numVal(0)
			interp.globals["RLENGTH"] = numVal(-1)
			return numVal(0), nil, true
		}
		interp.globals["RSTART"] = numVal(float64(loc[0] + 1))
		interp.globals["RLENGTH"] = numVal(float64(loc[1] - loc[0]))
		return numVal(float64(loc[0] + 1)), nil, true

	case "sprintf":
		args, err := evalArgs()
		if err != nil {
			return zeroValue, err, true
		}
		return strVal(awkSprintf(args)), nil, true

	case "tolower":
		args, err := evalArgs()
		if err != nil {
			return zeroValue, err, true
		}
		if len(args) < 1 {
			return strVal(""), nil, true
		}
		return strVal(strings.ToLower(args[0].toStr())), nil, true

	case "toupper":
		args, err := evalArgs()
		if err != nil {
			return zeroValue, err, true
		}
		if len(args) < 1 {
			return strVal(""), nil, true
		}
		return strVal(strings.ToUpper(args[0].toStr())), nil, true

	case "int":
		args, err := evalArgs()
		if err != nil {
			return zeroValue, err, true
		}
		if len(args) < 1 {
			return numVal(0), nil, true
		}
		return numVal(float64(int64(args[0].toNum()))), nil, true

	case "sqrt":
		args, err := evalArgs()
		if err != nil {
			return zeroValue, err, true
		}
		if len(args) < 1 {
			return numVal(0), nil, true
		}
		return numVal(math.Sqrt(args[0].toNum())), nil, true

	case "sin":
		args, err := evalArgs()
		if err != nil {
			return zeroValue, err, true
		}
		if len(args) < 1 {
			return numVal(0), nil, true
		}
		return numVal(math.Sin(args[0].toNum())), nil, true

	case "cos":
		args, err := evalArgs()
		if err != nil {
			return zeroValue, err, true
		}
		if len(args) < 1 {
			return numVal(1), nil, true
		}
		return numVal(math.Cos(args[0].toNum())), nil, true

	case "atan2":
		args, err := evalArgs()
		if err != nil {
			return zeroValue, err, true
		}
		if len(args) < 2 {
			return numVal(0), nil, true
		}
		return numVal(math.Atan2(args[0].toNum(), args[1].toNum())), nil, true

	case "exp":
		args, err := evalArgs()
		if err != nil {
			return zeroValue, err, true
		}
		if len(args) < 1 {
			return numVal(1), nil, true
		}
		return numVal(math.Exp(args[0].toNum())), nil, true

	case "log":
		args, err := evalArgs()
		if err != nil {
			return zeroValue, err, true
		}
		if len(args) < 1 {
			return numVal(math.Inf(-1)), nil, true
		}
		return numVal(math.Log(args[0].toNum())), nil, true

	case "rand":
		return numVal(interp.rng.Float64()), nil, true

	case "srand":
		args, err := evalArgs()
		if err != nil {
			return zeroValue, err, true
		}
		var seed int64
		if len(args) > 0 {
			seed = int64(args[0].toNum())
		} else {
			seed = 1
		}
		interp.rng = rand.New(rand.NewSource(seed))
		return numVal(float64(seed)), nil, true

	case "system":
		return zeroValue, &RuntimeError{
			msg:      "system() is not supported (blocked for safety)",
			exitCode: 1,
		}, true

	case "close":
		return zeroValue, &RuntimeError{
			msg:      "close() is not supported (no I/O redirection)",
			exitCode: 1,
		}, true
	}
	return zeroValue, nil, false
}

func (interp *Interpreter) callSub(ctx context.Context, e *CallExpr, global bool) (value, error, bool) {
	if len(e.Args) < 2 {
		name := "sub"
		if global {
			name = "gsub"
		}
		return zeroValue, &RuntimeError{msg: fmt.Sprintf("%s: too few arguments", name), exitCode: 1}, true
	}
	pattern := interp.regexPattern(ctx, e.Args[0])
	replVal, err := interp.eval(ctx, e.Args[1])
	if err != nil {
		return zeroValue, err, true
	}
	var target Expr
	if len(e.Args) >= 3 {
		target = e.Args[2]
	} else {
		target = &FieldExpr{Index: &NumberLit{Val: "0"}}
	}
	origVal, err := interp.eval(ctx, target)
	if err != nil {
		return zeroValue, err, true
	}

	re, err := interp.compileRegex(pattern)
	if err != nil {
		return zeroValue, &RuntimeError{msg: fmt.Sprintf("sub: %s", err), exitCode: 1}, true
	}

	orig := origVal.toStr()
	repl := replVal.toStr()
	// In awk, & in replacement means matched text.
	count := 0
	var result string
	if global {
		result = re.ReplaceAllStringFunc(orig, func(m string) string {
			count++
			return expandReplacement(repl, m)
		})
	} else {
		replaced := false
		result = re.ReplaceAllStringFunc(orig, func(m string) string {
			if replaced {
				return m
			}
			replaced = true
			count++
			return expandReplacement(repl, m)
		})
	}

	interp.assignTo(ctx, target, strVal(result))
	return numVal(float64(count)), nil, true
}

func expandReplacement(repl, matched string) string {
	var sb strings.Builder
	for i := 0; i < len(repl); i++ {
		if repl[i] == '\\' && i+1 < len(repl) {
			if repl[i+1] == '&' {
				sb.WriteByte('&')
				i++
				continue
			}
			if repl[i+1] == '\\' {
				sb.WriteByte('\\')
				i++
				continue
			}
		}
		if repl[i] == '&' {
			sb.WriteString(matched)
			continue
		}
		sb.WriteByte(repl[i])
	}
	return sb.String()
}

func (interp *Interpreter) buildArrayKey(ctx context.Context, indices []Expr) (string, error) {
	if len(indices) == 1 {
		v, err := interp.eval(ctx, indices[0])
		if err != nil {
			return "", err
		}
		return v.toStr(), nil
	}
	subsep := interp.globals["SUBSEP"].toStr()
	var parts []string
	for _, idx := range indices {
		v, err := interp.eval(ctx, idx)
		if err != nil {
			return "", err
		}
		parts = append(parts, v.toStr())
	}
	return strings.Join(parts, subsep), nil
}

// regexPattern extracts a regex pattern string from an expression.
// If the expression is a RegexLit, returns its pattern directly.
// Otherwise, evaluates the expression and returns its string value.
func (interp *Interpreter) regexPattern(ctx context.Context, expr Expr) string {
	if re, ok := expr.(*RegexLit); ok {
		return re.Val
	}
	v, _ := interp.eval(ctx, expr)
	return v.toStr()
}

func (interp *Interpreter) compileRegex(pattern string) (*regexp.Regexp, error) {
	if re, ok := interp.regexCache[pattern]; ok {
		return re, nil
	}
	if len(interp.regexCache) >= MaxRegexCache {
		// Evict all to prevent unbounded growth.
		interp.regexCache = make(map[string]*regexp.Regexp)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid regex %q: %s", pattern, err)
	}
	interp.regexCache[pattern] = re
	return re, nil
}

func (interp *Interpreter) matchRegex(pattern, s string) (bool, error) {
	re, err := interp.compileRegex(pattern)
	if err != nil {
		return false, &RuntimeError{msg: err.Error(), exitCode: 1}
	}
	return re.MatchString(s), nil
}

func (interp *Interpreter) writeOut(s string) {
	if interp.outBytes+len(s) > MaxOutputBytes {
		remaining := MaxOutputBytes - interp.outBytes
		if remaining > 0 {
			_, _ = io.WriteString(interp.stdout, s[:remaining])
			interp.outBytes = MaxOutputBytes
		}
		return
	}
	_, _ = io.WriteString(interp.stdout, s)
	interp.outBytes += len(s)
}

// awkSprintf implements awk's sprintf formatting.
func awkSprintf(args []value) string {
	if len(args) == 0 {
		return ""
	}
	format := args[0].toStr()
	argIdx := 1
	var sb strings.Builder
	i := 0
	for i < len(format) {
		if format[i] == '%' {
			i++
			if i >= len(format) {
				sb.WriteByte('%')
				break
			}
			if format[i] == '%' {
				sb.WriteByte('%')
				i++
				continue
			}
			// Parse format specifier.
			specStart := i - 1
			// Flags.
			for i < len(format) && (format[i] == '-' || format[i] == '+' || format[i] == ' ' || format[i] == '0' || format[i] == '#') {
				i++
			}
			// Width (capped to prevent memory exhaustion).
			widthStart := i
			if i < len(format) && format[i] == '*' {
				i++
			} else {
				for i < len(format) && format[i] >= '0' && format[i] <= '9' {
					i++
				}
			}
			if w, err := strconv.Atoi(format[widthStart:i]); err == nil && w > MaxSprintfWidth {
				i = widthStart
				for i < len(format) && format[i] >= '0' && format[i] <= '9' {
					i++
				}
			}
			// Precision (capped to prevent memory exhaustion).
			if i < len(format) && format[i] == '.' {
				i++
				precStart := i
				if i < len(format) && format[i] == '*' {
					i++
				} else {
					for i < len(format) && format[i] >= '0' && format[i] <= '9' {
						i++
					}
				}
				if p, err := strconv.Atoi(format[precStart:i]); err == nil && p > MaxSprintfWidth {
					_ = p
				}
			}
			if i >= len(format) {
				sb.WriteString(format[specStart:])
				break
			}
			conv := format[i]
			i++
			rawSpec := format[specStart:i]
			spec := clampFormatSpec(rawSpec, MaxSprintfWidth)

			var arg value
			if argIdx < len(args) {
				arg = args[argIdx]
				argIdx++
			}
			switch conv {
			case 'd', 'i':
				sb.WriteString(fmt.Sprintf(strings.Replace(spec, string(conv), "d", 1), int64(arg.toNum())))
			case 'o', 'x', 'X':
				n := int64(arg.toNum())
				if n < 0 {
					n = 0
				}
				sb.WriteString(fmt.Sprintf(spec, uint64(n)))
			case 'f', 'e', 'E', 'g', 'G':
				sb.WriteString(fmt.Sprintf(spec, arg.toNum()))
			case 's':
				sb.WriteString(fmt.Sprintf(spec, arg.toStr()))
			case 'c':
				n := int(arg.toNum())
				if n > 0 && n < 128 {
					sb.WriteByte(byte(n))
				} else {
					s := arg.toStr()
					if s != "" {
						sb.WriteByte(s[0])
					}
				}
			default:
				sb.WriteString(spec)
			}
		} else if format[i] == '\\' && i+1 < len(format) {
			i++
			switch format[i] {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '\\':
				sb.WriteByte('\\')
			case '"':
				sb.WriteByte('"')
			default:
				sb.WriteByte('\\')
				sb.WriteByte(format[i])
			}
			i++
		} else {
			sb.WriteByte(format[i])
			i++
		}
	}
	return sb.String()
}

// clampFormatSpec rewrites a printf format specifier to cap width and
// precision at maxW, preventing memory exhaustion from specs like "%999999999s".
func clampFormatSpec(spec string, maxW int) string {
	if len(spec) < 2 {
		return spec
	}
	maxStr := strconv.Itoa(maxW)
	var result strings.Builder
	i := 0
	result.WriteByte(spec[i])
	i++
	for i < len(spec) && (spec[i] == '-' || spec[i] == '+' || spec[i] == ' ' || spec[i] == '0' || spec[i] == '#') {
		result.WriteByte(spec[i])
		i++
	}
	if i < len(spec) && spec[i] == '*' {
		result.WriteByte(spec[i])
		i++
	} else {
		start := i
		for i < len(spec) && spec[i] >= '0' && spec[i] <= '9' {
			i++
		}
		if w, err := strconv.Atoi(spec[start:i]); err == nil && w > maxW {
			result.WriteString(maxStr)
		} else {
			result.WriteString(spec[start:i])
		}
	}
	if i < len(spec) && spec[i] == '.' {
		result.WriteByte('.')
		i++
		if i < len(spec) && spec[i] == '*' {
			result.WriteByte(spec[i])
			i++
		} else {
			start := i
			for i < len(spec) && spec[i] >= '0' && spec[i] <= '9' {
				i++
			}
			if p, err := strconv.Atoi(spec[start:i]); err == nil && p > maxW {
				result.WriteString(maxStr)
			} else {
				result.WriteString(spec[start:i])
			}
		}
	}
	for i < len(spec) {
		result.WriteByte(spec[i])
		i++
	}
	return result.String()
}

// splitFields splits a string by the given field separator, following awk semantics.
func splitFields(s, fs string) []string {
	if fs == " " {
		return strings.Fields(s)
	}
	if fs == "" {
		var parts []string
		for _, r := range s {
			parts = append(parts, string(r))
		}
		return parts
	}
	if len(fs) == 1 {
		return strings.Split(s, fs)
	}
	re, err := regexp.Compile(fs)
	if err != nil {
		return []string{s}
	}
	return re.Split(s, -1)
}
