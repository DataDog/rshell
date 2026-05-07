// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var errNextRecord = errors.New("next record")
var errBreakLoop = errors.New("break loop")
var errContinueLoop = errors.New("continue loop")

func (rt *runtime) execStatements(stmts []stmt) error {
	for _, st := range stmts {
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
				if err := rt.execStatements(s.thenStmts); err != nil {
					return err
				}
			} else if len(s.elseStmts) > 0 {
				if err := rt.execStatements(s.elseStmts); err != nil {
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
				if err := rt.execStatements(s.body); err != nil {
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
			if err := rt.execFor(s); err != nil {
				return err
			}
		case *whileStmt:
			if err := rt.execWhile(s); err != nil {
				return err
			}
		case *nextStmt:
			return errNextRecord
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
			key, err := rt.eval(s.index)
			if err != nil {
				return err
			}
			if err := rt.deleteArrayElem(s.name, key.String()); err != nil {
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

func (rt *runtime) execFor(s *forStmt) error {
	if s.init != nil {
		if _, err := rt.eval(s.init); err != nil {
			return err
		}
	}
	for {
		if s.cond != nil {
			cond, err := rt.eval(s.cond)
			if err != nil {
				return err
			}
			if !cond.Bool() {
				return nil
			}
		}
		err := rt.execStatements(s.body)
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

func (rt *runtime) execWhile(s *whileStmt) error {
	for {
		cond, err := rt.eval(s.cond)
		if err != nil {
			return err
		}
		if !cond.Bool() {
			return nil
		}
		err = rt.execStatements(s.body)
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
		if rt.isArray(e.name) {
			return value{}, fmt.Errorf("cannot use array %s as scalar", e.name)
		}
		return rt.getVar(e.name), nil
	case *arrayRefExpr:
		return rt.evalArrayRef(e)
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
	default:
		return value{}, fmt.Errorf("function calls are not supported")
	}
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
		elems[strconv.Itoa(i+1)] = inputStringValue(part)
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
		key, err := rt.eval(v.index)
		if err != nil {
			return assignTarget{}, value{}, err
		}
		keyString := key.String()
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

func (rt *runtime) evalArrayRef(ref *arrayRefExpr) (value, error) {
	key, err := rt.eval(ref.index)
	if err != nil {
		return value{}, err
	}
	return rt.getArrayElem(ref.name, key.String())
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
