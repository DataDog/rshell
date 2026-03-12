// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package testcmd implements the POSIX test and [ builtin commands.
//
// Usage:
//
//	test EXPRESSION
//	[ EXPRESSION ]
//
// Evaluate a conditional expression and exit with status 0 (true) or 1 (false).
// The [ form requires a closing ] as the last argument.
//
// Exit codes:
//
//	0 — expression evaluates to true
//	1 — expression evaluates to false
//	2 — syntax/usage error
//
// Supported operators:
//
// File tests (unary):
//
//	-a FILE    FILE exists (deprecated POSIX synonym for -e)
//	-e FILE    FILE exists
//	-f FILE    FILE exists and is a regular file
//	-d FILE    FILE exists and is a directory
//	-s FILE    FILE exists and has size greater than zero
//	-r FILE    FILE exists and is readable
//	-w FILE    FILE exists and is writable
//	-x FILE    FILE exists and is executable
//	-h FILE    FILE exists and is a symbolic link
//	-L FILE    FILE exists and is a symbolic link (same as -h)
//	-p FILE    FILE exists and is a named pipe (FIFO)
//
// File comparison (binary):
//
//	FILE1 -nt FILE2    FILE1 is newer (modification time) than FILE2
//	FILE1 -ot FILE2    FILE1 is older (modification time) than FILE2
//
// String tests (unary):
//
//	-z STRING    length of STRING is zero
//	-n STRING    length of STRING is non-zero
//	STRING       STRING is non-empty (equivalent to -n STRING)
//
// String comparison (binary):
//
//	S1 = S2     strings are equal
//	S1 != S2    strings are not equal
//	S1 < S2     S1 sorts before S2 (lexicographic)
//	S1 > S2     S1 sorts after S2 (lexicographic)
//
// Integer comparison (binary):
//
//	N1 -eq N2   integers are equal
//	N1 -ne N2   integers are not equal
//	N1 -lt N2   N1 is less than N2
//	N1 -le N2   N1 is less than or equal to N2
//	N1 -gt N2   N1 is greater than N2
//	N1 -ge N2   N1 is greater than or equal to N2
//
// Logical operators:
//
//	! EXPR          EXPR is false
//	EXPR1 -a EXPR2  both EXPR1 and EXPR2 are true
//	EXPR1 -o EXPR2  either EXPR1 or EXPR2 is true
//	( EXPR )        grouping (parentheses must be shell-escaped)
package testcmd

import (
	"context"
	"io/fs"
	"strconv"
	"strings"

	"github.com/DataDog/rshell/interp/builtins"
)

// Cmd is the "test" builtin command registration.
var Cmd = builtins.Command{Name: "test", MakeFlags: builtins.NoFlags(runTest)}

// BracketCmd is the "[" builtin command registration.
var BracketCmd = builtins.Command{Name: "[", MakeFlags: builtins.NoFlags(runBracket)}

const helpText = `Usage: test EXPRESSION
   or: [ EXPRESSION ]

Evaluate conditional expression.

Exit status:
  0  if EXPRESSION is true,
  1  if EXPRESSION is false,
  2  if an error occurred.

File tests:
  -a FILE   FILE exists (deprecated synonym for -e)
  -e FILE   FILE exists
  -f FILE   FILE is a regular file
  -d FILE   FILE is a directory
  -s FILE   FILE has size > 0
  -r FILE   FILE is readable
  -w FILE   FILE is writable
  -x FILE   FILE is executable
  -h FILE   FILE is a symbolic link
  -L FILE   FILE is a symbolic link (same as -h)
  -p FILE   FILE is a named pipe

File comparison:
  FILE1 -nt FILE2   FILE1 is newer than FILE2
  FILE1 -ot FILE2   FILE1 is older than FILE2

String tests:
  -z STRING         STRING has zero length
  -n STRING         STRING has non-zero length
  STRING            STRING is non-empty

String comparison:
  S1 = S2    strings are equal
  S1 != S2   strings are not equal
  S1 < S2    S1 sorts before S2
  S1 > S2    S1 sorts after S2

Integer comparison:
  N1 -eq N2  N1 equals N2
  N1 -ne N2  N1 is not equal to N2
  N1 -lt N2  N1 is less than N2
  N1 -le N2  N1 is less or equal to N2
  N1 -gt N2  N1 is greater than N2
  N1 -ge N2  N1 is greater or equal to N2

Logical:
  ! EXPR            EXPR is false
  EXPR1 -a EXPR2   both true
  EXPR1 -o EXPR2   either true
  ( EXPR )         grouping
`

const exitSyntaxError = 2

func runTest(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	if len(args) == 1 && args[0] == "--help" {
		callCtx.Out(helpText)
		return builtins.Result{}
	}
	return evaluate(ctx, callCtx, "test", args)
}

func runBracket(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	if len(args) == 1 && args[0] == "--help" {
		callCtx.Out(helpText)
		return builtins.Result{}
	}
	if len(args) == 0 || args[len(args)-1] != "]" {
		callCtx.Errf("[: missing `]'\n")
		return builtins.Result{Code: exitSyntaxError}
	}
	return evaluate(ctx, callCtx, "[", args[:len(args)-1])
}

// parser holds the state for recursive-descent parsing of test expressions.
type parser struct {
	ctx     context.Context
	callCtx *builtins.CallContext
	cmdName string
	args    []string
	pos     int
	err     bool
	depth   int
}

const maxParenDepth = 128

func evaluate(ctx context.Context, callCtx *builtins.CallContext, cmdName string, args []string) builtins.Result {
	if len(args) == 0 {
		return builtins.Result{Code: 1}
	}

	p := &parser{
		ctx:     ctx,
		callCtx: callCtx,
		cmdName: cmdName,
		args:    args,
	}

	result := p.parseOr()

	if p.err {
		return builtins.Result{Code: exitSyntaxError}
	}
	if p.pos < len(p.args) {
		p.callCtx.Errf("%s: extra argument '%s'\n", p.cmdName, p.args[p.pos])
		return builtins.Result{Code: exitSyntaxError}
	}
	if result {
		return builtins.Result{}
	}
	return builtins.Result{Code: 1}
}

func (p *parser) peek() string {
	if p.pos >= len(p.args) {
		return ""
	}
	return p.args[p.pos]
}

func (p *parser) advance() string {
	s := p.args[p.pos]
	p.pos++
	return s
}

// parseOr handles EXPR1 -o EXPR2 (lowest precedence).
func (p *parser) parseOr() bool {
	left := p.parseAnd()
	for !p.err && p.ctx.Err() == nil && p.pos < len(p.args) && p.peek() == "-o" {
		p.advance()
		right := p.parseAnd()
		left = left || right
	}
	return left
}

// parseAnd handles EXPR1 -a EXPR2.
func (p *parser) parseAnd() bool {
	left := p.parseNot()
	for !p.err && p.ctx.Err() == nil && p.pos < len(p.args) && p.peek() == "-a" {
		p.advance()
		right := p.parseNot()
		left = left && right
	}
	return left
}

// parseNot handles ! EXPR. When ! is the last remaining token, it is
// treated as a non-empty string per POSIX single-argument rules.
// When ! is followed by a binary operator (e.g., "! = !"), it is treated
// as a literal string operand, not negation.
func (p *parser) parseNot() bool {
	if p.pos < len(p.args) && p.peek() == "!" {
		remaining := len(p.args) - p.pos
		if remaining == 1 {
			p.advance()
			return true
		}
		// POSIX 3-arg rule: if exactly 3 tokens remain and "!" is followed
		// by a binary operator, treat it as a literal string operand
		// (fall through to parsePrimary for binary expression).
		// With more than 3 tokens, "!" is always negation.
		if remaining == 3 && isBinaryOp(p.args[p.pos+1]) {
			return p.parsePrimary()
		}
		if remaining == 3 && (p.args[p.pos+1] == "-a" || p.args[p.pos+1] == "-o") {
			return p.parsePrimary()
		}
		if p.depth >= maxParenDepth {
			p.callCtx.Errf("%s: expression too deeply nested\n", p.cmdName)
			p.err = true
			return false
		}
		p.depth++
		p.advance()
		result := !p.parseNot()
		p.depth--
		return result
	}
	return p.parsePrimary()
}

// parsePrimary handles parenthesized expressions, unary operators, binary
// operators, and bare strings. It uses lookahead to implement the POSIX
// disambiguation rules (e.g., with 1 remaining arg, everything is a string).
func (p *parser) parsePrimary() bool {
	if p.err || p.pos >= len(p.args) {
		p.callCtx.Errf("%s: missing argument\n", p.cmdName)
		p.err = true
		return false
	}

	cur := p.peek()
	remaining := len(p.args) - p.pos

	// Only treat "(" as grouping when there are enough tokens and it is not
	// used as a literal operand in a binary expression. A lone "(" with
	// remaining==1 is a bare non-empty string per POSIX single-argument rules.
	// When "(" is followed by a binary operator (e.g., "(" = "("), treat it
	// as a literal string operand.
	if cur == "(" && remaining > 1 && !(remaining == 3 && isBinaryOp(p.args[p.pos+1])) && !(remaining == 3 && (p.args[p.pos+1] == "-a" || p.args[p.pos+1] == "-o")) {
		if p.depth >= maxParenDepth {
			p.callCtx.Errf("%s: expression too deeply nested\n", p.cmdName)
			p.err = true
			return false
		}
		p.depth++
		p.advance()
		if p.pos >= len(p.args) || p.peek() == ")" {
			p.callCtx.Errf("%s: missing argument\n", p.cmdName)
			p.err = true
			return false
		}
		result := p.parseOr()
		p.depth--
		if p.err {
			return false
		}
		if p.pos >= len(p.args) || p.peek() != ")" {
			p.callCtx.Errf("%s: missing ')'\n", p.cmdName)
			p.err = true
			return false
		}
		p.advance()
		return result
	}

	// POSIX disambiguation: if there are at least 3 remaining tokens and
	// the second one is a binary operator, parse as a binary expression.
	// This must be checked before unary operators to handle cases like
	// "test -f = -f" (string comparison, not file test).
	if remaining >= 3 {
		op := p.args[p.pos+1]
		if isBinaryOp(op) {
			return p.parseBinaryExpr()
		}
		// POSIX 3-arg rule: when exactly 3 tokens remain and the middle
		// one is -a/-o, treat as binary AND/OR with string operands.
		// e.g., "test -f -a -d" → "-f" (non-empty) AND "-d" (non-empty).
		// Only when exactly 3 remain — with more tokens, -a/-o are handled
		// by parseAnd/parseOr at their proper precedence level.
		if remaining == 3 && (op == "-a" || op == "-o") {
			return p.parseBinaryExpr()
		}
	}

	// With 2+ remaining tokens, check for unary operators.
	if remaining >= 2 {
		if isUnaryFileOp(cur) {
			return p.parseUnaryFileOp()
		}
		if cur == "-z" || cur == "-n" {
			return p.parseUnaryStringOp()
		}
		// -o as unary tests whether a shell option is set.
		// This restricted shell has no options, so always false.
		if cur == "-o" {
			p.advance() // consume -o
			p.advance() // consume operand
			return false
		}
	}

	// Single token or unrecognised: treat as bare string (true if non-empty).
	p.advance()
	return cur != ""
}

func isBinaryOp(op string) bool {
	switch op {
	case "=", "==", "!=", "<", ">",
		"-eq", "-ne", "-lt", "-le", "-gt", "-ge",
		"-nt", "-ot":
		return true
	}
	return false
}

func isUnaryFileOp(op string) bool {
	switch op {
	case "-a", "-e", "-f", "-d", "-s", "-r", "-w", "-x", "-h", "-L", "-p":
		return true
	}
	return false
}

func (p *parser) consumeUnaryOperand(op string) (string, bool) {
	if p.pos >= len(p.args) {
		p.callCtx.Errf("%s: '%s': unary operator expected\n", p.cmdName, op)
		p.err = true
		return "", false
	}
	return p.advance(), true
}

func (p *parser) parseUnaryFileOp() bool {
	op := p.advance()
	operand, ok := p.consumeUnaryOperand(op)
	if !ok {
		return false
	}
	return p.evalFileTest(op, operand)
}

func (p *parser) parseUnaryStringOp() bool {
	op := p.advance()
	s, ok := p.consumeUnaryOperand(op)
	if !ok {
		return false
	}
	return (op == "-z") == (s == "")
}

func (p *parser) parseBinaryExpr() bool {
	left := p.advance()
	op := p.advance()
	if p.pos >= len(p.args) {
		p.callCtx.Errf("%s: missing argument after '%s'\n", p.cmdName, op)
		p.err = true
		return false
	}
	right := p.advance()

	switch op {
	case "=", "==":
		return left == right
	case "!=":
		return left != right
	case "<":
		return left < right
	case ">":
		return left > right
	case "-eq", "-ne", "-lt", "-le", "-gt", "-ge":
		return p.evalIntCompare(left, op, right)
	case "-nt", "-ot":
		return p.evalFileCompare(left, op, right)
	case "-a":
		return left != "" && right != ""
	case "-o":
		return left != "" || right != ""
	default:
		p.callCtx.Errf("%s: unknown binary operator '%s'\n", p.cmdName, op)
		p.err = true
		return false
	}
}

func (p *parser) evalFileTest(op, path string) bool {
	if path == "" {
		return false
	}
	// For -r/-w/-x, use real access checks instead of mode bits.
	if p.callCtx.AccessFile != nil {
		switch op {
		case "-r":
			return p.callCtx.AccessFile(p.ctx, path, 0x04) == nil
		case "-w":
			return p.callCtx.AccessFile(p.ctx, path, 0x02) == nil
		case "-x":
			return p.callCtx.AccessFile(p.ctx, path, 0x01) == nil
		}
	}

	switch op {
	case "-h", "-L":
		if p.callCtx.LstatFile == nil {
			return false
		}
		info, err := p.callCtx.LstatFile(p.ctx, path)
		if err != nil {
			return false
		}
		return info.Mode()&fs.ModeSymlink != 0
	default:
		if p.callCtx.StatFile == nil {
			return false
		}
		info, err := p.callCtx.StatFile(p.ctx, path)
		if err != nil {
			return false
		}
		return evalFileInfo(op, info)
	}
}

func evalFileInfo(op string, info fs.FileInfo) bool {
	switch op {
	case "-a", "-e":
		return true
	case "-f":
		return info.Mode().IsRegular()
	case "-d":
		return info.IsDir()
	case "-s":
		return info.Size() > 0
	case "-r":
		// NOTE: This fallback checks any permission bit (user/group/other) and does not
		// account for file ownership. In production AccessFile is always set and this path
		// is not reached; actual file access still goes through the sandbox.
		return info.Mode().Perm()&0444 != 0
	case "-w":
		return info.Mode().Perm()&0222 != 0
	case "-x":
		return info.Mode().Perm()&0111 != 0
	case "-p":
		return info.Mode()&fs.ModeNamedPipe != 0
	}
	return false
}

func (p *parser) evalIntCompare(left, op, right string) bool {
	l, ok := p.parseInt(left)
	if !ok {
		return false
	}
	r, ok := p.parseInt(right)
	if !ok {
		return false
	}
	switch op {
	case "-eq":
		return l == r
	case "-ne":
		return l != r
	case "-lt":
		return l < r
	case "-le":
		return l <= r
	case "-gt":
		return l > r
	case "-ge":
		return l >= r
	}
	return false
}

func (p *parser) parseInt(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		p.callCtx.Errf("%s: : integer expression expected\n", p.cmdName)
		p.err = true
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		p.callCtx.Errf("%s: %s: integer expression expected\n", p.cmdName, s)
		p.err = true
		return 0, false
	}
	return n, true
}

func (p *parser) evalFileCompare(left, op, right string) bool {
	if p.callCtx.StatFile == nil {
		return false
	}
	leftInfo, leftErr := p.callCtx.StatFile(p.ctx, left)
	rightInfo, rightErr := p.callCtx.StatFile(p.ctx, right)

	switch op {
	case "-nt":
		if leftErr != nil {
			return false
		}
		if rightErr != nil {
			return true
		}
		return leftInfo.ModTime().After(rightInfo.ModTime())
	case "-ot":
		if rightErr != nil {
			return false
		}
		if leftErr != nil {
			return true
		}
		return leftInfo.ModTime().Before(rightInfo.ModTime())
	}
	return false
}
