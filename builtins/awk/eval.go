// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

var errNextRecord = errors.New("next record")

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
		case *nextStmt:
			return errNextRecord
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
		return rt.getVar(e.name), nil
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
	args := make([]value, 0, len(e.args))
	for _, arg := range e.args {
		v, err := rt.eval(arg)
		if err != nil {
			return value{}, err
		}
		args = append(args, v)
	}
	switch e.name {
	case "length":
		if len(args) > 1 {
			return value{}, fmt.Errorf("length expects at most 1 argument")
		}
		s := rt.field(0).String()
		if len(args) == 1 {
			s = args[0].String()
		}
		return numberValue(float64(len([]rune(s)))), nil
	case "substr":
		if len(args) != 2 && len(args) != 3 {
			return value{}, fmt.Errorf("substr expects 2 or 3 arguments")
		}
		s := []rune(args[0].String())
		start := int(args[1].Number()) - 1
		if start < 0 {
			start = 0
		}
		if start >= len(s) {
			return stringValue(""), nil
		}
		end := len(s)
		if len(args) == 3 {
			count := int(args[2].Number())
			if count <= 0 {
				return stringValue(""), nil
			}
			if start+count < end {
				end = start + count
			}
		}
		return stringValue(string(s[start:end])), nil
	case "index":
		if len(args) != 2 {
			return value{}, fmt.Errorf("index expects 2 arguments")
		}
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
		if len(args) != 1 {
			return value{}, fmt.Errorf("tolower expects 1 argument")
		}
		s := args[0].String()
		return stringValue(strings.ToLower(s)), nil
	case "toupper":
		if len(args) != 1 {
			return value{}, fmt.Errorf("toupper expects 1 argument")
		}
		s := args[0].String()
		return stringValue(strings.ToUpper(s)), nil
	case "int":
		if len(args) != 1 {
			return value{}, fmt.Errorf("int expects 1 argument")
		}
		v := args[0]
		return numberValue(math.Trunc(v.Number())), nil
	default:
		return value{}, fmt.Errorf("function calls are not supported")
	}
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
	lhs, ok := e.left.(*varExpr)
	if !ok {
		return value{}, fmt.Errorf("assignment requires a scalar variable")
	}
	right, err := rt.eval(e.right)
	if err != nil {
		return value{}, err
	}
	if e.op != "=" {
		left := rt.getVar(lhs.name)
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
	if err := rt.setVar(lhs.name, right); err != nil {
		return value{}, err
	}
	return right, nil
}

func (rt *runtime) evalIncDec(e *incDecExpr) (value, error) {
	vref, ok := e.x.(*varExpr)
	if !ok {
		return value{}, fmt.Errorf("increment and decrement require scalar variables")
	}
	old := rt.getVar(vref.name)
	next := old.Number()
	if e.op == "++" {
		next++
	} else {
		next--
	}
	nv := numberValue(next)
	if err := rt.setVar(vref.name, nv); err != nil {
		return value{}, err
	}
	if e.prefix {
		return nv, nil
	}
	return old, nil
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
