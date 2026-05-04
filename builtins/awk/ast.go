// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"regexp"
)

// Expression nodes. The interpreter walks these directly — no IR or codegen.

type expr interface{ exprNode() }

type numExpr struct {
	val float64
	src string // original lexeme, preserved so toString can use it for OFMT
}

type strExpr struct {
	val string
}

// regexExpr is a /regex/ literal used in pattern position or with ~/!~.
// In any other context (e.g. as an expression value), it is implicitly
// $0 ~ /re/, but we keep the parsing simple by handling that in the parser.
type regexExpr struct {
	re  *regexp.Regexp
	src string
}

type identExpr struct {
	name string
}

type fieldExpr struct {
	index expr
}

type indexExpr struct {
	name    string
	indices []expr
}

type unaryExpr struct {
	op      tokenKind
	operand expr
}

type binaryExpr struct {
	op          tokenKind
	left, right expr
}

type concatExpr struct {
	parts []expr
}

type assignExpr struct {
	op    tokenKind // tkAssign, tkAddAssign, ...
	left  expr      // identExpr | indexExpr | fieldExpr
	right expr
}

type incrExpr struct {
	post bool      // true: x++ / x--, false: ++x / --x
	op   tokenKind // tkInc | tkDec
	expr expr
}

type condExpr struct {
	cond, then, else_ expr
}

type callExpr struct {
	name string
	args []expr
}

// inExpr models "(idx in arr)" and "((i,j) in arr)" forms.
type inExpr struct {
	keys     []expr
	arrayVar string
}

// matchExpr models e ~ /re/ and e !~ /re/.
// We compile the regex once at parse time when the right-hand side is a
// regex literal.
type matchExpr struct {
	negate bool
	left   expr
	re     *regexp.Regexp // non-nil when right was a literal regex
	right  expr           // non-nil when right is a dynamic expression
}

// groupingExpr is a parenthesised expression. Most of the time we collapse
// these in the parser, but we keep them when they affect concat semantics.
// (Currently unused; concat is handled implicitly by the parser.)

func (*numExpr) exprNode()    {}
func (*strExpr) exprNode()    {}
func (*regexExpr) exprNode()  {}
func (*identExpr) exprNode()  {}
func (*fieldExpr) exprNode()  {}
func (*indexExpr) exprNode()  {}
func (*unaryExpr) exprNode()  {}
func (*binaryExpr) exprNode() {}
func (*concatExpr) exprNode() {}
func (*assignExpr) exprNode() {}
func (*incrExpr) exprNode()   {}
func (*condExpr) exprNode()   {}
func (*callExpr) exprNode()   {}
func (*inExpr) exprNode()     {}
func (*matchExpr) exprNode()  {}

// Statement nodes.

type stmt interface{ stmtNode() }

type printStmt struct {
	args []expr
}

type printfStmt struct {
	args []expr
}

type exprStmt struct {
	expr expr
}

type blockStmt struct {
	body []stmt
}

type ifStmt struct {
	cond  expr
	then  stmt
	else_ stmt // nil when absent
}

type whileStmt struct {
	cond expr
	body stmt
}

type doWhileStmt struct {
	cond expr
	body stmt
}

type forStmt struct {
	init stmt
	cond expr // nil = "always true"
	post stmt
	body stmt
}

type forInStmt struct {
	loopVar  string
	arrayVar string
	body     stmt
}

type breakStmt struct{}
type continueStmt struct{}
type nextStmt struct{}

type exitStmt struct {
	code expr // nil = no expression
}

type deleteStmt struct {
	arrayVar string
	indices  []expr // nil = delete entire array
}

func (*printStmt) stmtNode()    {}
func (*printfStmt) stmtNode()   {}
func (*exprStmt) stmtNode()     {}
func (*blockStmt) stmtNode()    {}
func (*ifStmt) stmtNode()       {}
func (*whileStmt) stmtNode()    {}
func (*doWhileStmt) stmtNode()  {}
func (*forStmt) stmtNode()      {}
func (*forInStmt) stmtNode()    {}
func (*breakStmt) stmtNode()    {}
func (*continueStmt) stmtNode() {}
func (*nextStmt) stmtNode()     {}
func (*exitStmt) stmtNode()     {}
func (*deleteStmt) stmtNode()   {}

// Pattern nodes.

type pattern interface{ patternNode() }

type beginPattern struct{}
type endPattern struct{}

// alwaysPattern matches every record (no pattern given).
type alwaysPattern struct{}

// exprPattern wraps an arbitrary expression; truthy = match.
type exprPattern struct {
	e expr
}

// regexPattern is shorthand for $0 ~ /re/.
type regexPattern struct {
	re  *regexp.Regexp
	src string
}

// rangePattern matches between start (inclusive) and end (inclusive), then
// resets. start/end are arbitrary expressions, evaluated against $0 if a
// regex literal is provided.
type rangePattern struct {
	start, end pattern // typically exprPattern or regexPattern
}

func (*beginPattern) patternNode()  {}
func (*endPattern) patternNode()    {}
func (*alwaysPattern) patternNode() {}
func (*exprPattern) patternNode()   {}
func (*regexPattern) patternNode()  {}
func (*rangePattern) patternNode()  {}

// rule pairs a pattern with its action (a block).
type rule struct {
	pat    pattern
	action *blockStmt // nil: default action {print}
}

// program is the parsed awk source.
type program struct {
	rules []rule
}
