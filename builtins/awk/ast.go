// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

type program struct {
	rules []rule
}

type ruleKind int

const (
	ruleNormal ruleKind = iota
	ruleBegin
	ruleEnd
)

type rule struct {
	kind    ruleKind
	pattern expr
	action  []stmt
}

type stmt interface {
	stmtNode()
}

type printStmt struct {
	args []expr
}

func (*printStmt) stmtNode() {}

type printfStmt struct {
	args []expr
}

func (*printfStmt) stmtNode() {}

type ifStmt struct {
	cond      expr
	thenStmts []stmt
	elseStmts []stmt
}

func (*ifStmt) stmtNode() {}

type nextStmt struct{}

func (*nextStmt) stmtNode() {}

type exprStmt struct {
	x expr
}

func (*exprStmt) stmtNode() {}

type expr interface {
	exprNode()
}

type numberExpr struct {
	text string
	num  float64
}

func (*numberExpr) exprNode() {}

type stringExpr struct {
	value string
}

func (*stringExpr) exprNode() {}

type regexExpr struct {
	pattern string
}

func (*regexExpr) exprNode() {}

type varExpr struct {
	name string
}

func (*varExpr) exprNode() {}

type arrayRefExpr struct {
	name  string
	index expr
}

func (*arrayRefExpr) exprNode() {}

type fieldExpr struct {
	index expr
}

func (*fieldExpr) exprNode() {}

type groupedExpr struct {
	x expr
}

func (*groupedExpr) exprNode() {}

type unaryExpr struct {
	op string
	x  expr
}

func (*unaryExpr) exprNode() {}

type binaryExpr struct {
	op    string
	left  expr
	right expr
}

func (*binaryExpr) exprNode() {}

type assignExpr struct {
	op    string
	left  expr
	right expr
}

func (*assignExpr) exprNode() {}

type incDecExpr struct {
	op     string
	x      expr
	prefix bool
}

func (*incDecExpr) exprNode() {}

type callExpr struct {
	name string
	args []expr
}

func (*callExpr) exprNode() {}
