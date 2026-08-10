// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var errNextRecord = errors.New("next record")
var errBreakLoop = errors.New("break loop")
var errContinueLoop = errors.New("continue loop")

type exitError struct {
	code int
}

func (e *exitError) Error() string {
	return "exit"
}

type returnError struct {
	value value
}

func (e *returnError) Error() string {
	return "return"
}

func (rt *runtime) execStatementsWithFuture(ctx context.Context, stmts []stmt, future []stmt) error {
	prevCtx := rt.ctx
	rt.ctx = ctx
	defer func() { rt.ctx = prevCtx }()
	prevFuture := rt.futureStmts
	defer func() { rt.futureStmts = prevFuture }()
	for i, st := range stmts {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := stmtFuture(stmts[i+1:], future)
		rt.futureStmts = remaining
		switch s := st.(type) {
		case *printStmt:
			vals := make([]value, 0, len(s.args))
			if len(s.args) == 0 {
				vals = append(vals, rt.field(0))
			} else {
				for _, arg := range s.args {
					v, err := rt.eval(arg)
					if err != nil {
						return err
					}
					vals = append(vals, v)
				}
			}
			out, err := rt.formatPrintValues(vals)
			if err != nil {
				return err
			}
			if err := rt.writeOutput(ctx, s.pipe, out, remaining); err != nil {
				return err
			}
		case *printfStmt:
			if len(s.args) == 0 {
				return fmt.Errorf("printf requires a format expression")
			}
			vals := make([]value, 0, len(s.args))
			for _, arg := range s.args {
				v, err := rt.eval(arg)
				if err != nil {
					return err
				}
				vals = append(vals, v)
			}
			out, err := formatPrintf(vals[0].String(), vals[1:])
			if err != nil {
				return err
			}
			if err := rt.writeOutput(ctx, s.pipe, out, remaining); err != nil {
				return err
			}
		case *ifStmt:
			cond, err := rt.eval(s.cond)
			if err != nil {
				return err
			}
			if cond.Bool() {
				if err := rt.execStatementsWithFuture(ctx, s.thenStmts, remaining); err != nil {
					return err
				}
			} else if len(s.elseStmts) > 0 {
				if err := rt.execStatementsWithFuture(ctx, s.elseStmts, remaining); err != nil {
					return err
				}
			}
		case *forInStmt:
			keys, err := rt.arrayKeys(s.arrayName)
			if err != nil {
				return err
			}
			for _, key := range keys {
				if err := rt.setVar(s.varName, stringValue(key)); err != nil {
					return err
				}
				if err := rt.execStatementsWithFuture(ctx, s.body, stmtFuture(s.body, remaining)); err != nil {
					if errors.Is(err, errBreakLoop) {
						break
					}
					if errors.Is(err, errContinueLoop) {
						continue
					}
					return err
				}
			}
		case *forStmt:
			if err := rt.execFor(ctx, s, remaining); err != nil {
				return err
			}
		case *whileStmt:
			if err := rt.execWhile(ctx, s, remaining); err != nil {
				return err
			}
		case *nextStmt:
			return errNextRecord
		case *exitStmt:
			code := rt.exitCode
			if s.status != nil {
				status, err := rt.eval(s.status)
				if err != nil {
					return err
				}
				code = int(status.Number())
			}
			rt.exitCode = code
			return &exitError{code: code}
		case *returnStmt:
			if s.value == nil {
				return &returnError{value: unassignedValue()}
			}
			v, err := rt.eval(s.value)
			if err != nil {
				return err
			}
			return &returnError{value: v}
		case *breakStmt:
			return errBreakLoop
		case *continueStmt:
			return errContinueLoop
		case *deleteStmt:
			if s.all {
				if err := rt.deleteArray(s.name); err != nil {
					return err
				}
				continue
			}
			key, err := rt.evalArrayKey(s.indices)
			if err != nil {
				return err
			}
			if err := rt.deleteArrayElem(s.name, key); err != nil {
				return err
			}
		case *exprStmt:
			if _, err := rt.eval(s.x); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown statement")
		}
	}
	return nil
}

func stmtFuture(remaining, future []stmt) []stmt {
	if len(remaining) == 0 {
		return future
	}
	if len(future) == 0 {
		return remaining
	}
	out := make([]stmt, 0, len(remaining)+len(future))
	out = append(out, remaining...)
	out = append(out, future...)
	return out
}

func (rt *runtime) execFor(ctx context.Context, s *forStmt, future []stmt) error {
	if s.init != nil {
		if _, err := rt.eval(s.init); err != nil {
			return err
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.cond != nil {
			cond, err := rt.eval(s.cond)
			if err != nil {
				return err
			}
			if !cond.Bool() {
				return nil
			}
		}
		err := rt.execStatementsWithFuture(ctx, s.body, stmtFuture(s.body, future))
		if errors.Is(err, errBreakLoop) {
			return nil
		}
		if err != nil && !errors.Is(err, errContinueLoop) {
			return err
		}
		if s.post != nil {
			if _, postErr := rt.eval(s.post); postErr != nil {
				return postErr
			}
		}
	}
}

func (rt *runtime) execWhile(ctx context.Context, s *whileStmt, future []stmt) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		cond, err := rt.eval(s.cond)
		if err != nil {
			return err
		}
		if !cond.Bool() {
			return nil
		}
		err = rt.execStatementsWithFuture(ctx, s.body, stmtFuture(s.body, future))
		if errors.Is(err, errBreakLoop) {
			return nil
		}
		if errors.Is(err, errContinueLoop) {
			continue
		}
		if err != nil {
			return err
		}
	}
}

func substrStart(n float64, length int) int {
	if n <= 1 || math.IsNaN(n) {
		return 0
	}
	if n > float64(length) {
		return length
	}
	return int(n) - 1
}

func substrEnd(start, length int, count float64) int {
	if count <= 0 || math.IsNaN(count) {
		return start
	}
	if count >= float64(length-start) {
		return length
	}
	return start + int(count)
}

func (rt *runtime) formatPrintValues(vals []value) (string, error) {
	var b strings.Builder
	ofs := rt.getVar("OFS").String()
	for i, v := range vals {
		if i > 0 {
			if err := appendPrintString(&b, ofs); err != nil {
				return "", err
			}
		}
		if err := appendPrintString(&b, v.String()); err != nil {
			return "", err
		}
	}
	if err := appendPrintString(&b, rt.getVar("ORS").String()); err != nil {
		return "", err
	}
	return b.String(), nil
}

func appendPrintString(b *strings.Builder, s string) error {
	if len(s) > MaxVariableBytes-b.Len() {
		return fmt.Errorf("print output exceeds %d bytes", MaxVariableBytes)
	}
	b.WriteString(s)
	return nil
}

func (rt *runtime) writeOutput(ctx context.Context, pipe expr, out string, remaining []stmt) error {
	if pipe == nil {
		return rt.writeStdoutString(ctx, out, remaining)
	}
	return rt.writeCommandPipe(ctx, pipe, out)
}

func (rt *runtime) eval(x expr) (value, error) {
	if rt.ctx != nil {
		if err := rt.ctx.Err(); err != nil {
			return value{}, err
		}
	}
	switch e := x.(type) {
	case *numberExpr:
		return numberValue(e.num), nil
	case *stringExpr:
		return stringValue(e.value), nil
	case *regexExpr:
		re, err := rt.compileRegex(e.pattern)
		if err != nil {
			return value{}, err
		}
		return boolValue(re.MatchString(rt.record)), nil
	case *varExpr:
		if rt.isArray(e.name) || isBuiltinArrayName(e.name) {
			return value{}, fmt.Errorf("cannot use array %s as scalar", e.name)
		}
		return rt.getVar(e.name), nil
	case *arrayRefExpr:
		return rt.evalArrayRef(e)
	case *compositeExpr:
		key, err := rt.evalArrayKey(e.parts)
		if err != nil {
			return value{}, err
		}
		return stringValue(key), nil
	case *fieldExpr:
		v, err := rt.eval(e.index)
		if err != nil {
			return value{}, err
		}
		n := int(v.Number())
		if n < 0 {
			return value{}, fmt.Errorf("invalid field index")
		}
		return rt.field(n), nil
	case *groupedExpr:
		return rt.eval(e.x)
	case *unaryExpr:
		v, err := rt.eval(e.x)
		if err != nil {
			return value{}, err
		}
		switch e.op {
		case "+":
			return numberValue(v.Number()), nil
		case "-":
			return numberValue(-v.Number()), nil
		case "!":
			if v.Bool() {
				return numberValue(0), nil
			}
			return numberValue(1), nil
		default:
			return value{}, fmt.Errorf("unknown unary operator %s", e.op)
		}
	case *binaryExpr:
		return rt.evalBinary(e)
	case *ternaryExpr:
		cond, err := rt.eval(e.cond)
		if err != nil {
			return value{}, err
		}
		if cond.Bool() {
			return rt.eval(e.then)
		}
		return rt.eval(e.els)
	case *assignExpr:
		return rt.evalAssign(e)
	case *incDecExpr:
		return rt.evalIncDec(e)
	case *getlineExpr:
		return rt.evalGetline(e)
	case *callExpr:
		return rt.evalCall(e)
	default:
		return value{}, fmt.Errorf("unknown expression")
	}
}

func (rt *runtime) evalCall(e *callExpr) (value, error) {
	if fn, ok := rt.prog.functions[e.name]; ok {
		return rt.evalUserFunction(fn, e.args)
	}
	if e.name == "split" {
		return rt.evalSplit(e)
	}
	if e.name == "sub" || e.name == "gsub" {
		return rt.evalSubstitution(e)
	}
	if e.name == "match" {
		return rt.evalMatch(e)
	}
	if e.name == "gensub" {
		return rt.evalGensub(e)
	}
	if e.name == "length" {
		return rt.evalLength(e)
	}
	if e.name == "close" {
		return rt.evalClose(e)
	}
	if e.name == "asorti" {
		return rt.evalAsorti(e)
	}
	args := make([]value, 0, len(e.args))
	for _, arg := range e.args {
		v, err := rt.eval(arg)
		if err != nil {
			return value{}, err
		}
		args = append(args, v)
	}
	switch e.name {
	case "substr":
		s := args[0].String()
		length := runeLen(s)
		start := substrStart(args[1].Number(), length)
		if start >= length {
			return stringValue(""), nil
		}
		end := length
		if len(args) == 3 {
			end = substrEnd(start, length, args[2].Number())
		}
		startByte, endByte := 0, len(s)
		runeIndex := 0
		for byteIndex := range s {
			if runeIndex == start {
				startByte = byteIndex
			}
			if runeIndex == end {
				endByte = byteIndex
				break
			}
			runeIndex++
		}
		return stringValue(s[startByte:endByte]), nil
	case "index":
		pos, err := rt.indexString(args[0].String(), args[1].String())
		if err != nil {
			return value{}, err
		}
		return numberValue(float64(pos)), nil
	case "tolower":
		s := args[0].String()
		return stringValue(strings.ToLower(s)), nil
	case "toupper":
		s := args[0].String()
		return stringValue(strings.ToUpper(s)), nil
	case "int":
		v := args[0]
		return numberValue(math.Trunc(v.Number())), nil
	case "strtonum":
		return numberValue(parseAwkNumberLiteral(args[0].String())), nil
	case "sprintf":
		out, err := formatPrintf(args[0].String(), args[1:])
		if err != nil {
			return value{}, err
		}
		return stringValue(out), nil
	default:
		if _, ok := unsupportedBuiltinFunctions[e.name]; ok {
			return value{}, fmt.Errorf("function calls are not supported")
		}
		return value{}, fmt.Errorf("function %q not defined", e.name)
	}
}

func (rt *runtime) evalClose(e *callExpr) (value, error) {
	command, err := rt.eval(e.args[0])
	if err != nil {
		return value{}, err
	}
	status, ok, err := rt.closeCommandPipe(rt.ctx, command.String(), true)
	if err != nil {
		return value{}, err
	}
	if ok {
		return numberValue(float64(status)), nil
	}
	status, ok, err = rt.closeCommandInput(command.String())
	if err != nil {
		return value{}, err
	}
	if ok {
		return numberValue(float64(status)), nil
	}
	if status, ok := rt.closeInputFile(command.String()); ok {
		return numberValue(float64(status)), nil
	}
	rt.setErrnoString("close of redirection that was never opened")
	return numberValue(-1), nil
}

func (rt *runtime) evalGetline(e *getlineExpr) (value, error) {
	var target assignTarget
	hasTarget := e.target != nil
	if hasTarget {
		resolved, _, err := rt.resolveAssignable(e.target)
		if err != nil {
			return value{}, err
		}
		target = resolved
	}

	rec, status, err := rt.readGetlineRecord(e)
	if err != nil {
		return value{}, err
	}
	if status != 1 {
		return numberValue(float64(status)), nil
	}
	if hasTarget {
		if err := rt.setResolvedAssignable(target, inputStringValue(rec)); err != nil {
			return value{}, err
		}
		return numberValue(1), nil
	}
	if err := rt.setRecord(rec); err != nil {
		return value{}, err
	}
	return numberValue(1), nil
}

func (rt *runtime) readGetlineRecord(e *getlineExpr) (string, int, error) {
	switch e.kind {
	case getlineMain:
		rec, ok, err := rt.readMainRecord(rt.ctx)
		if err != nil {
			return "", 0, err
		}
		if !ok {
			return "", 0, nil
		}
		return rec, 1, nil
	case getlineFile:
		source, err := rt.eval(e.source)
		if err != nil {
			return "", 0, err
		}
		return rt.getlineFileRecord(rt.ctx, source.String())
	case getlineCommand:
		source, err := rt.eval(e.source)
		if err != nil {
			return "", 0, err
		}
		return rt.getlineCommandRecord(rt.ctx, source.String())
	default:
		return "", 0, fmt.Errorf("unknown getline source")
	}
}

func (rt *runtime) evalLength(e *callExpr) (value, error) {
	if len(e.args) == 0 {
		return numberValue(float64(len([]rune(rt.field(0).String())))), nil
	}
	if arg, ok := e.args[0].(*varExpr); ok {
		rt.ensureBuiltinArray(arg.name)
		if rt.isArray(arg.name) {
			keys, err := rt.arrayKeys(arg.name)
			if err != nil {
				return value{}, err
			}
			return numberValue(float64(len(keys))), nil
		}
	}
	v, err := rt.eval(e.args[0])
	if err != nil {
		return value{}, err
	}
	return numberValue(float64(len([]rune(v.String())))), nil
}

type functionArg struct {
	value           value
	valueSet        bool
	arrayAlias      *localVar
	globalArrayName string
}

func (rt *runtime) evalUserFunction(fn *functionDef, args []expr) (value, error) {
	if len(args) > len(fn.params) {
		return value{}, fmt.Errorf("function %q called with too many arguments", fn.name)
	}
	if rt.functionDepth >= maxFunctionDepth {
		return value{}, fmt.Errorf("function call depth limit exceeded (maximum %d)", maxFunctionDepth)
	}
	rt.functionDepth++
	defer func() {
		rt.functionDepth--
	}()
	callArgs := make([]functionArg, len(args))
	for i, arg := range args {
		v, err := rt.evalFunctionArg(arg)
		if err != nil {
			return value{}, err
		}
		callArgs[i] = v
	}
	frame := callFrame{locals: make(map[string]*localVar, len(fn.params))}
	for _, param := range fn.params {
		frame.locals[param] = &localVar{}
	}
	globalAliases := make(map[string]*localVar)
	rt.frames = append(rt.frames, frame)
	defer rt.popFrame()
	for i, arg := range callArgs {
		local := rt.lookupLocal(fn.params[i])
		local.arrayAlias = arg.arrayAlias
		if arg.globalArrayName != "" {
			alias := globalAliases[arg.globalArrayName]
			if alias == nil {
				alias = &localVar{globalArrayName: arg.globalArrayName}
				globalAliases[arg.globalArrayName] = alias
			}
			local.arrayAlias = alias
		}
		if arg.valueSet {
			if err := rt.setLocalScalar(local, arg.value); err != nil {
				return value{}, err
			}
		}
	}
	if rt.ctx == nil {
		return value{}, fmt.Errorf("missing evaluation context")
	}
	err := rt.execStatementsWithFuture(rt.ctx, fn.body, rt.futureStmts)
	if ret, ok := err.(*returnError); ok {
		return ret.value, nil
	}
	if err != nil {
		return value{}, err
	}
	return unassignedValue(), nil
}

func (rt *runtime) evalFunctionArg(arg expr) (functionArg, error) {
	if v, ok := arg.(*varExpr); ok {
		return rt.evalVariableFunctionArg(v.name)
	}
	value, err := rt.eval(arg)
	if err != nil {
		return functionArg{}, err
	}
	return functionArg{value: value, valueSet: true}, nil
}

func (rt *runtime) evalVariableFunctionArg(name string) (functionArg, error) {
	if local := rt.lookupLocal(name); local != nil {
		arg := functionArg{}
		if local.valueSet {
			arg.value = local.value
			arg.valueSet = true
		}
		root := rootLocalVar(local)
		if rt.localIsArray(root) || !local.valueSet {
			arg.arrayAlias = root
		}
		return arg, nil
	}
	if rt.isGlobalArray(name) {
		return functionArg{globalArrayName: name}, nil
	}
	if v, ok := rt.vars[name]; ok {
		return functionArg{value: v, valueSet: true}, nil
	}
	if isBuiltinArrayName(name) {
		return functionArg{globalArrayName: name}, nil
	}
	if isBuiltinScalarName(name) {
		return functionArg{value: rt.getVar(name), valueSet: true}, nil
	}
	return functionArg{globalArrayName: name}, nil
}

func (rt *runtime) popFrame() {
	frame := rt.frames[len(rt.frames)-1]
	rt.frames = rt.frames[:len(rt.frames)-1]
	for _, local := range frame.locals {
		rt.varBytes -= local.valueSize
		if local.arrayAlias != nil || local.globalArrayName != "" {
			continue
		}
		for _, size := range local.arraySizes {
			rt.varBytes -= size
		}
	}
	if rt.varBytes < 0 {
		rt.varBytes = 0
	}
}

func (rt *runtime) evalSubstitution(e *callExpr) (value, error) {
	re, err := rt.compileRegexArg(e.args[0])
	if err != nil {
		return value{}, err
	}
	repl, err := rt.eval(e.args[1])
	if err != nil {
		return value{}, err
	}
	var target assignTarget
	var current value
	if len(e.args) == 3 {
		target, current, err = rt.resolveAssignable(e.args[2])
		if err != nil {
			return value{}, err
		}
	} else {
		target = assignTarget{field: true, fieldIndex: 0}
		current = rt.field(0)
	}
	next, count, err := substituteAwk(re, current.String(), repl.String(), e.name == "gsub")
	if err != nil {
		return value{}, err
	}
	if count == 0 {
		return numberValue(0), nil
	}
	if err := rt.setResolvedAssignable(target, stringValue(next)); err != nil {
		return value{}, err
	}
	return numberValue(float64(count)), nil
}

func (rt *runtime) evalMatch(e *callExpr) (value, error) {
	var captures *varExpr
	if len(e.args) == 3 {
		var ok bool
		captures, ok = e.args[2].(*varExpr)
		if !ok {
			return value{}, fmt.Errorf("match capture destination must be an array variable")
		}
	}
	input, err := rt.eval(e.args[0])
	if err != nil {
		return value{}, err
	}
	re, err := rt.compileRegexArg(e.args[1])
	if err != nil {
		return value{}, err
	}
	if captures != nil {
		if err := rt.deleteArray(captures.name); err != nil {
			return value{}, err
		}
	}
	text := input.String()
	match := re.FindStringRuneIndex(text)
	if match == nil {
		if err := rt.setVar("RSTART", numberValue(0)); err != nil {
			return value{}, err
		}
		if err := rt.setVar("RLENGTH", numberValue(-1)); err != nil {
			return value{}, err
		}
		return numberValue(0), nil
	}
	start := match[0] + 1
	length := match[1] - match[0]
	if err := rt.setVar("RSTART", numberValue(float64(start))); err != nil {
		return value{}, err
	}
	if err := rt.setVar("RLENGTH", numberValue(float64(length))); err != nil {
		return value{}, err
	}
	if captures != nil {
		if err := rt.setMatchCaptures(captures.name, text, re); err != nil {
			return value{}, err
		}
	}
	return numberValue(float64(start)), nil
}

func (rt *runtime) setMatchCaptures(name, text string, re *awkRegex) error {
	locs := re.FindStringSubmatchIndex(text)
	for i := 0; i+1 < len(locs); i += 2 {
		key := fmt.Sprintf("%d", i/2)
		value := ""
		if locs[i] >= 0 {
			value = text[locs[i]:locs[i+1]]
		}
		if err := rt.setArrayElem(name, key, inputStringValue(value)); err != nil {
			return err
		}
	}
	return nil
}

func (rt *runtime) evalGensub(e *callExpr) (value, error) {
	re, err := rt.compileRegexArg(e.args[0])
	if err != nil {
		return value{}, err
	}
	repl, err := rt.eval(e.args[1])
	if err != nil {
		return value{}, err
	}
	how, err := rt.eval(e.args[2])
	if err != nil {
		return value{}, err
	}
	target := rt.field(0)
	if len(e.args) == 4 {
		target, err = rt.eval(e.args[3])
		if err != nil {
			return value{}, err
		}
	}
	out, err := gensubAwk(re, target.String(), repl.String(), how)
	if err != nil {
		return value{}, err
	}
	return stringValue(out), nil
}

func (rt *runtime) evalAsorti(e *callExpr) (value, error) {
	source, ok := e.args[0].(*varExpr)
	if !ok {
		return value{}, fmt.Errorf("asorti source must be an array variable")
	}
	destName := source.name
	if len(e.args) == 2 {
		dest, ok := e.args[1].(*varExpr)
		if !ok {
			return value{}, fmt.Errorf("asorti destination must be an array variable")
		}
		destName = dest.name
	}
	keys, err := rt.arrayKeysSorted(source.name, rt.ignoreCase())
	if err != nil {
		return value{}, err
	}
	elems := make(map[string]value, len(keys))
	for i, key := range keys {
		elems[fmt.Sprintf("%d", i+1)] = inputStringValue(key)
	}
	if err := rt.replaceArray(destName, elems); err != nil {
		return value{}, err
	}
	return numberValue(float64(len(keys))), nil
}

func (rt *runtime) compileRegexArg(x expr) (*awkRegex, error) {
	if rx, ok := x.(*regexExpr); ok {
		return rt.compileRegex(rx.pattern)
	}
	v, err := rt.eval(x)
	if err != nil {
		return nil, err
	}
	return rt.compileRegex(v.String())
}

func substituteAwk(re *awkRegex, input, replacement string, all bool) (string, int, error) {
	var matches [][]int
	if all {
		matches = re.FindAllStringIndex(input, -1)
	} else if loc := re.FindStringIndex(input); loc != nil {
		matches = [][]int{loc}
	}
	if len(matches) == 0 {
		return input, 0, nil
	}

	var b strings.Builder
	last := 0
	for _, loc := range matches {
		start := loc[0]
		end := loc[1]
		if err := appendLimitedString(&b, input[last:start]); err != nil {
			return "", 0, err
		}
		if err := appendAwkReplacement(&b, replacement, input[start:end]); err != nil {
			return "", 0, err
		}
		last = end
	}
	if err := appendLimitedString(&b, input[last:]); err != nil {
		return "", 0, err
	}
	return b.String(), len(matches), nil
}

func gensubAwk(re *awkRegex, input, replacement string, how value) (string, error) {
	locs := re.FindAllStringSubmatchIndex(input, -1)
	if len(locs) == 0 {
		return input, nil
	}
	global := false
	nth := int(how.Number())
	howString := how.String()
	if hasLeadingG(howString) {
		global = true
		nth = 1
	}
	if nth < 1 {
		nth = 1
	}

	var b strings.Builder
	last := 0
	seen := 0
	for _, loc := range locs {
		if loc[0] == loc[1] && loc[0] == last && seen > 0 {
			continue
		}
		seen++
		replace := global || seen == nth
		if !replace {
			continue
		}
		if err := appendLimitedString(&b, input[last:loc[0]]); err != nil {
			return "", err
		}
		if err := appendGensubReplacement(&b, replacement, input, loc); err != nil {
			return "", err
		}
		last = loc[1]
		if !global {
			break
		}
	}
	if last == 0 && !(global || seen >= nth) {
		return input, nil
	}
	if err := appendLimitedString(&b, input[last:]); err != nil {
		return "", err
	}
	return b.String(), nil
}

func hasLeadingG(s string) bool {
	return len(s) > 0 && (s[0] == 'g' || s[0] == 'G')
}

func appendGensubReplacement(b *strings.Builder, replacement, input string, loc []int) error {
	for i := 0; i < len(replacement); i++ {
		switch replacement[i] {
		case '&':
			if err := appendSubmatch(b, input, loc, 0); err != nil {
				return err
			}
		case '\\':
			if i+1 >= len(replacement) {
				if err := appendLimitedString(b, `\`); err != nil {
					return err
				}
				continue
			}
			next := replacement[i+1]
			i++
			if next >= '0' && next <= '9' {
				if err := appendSubmatch(b, input, loc, int(next-'0')); err != nil {
					return err
				}
				continue
			}
			if err := appendLimitedString(b, string(next)); err != nil {
				return err
			}
		default:
			if err := appendLimitedString(b, replacement[i:i+1]); err != nil {
				return err
			}
		}
	}
	return nil
}

func appendSubmatch(b *strings.Builder, input string, loc []int, group int) error {
	i := group * 2
	if i+1 >= len(loc) || loc[i] < 0 {
		return nil
	}
	return appendLimitedString(b, input[loc[i]:loc[i+1]])
}

func appendAwkReplacement(b *strings.Builder, replacement, matched string) error {
	for i := 0; i < len(replacement); i++ {
		switch replacement[i] {
		case '&':
			if err := appendLimitedString(b, matched); err != nil {
				return err
			}
		case '\\':
			if i+1 >= len(replacement) {
				if err := appendLimitedString(b, `\`); err != nil {
					return err
				}
				continue
			}
			next := replacement[i+1]
			i++
			if next == '&' || next == '\\' {
				if err := appendLimitedString(b, string(next)); err != nil {
					return err
				}
				continue
			}
			if err := appendLimitedString(b, `\`+string(next)); err != nil {
				return err
			}
		default:
			if err := appendLimitedString(b, replacement[i:i+1]); err != nil {
				return err
			}
		}
	}
	return nil
}

func parseAwkNumberLiteral(s string) float64 {
	text := strings.TrimSpace(s)
	if text == "" {
		return 0
	}
	sign := 1.0
	if text[0] == '+' || text[0] == '-' {
		if text[0] == '-' {
			sign = -1
		}
		text = text[1:]
	}
	if len(text) > 2 && text[0] == '0' && (text[1] == 'x' || text[1] == 'X') {
		if n, ok := parseUnsignedBasePrefix(text[2:], 16); ok {
			return sign * float64(n)
		}
		return 0
	}
	if shouldParseAwkOctalPrefix(text) {
		if n, ok := parseUnsignedBasePrefix(text[1:], 8); ok {
			return sign * float64(n)
		}
		return 0
	}
	prefix := numericPrefix(text)
	if prefix == "" {
		return 0
	}
	if n, err := strconv.ParseFloat(prefix, 64); err == nil {
		return sign * n
	}
	return 0
}

func shouldParseAwkOctalPrefix(s string) bool {
	if len(s) <= 1 || s[0] != '0' || s[1] < '0' || s[1] > '7' {
		return false
	}
	for i := 1; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch >= '0' && ch <= '7':
			continue
		case ch == '.' || ch == 'e' || ch == 'E' || ch == '8' || ch == '9':
			return false
		default:
			return true
		}
	}
	return true
}

func parseUnsignedBasePrefix(s string, base int) (uint64, bool) {
	if s == "" {
		return 0, false
	}
	var n uint64
	for i := 0; i < len(s); i++ {
		digit, ok := digitValue(s[i])
		if !ok || digit >= base {
			return n, i > 0
		}
		n = n*uint64(base) + uint64(digit)
	}
	return n, true
}

func digitValue(ch byte) (int, bool) {
	switch {
	case ch >= '0' && ch <= '9':
		return int(ch - '0'), true
	case ch >= 'a' && ch <= 'f':
		return int(ch-'a') + 10, true
	case ch >= 'A' && ch <= 'F':
		return int(ch-'A') + 10, true
	default:
		return 0, false
	}
}

func appendLimitedString(b *strings.Builder, s string) error {
	if len(s) > MaxVariableBytes-b.Len() {
		return fmt.Errorf("replacement output exceeds %d bytes", MaxVariableBytes)
	}
	b.WriteString(s)
	return nil
}

func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

func (rt *runtime) evalSplit(e *callExpr) (value, error) {
	target, ok := e.args[1].(*varExpr)
	if !ok {
		return value{}, fmt.Errorf("split destination must be an array variable")
	}
	input, err := rt.eval(e.args[0])
	if err != nil {
		return value{}, err
	}
	sep := rt.getVar("FS").String()
	charSplit := false
	regexSplit := false
	if len(e.args) == 3 {
		if rx, ok := e.args[2].(*regexExpr); ok {
			sep = rx.pattern
			regexSplit = true
		} else {
			sepValue, err := rt.eval(e.args[2])
			if err != nil {
				return value{}, err
			}
			sep = sepValue.String()
			charSplit = sep == ""
		}
	}
	var parts []string
	if charSplit {
		parts, err = splitAwkChars(input.String())
	} else if regexSplit {
		parts, err = rt.splitAwkRegex(input.String(), sep)
	} else {
		parts, err = rt.splitAwkFields(input.String(), sep)
	}
	if err != nil {
		if errors.Is(err, errTooManyFields) {
			return value{}, fmt.Errorf("split result exceeds %d fields", MaxFields)
		}
		return value{}, err
	}
	if len(parts) > MaxFields {
		return value{}, fmt.Errorf("split result exceeds %d fields", MaxFields)
	}
	elems := make(map[string]value, len(parts))
	for i, part := range parts {
		if rt.ctx != nil && i%256 == 0 {
			if err := rt.ctx.Err(); err != nil {
				return value{}, err
			}
		}
		elems[fmt.Sprintf("%d", i+1)] = inputStringValue(part)
	}
	if err := rt.replaceArray(target.name, elems); err != nil {
		return value{}, err
	}
	return numberValue(float64(len(parts))), nil
}

func (rt *runtime) evalBinary(e *binaryExpr) (value, error) {
	if e.op == "&&" {
		left, err := rt.eval(e.left)
		if err != nil {
			return value{}, err
		}
		if !left.Bool() {
			return numberValue(0), nil
		}
		right, err := rt.eval(e.right)
		if err != nil {
			return value{}, err
		}
		if right.Bool() {
			return numberValue(1), nil
		}
		return numberValue(0), nil
	}
	if e.op == "||" {
		left, err := rt.eval(e.left)
		if err != nil {
			return value{}, err
		}
		if left.Bool() {
			return numberValue(1), nil
		}
		right, err := rt.eval(e.right)
		if err != nil {
			return value{}, err
		}
		if right.Bool() {
			return numberValue(1), nil
		}
		return numberValue(0), nil
	}
	left, err := rt.eval(e.left)
	if err != nil {
		return value{}, err
	}
	switch e.op {
	case "~", "!~":
		matched, err := rt.matchRegexExpr(left, e.right)
		if err != nil {
			return value{}, err
		}
		if e.op == "!~" {
			matched = !matched
		}
		return boolValue(matched), nil
	case "in":
		arrayName, ok := e.right.(*varExpr)
		if !ok {
			return value{}, fmt.Errorf("right side of in requires an array variable")
		}
		ok, err := rt.hasArrayElem(arrayName.name, left.String())
		if err != nil {
			return value{}, err
		}
		return boolValue(ok), nil
	}
	right, err := rt.eval(e.right)
	if err != nil {
		return value{}, err
	}
	switch e.op {
	case "concat":
		leftString, rightString := left.String(), right.String()
		if len(rightString) > MaxPipeBytes-len(leftString) {
			return value{}, fmt.Errorf("string expression exceeds %d bytes", MaxPipeBytes)
		}
		return stringValue(leftString + rightString), nil
	case "+":
		return numberValue(left.Number() + right.Number()), nil
	case "-":
		return numberValue(left.Number() - right.Number()), nil
	case "*":
		return numberValue(left.Number() * right.Number()), nil
	case "/":
		if right.Number() == 0 {
			return value{}, fmt.Errorf("division by zero attempted")
		}
		return numberValue(left.Number() / right.Number()), nil
	case "%":
		if right.Number() == 0 {
			return value{}, fmt.Errorf("division by zero attempted")
		}
		return numberValue(math.Mod(left.Number(), right.Number())), nil
	case "==", "!=", "<", "<=", ">", ">=":
		return boolValue(compareValues(left, right, e.op, rt.ignoreCase())), nil
	default:
		return value{}, fmt.Errorf("unknown binary operator %s", e.op)
	}
}

func (rt *runtime) matchRegexExpr(left value, rightExpr expr) (bool, error) {
	if rx, ok := rightExpr.(*regexExpr); ok {
		re, err := rt.compileRegex(rx.pattern)
		if err != nil {
			return false, err
		}
		return re.MatchString(left.String()), nil
	}
	right, err := rt.eval(rightExpr)
	if err != nil {
		return false, err
	}
	re, err := rt.compileRegex(right.String())
	if err != nil {
		return false, err
	}
	return re.MatchString(left.String()), nil
}

func (rt *runtime) evalAssign(e *assignExpr) (value, error) {
	target, left, err := rt.resolveAssignable(e.left)
	if err != nil {
		return value{}, err
	}
	right, err := rt.eval(e.right)
	if err != nil {
		return value{}, err
	}
	if e.op != "=" {
		left, err = rt.currentResolvedAssignable(target)
		if err != nil {
			return value{}, err
		}
		switch e.op {
		case "+=":
			right = numberValue(left.Number() + right.Number())
		case "-=":
			right = numberValue(left.Number() - right.Number())
		case "*=":
			right = numberValue(left.Number() * right.Number())
		case "/=":
			if right.Number() == 0 {
				return value{}, fmt.Errorf("division by zero attempted")
			}
			right = numberValue(left.Number() / right.Number())
		case "%=":
			if right.Number() == 0 {
				return value{}, fmt.Errorf("division by zero attempted")
			}
			right = numberValue(math.Mod(left.Number(), right.Number()))
		default:
			return value{}, fmt.Errorf("unknown assignment operator %s", e.op)
		}
	}
	if err := rt.setResolvedAssignable(target, right); err != nil {
		return value{}, err
	}
	return right, nil
}

func (rt *runtime) evalIncDec(e *incDecExpr) (value, error) {
	target, old, err := rt.resolveAssignable(e.x)
	if err != nil {
		return value{}, err
	}
	next := old.Number()
	if e.op == "++" {
		next++
	} else {
		next--
	}
	nv := numberValue(next)
	if err := rt.setResolvedAssignable(target, nv); err != nil {
		return value{}, err
	}
	if e.prefix {
		return nv, nil
	}
	return old, nil
}

type assignTarget struct {
	name       string
	key        string
	array      bool
	field      bool
	fieldIndex int
}

func (rt *runtime) resolveAssignable(x expr) (assignTarget, value, error) {
	switch v := x.(type) {
	case *varExpr:
		if rt.isArray(v.name) {
			return assignTarget{}, value{}, fmt.Errorf("cannot use array %s as scalar", v.name)
		}
		return assignTarget{name: v.name}, rt.getVar(v.name), nil
	case *arrayRefExpr:
		keyString, err := rt.evalArrayKey(v.indices)
		if err != nil {
			return assignTarget{}, value{}, err
		}
		current, err := rt.getArrayElem(v.name, keyString)
		if err != nil {
			return assignTarget{}, value{}, err
		}
		return assignTarget{name: v.name, key: keyString, array: true}, current, nil
	case *fieldExpr:
		index, err := rt.eval(v.index)
		if err != nil {
			return assignTarget{}, value{}, err
		}
		n := int(index.Number())
		if n < 0 {
			return assignTarget{}, value{}, fmt.Errorf("invalid field index")
		}
		return assignTarget{field: true, fieldIndex: n}, rt.field(n), nil
	default:
		return assignTarget{}, value{}, fmt.Errorf("expected variable")
	}
}

func (rt *runtime) setResolvedAssignable(target assignTarget, v value) error {
	if target.array {
		return rt.setArrayElem(target.name, target.key, v)
	}
	if target.field {
		return rt.setField(target.fieldIndex, v)
	}
	return rt.setVar(target.name, v)
}

func (rt *runtime) currentResolvedAssignable(target assignTarget) (value, error) {
	if target.array {
		return rt.getArrayElem(target.name, target.key)
	}
	if target.field {
		return rt.field(target.fieldIndex), nil
	}
	return rt.getVar(target.name), nil
}

func (rt *runtime) evalArrayRef(ref *arrayRefExpr) (value, error) {
	key, err := rt.evalArrayKey(ref.indices)
	if err != nil {
		return value{}, err
	}
	return rt.getArrayElem(ref.name, key)
}

func (rt *runtime) evalArrayKey(indices []expr) (string, error) {
	if len(indices) == 0 {
		return "", fmt.Errorf("array index is required")
	}
	parts := make([]string, len(indices))
	for i, index := range indices {
		v, err := rt.eval(index)
		if err != nil {
			return "", err
		}
		parts[i] = v.String()
	}
	if len(parts) == 1 {
		return parts[0], nil
	}
	return strings.Join(parts, rt.getVar("SUBSEP").String()), nil
}

func boolValue(ok bool) value {
	if ok {
		return numberValue(1)
	}
	return numberValue(0)
}

func compareValues(left, right value, op string, ignoreCase bool) bool {
	var cmp int
	if valuesAreNumeric(left, right) {
		ln, rn := left.Number(), right.Number()
		switch {
		case ln < rn:
			cmp = -1
		case ln > rn:
			cmp = 1
		default:
			cmp = 0
		}
	} else {
		ls, rs := left.String(), right.String()
		if ignoreCase {
			ls = strings.ToLower(ls)
			rs = strings.ToLower(rs)
		}
		switch {
		case ls < rs:
			cmp = -1
		case ls > rs:
			cmp = 1
		default:
			cmp = 0
		}
	}
	switch op {
	case "==":
		return cmp == 0
	case "!=":
		return cmp != 0
	case "<":
		return cmp < 0
	case "<=":
		return cmp <= 0
	case ">":
		return cmp > 0
	case ">=":
		return cmp >= 0
	default:
		return false
	}
}

func valuesAreNumeric(left, right value) bool {
	switch left.kind {
	case valueNumber, valueStrNum:
		switch right.kind {
		case valueNumber, valueStrNum:
			return true
		}
	}
	return false
}
