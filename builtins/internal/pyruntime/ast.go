// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package pyruntime

// Pos represents a source position.
type Pos struct {
	Line int
	Col  int
}

// Node is the base interface for all AST nodes.
type Node interface {
	nodePos() Pos
}

// Stmt is an alias for Node representing a statement.
type Stmt = Node

// Expr is an alias for Node representing an expression.
type Expr = Node

// ---- Statements ----

// Module is the top-level AST node.
type Module struct {
	Pos
	Body []Stmt
}

func (n *Module) nodePos() Pos { return n.Pos }

// AssignStmt handles assignment: a = b = c and tuple unpacking.
type AssignStmt struct {
	Pos
	Targets []Expr
	Value   Expr
}

func (n *AssignStmt) nodePos() Pos { return n.Pos }

// AugAssignStmt handles augmented assignment: a += b.
type AugAssignStmt struct {
	Pos
	Target Expr
	Op     string
	Value  Expr
}

func (n *AugAssignStmt) nodePos() Pos { return n.Pos }

// AnnAssignStmt handles annotated assignment: x: int = 5.
type AnnAssignStmt struct {
	Pos
	Target     Expr
	Annotation Expr
	Value      Expr // may be nil
}

func (n *AnnAssignStmt) nodePos() Pos { return n.Pos }

// ExprStmt wraps a bare expression used as a statement.
type ExprStmt struct {
	Pos
	Value Expr
}

func (n *ExprStmt) nodePos() Pos { return n.Pos }

// IfStmt represents an if/elif/else construct.
type IfStmt struct {
	Pos
	Test   Expr
	Body   []Stmt
	Orelse []Stmt
}

func (n *IfStmt) nodePos() Pos { return n.Pos }

// WhileStmt represents a while loop.
type WhileStmt struct {
	Pos
	Test   Expr
	Body   []Stmt
	Orelse []Stmt
}

func (n *WhileStmt) nodePos() Pos { return n.Pos }

// ForStmt represents a for loop.
type ForStmt struct {
	Pos
	Target Expr
	Iter   Expr
	Body   []Stmt
	Orelse []Stmt
}

func (n *ForStmt) nodePos() Pos { return n.Pos }

// FuncDef represents a function definition.
// IsGen is true if the body contains a yield expression.
type FuncDef struct {
	Pos
	Name       string
	Args       *Arguments
	Body       []Stmt
	Decorators []Expr
	IsGen      bool
}

func (n *FuncDef) nodePos() Pos { return n.Pos }

// ClassDef represents a class definition.
type ClassDef struct {
	Pos
	Name       string
	Bases      []Expr
	Body       []Stmt
	Decorators []Expr
}

func (n *ClassDef) nodePos() Pos { return n.Pos }

// ReturnStmt represents a return statement. Value may be nil.
type ReturnStmt struct {
	Pos
	Value Expr // may be nil
}

func (n *ReturnStmt) nodePos() Pos { return n.Pos }

// BreakStmt represents a break statement.
type BreakStmt struct {
	Pos
}

func (n *BreakStmt) nodePos() Pos { return n.Pos }

// ContinueStmt represents a continue statement.
type ContinueStmt struct {
	Pos
}

func (n *ContinueStmt) nodePos() Pos { return n.Pos }

// PassStmt represents a pass statement.
type PassStmt struct {
	Pos
}

func (n *PassStmt) nodePos() Pos { return n.Pos }

// RaiseStmt represents a raise statement. Both Exc and Cause may be nil.
type RaiseStmt struct {
	Pos
	Exc   Expr // may be nil
	Cause Expr // may be nil
}

func (n *RaiseStmt) nodePos() Pos { return n.Pos }

// TryStmt represents a try/except/else/finally construct.
type TryStmt struct {
	Pos
	Body     []Stmt
	Handlers []*ExceptHandler
	Orelse   []Stmt
	Finally  []Stmt
}

func (n *TryStmt) nodePos() Pos { return n.Pos }

// WithStmt represents a with statement.
type WithStmt struct {
	Pos
	Items []*WithItem
	Body  []Stmt
}

func (n *WithStmt) nodePos() Pos { return n.Pos }

// ImportStmt represents an import statement: import X, import X as Y.
type ImportStmt struct {
	Pos
	Names []ImportName
}

func (n *ImportStmt) nodePos() Pos { return n.Pos }

// ImportFromStmt represents a from X import a, b statement.
// Names[0].Name == "*" for star import.
type ImportFromStmt struct {
	Pos
	Module string
	Names  []ImportName
}

func (n *ImportFromStmt) nodePos() Pos { return n.Pos }

// GlobalStmt represents a global declaration.
type GlobalStmt struct {
	Pos
	Names []string
}

func (n *GlobalStmt) nodePos() Pos { return n.Pos }

// NonlocalStmt represents a nonlocal declaration.
type NonlocalStmt struct {
	Pos
	Names []string
}

func (n *NonlocalStmt) nodePos() Pos { return n.Pos }

// DelStmt represents a del statement.
type DelStmt struct {
	Pos
	Targets []Expr
}

func (n *DelStmt) nodePos() Pos { return n.Pos }

// AssertStmt represents an assert statement. Msg may be nil.
type AssertStmt struct {
	Pos
	Test Expr
	Msg  Expr // may be nil
}

func (n *AssertStmt) nodePos() Pos { return n.Pos }

// ---- Helper types ----

// ExceptHandler is a single except clause. Type may be nil for bare except.
type ExceptHandler struct {
	Pos
	Type Expr   // may be nil
	Name string // may be ""
	Body []Stmt
}

// WithItem is a single item in a with statement.
type WithItem struct {
	CtxExpr Expr
	OptVar  Expr // may be nil
}

// ImportName holds a single import name and its optional alias.
type ImportName struct {
	Name  string
	Alias string // may be ""
}

// Arguments describes the argument specification of a function.
type Arguments struct {
	Args       []string
	Defaults   []Expr
	Vararg     string // "" if no *args
	Kwarg      string // "" if no **kwargs
	KwOnly     []string
	KwDefaults []Expr
}

// ---- Expressions ----

// BinOp represents a binary operation.
type BinOp struct {
	Pos
	Left  Expr
	Right Expr
	Op    string
}

func (n *BinOp) nodePos() Pos { return n.Pos }

// UnaryOp represents a unary operation. Op: "-", "+", "~", "not".
type UnaryOp struct {
	Pos
	Operand Expr
	Op      string
}

func (n *UnaryOp) nodePos() Pos { return n.Pos }

// BoolOp represents a boolean operation. Op: "and" or "or".
type BoolOp struct {
	Pos
	Op     string
	Values []Expr
}

func (n *BoolOp) nodePos() Pos { return n.Pos }

// Compare represents a chained comparison: a < b <= c.
type Compare struct {
	Pos
	Left        Expr
	Ops         []string
	Comparators []Expr
}

func (n *Compare) nodePos() Pos { return n.Pos }

// CallExpr represents a function call.
type CallExpr struct {
	Pos
	Func     Expr
	Args     []Expr
	Keywords []*Keyword
	Starargs []Expr
	Kwargs   []Expr
}

func (n *CallExpr) nodePos() Pos { return n.Pos }

// AttributeExpr represents attribute access: value.attr.
type AttributeExpr struct {
	Pos
	Value Expr
	Attr  string
}

func (n *AttributeExpr) nodePos() Pos { return n.Pos }

// SubscriptExpr represents subscript access: value[slice].
type SubscriptExpr struct {
	Pos
	Value Expr
	Slice Expr
}

func (n *SubscriptExpr) nodePos() Pos { return n.Pos }

// SliceExpr represents a slice: lower:upper:step. Any field may be nil.
type SliceExpr struct {
	Pos
	Lower Expr // may be nil
	Upper Expr // may be nil
	Step  Expr // may be nil
}

func (n *SliceExpr) nodePos() Pos { return n.Pos }

// NameExpr represents a name reference.
type NameExpr struct {
	Pos
	Id string
}

func (n *NameExpr) nodePos() Pos { return n.Pos }

// Constant represents a literal value.
// Value holds: int64, float64, string, []byte, bool, or nil.
type Constant struct {
	Pos
	Value interface{}
}

func (n *Constant) nodePos() Pos { return n.Pos }

// ListExpr represents a list literal.
type ListExpr struct {
	Pos
	Elts []Expr
}

func (n *ListExpr) nodePos() Pos { return n.Pos }

// TupleExpr represents a tuple literal.
type TupleExpr struct {
	Pos
	Elts []Expr
}

func (n *TupleExpr) nodePos() Pos { return n.Pos }

// DictExpr represents a dict literal. Key==nil means **unpack.
type DictExpr struct {
	Pos
	Keys   []Expr
	Values []Expr
}

func (n *DictExpr) nodePos() Pos { return n.Pos }

// SetExpr represents a set literal.
type SetExpr struct {
	Pos
	Elts []Expr
}

func (n *SetExpr) nodePos() Pos { return n.Pos }

// IfExp represents a ternary expression: body if test else orelse.
type IfExp struct {
	Pos
	Test   Expr
	Body   Expr
	Orelse Expr
}

func (n *IfExp) nodePos() Pos { return n.Pos }

// Lambda represents a lambda expression.
type Lambda struct {
	Pos
	Args *Arguments
	Body Expr
}

func (n *Lambda) nodePos() Pos { return n.Pos }

// ListComp represents a list comprehension.
type ListComp struct {
	Pos
	Elt        Expr
	Generators []*Comprehension
}

func (n *ListComp) nodePos() Pos { return n.Pos }

// DictComp represents a dict comprehension.
type DictComp struct {
	Pos
	Key        Expr
	Value      Expr
	Generators []*Comprehension
}

func (n *DictComp) nodePos() Pos { return n.Pos }

// SetComp represents a set comprehension.
type SetComp struct {
	Pos
	Elt        Expr
	Generators []*Comprehension
}

func (n *SetComp) nodePos() Pos { return n.Pos }

// GeneratorExp represents a generator expression.
type GeneratorExp struct {
	Pos
	Elt        Expr
	Generators []*Comprehension
}

func (n *GeneratorExp) nodePos() Pos { return n.Pos }

// Yield represents a yield expression. Value may be nil.
type Yield struct {
	Pos
	Value Expr // may be nil
}

func (n *Yield) nodePos() Pos { return n.Pos }

// YieldFrom represents a yield from expression.
type YieldFrom struct {
	Pos
	Value Expr
}

func (n *YieldFrom) nodePos() Pos { return n.Pos }

// Starred represents a starred expression: *x.
type Starred struct {
	Pos
	Value Expr
}

func (n *Starred) nodePos() Pos { return n.Pos }

// Comprehension represents a single for clause in a comprehension.
type Comprehension struct {
	Target Expr
	Iter   Expr
	Ifs    []Expr
}

// Keyword represents a keyword argument. Arg=="" means **unpack.
type Keyword struct {
	Arg   string // "" for **unpack
	Value Expr
}

// containsYield walks stmts recursively looking for Yield or YieldFrom nodes.
// It does NOT recurse into nested FuncDef or Lambda bodies.
func containsYield(stmts []Stmt) bool {
	for _, s := range stmts {
		if yieldInStmt(s) {
			return true
		}
	}
	return false
}

func yieldInStmt(s Stmt) bool {
	switch n := s.(type) {
	case *ExprStmt:
		return yieldInExpr(n.Value)
	case *AssignStmt:
		if yieldInExpr(n.Value) {
			return true
		}
		for _, t := range n.Targets {
			if yieldInExpr(t) {
				return true
			}
		}
	case *AugAssignStmt:
		return yieldInExpr(n.Value)
	case *AnnAssignStmt:
		return yieldInExpr(n.Value)
	case *ReturnStmt:
		return yieldInExpr(n.Value)
	case *IfStmt:
		return yieldInExpr(n.Test) || containsYield(n.Body) || containsYield(n.Orelse)
	case *WhileStmt:
		return yieldInExpr(n.Test) || containsYield(n.Body) || containsYield(n.Orelse)
	case *ForStmt:
		return yieldInExpr(n.Iter) || containsYield(n.Body) || containsYield(n.Orelse)
	case *TryStmt:
		if containsYield(n.Body) || containsYield(n.Orelse) || containsYield(n.Finally) {
			return true
		}
		for _, h := range n.Handlers {
			if containsYield(h.Body) {
				return true
			}
		}
	case *WithStmt:
		return containsYield(n.Body)
	case *RaiseStmt:
		return yieldInExpr(n.Exc) || yieldInExpr(n.Cause)
	case *DelStmt:
		for _, t := range n.Targets {
			if yieldInExpr(t) {
				return true
			}
		}
	case *AssertStmt:
		return yieldInExpr(n.Test) || yieldInExpr(n.Msg)
		// FuncDef and ClassDef: do NOT recurse into nested function bodies
	}
	return false
}

func yieldInExpr(e Expr) bool {
	if e == nil {
		return false
	}
	switch n := e.(type) {
	case *Yield:
		return true
	case *YieldFrom:
		return true
	case *BinOp:
		return yieldInExpr(n.Left) || yieldInExpr(n.Right)
	case *UnaryOp:
		return yieldInExpr(n.Operand)
	case *BoolOp:
		for _, v := range n.Values {
			if yieldInExpr(v) {
				return true
			}
		}
	case *Compare:
		if yieldInExpr(n.Left) {
			return true
		}
		for _, c := range n.Comparators {
			if yieldInExpr(c) {
				return true
			}
		}
	case *CallExpr:
		if yieldInExpr(n.Func) {
			return true
		}
		for _, a := range n.Args {
			if yieldInExpr(a) {
				return true
			}
		}
		for _, kw := range n.Keywords {
			if yieldInExpr(kw.Value) {
				return true
			}
		}
	case *AttributeExpr:
		return yieldInExpr(n.Value)
	case *SubscriptExpr:
		return yieldInExpr(n.Value) || yieldInExpr(n.Slice)
	case *SliceExpr:
		return yieldInExpr(n.Lower) || yieldInExpr(n.Upper) || yieldInExpr(n.Step)
	case *ListExpr:
		for _, elt := range n.Elts {
			if yieldInExpr(elt) {
				return true
			}
		}
	case *TupleExpr:
		for _, elt := range n.Elts {
			if yieldInExpr(elt) {
				return true
			}
		}
	case *DictExpr:
		for i, k := range n.Keys {
			if yieldInExpr(k) || yieldInExpr(n.Values[i]) {
				return true
			}
		}
	case *SetExpr:
		for _, elt := range n.Elts {
			if yieldInExpr(elt) {
				return true
			}
		}
	case *IfExp:
		return yieldInExpr(n.Test) || yieldInExpr(n.Body) || yieldInExpr(n.Orelse)
	case *Starred:
		return yieldInExpr(n.Value)
		// Lambda: do NOT recurse into lambda body
	}
	return false
}
