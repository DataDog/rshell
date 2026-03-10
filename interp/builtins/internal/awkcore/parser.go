// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awkcore

import "fmt"

// Parse parses an awk program text into an AST.
func Parse(src string) (*Program, error) {
	tokens, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens, funcs: make(map[string]*FuncDef)}
	return p.parse()
}

type parser struct {
	tokens  []token
	pos     int
	funcs   map[string]*FuncDef
	inPrint bool
}

func (p *parser) parse() (*Program, error) {
	prog := &Program{Funcs: make(map[string]*FuncDef)}
	p.skipNewlines()
	for p.cur().typ != tokEOF {
		if err := p.parseTopLevel(prog); err != nil {
			return nil, err
		}
		p.skipNewlines()
	}
	prog.Funcs = p.funcs
	return prog, nil
}

func (p *parser) parseTopLevel(prog *Program) error {
	p.skipNewlines()

	if p.cur().typ == tokFUNCTION {
		return p.parseFuncDef()
	}

	if p.cur().typ == tokBEGIN {
		p.advance()
		p.skipNewlines()
		action, err := p.parseAction()
		if err != nil {
			return err
		}
		prog.Begin = append(prog.Begin, action)
		return nil
	}

	if p.cur().typ == tokEND {
		p.advance()
		p.skipNewlines()
		action, err := p.parseAction()
		if err != nil {
			return err
		}
		prog.End = append(prog.End, action)
		return nil
	}

	// pattern { action } or pattern or { action }
	if p.cur().typ == tokLBRACE {
		action, err := p.parseAction()
		if err != nil {
			return err
		}
		prog.Rules = append(prog.Rules, &Rule{Action: action})
		return nil
	}

	// Parse pattern.
	expr, err := p.parseExpr()
	if err != nil {
		return err
	}

	// Check for range pattern.
	if p.cur().typ == tokCOMMA {
		p.advance()
		p.skipNewlines()
		end, err := p.parseExpr()
		if err != nil {
			return err
		}
		p.skipNewlines()
		var action *Action
		if p.cur().typ == tokLBRACE {
			action, err = p.parseAction()
			if err != nil {
				return err
			}
		}
		prog.Rules = append(prog.Rules, &Rule{
			Pattern: &RangePattern{Begin: expr, End: end},
			Action:  action,
		})
		return nil
	}

	p.skipNewlines()
	var action *Action
	if p.cur().typ == tokLBRACE {
		action, err = p.parseAction()
		if err != nil {
			return err
		}
	}
	prog.Rules = append(prog.Rules, &Rule{
		Pattern: &ExprPattern{Expr: expr},
		Action:  action,
	})
	return nil
}

func (p *parser) parseFuncDef() error {
	p.advance() // skip 'function'
	p.skipNewlines()
	if p.cur().typ != tokIDENT {
		return p.errorf("expected function name")
	}
	name := p.cur().val
	p.advance()
	if p.cur().typ != tokLPAREN {
		return p.errorf("expected '(' after function name")
	}
	p.advance()
	var params []string
	for p.cur().typ != tokRPAREN {
		if p.cur().typ != tokIDENT {
			return p.errorf("expected parameter name")
		}
		params = append(params, p.cur().val)
		p.advance()
		if p.cur().typ == tokCOMMA {
			p.advance()
		}
	}
	p.advance() // skip ')'
	p.skipNewlines()
	action, err := p.parseAction()
	if err != nil {
		return err
	}
	p.funcs[name] = &FuncDef{Name: name, Params: params, Body: action.Stmts}
	return nil
}

func (p *parser) parseAction() (*Action, error) {
	if p.cur().typ != tokLBRACE {
		return nil, p.errorf("expected '{'")
	}
	p.advance()
	p.skipNewlines()
	var stmts []Stmt
	for p.cur().typ != tokRBRACE && p.cur().typ != tokEOF {
		stmt, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		if stmt != nil {
			stmts = append(stmts, stmt)
		}
		p.skipNewlines()
	}
	if p.cur().typ != tokRBRACE {
		return nil, p.errorf("expected '}'")
	}
	p.advance()
	return &Action{Stmts: stmts}, nil
}

func (p *parser) parseStmt() (Stmt, error) {
	switch p.cur().typ {
	case tokNEWLINE, tokSEMI:
		p.advance()
		return nil, nil
	case tokLBRACE:
		return p.parseBlockStmt()
	case tokIF:
		return p.parseIf()
	case tokWHILE:
		return p.parseWhile()
	case tokDO:
		return p.parseDo()
	case tokFOR:
		return p.parseFor()
	case tokBREAK:
		p.advance()
		return &BreakStmt{}, nil
	case tokCONTINUE:
		p.advance()
		return &ContinueStmt{}, nil
	case tokNEXT:
		p.advance()
		return &NextStmt{}, nil
	case tokEXIT:
		return p.parseExit()
	case tokDELETE:
		return p.parseDelete()
	case tokPRINT:
		return p.parsePrint()
	case tokPRINTF:
		return p.parsePrintf()
	case tokRETURN:
		return p.parseReturn()
	default:
		return p.parseExprStmt()
	}
}

func (p *parser) parseBlockStmt() (Stmt, error) {
	action, err := p.parseAction()
	if err != nil {
		return nil, err
	}
	return &BlockStmt{Stmts: action.Stmts}, nil
}

func (p *parser) parseIf() (Stmt, error) {
	p.advance() // skip 'if'
	p.skipNewlines()
	if p.cur().typ != tokLPAREN {
		return nil, p.errorf("expected '(' after if")
	}
	p.advance()
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.cur().typ != tokRPAREN {
		return nil, p.errorf("expected ')' after if condition")
	}
	p.advance()
	p.skipNewlines()
	thenStmts, err := p.parseStmtBody()
	if err != nil {
		return nil, err
	}
	var elseStmts []Stmt
	p.skipNewlines()
	if p.cur().typ == tokELSE {
		p.advance()
		p.skipNewlines()
		elseStmts, err = p.parseStmtBody()
		if err != nil {
			return nil, err
		}
	}
	return &IfStmt{Cond: cond, Then: thenStmts, Else: elseStmts}, nil
}

func (p *parser) parseStmtBody() ([]Stmt, error) {
	if p.cur().typ == tokLBRACE {
		action, err := p.parseAction()
		if err != nil {
			return nil, err
		}
		return action.Stmts, nil
	}
	stmt, err := p.parseStmt()
	if err != nil {
		return nil, err
	}
	if stmt == nil {
		return nil, nil
	}
	return []Stmt{stmt}, nil
}

func (p *parser) parseWhile() (Stmt, error) {
	p.advance() // skip 'while'
	p.skipNewlines()
	if p.cur().typ != tokLPAREN {
		return nil, p.errorf("expected '(' after while")
	}
	p.advance()
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.cur().typ != tokRPAREN {
		return nil, p.errorf("expected ')' after while condition")
	}
	p.advance()
	p.skipNewlines()
	body, err := p.parseStmtBody()
	if err != nil {
		return nil, err
	}
	return &WhileStmt{Cond: cond, Body: body}, nil
}

func (p *parser) parseDo() (Stmt, error) {
	p.advance() // skip 'do'
	p.skipNewlines()
	body, err := p.parseStmtBody()
	if err != nil {
		return nil, err
	}
	p.skipNewlines()
	if p.cur().typ != tokWHILE {
		return nil, p.errorf("expected 'while' after do body")
	}
	p.advance()
	p.skipNewlines()
	if p.cur().typ != tokLPAREN {
		return nil, p.errorf("expected '(' after while in do-while")
	}
	p.advance()
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.cur().typ != tokRPAREN {
		return nil, p.errorf("expected ')' in do-while")
	}
	p.advance()
	return &DoWhileStmt{Cond: cond, Body: body}, nil
}

func (p *parser) parseFor() (Stmt, error) {
	p.advance() // skip 'for'
	p.skipNewlines()
	if p.cur().typ != tokLPAREN {
		return nil, p.errorf("expected '(' after for")
	}
	p.advance()
	p.skipNewlines()

	// Check for 'for (var in array)'
	if p.cur().typ == tokIDENT {
		saved := p.pos
		varName := p.cur().val
		p.advance()
		if p.cur().typ == tokIN {
			p.advance()
			if p.cur().typ != tokIDENT {
				return nil, p.errorf("expected array name after 'in'")
			}
			arrayName := p.cur().val
			p.advance()
			if p.cur().typ != tokRPAREN {
				return nil, p.errorf("expected ')' in for-in")
			}
			p.advance()
			p.skipNewlines()
			body, err := p.parseStmtBody()
			if err != nil {
				return nil, err
			}
			return &ForInStmt{Var: varName, Array: arrayName, Body: body}, nil
		}
		p.pos = saved
	}

	// Standard C-style for loop.
	var init Stmt
	if p.cur().typ != tokSEMI {
		s, err := p.parseSimpleStmt()
		if err != nil {
			return nil, err
		}
		init = s
	}
	if p.cur().typ != tokSEMI {
		return nil, p.errorf("expected ';' in for loop")
	}
	p.advance()
	p.skipNewlines()
	var cond Expr
	if p.cur().typ != tokSEMI {
		var err error
		cond, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
	}
	if p.cur().typ != tokSEMI {
		return nil, p.errorf("expected ';' in for loop")
	}
	p.advance()
	p.skipNewlines()
	var post Stmt
	if p.cur().typ != tokRPAREN {
		s, err := p.parseSimpleStmt()
		if err != nil {
			return nil, err
		}
		post = s
	}
	if p.cur().typ != tokRPAREN {
		return nil, p.errorf("expected ')' in for loop")
	}
	p.advance()
	p.skipNewlines()
	body, err := p.parseStmtBody()
	if err != nil {
		return nil, err
	}
	return &ForStmt{Init: init, Cond: cond, Post: post, Body: body}, nil
}

func (p *parser) parseSimpleStmt() (Stmt, error) {
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &ExprStmt{Expr: expr}, nil
}

func (p *parser) parseExit() (Stmt, error) {
	p.advance() // skip 'exit'
	var val Expr
	if p.cur().typ != tokNEWLINE && p.cur().typ != tokSEMI &&
		p.cur().typ != tokRBRACE && p.cur().typ != tokEOF {
		var err error
		val, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
	}
	return &ExitStmt{Value: val}, nil
}

func (p *parser) parseDelete() (Stmt, error) {
	p.advance() // skip 'delete'
	if p.cur().typ != tokIDENT {
		return nil, p.errorf("expected array name after delete")
	}
	name := p.cur().val
	p.advance()
	if p.cur().typ == tokLBRACKET {
		p.advance()
		indices, err := p.parseExprList()
		if err != nil {
			return nil, err
		}
		if p.cur().typ != tokRBRACKET {
			return nil, p.errorf("expected ']' after delete index")
		}
		p.advance()
		return &DeleteStmt{Array: name, Index: indices}, nil
	}
	return &DeleteStmt{Array: name}, nil
}

func (p *parser) parsePrint() (Stmt, error) {
	p.advance() // skip 'print'
	var args []Expr
	var dest *OutputDest

	if p.cur().typ != tokNEWLINE && p.cur().typ != tokSEMI &&
		p.cur().typ != tokRBRACE && p.cur().typ != tokEOF &&
		p.cur().typ != tokPIPE && p.cur().typ != tokGT && p.cur().typ != tokAPPEND {
		exprs, err := p.parsePrintArgs()
		if err != nil {
			return nil, err
		}
		args = exprs
	}

	var err error
	dest, err = p.parseOutputDest()
	if err != nil {
		return nil, err
	}

	return &PrintStmt{Args: args, Dest: dest}, nil
}

func (p *parser) parsePrintf() (Stmt, error) {
	p.advance() // skip 'printf'
	args, err := p.parsePrintArgs()
	if err != nil {
		return nil, err
	}

	dest, err := p.parseOutputDest()
	if err != nil {
		return nil, err
	}

	return &PrintfStmt{Args: args, Dest: dest}, nil
}

func (p *parser) parsePrintArgs() ([]Expr, error) {
	saved := p.inPrint
	p.inPrint = true
	defer func() { p.inPrint = saved }()
	var args []Expr
	expr, err := p.parseNonAssignExpr()
	if err != nil {
		return nil, err
	}
	args = append(args, expr)
	for p.cur().typ == tokCOMMA {
		p.advance()
		p.skipNewlines()
		expr, err = p.parseNonAssignExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, expr)
	}
	return args, nil
}

func (p *parser) parseOutputDest() (*OutputDest, error) {
	var dtype outputDestType
	switch p.cur().typ {
	case tokGT:
		dtype = destFile
	case tokAPPEND:
		dtype = destAppend
	case tokPIPE:
		dtype = destPipe
	default:
		return nil, nil
	}
	p.advance()
	saved := p.inPrint
	p.inPrint = false
	defer func() { p.inPrint = saved }()
	target, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &OutputDest{Type: dtype, Target: target}, nil
}

func (p *parser) parseReturn() (Stmt, error) {
	p.advance() // skip 'return'
	var val Expr
	if p.cur().typ != tokNEWLINE && p.cur().typ != tokSEMI &&
		p.cur().typ != tokRBRACE && p.cur().typ != tokEOF {
		var err error
		val, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
	}
	return &ReturnStmt{Value: val}, nil
}

func (p *parser) parseExprStmt() (Stmt, error) {
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &ExprStmt{Expr: expr}, nil
}

// Expression parsing with precedence climbing.

func (p *parser) parseExpr() (Expr, error) {
	return p.parseAssign()
}

func (p *parser) parseNonAssignExpr() (Expr, error) {
	return p.parseTernary()
}

func (p *parser) parseAssign() (Expr, error) {
	expr, err := p.parseTernary()
	if err != nil {
		return nil, err
	}

	switch p.cur().typ {
	case tokASSIGN, tokPLUSASSIGN, tokMINUSASSIGN, tokSTARASSIGN,
		tokSLASHASSIGN, tokPERCENTASSIGN, tokPOWERASSIGN:
		op := p.cur().typ
		p.advance()
		val, err := p.parseAssign()
		if err != nil {
			return nil, err
		}
		return &AssignExpr{Op: op, Target: expr, Value: val}, nil
	}
	return expr, nil
}

func (p *parser) parseTernary() (Expr, error) {
	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.cur().typ == tokQUESTION {
		p.advance()
		then, err := p.parseAssign()
		if err != nil {
			return nil, err
		}
		if p.cur().typ != tokCOLON {
			return nil, p.errorf("expected ':' in ternary expression")
		}
		p.advance()
		els, err := p.parseAssign()
		if err != nil {
			return nil, err
		}
		return &TernaryExpr{Cond: expr, Then: then, Else: els}, nil
	}
	return expr, nil
}

func (p *parser) parseOr() (Expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.cur().typ == tokOR {
		p.advance()
		p.skipNewlines()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: tokOR, Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (Expr, error) {
	left, err := p.parseIn()
	if err != nil {
		return nil, err
	}
	for p.cur().typ == tokAND {
		p.advance()
		p.skipNewlines()
		right, err := p.parseIn()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: tokAND, Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseIn() (Expr, error) {
	left, err := p.parseMatch()
	if err != nil {
		return nil, err
	}
	if p.cur().typ == tokIN {
		p.advance()
		if p.cur().typ != tokIDENT {
			return nil, p.errorf("expected array name after 'in'")
		}
		name := p.cur().val
		p.advance()
		idxList := []Expr{left}
		return &InExpr{Index: idxList, Array: name}, nil
	}
	return left, nil
}

func (p *parser) parseMatch() (Expr, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.cur().typ == tokMATCH || p.cur().typ == tokNOTMATCH {
		not := p.cur().typ == tokNOTMATCH
		p.advance()
		p.skipNewlines()
		right, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		left = &MatchExpr{Expr: left, Regex: right, Not: not}
	}
	return left, nil
}

func (p *parser) parseComparison() (Expr, error) {
	left, err := p.parseConcat()
	if err != nil {
		return nil, err
	}
	for p.cur().typ == tokLT || p.cur().typ == tokLE ||
		(!p.inPrint && p.cur().typ == tokGT) || p.cur().typ == tokGE ||
		p.cur().typ == tokEQ || p.cur().typ == tokNE {
		op := p.cur().typ
		p.advance()
		p.skipNewlines()
		right, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseConcat() (Expr, error) {
	left, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	for p.canStartConcat() {
		right, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		left = &ConcatExpr{Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) canStartConcat() bool {
	t := p.cur().typ
	return t == tokNUMBER || t == tokSTRING || t == tokIDENT ||
		t == tokDOLLAR || t == tokLPAREN || t == tokNOT ||
		t == tokINCR || t == tokDECR
}

func (p *parser) parseAdd() (Expr, error) {
	left, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.cur().typ == tokPLUS || p.cur().typ == tokMINUS {
		op := p.cur().typ
		p.advance()
		p.skipNewlines()
		right, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseMul() (Expr, error) {
	left, err := p.parsePower()
	if err != nil {
		return nil, err
	}
	for p.cur().typ == tokSTAR || p.cur().typ == tokSLASH || p.cur().typ == tokPERCENT {
		op := p.cur().typ
		p.advance()
		p.skipNewlines()
		right, err := p.parsePower()
		if err != nil {
			return nil, err
		}
		left = &BinaryExpr{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parsePower() (Expr, error) {
	base, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	if p.cur().typ == tokPOWER {
		p.advance()
		p.skipNewlines()
		exp, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &BinaryExpr{Op: tokPOWER, Left: base, Right: exp}, nil
	}
	return base, nil
}

func (p *parser) parseUnary() (Expr, error) {
	if p.cur().typ == tokNOT {
		p.advance()
		expr, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: tokNOT, Expr: expr}, nil
	}
	if p.cur().typ == tokMINUS {
		p.advance()
		expr, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &UnaryExpr{Op: tokMINUS, Expr: expr}, nil
	}
	if p.cur().typ == tokPLUS {
		p.advance()
		return p.parseUnary()
	}
	if p.cur().typ == tokINCR || p.cur().typ == tokDECR {
		op := p.cur().typ
		p.advance()
		expr, err := p.parsePostfix()
		if err != nil {
			return nil, err
		}
		return &IncrDecrExpr{Op: op, Expr: expr, Pre: true}, nil
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() (Expr, error) {
	expr, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	if p.cur().typ == tokINCR || p.cur().typ == tokDECR {
		op := p.cur().typ
		p.advance()
		return &IncrDecrExpr{Op: op, Expr: expr, Pre: false}, nil
	}
	return expr, nil
}

func (p *parser) parsePrimary() (Expr, error) {
	switch p.cur().typ {
	case tokNUMBER:
		val := p.cur().val
		p.advance()
		return &NumberLit{Val: val}, nil
	case tokSTRING:
		val := p.cur().val
		p.advance()
		return &StringLit{Val: val}, nil
	case tokREGEX:
		val := p.cur().val
		p.advance()
		return &RegexLit{Val: val}, nil
	case tokDOLLAR:
		p.advance()
		expr, err := p.parsePrimary()
		if err != nil {
			return nil, err
		}
		return &FieldExpr{Index: expr}, nil
	case tokLPAREN:
		p.advance()
		saved := p.inPrint
		p.inPrint = false
		expr, err := p.parseExpr()
		p.inPrint = saved
		if err != nil {
			return nil, err
		}
		if p.cur().typ != tokRPAREN {
			return nil, p.errorf("expected ')'")
		}
		p.advance()
		return expr, nil
	case tokIDENT:
		return p.parseIdentExpr()
	case tokGETLINE:
		return p.parseGetline()
	default:
		return nil, p.errorf("unexpected token %q", p.cur().val)
	}
}

func (p *parser) parseIdentExpr() (Expr, error) {
	name := p.cur().val
	p.advance()

	// Function call: ident(args...)
	if p.cur().typ == tokLPAREN {
		p.advance()
		var args []Expr
		if p.cur().typ != tokRPAREN {
			exprs, err := p.parseExprList()
			if err != nil {
				return nil, err
			}
			args = exprs
		}
		if p.cur().typ != tokRPAREN {
			return nil, p.errorf("expected ')' after function arguments")
		}
		p.advance()
		return &CallExpr{Name: name, Args: args}, nil
	}

	// Array subscript: ident[expr]
	if p.cur().typ == tokLBRACKET {
		p.advance()
		indices, err := p.parseExprList()
		if err != nil {
			return nil, err
		}
		if p.cur().typ != tokRBRACKET {
			return nil, p.errorf("expected ']' after array index")
		}
		p.advance()
		return &ArrayExpr{Name: name, Index: indices}, nil
	}

	return &VarExpr{Name: name}, nil
}

func (p *parser) parseGetline() (Expr, error) {
	p.advance() // skip 'getline'
	var varName string
	if p.cur().typ == tokIDENT {
		varName = p.cur().val
		p.advance()
	}
	return &GetlineExpr{Var: varName}, nil
}

func (p *parser) parseExprList() ([]Expr, error) {
	var exprs []Expr
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	exprs = append(exprs, expr)
	for p.cur().typ == tokCOMMA {
		p.advance()
		p.skipNewlines()
		expr, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, expr)
	}
	return exprs, nil
}

// Helper methods.

func (p *parser) cur() token {
	if p.pos >= len(p.tokens) {
		return token{typ: tokEOF}
	}
	return p.tokens[p.pos]
}

func (p *parser) advance() {
	if p.pos < len(p.tokens) {
		p.pos++
	}
}

func (p *parser) skipNewlines() {
	for p.cur().typ == tokNEWLINE || p.cur().typ == tokSEMI {
		p.advance()
	}
}

func (p *parser) errorf(format string, args ...any) error {
	pos := 0
	if p.pos < len(p.tokens) {
		pos = p.tokens[p.pos].pos
	}
	return fmt.Errorf("parse error at position %d: %s", pos, fmt.Sprintf(format, args...))
}
