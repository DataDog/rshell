// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awkcore

// Program represents a complete awk program.
type Program struct {
	Begin []*Action
	End   []*Action
	Rules []*Rule
	Funcs map[string]*FuncDef
}

// Rule is a pattern-action pair.
type Rule struct {
	Pattern Pattern
	Action  *Action
}

// Action is a block of statements.
type Action struct {
	Stmts []Stmt
}

// Pattern types.
type Pattern interface {
	patternNode()
}

type BeginPattern struct{}
type EndPattern struct{}

// ExprPattern matches when the expression evaluates to true.
type ExprPattern struct {
	Expr Expr
}

// RangePattern matches from the first pattern to the second.
type RangePattern struct {
	Begin Expr
	End   Expr
}

func (BeginPattern) patternNode()  {}
func (EndPattern) patternNode()    {}
func (*ExprPattern) patternNode()  {}
func (*RangePattern) patternNode() {}

// Stmt types.
type Stmt interface {
	stmtNode()
}

type ExprStmt struct {
	Expr Expr
}

type PrintStmt struct {
	Args []Expr
	Dest *OutputDest
}

type PrintfStmt struct {
	Args []Expr
	Dest *OutputDest
}

// OutputDest represents an output redirection (blocked in safe mode).
type OutputDest struct {
	Type   outputDestType
	Target Expr
}

type outputDestType int

const (
	destFile   outputDestType = iota // > file (blocked)
	destAppend                       // >> file (blocked)
	destPipe                         // | cmd (blocked)
)

type IfStmt struct {
	Cond Expr
	Then []Stmt
	Else []Stmt
}

type WhileStmt struct {
	Cond Expr
	Body []Stmt
}

type DoWhileStmt struct {
	Cond Expr
	Body []Stmt
}

type ForStmt struct {
	Init Stmt
	Cond Expr
	Post Stmt
	Body []Stmt
}

type ForInStmt struct {
	Var   string
	Array string
	Body  []Stmt
}

type BreakStmt struct{}
type ContinueStmt struct{}
type NextStmt struct{}

type ExitStmt struct {
	Value Expr // may be nil
}

type DeleteStmt struct {
	Array string
	Index []Expr // nil means delete entire array
}

type BlockStmt struct {
	Stmts []Stmt
}

type ReturnStmt struct {
	Value Expr // may be nil
}

func (*ExprStmt) stmtNode()     {}
func (*PrintStmt) stmtNode()    {}
func (*PrintfStmt) stmtNode()   {}
func (*IfStmt) stmtNode()       {}
func (*WhileStmt) stmtNode()    {}
func (*DoWhileStmt) stmtNode()  {}
func (*ForStmt) stmtNode()      {}
func (*ForInStmt) stmtNode()    {}
func (*BreakStmt) stmtNode()    {}
func (*ContinueStmt) stmtNode() {}
func (*NextStmt) stmtNode()     {}
func (*ExitStmt) stmtNode()     {}
func (*DeleteStmt) stmtNode()   {}
func (*BlockStmt) stmtNode()    {}
func (*ReturnStmt) stmtNode()   {}

// Expr types.
type Expr interface {
	exprNode()
}

type NumberLit struct {
	Val string
}

type StringLit struct {
	Val string
}

type RegexLit struct {
	Val string
}

type UnaryExpr struct {
	Op   tokenType
	Expr Expr
}

type BinaryExpr struct {
	Op    tokenType
	Left  Expr
	Right Expr
}

type TernaryExpr struct {
	Cond Expr
	Then Expr
	Else Expr
}

type AssignExpr struct {
	Op     tokenType // tokASSIGN, tokPLUSASSIGN, etc.
	Target Expr
	Value  Expr
}

type IncrDecrExpr struct {
	Op   tokenType // tokINCR or tokDECR
	Expr Expr
	Pre  bool // prefix (++x) vs postfix (x++)
}

type FieldExpr struct {
	Index Expr
}

type VarExpr struct {
	Name string
}

type ArrayExpr struct {
	Name  string
	Index []Expr
}

type InExpr struct {
	Index []Expr
	Array string
}

type MatchExpr struct {
	Expr  Expr
	Regex Expr
	Not   bool
}

type ConcatExpr struct {
	Left  Expr
	Right Expr
}

type CallExpr struct {
	Name string
	Args []Expr
}

type GetlineExpr struct {
	// simplified: plain getline with no I/O
	Var string // optional variable to read into
}

// FuncDef is a user-defined function.
type FuncDef struct {
	Name   string
	Params []string
	Body   []Stmt
}

func (*NumberLit) exprNode()    {}
func (*StringLit) exprNode()    {}
func (*RegexLit) exprNode()     {}
func (*UnaryExpr) exprNode()    {}
func (*BinaryExpr) exprNode()   {}
func (*TernaryExpr) exprNode()  {}
func (*AssignExpr) exprNode()   {}
func (*IncrDecrExpr) exprNode() {}
func (*FieldExpr) exprNode()    {}
func (*VarExpr) exprNode()      {}
func (*ArrayExpr) exprNode()    {}
func (*InExpr) exprNode()       {}
func (*MatchExpr) exprNode()    {}
func (*ConcatExpr) exprNode()   {}
func (*CallExpr) exprNode()     {}
func (*GetlineExpr) exprNode()  {}
