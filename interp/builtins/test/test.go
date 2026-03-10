// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package test implements the test and [ builtin commands.
//
// test — evaluate conditional expression
//
// Usage:
//
//	test EXPRESSION
//	[ EXPRESSION ]
//
// Evaluate EXPRESSION and exit with status 0 (true) or 1 (false).
// With no EXPRESSION, test returns 1 (false). The [ form requires
// a closing ] as the last argument.
//
// String operators:
//
//	-n STRING       True if string length is non-zero.
//	-z STRING       True if string length is zero.
//	STRING          True if string is not empty (1-argument form).
//	S1 = S2         True if strings are identical.
//	S1 == S2        True if strings are identical (alias for =).
//	S1 != S2        True if strings differ.
//	S1 < S2         True if S1 sorts before S2 lexicographically.
//	S1 > S2         True if S1 sorts after S2 lexicographically.
//
// Integer comparison operators:
//
//	N1 -eq N2       True if integers are equal.
//	N1 -ne N2       True if integers are not equal.
//	N1 -gt N2       True if N1 is greater than N2.
//	N1 -ge N2       True if N1 is greater than or equal to N2.
//	N1 -lt N2       True if N1 is less than N2.
//	N1 -le N2       True if N1 is less than or equal to N2.
//
// File test operators:
//
//	-e FILE         True if file exists.
//	-f FILE         True if file exists and is a regular file.
//	-d FILE         True if file exists and is a directory.
//	-s FILE         True if file exists and has size greater than zero.
//	-r FILE         True if file exists and is readable.
//	-w FILE         True if file exists and is writable.
//	-x FILE         True if file exists and is executable.
//	-L FILE         True if file is a symbolic link.
//	-h FILE         True if file is a symbolic link (alias for -L).
//	-b FILE         True if file is a block special file.
//	-c FILE         True if file is a character special file.
//	-p FILE         True if file is a named pipe (FIFO).
//	-S FILE         True if file is a socket.
//	-g FILE         True if file has the set-group-ID bit set.
//	-u FILE         True if file has the set-user-ID bit set.
//	-k FILE         True if file has the sticky bit set.
//	FILE1 -nt FILE2 True if FILE1 is newer than FILE2.
//	FILE1 -ot FILE2 True if FILE1 is older than FILE2.
//	FILE1 -ef FILE2 True if FILE1 and FILE2 refer to the same device and inode.
//
// Logical operators:
//
//	! EXPRESSION            Logical NOT.
//	EXPR1 -a EXPR2          Logical AND.
//	EXPR1 -o EXPR2          Logical OR.
//	( EXPRESSION )          Grouping for precedence.
//
// Exit codes:
//
//	0  Expression evaluated to true.
//	1  Expression evaluated to false or expression is missing.
//	2  An error occurred (syntax error, invalid argument, etc.).
package test

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/DataDog/rshell/interp/builtins"
)

func init() {
	builtins.Register("test", runTest)
	builtins.Register("[", runBracket)
}

func runTest(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	if len(args) == 1 && args[0] == "--help" {
		printHelp(callCtx, "test")
		return builtins.Result{}
	}
	return eval(ctx, callCtx, "test", args)
}

func runBracket(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	if len(args) == 0 || args[len(args)-1] != "]" {
		callCtx.Errf("[: missing ']'\n")
		return builtins.Result{Code: 2}
	}
	inner := args[:len(args)-1]
	if len(inner) == 1 && inner[0] == "--help" {
		printHelp(callCtx, "[")
		return builtins.Result{}
	}
	return eval(ctx, callCtx, "[", inner)
}

func printHelp(callCtx *builtins.CallContext, cmdName string) {
	if cmdName == "[" {
		callCtx.Out("Usage: [ EXPRESSION ]\n")
	} else {
		callCtx.Out("Usage: test EXPRESSION\n")
	}
	callCtx.Out("Evaluate conditional expression.\n\n")
	callCtx.Out("String operators:\n")
	callCtx.Out("  -n STRING         true if string length is non-zero\n")
	callCtx.Out("  -z STRING         true if string length is zero\n")
	callCtx.Out("  S1 = S2           true if strings are equal\n")
	callCtx.Out("  S1 != S2          true if strings differ\n")
	callCtx.Out("  S1 < S2           true if S1 sorts before S2\n")
	callCtx.Out("  S1 > S2           true if S1 sorts after S2\n\n")
	callCtx.Out("Integer operators:\n")
	callCtx.Out("  N1 -eq N2         true if integers are equal\n")
	callCtx.Out("  N1 -ne N2         true if integers are not equal\n")
	callCtx.Out("  N1 -gt N2         true if N1 > N2\n")
	callCtx.Out("  N1 -ge N2         true if N1 >= N2\n")
	callCtx.Out("  N1 -lt N2         true if N1 < N2\n")
	callCtx.Out("  N1 -le N2         true if N1 <= N2\n\n")
	callCtx.Out("File operators:\n")
	callCtx.Out("  -e FILE           true if file exists\n")
	callCtx.Out("  -f FILE           true if file is a regular file\n")
	callCtx.Out("  -d FILE           true if file is a directory\n")
	callCtx.Out("  -s FILE           true if file has size > 0\n")
	callCtx.Out("  -r FILE           true if file is readable\n")
	callCtx.Out("  -w FILE           true if file is writable\n")
	callCtx.Out("  -x FILE           true if file is executable\n")
	callCtx.Out("  -L/-h FILE        true if file is a symbolic link\n")
	callCtx.Out("  FILE1 -nt FILE2   true if FILE1 is newer\n")
	callCtx.Out("  FILE1 -ot FILE2   true if FILE1 is older\n")
	callCtx.Out("  FILE1 -ef FILE2   true if same file\n\n")
	callCtx.Out("Logical operators:\n")
	callCtx.Out("  ! EXPR            logical NOT\n")
	callCtx.Out("  EXPR1 -a EXPR2    logical AND\n")
	callCtx.Out("  EXPR1 -o EXPR2    logical OR\n")
	callCtx.Out("  ( EXPR )          grouping\n")
}

// eval evaluates a test expression and returns the result.
// It uses the POSIX algorithm for 0–4 arguments and falls back to
// recursive descent for 5+ arguments, matching GNU coreutils behavior.
func eval(ctx context.Context, callCtx *builtins.CallContext, cmdName string, args []string) builtins.Result {
	e := &evaluator{
		ctx:     ctx,
		callCtx: callCtx,
		cmdName: cmdName,
	}

	var result bool
	var err error

	switch {
	case len(args) == 0:
		return builtins.Result{Code: 1}
	case len(args) <= 4:
		result, err = e.posixEval(args)
	default:
		p := &parser{args: args}
		result, err = e.orExpr(p)
		if err == nil && !p.done() {
			err = testError("extra argument '" + p.peek() + "'")
		}
	}

	if err != nil {
		callCtx.Errf("%s: %s\n", cmdName, err)
		return builtins.Result{Code: 2}
	}
	if result {
		return builtins.Result{}
	}
	return builtins.Result{Code: 1}
}

// testError is a simple error type that avoids importing fmt or errors.
type testError string

func (e testError) Error() string { return string(e) }

// parser tracks position within a token slice for recursive descent parsing.
type parser struct {
	args []string
	pos  int
}

func (p *parser) done() bool      { return p.pos >= len(p.args) }
func (p *parser) peek() string    { return p.args[p.pos] }
func (p *parser) remaining() int  { return len(p.args) - p.pos }
func (p *parser) advance() string { s := p.args[p.pos]; p.pos++; return s }

// evaluator holds the context needed for expression evaluation.
type evaluator struct {
	ctx     context.Context
	callCtx *builtins.CallContext
	cmdName string
}

// --- POSIX algorithm for 0-4 arguments ---

func (e *evaluator) posixEval(args []string) (bool, error) {
	switch len(args) {
	case 0:
		return false, nil
	case 1:
		return args[0] != "", nil
	case 2:
		return e.posixTwoArgs(args)
	case 3:
		return e.posixThreeArgs(args)
	case 4:
		return e.posixFourArgs(args)
	}
	return false, nil
}

func (e *evaluator) posixTwoArgs(args []string) (bool, error) {
	if args[0] == "!" {
		return args[1] == "", nil
	}
	if isUnaryOp(args[0]) {
		return e.evalUnary(args[0], args[1])
	}
	return false, testError("'" + args[0] + "': unary operator expected")
}

func (e *evaluator) posixThreeArgs(args []string) (bool, error) {
	if isBinaryOp(args[1]) {
		return e.evalBinary(args[0], args[1], args[2])
	}
	if args[0] == "!" {
		result, err := e.posixTwoArgs(args[1:])
		if err != nil {
			return false, err
		}
		return !result, nil
	}
	if args[0] == "(" && args[2] == ")" {
		return args[1] != "", nil
	}
	p := &parser{args: args}
	result, err := e.orExpr(p)
	if err == nil && !p.done() {
		err = testError("extra argument '" + p.peek() + "'")
	}
	return result, err
}

func (e *evaluator) posixFourArgs(args []string) (bool, error) {
	if args[0] == "!" {
		result, err := e.posixThreeArgs(args[1:])
		if err != nil {
			return false, err
		}
		return !result, nil
	}
	if args[0] == "(" && args[3] == ")" {
		return e.posixTwoArgs(args[1:3])
	}
	p := &parser{args: args}
	result, err := e.orExpr(p)
	if err == nil && !p.done() {
		err = testError("extra argument '" + p.peek() + "'")
	}
	return result, err
}

// --- Recursive descent parser for 5+ arguments ---

func (e *evaluator) orExpr(p *parser) (bool, error) {
	result, err := e.andExpr(p)
	if err != nil {
		return false, err
	}
	for !p.done() && p.peek() == "-o" {
		p.advance()
		right, err := e.andExpr(p)
		if err != nil {
			return false, err
		}
		result = result || right
	}
	return result, nil
}

func (e *evaluator) andExpr(p *parser) (bool, error) {
	result, err := e.notExpr(p)
	if err != nil {
		return false, err
	}
	for !p.done() && p.peek() == "-a" {
		p.advance()
		right, err := e.notExpr(p)
		if err != nil {
			return false, err
		}
		result = result && right
	}
	return result, nil
}

func (e *evaluator) notExpr(p *parser) (bool, error) {
	if !p.done() && p.peek() == "!" {
		p.advance()
		result, err := e.notExpr(p)
		if err != nil {
			return false, err
		}
		return !result, nil
	}
	return e.primary(p)
}

func (e *evaluator) primary(p *parser) (bool, error) {
	if p.done() {
		return false, testError("missing argument after '" + e.cmdName + "'")
	}

	tok := p.peek()

	if tok == "(" {
		p.advance()
		if p.done() {
			return false, testError("missing argument after '('")
		}
		result, err := e.orExpr(p)
		if err != nil {
			return false, err
		}
		if p.done() || p.peek() != ")" {
			return false, testError("missing ')'")
		}
		p.advance()
		return result, nil
	}

	if p.remaining() >= 3 && isBinaryOp(p.args[p.pos+1]) {
		left := p.advance()
		op := p.advance()
		right := p.advance()
		return e.evalBinary(left, op, right)
	}

	if isUnaryOp(tok) && p.remaining() >= 2 {
		op := p.advance()
		operand := p.advance()
		return e.evalUnary(op, operand)
	}

	p.advance()
	return tok != "", nil
}

// --- Operator classification ---

var unaryOps = map[string]bool{
	"-n": true, "-z": true,
	"-e": true, "-f": true, "-d": true, "-s": true,
	"-r": true, "-w": true, "-x": true,
	"-L": true, "-h": true,
	"-b": true, "-c": true, "-p": true, "-S": true,
	"-g": true, "-u": true, "-k": true,
}

func isUnaryOp(s string) bool { return unaryOps[s] }

var binaryOps = map[string]bool{
	"=": true, "==": true, "!=": true, "<": true, ">": true,
	"-eq": true, "-ne": true, "-gt": true, "-ge": true, "-lt": true, "-le": true,
	"-nt": true, "-ot": true, "-ef": true,
}

func isBinaryOp(s string) bool { return binaryOps[s] }

// --- Unary evaluation ---

func (e *evaluator) statFile(path string) (os.FileInfo, error) {
	if path == "" {
		return nil, testError("empty path")
	}
	return e.callCtx.StatFile(e.ctx, path)
}

func (e *evaluator) lstatFile(path string) (os.FileInfo, error) {
	if path == "" {
		return nil, testError("empty path")
	}
	return e.callCtx.LstatFile(e.ctx, path)
}

func (e *evaluator) evalUnary(op, operand string) (bool, error) {
	switch op {
	case "-n":
		return operand != "", nil
	case "-z":
		return operand == "", nil

	case "-e":
		_, err := e.statFile(operand)
		return err == nil, nil
	case "-f":
		info, err := e.statFile(operand)
		return err == nil && info.Mode().IsRegular(), nil
	case "-d":
		info, err := e.statFile(operand)
		return err == nil && info.IsDir(), nil
	case "-s":
		info, err := e.statFile(operand)
		return err == nil && info.Size() > 0, nil
	case "-r":
		info, err := e.statFile(operand)
		return err == nil && info.Mode().Perm()&0444 != 0, nil
	case "-w":
		info, err := e.statFile(operand)
		return err == nil && info.Mode().Perm()&0222 != 0, nil
	case "-x":
		info, err := e.statFile(operand)
		return err == nil && info.Mode().Perm()&0111 != 0, nil
	case "-L", "-h":
		info, err := e.lstatFile(operand)
		return err == nil && info.Mode()&os.ModeSymlink != 0, nil
	case "-b":
		info, err := e.statFile(operand)
		return err == nil && info.Mode()&os.ModeDevice != 0 && info.Mode()&os.ModeCharDevice == 0, nil
	case "-c":
		info, err := e.statFile(operand)
		return err == nil && info.Mode()&os.ModeCharDevice != 0, nil
	case "-p":
		info, err := e.statFile(operand)
		return err == nil && info.Mode()&os.ModeNamedPipe != 0, nil
	case "-S":
		info, err := e.statFile(operand)
		return err == nil && info.Mode()&os.ModeSocket != 0, nil
	case "-g":
		info, err := e.statFile(operand)
		return err == nil && info.Mode()&os.ModeSetgid != 0, nil
	case "-u":
		info, err := e.statFile(operand)
		return err == nil && info.Mode()&os.ModeSetuid != 0, nil
	case "-k":
		info, err := e.statFile(operand)
		return err == nil && info.Mode()&os.ModeSticky != 0, nil
	}
	return false, testError("'" + op + "': unary operator expected")
}

// --- Binary evaluation ---

func (e *evaluator) evalBinary(left, op, right string) (bool, error) {
	switch op {
	case "=", "==":
		return left == right, nil
	case "!=":
		return left != right, nil
	case "<":
		return left < right, nil
	case ">":
		return left > right, nil

	case "-eq", "-ne", "-gt", "-ge", "-lt", "-le":
		return e.evalIntCmp(left, op, right)

	case "-nt":
		return e.evalNt(left, right)
	case "-ot":
		return e.evalOt(left, right)
	case "-ef":
		return e.evalEf(left, right)
	}
	return false, testError("'" + op + "': binary operator expected")
}

func (e *evaluator) evalIntCmp(left, op, right string) (bool, error) {
	l, err := parseTestInt(left)
	if err != nil {
		return false, testError("invalid integer '" + left + "'")
	}
	r, err := parseTestInt(right)
	if err != nil {
		return false, testError("invalid integer '" + right + "'")
	}
	switch op {
	case "-eq":
		return l == r, nil
	case "-ne":
		return l != r, nil
	case "-gt":
		return l > r, nil
	case "-ge":
		return l >= r, nil
	case "-lt":
		return l < r, nil
	case "-le":
		return l <= r, nil
	}
	return false, nil
}

func parseTestInt(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, testError("empty")
	}
	return strconv.ParseInt(s, 10, 64)
}

func (e *evaluator) evalNt(left, right string) (bool, error) {
	li, lerr := e.callCtx.StatFile(e.ctx, left)
	ri, rerr := e.callCtx.StatFile(e.ctx, right)
	if lerr != nil && rerr != nil {
		return false, nil
	}
	if lerr != nil {
		return false, nil
	}
	if rerr != nil {
		return true, nil
	}
	return li.ModTime().After(ri.ModTime()), nil
}

func (e *evaluator) evalOt(left, right string) (bool, error) {
	li, lerr := e.callCtx.StatFile(e.ctx, left)
	ri, rerr := e.callCtx.StatFile(e.ctx, right)
	if lerr != nil && rerr != nil {
		return false, nil
	}
	if lerr != nil {
		return true, nil
	}
	if rerr != nil {
		return false, nil
	}
	return li.ModTime().Before(ri.ModTime()), nil
}

func (e *evaluator) evalEf(left, right string) (bool, error) {
	li, lerr := e.callCtx.StatFile(e.ctx, left)
	ri, rerr := e.callCtx.StatFile(e.ctx, right)
	if lerr != nil || rerr != nil {
		return false, nil
	}
	return os.SameFile(li, ri), nil
}
