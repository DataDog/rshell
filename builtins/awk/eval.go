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
	"regexp"
	"strings"
	"unicode/utf8"
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

func (rt *runtime) execStatements(ctx context.Context, stmts []stmt) error {
	for _, st := range stmts {
		if err := ctx.Err(); err != nil {
			return err
		}
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
			if err := rt.printValues(vals); err != nil {
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
			rt.callCtx.Out(out)
		case *ifStmt:
			cond, err := rt.eval(s.cond)
			if err != nil {
				return err
			}
			if cond.Bool() {
				if err := rt.execStatements(ctx, s.thenStmts); err != nil {
					return err
				}
			} else if len(s.elseStmts) > 0 {
				if err := rt.execStatements(ctx, s.elseStmts); err != nil {
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
				if err := rt.execStatements(ctx, s.body); err != nil {
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
			if err := rt.execFor(ctx, s); err != nil {
				return err
			}
		case *whileStmt:
			if err := rt.execWhile(ctx, s); err != nil {
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

func (rt *runtime) execFor(ctx context.Context, s *forStmt) error {
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
		err := rt.execStatements(ctx, s.body)
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

func (rt *runtime) execWhile(ctx context.Context, s *whileStmt) error {
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
		err = rt.execStatements(ctx, s.body)
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

func (rt *runtime) printValues(vals []value) error {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = v.String()
	}
	rt.callCtx.Out(strings.Join(parts, rt.getVar("OFS").String()))
	rt.callCtx.Out(rt.getVar("ORS").String())
	return nil
}

func (rt *runtime) eval(x expr) (value, error) {
	switch e := x.(type) {
	case *numberExpr:
		return numberValue(e.num), nil
	case *stringExpr:
		return stringValue(e.value), nil
	case *regexExpr:
		re, err := compileRegex(e.pattern)
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
	case *callExpr:
		return rt.evalCall(e)
	default:
		return value{}, fmt.Errorf("unknown expression")
	}
}

func (rt *runtime) evalCall(e *callExpr) (value, error) {
	if e.name == "split" {
		return rt.evalSplit(e)
	}
	if e.name == "sub" || e.name == "gsub" {
		return rt.evalSubstitution(e)
	}
	if e.name == "match" {
		return rt.evalMatch(e)
	}
	args := make([]value, 0, len(e.args))
	for _, arg := range e.args {
		v, err := rt.eval(arg)
		if err != nil {
			return value{}, err
		}
		args = append(args, v)
	}
	if err := validateBuiltinCallArity(e.name, len(args)); err != nil {
		return value{}, err
	}
	switch e.name {
	case "length":
		s := rt.field(0).String()
		if len(args) == 1 {
			s = args[0].String()
		}
		return numberValue(float64(len([]rune(s)))), nil
	case "substr":
		s := []rune(args[0].String())
		start := substrStart(args[1].Number(), len(s))
		if start >= len(s) {
			return stringValue(""), nil
		}
		end := len(s)
		if len(args) == 3 {
			end = substrEnd(start, len(s), args[2].Number())
		}
		return stringValue(string(s[start:end])), nil
	case "index":
		haystack := args[0].String()
		needle := args[1].String()
		if needle == "" {
			return numberValue(1), nil
		}
		pos := strings.Index(haystack, needle)
		if pos < 0 {
			return numberValue(0), nil
		}
		return numberValue(float64(len([]rune(haystack[:pos])) + 1)), nil
	case "tolower":
		s := args[0].String()
		return stringValue(strings.ToLower(s)), nil
	case "toupper":
		s := args[0].String()
		return stringValue(strings.ToUpper(s)), nil
	case "int":
		v := args[0]
		return numberValue(math.Trunc(v.Number())), nil
	case "sprintf":
		out, err := formatPrintf(args[0].String(), args[1:])
		if err != nil {
			return value{}, err
		}
		return stringValue(out), nil
	default:
		return value{}, fmt.Errorf("function calls are not supported")
	}
}

func (rt *runtime) evalSubstitution(e *callExpr) (value, error) {
	if err := validateBuiltinCallArity(e.name, len(e.args)); err != nil {
		return value{}, err
	}
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
	if err := validateBuiltinCallArity(e.name, len(e.args)); err != nil {
		return value{}, err
	}
	input, err := rt.eval(e.args[0])
	if err != nil {
		return value{}, err
	}
	re, err := rt.compileRegexArg(e.args[1])
	if err != nil {
		return value{}, err
	}
	match := re.FindStringIndex(input.String())
	if match == nil {
		if err := rt.setVar("RSTART", numberValue(0)); err != nil {
			return value{}, err
		}
		if err := rt.setVar("RLENGTH", numberValue(-1)); err != nil {
			return value{}, err
		}
		return numberValue(0), nil
	}
	start := runeLen(input.String()[:match[0]]) + 1
	length := runeLen(input.String()[match[0]:match[1]])
	if err := rt.setVar("RSTART", numberValue(float64(start))); err != nil {
		return value{}, err
	}
	if err := rt.setVar("RLENGTH", numberValue(float64(length))); err != nil {
		return value{}, err
	}
	return numberValue(float64(start)), nil
}

func (rt *runtime) compileRegexArg(x expr) (*regexp.Regexp, error) {
	if rx, ok := x.(*regexExpr); ok {
		return compileRegex(rx.pattern)
	}
	v, err := rt.eval(x)
	if err != nil {
		return nil, err
	}
	return compileRegex(v.String())
}

func substituteAwk(re *regexp.Regexp, input, replacement string, all bool) (string, int, error) {
	var b strings.Builder
	count := 0
	last := 0
	searchStart := 0
	for searchStart <= len(input) {
		loc := re.FindStringIndex(input[searchStart:])
		if loc == nil {
			break
		}
		start := searchStart + loc[0]
		end := searchStart + loc[1]
		if err := appendLimitedString(&b, input[last:start]); err != nil {
			return "", 0, err
		}
		if err := appendAwkReplacement(&b, replacement, input[start:end]); err != nil {
			return "", 0, err
		}
		count++
		last = end
		if !all {
			break
		}
		if start == end {
			if end >= len(input) {
				searchStart = len(input) + 1
				continue
			}
			_, size := utf8.DecodeRuneInString(input[end:])
			if size == 0 {
				size = 1
			}
			searchStart = end + size
			continue
		}
		searchStart = end
	}
	if count == 0 {
		return input, 0, nil
	}
	if err := appendLimitedString(&b, input[last:]); err != nil {
		return "", 0, err
	}
	return b.String(), count, nil
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
	if err := validateBuiltinCallArity(e.name, len(e.args)); err != nil {
		return value{}, err
	}
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
		parts = splitAwkChars(input.String())
	} else if regexSplit || sep != " " {
		if regexSplit {
			parts, err = splitAwkRegex(input.String(), sep)
		} else {
			parts, err = splitAwkFields(input.String(), sep)
		}
		if err != nil {
			return value{}, err
		}
	} else {
		parts, err = splitAwkFields(input.String(), sep)
		if err != nil {
			return value{}, err
		}
	}
	elems := make(map[string]value, len(parts))
	for i, part := range parts {
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
		return stringValue(left.String() + right.String()), nil
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
		return boolValue(compareValues(left, right, e.op)), nil
	default:
		return value{}, fmt.Errorf("unknown binary operator %s", e.op)
	}
}

func (rt *runtime) matchRegexExpr(left value, rightExpr expr) (bool, error) {
	if rx, ok := rightExpr.(*regexExpr); ok {
		re, err := compileRegex(rx.pattern)
		if err != nil {
			return false, err
		}
		return re.MatchString(left.String()), nil
	}
	right, err := rt.eval(rightExpr)
	if err != nil {
		return false, err
	}
	re, err := compileRegex(right.String())
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

func compareValues(left, right value, op string) bool {
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
