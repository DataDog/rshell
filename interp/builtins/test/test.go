// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package test implements the test/[ builtin — evaluate conditional expressions.
//
// Usage: test EXPRESSION
//
//	[ EXPRESSION ]
//
// Evaluate a conditional expression EXPRESSION and exit with status 0 (true)
// or 1 (false). With no arguments, test exits with status 1.
//
// Supported operators:
//
//	File tests:
//	  -e FILE   FILE exists
//	  -f FILE   FILE exists and is a regular file
//	  -d FILE   FILE exists and is a directory
//	  -s FILE   FILE exists and has a size greater than zero
//	  -r FILE   FILE exists and read permission is granted
//	  -w FILE   FILE exists and write permission is granted
//	  -x FILE   FILE exists and execute permission is granted
//	  -L FILE   FILE exists and is a symbolic link (same as -h)
//	  -h FILE   FILE exists and is a symbolic link (same as -L)
//
//	File comparisons:
//	  FILE1 -nt FILE2   FILE1 is newer (modification date) than FILE2
//	  FILE1 -ot FILE2   FILE1 is older than FILE2
//	  FILE1 -ef FILE2   FILE1 and FILE2 refer to the same device and inode
//
//	String tests:
//	  -n STRING          STRING has non-zero length
//	  -z STRING          STRING has zero length
//	  STRING1 = STRING2  the strings are equal
//	  STRING1 == STRING2 the strings are equal
//	  STRING1 != STRING2 the strings are not equal
//
//	Integer comparisons:
//	  INT1 -eq INT2  INT1 is equal to INT2
//	  INT1 -ne INT2  INT1 is not equal to INT2
//	  INT1 -lt INT2  INT1 is less than INT2
//	  INT1 -gt INT2  INT1 is greater than INT2
//	  INT1 -le INT2  INT1 is less than or equal to INT2
//	  INT1 -ge INT2  INT1 is greater than or equal to INT2
//
//	Logical operators:
//	  ! EXPRESSION             EXPRESSION is false
//	  EXPRESSION -a EXPRESSION both expressions are true
//	  EXPRESSION -o EXPRESSION either expression is true
//	  ( EXPRESSION )           grouping
//
// Exit codes:
//
//	0  Expression is true.
//	1  Expression is false or missing.
//	2  Syntax or usage error.
package test

import (
	"context"
	"os"
	"strconv"

	"github.com/DataDog/rshell/interp/builtins"
)

func init() {
	builtins.Register("test", runTest)
	builtins.Register("[", runBracket)
}

func runTest(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	return run(ctx, callCtx, args, false)
}

func runBracket(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	return run(ctx, callCtx, args, true)
}

func run(ctx context.Context, callCtx *builtins.CallContext, args []string, isBracket bool) builtins.Result {
	name := "test"
	if isBracket {
		name = "["
		if len(args) == 0 || args[len(args)-1] != "]" {
			callCtx.Errf("[: missing `]'\n")
			return builtins.Result{Code: 2}
		}
		args = args[:len(args)-1]
	}

	// No arguments: false.
	if len(args) == 0 {
		return builtins.Result{Code: 1}
	}

	p := &testParser{rem: args}
	expr := p.classicTest()
	if p.err != "" {
		callCtx.Errf("%s: %s\n", name, p.err)
		return builtins.Result{Code: 2}
	}
	if len(p.rem) > 0 {
		callCtx.Errf("%s: extra argument '%s'\n", name, p.rem[0])
		return builtins.Result{Code: 2}
	}

	if evalTest(ctx, callCtx, expr) {
		return builtins.Result{}
	}
	return builtins.Result{Code: 1}
}

// --- Expression AST ---

type testExpr interface{ testExpr() }

type testWord struct{ val string }
type testUnary struct {
	op string
	x  testExpr
}
type testBinary struct {
	op   string
	x, y testExpr
}
type testParen struct{ x testExpr }

func (testWord) testExpr()   {}
func (testUnary) testExpr()  {}
func (testBinary) testExpr() {}
func (testParen) testExpr()  {}

// --- Parser ---

// testParser implements recursive descent parsing for classic test expressions,
// following the same algorithm as mvdan/sh.
type testParser struct {
	rem []string
	err string
}

func (p *testParser) next() string {
	if len(p.rem) == 0 {
		return ""
	}
	s := p.rem[0]
	p.rem = p.rem[1:]
	return s
}

func (p *testParser) peek() string {
	if len(p.rem) == 0 {
		return ""
	}
	return p.rem[0]
}

func (p *testParser) hasMore() bool {
	return len(p.rem) > 0
}

// classicTest parses a full test expression handling -o (OR) at the lowest precedence.
func (p *testParser) classicTest() testExpr {
	return p.testOrExpr()
}

func (p *testParser) testOrExpr() testExpr {
	left := p.testAndExpr()
	for p.err == "" && p.hasMore() && p.peek() == "-o" {
		p.next() // consume -o
		right := p.testAndExpr()
		left = testBinary{op: "-o", x: left, y: right}
	}
	return left
}

func (p *testParser) testAndExpr() testExpr {
	left := p.testExprBase()
	for p.err == "" && p.hasMore() && p.peek() == "-a" {
		p.next() // consume -a
		right := p.testExprBase()
		left = testBinary{op: "-a", x: left, y: right}
	}
	return left
}

var testUnaryOps = map[string]bool{
	"-e": true, "-f": true, "-d": true, "-s": true,
	"-r": true, "-w": true, "-x": true,
	"-L": true, "-h": true,
	"-n": true, "-z": true,
}

var testBinaryOps = map[string]bool{
	"=": true, "==": true, "!=": true,
	"-eq": true, "-ne": true, "-lt": true, "-gt": true, "-le": true, "-ge": true,
	"-nt": true, "-ot": true, "-ef": true,
}

func (p *testParser) testExprBase() testExpr {
	if p.err != "" {
		return testWord{}
	}

	if !p.hasMore() {
		p.err = "missing argument after operator"
		return testWord{}
	}

	s := p.peek()

	// Negation.
	if s == "!" {
		p.next()
		return testUnary{op: "!", x: p.testExprBase()}
	}

	// Parenthesized group.
	if s == "(" {
		p.next()
		expr := p.classicTest()
		if !p.hasMore() || p.peek() != ")" {
			p.err = "missing ')'"
			return testWord{}
		}
		p.next()
		return testParen{x: expr}
	}

	// Unary operator.
	if testUnaryOps[s] {
		p.next()
		if !p.hasMore() {
			p.err = "missing argument after '" + s + "'"
			return testWord{}
		}
		arg := p.next()
		return testUnary{op: s, x: testWord{val: arg}}
	}

	// Consume first word.
	p.next()

	// Check for binary operator.
	if p.hasMore() && testBinaryOps[p.peek()] {
		op := p.next()
		if !p.hasMore() {
			p.err = "missing argument after '" + op + "'"
			return testWord{}
		}
		rhs := p.next()
		return testBinary{op: op, x: testWord{val: s}, y: testWord{val: rhs}}
	}

	// Plain word: non-empty string is true.
	return testWord{val: s}
}

// --- Evaluator ---

func evalTest(ctx context.Context, callCtx *builtins.CallContext, expr testExpr) bool {
	switch e := expr.(type) {
	case testWord:
		return e.val != ""
	case testUnary:
		return evalUnary(ctx, callCtx, e)
	case testBinary:
		return evalBinary(ctx, callCtx, e)
	case testParen:
		return evalTest(ctx, callCtx, e.x)
	}
	return false
}

func evalUnary(ctx context.Context, callCtx *builtins.CallContext, e testUnary) bool {
	switch e.op {
	case "!":
		return !evalTest(ctx, callCtx, e.x)
	case "-n":
		if w, ok := e.x.(testWord); ok {
			return w.val != ""
		}
		return evalTest(ctx, callCtx, e.x)
	case "-z":
		if w, ok := e.x.(testWord); ok {
			return w.val == ""
		}
		return !evalTest(ctx, callCtx, e.x)
	case "-e", "-f", "-d", "-s", "-r", "-w", "-x":
		w, ok := e.x.(testWord)
		if !ok {
			return false
		}
		fi, err := callCtx.StatFile(ctx, w.val)
		if err != nil {
			return false
		}
		return evalFileStat(e.op, fi)
	case "-L", "-h":
		w, ok := e.x.(testWord)
		if !ok {
			return false
		}
		fi, err := callCtx.LstatFile(ctx, w.val)
		if err != nil {
			return false
		}
		return fi.Mode()&os.ModeSymlink != 0
	}
	return false
}

func evalFileStat(op string, fi os.FileInfo) bool {
	switch op {
	case "-e":
		return true // stat succeeded
	case "-f":
		return fi.Mode().IsRegular()
	case "-d":
		return fi.IsDir()
	case "-s":
		return fi.Size() > 0
	case "-r":
		return fi.Mode().Perm()&0444 != 0
	case "-w":
		return fi.Mode().Perm()&0222 != 0
	case "-x":
		return fi.Mode().Perm()&0111 != 0
	}
	return false
}

func evalBinary(ctx context.Context, callCtx *builtins.CallContext, e testBinary) bool {
	switch e.op {
	case "-a":
		return evalTest(ctx, callCtx, e.x) && evalTest(ctx, callCtx, e.y)
	case "-o":
		return evalTest(ctx, callCtx, e.x) || evalTest(ctx, callCtx, e.y)
	case "=", "==":
		return wordVal(e.x) == wordVal(e.y)
	case "!=":
		return wordVal(e.x) != wordVal(e.y)
	case "-eq", "-ne", "-lt", "-gt", "-le", "-ge":
		return evalIntCmp(e.op, wordVal(e.x), wordVal(e.y))
	case "-nt":
		return evalNt(ctx, callCtx, wordVal(e.x), wordVal(e.y))
	case "-ot":
		return evalNt(ctx, callCtx, wordVal(e.y), wordVal(e.x))
	case "-ef":
		return evalEf(ctx, callCtx, wordVal(e.x), wordVal(e.y))
	}
	return false
}

func wordVal(e testExpr) string {
	if w, ok := e.(testWord); ok {
		return w.val
	}
	return ""
}

func evalIntCmp(op, a, b string) bool {
	ai, errA := strconv.ParseInt(a, 10, 64)
	bi, errB := strconv.ParseInt(b, 10, 64)
	if errA != nil || errB != nil {
		return false
	}
	switch op {
	case "-eq":
		return ai == bi
	case "-ne":
		return ai != bi
	case "-lt":
		return ai < bi
	case "-gt":
		return ai > bi
	case "-le":
		return ai <= bi
	case "-ge":
		return ai >= bi
	}
	return false
}

func evalNt(ctx context.Context, callCtx *builtins.CallContext, a, b string) bool {
	fiA, errA := callCtx.StatFile(ctx, a)
	fiB, errB := callCtx.StatFile(ctx, b)
	if errA != nil || errB != nil {
		return false
	}
	return fiA.ModTime().After(fiB.ModTime())
}

func evalEf(ctx context.Context, callCtx *builtins.CallContext, a, b string) bool {
	fiA, errA := callCtx.StatFile(ctx, a)
	fiB, errB := callCtx.StatFile(ctx, b)
	if errA != nil || errB != nil {
		return false
	}
	return os.SameFile(fiA, fiB)
}
