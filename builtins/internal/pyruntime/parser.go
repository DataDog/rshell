// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package pyruntime

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode/utf8"
)

// SyntaxError is returned for parse errors.
type SyntaxError struct {
	Msg  string
	Pos  Pos
	File string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("%s:%d:%d: SyntaxError: %s", e.File, e.Pos.Line, e.Pos.Col, e.Msg)
}

// Parse parses Python source code and returns the module AST or a SyntaxError.
func Parse(src, filename string) (*Module, error) {
	p := &Parser{
		lex:      NewLexer(src),
		filename: filename,
	}
	// Prime the two-token lookahead.
	p.cur = p.lex.Next()
	p.peek = p.lex.Next()

	return p.parseModule()
}

// Parser is a recursive-descent Python parser.
type Parser struct {
	lex      *Lexer
	cur      Token
	peek     Token
	filename string
}

// advance consumes the current token, shifting the lookahead window.
func (p *Parser) advance() Token {
	t := p.cur
	p.cur = p.peek
	p.peek = p.lex.Next()
	return t
}

// expect asserts that the current token matches and advances.
func (p *Parser) expect(kind TokenKind, value string) (Token, error) {
	if p.cur.Kind != kind || (value != "" && p.cur.Value != value) {
		return Token{}, p.syntaxErrorf("expected %s %q, got %s %q",
			tokenKindString(kind), value, tokenKindString(p.cur.Kind), p.cur.Value)
	}
	return p.advance(), nil
}

// check returns true if the current token matches without consuming.
func (p *Parser) check(kind TokenKind, value string) bool {
	return p.cur.Kind == kind && (value == "" || p.cur.Value == value)
}

// match consumes and returns true if the current token matches.
func (p *Parser) match(kind TokenKind, value string) bool {
	if p.check(kind, value) {
		p.advance()
		return true
	}
	return false
}

// syntaxErrorf creates a SyntaxError at the current position.
func (p *Parser) syntaxErrorf(format string, args ...interface{}) *SyntaxError {
	return &SyntaxError{
		Msg:  fmt.Sprintf(format, args...),
		Pos:  p.cur.Pos,
		File: p.filename,
	}
}

// syntaxErrorAt creates a SyntaxError at a specific position.
func (p *Parser) syntaxErrorAt(pos Pos, format string, args ...interface{}) *SyntaxError {
	return &SyntaxError{
		Msg:  fmt.Sprintf(format, args...),
		Pos:  pos,
		File: p.filename,
	}
}

// skipNewlines skips over any newline tokens.
func (p *Parser) skipNewlines() {
	for p.cur.Kind == TokNewline {
		p.advance()
	}
}

// ---- Top-level parsing ----

func (p *Parser) parseModule() (*Module, error) {
	mod := &Module{Pos: p.cur.Pos}
	p.skipNewlines()
	for p.cur.Kind != TokEOF {
		stmts, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		mod.Body = append(mod.Body, stmts...)
		p.skipNewlines()
	}
	return mod, nil
}

// parseStmtList parses an indented block: INDENT stmts DEDENT.
func (p *Parser) parseStmtList() ([]Stmt, error) {
	if _, err := p.expect(TokIndent, ""); err != nil {
		return nil, err
	}
	var stmts []Stmt
	p.skipNewlines()
	for p.cur.Kind != TokDedent && p.cur.Kind != TokEOF {
		ss, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, ss...)
		p.skipNewlines()
	}
	if p.cur.Kind == TokDedent {
		p.advance()
	}
	return stmts, nil
}

// parseStmt dispatches to the appropriate statement parser.
func (p *Parser) parseStmt() ([]Stmt, error) {
	// Handle decorators.
	if p.check(TokOp, "@") {
		s, err := p.parseDecorated()
		if err != nil {
			return nil, err
		}
		return []Stmt{s}, nil
	}

	if p.cur.Kind == TokName {
		switch p.cur.Value {
		case "if":
			s, err := p.parseIfProper()
			if err != nil {
				return nil, err
			}
			return []Stmt{s}, nil
		case "while":
			s, err := p.parseWhile()
			if err != nil {
				return nil, err
			}
			return []Stmt{s}, nil
		case "for":
			s, err := p.parseFor()
			if err != nil {
				return nil, err
			}
			return []Stmt{s}, nil
		case "def":
			s, err := p.parseFuncDef(nil)
			if err != nil {
				return nil, err
			}
			return []Stmt{s}, nil
		case "async":
			// async def or async for — treat async def as regular def
			if p.peek.Kind == TokName && p.peek.Value == "def" {
				p.advance() // consume 'async'
				s, err := p.parseFuncDef(nil)
				if err != nil {
					return nil, err
				}
				return []Stmt{s}, nil
			}
			if p.peek.Kind == TokName && p.peek.Value == "for" {
				p.advance() // consume 'async'
				s, err := p.parseFor()
				if err != nil {
					return nil, err
				}
				return []Stmt{s}, nil
			}
			// fall through to simple stmt
		case "class":
			s, err := p.parseClassDef(nil)
			if err != nil {
				return nil, err
			}
			return []Stmt{s}, nil
		case "try":
			s, err := p.parseTry()
			if err != nil {
				return nil, err
			}
			return []Stmt{s}, nil
		case "with":
			s, err := p.parseWith()
			if err != nil {
				return nil, err
			}
			return []Stmt{s}, nil
		}
	}

	return p.parseSimpleStmts()
}

// parseSimpleStmts parses one or more semicolon-separated simple statements
// terminated by a newline or EOF.
func (p *Parser) parseSimpleStmts() ([]Stmt, error) {
	var stmts []Stmt
	s, err := p.parseSimpleStmt()
	if err != nil {
		return nil, err
	}
	stmts = append(stmts, s)
	for p.match(TokOp, ";") {
		if p.cur.Kind == TokNewline || p.cur.Kind == TokEOF {
			break
		}
		s, err = p.parseSimpleStmt()
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, s)
	}
	// consume trailing newline
	if p.cur.Kind == TokNewline {
		p.advance()
	}
	return stmts, nil
}

// parseSimpleStmt parses a single simple statement.
func (p *Parser) parseSimpleStmt() (Stmt, error) {
	if p.cur.Kind != TokName {
		return p.parseAssignOrExprStmt()
	}
	pos := p.cur.Pos
	switch p.cur.Value {
	case "return":
		return p.parseReturn()
	case "raise":
		return p.parseRaise()
	case "del":
		return p.parseDel()
	case "pass":
		p.advance()
		return &PassStmt{Pos: pos}, nil
	case "break":
		p.advance()
		return &BreakStmt{Pos: pos}, nil
	case "continue":
		p.advance()
		return &ContinueStmt{Pos: pos}, nil
	case "global":
		return p.parseGlobal()
	case "nonlocal":
		return p.parseNonlocal()
	case "assert":
		return p.parseAssert()
	case "import":
		return p.parseImport()
	case "from":
		return p.parseFromImport()
	case "yield":
		e, err := p.parseYieldExpr()
		if err != nil {
			return nil, err
		}
		return &ExprStmt{Pos: pos, Value: e}, nil
	}
	return p.parseAssignOrExprStmt()
}

// ---- Compound statements ----

func (p *Parser) parseIfProper() (Stmt, error) {
	pos := p.cur.Pos
	p.advance() // consume 'if'
	test, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokOp, ":"); err != nil {
		return nil, err
	}
	body, err := p.parseSuite()
	if err != nil {
		return nil, err
	}

	outer := &IfStmt{Pos: pos, Test: test, Body: body}
	current := outer

	for p.cur.Kind == TokName && p.cur.Value == "elif" {
		elifPos := p.cur.Pos
		p.advance()
		elifTest, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokOp, ":"); err != nil {
			return nil, err
		}
		elifBody, err := p.parseSuite()
		if err != nil {
			return nil, err
		}
		nested := &IfStmt{Pos: elifPos, Test: elifTest, Body: elifBody}
		current.Orelse = []Stmt{nested}
		current = nested
	}

	if p.cur.Kind == TokName && p.cur.Value == "else" {
		p.advance()
		if _, err := p.expect(TokOp, ":"); err != nil {
			return nil, err
		}
		elseBody, err := p.parseSuite()
		if err != nil {
			return nil, err
		}
		current.Orelse = elseBody
	}

	return outer, nil
}

func (p *Parser) parseWhile() (Stmt, error) {
	pos := p.cur.Pos
	p.advance() // consume 'while'
	test, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokOp, ":"); err != nil {
		return nil, err
	}
	body, err := p.parseSuite()
	if err != nil {
		return nil, err
	}
	s := &WhileStmt{Pos: pos, Test: test, Body: body}
	if p.cur.Kind == TokName && p.cur.Value == "else" {
		p.advance()
		if _, err := p.expect(TokOp, ":"); err != nil {
			return nil, err
		}
		orelse, err := p.parseSuite()
		if err != nil {
			return nil, err
		}
		s.Orelse = orelse
	}
	return s, nil
}

func (p *Parser) parseFor() (Stmt, error) {
	pos := p.cur.Pos
	p.advance() // consume 'for'
	target, err := p.parseTargetList()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokName, "in"); err != nil {
		return nil, err
	}
	iter, err := p.parseTestList()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokOp, ":"); err != nil {
		return nil, err
	}
	body, err := p.parseSuite()
	if err != nil {
		return nil, err
	}
	s := &ForStmt{Pos: pos, Target: target, Iter: iter, Body: body}
	if p.cur.Kind == TokName && p.cur.Value == "else" {
		p.advance()
		if _, err := p.expect(TokOp, ":"); err != nil {
			return nil, err
		}
		orelse, err := p.parseSuite()
		if err != nil {
			return nil, err
		}
		s.Orelse = orelse
	}
	return s, nil
}

func (p *Parser) parseFuncDef(decorators []Expr) (Stmt, error) {
	pos := p.cur.Pos
	p.advance() // consume 'def'
	nameTok, err := p.expect(TokName, "")
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokOp, "("); err != nil {
		return nil, err
	}
	args, err := p.parseFuncArgs()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(TokOp, ")"); err != nil {
		return nil, err
	}
	// Optional return annotation: -> expr
	if p.match(TokOp, "->") {
		_, err = p.parseExpr() // discard annotation
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(TokOp, ":"); err != nil {
		return nil, err
	}
	body, err := p.parseSuite()
	if err != nil {
		return nil, err
	}
	isGen := containsYield(body)
	return &FuncDef{
		Pos:        pos,
		Name:       nameTok.Value,
		Args:       args,
		Body:       body,
		Decorators: decorators,
		IsGen:      isGen,
	}, nil
}

func (p *Parser) parseClassDef(decorators []Expr) (Stmt, error) {
	pos := p.cur.Pos
	p.advance() // consume 'class'
	nameTok, err := p.expect(TokName, "")
	if err != nil {
		return nil, err
	}
	var bases []Expr
	if p.match(TokOp, "(") {
		if !p.check(TokOp, ")") {
			bases, err = p.parseArgList()
			if err != nil {
				return nil, err
			}
		}
		if _, err := p.expect(TokOp, ")"); err != nil {
			return nil, err
		}
	}
	if _, err := p.expect(TokOp, ":"); err != nil {
		return nil, err
	}
	body, err := p.parseSuite()
	if err != nil {
		return nil, err
	}
	return &ClassDef{
		Pos:        pos,
		Name:       nameTok.Value,
		Bases:      bases,
		Body:       body,
		Decorators: decorators,
	}, nil
}

func (p *Parser) parseTry() (Stmt, error) {
	pos := p.cur.Pos
	p.advance() // consume 'try'
	if _, err := p.expect(TokOp, ":"); err != nil {
		return nil, err
	}
	body, err := p.parseSuite()
	if err != nil {
		return nil, err
	}

	var handlers []*ExceptHandler
	var orelse, finally []Stmt

	// except clauses
	for p.cur.Kind == TokName && p.cur.Value == "except" {
		hPos := p.cur.Pos
		p.advance()
		h := &ExceptHandler{Pos: hPos}
		if !p.check(TokOp, ":") {
			h.Type, err = p.parseExpr()
			if err != nil {
				return nil, err
			}
			if p.match(TokName, "as") {
				nameTok, err := p.expect(TokName, "")
				if err != nil {
					return nil, err
				}
				h.Name = nameTok.Value
			}
		}
		if _, err := p.expect(TokOp, ":"); err != nil {
			return nil, err
		}
		h.Body, err = p.parseSuite()
		if err != nil {
			return nil, err
		}
		handlers = append(handlers, h)
	}

	if p.cur.Kind == TokName && p.cur.Value == "else" {
		p.advance()
		if _, err := p.expect(TokOp, ":"); err != nil {
			return nil, err
		}
		orelse, err = p.parseSuite()
		if err != nil {
			return nil, err
		}
	}

	if p.cur.Kind == TokName && p.cur.Value == "finally" {
		p.advance()
		if _, err := p.expect(TokOp, ":"); err != nil {
			return nil, err
		}
		finally, err = p.parseSuite()
		if err != nil {
			return nil, err
		}
	}

	if len(handlers) == 0 && len(finally) == 0 {
		return nil, p.syntaxErrorAt(pos, "try statement must have at least one except or finally clause")
	}

	return &TryStmt{
		Pos:      pos,
		Body:     body,
		Handlers: handlers,
		Orelse:   orelse,
		Finally:  finally,
	}, nil
}

func (p *Parser) parseWith() (Stmt, error) {
	pos := p.cur.Pos
	p.advance() // consume 'with'

	var items []*WithItem
	item, err := p.parseWithItem()
	if err != nil {
		return nil, err
	}
	items = append(items, item)
	for p.match(TokOp, ",") {
		item, err = p.parseWithItem()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if _, err := p.expect(TokOp, ":"); err != nil {
		return nil, err
	}
	body, err := p.parseSuite()
	if err != nil {
		return nil, err
	}
	return &WithStmt{Pos: pos, Items: items, Body: body}, nil
}

func (p *Parser) parseWithItem() (*WithItem, error) {
	ctx, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	item := &WithItem{CtxExpr: ctx}
	if p.match(TokName, "as") {
		optVar, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		item.OptVar = optVar
	}
	return item, nil
}

func (p *Parser) parseDecorated() (Stmt, error) {
	var decorators []Expr
	for p.check(TokOp, "@") {
		p.advance() // consume '@'
		dec, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		decorators = append(decorators, dec)
		if p.cur.Kind == TokNewline {
			p.advance()
		}
		p.skipNewlines()
	}
	if p.cur.Kind == TokName && p.cur.Value == "def" {
		return p.parseFuncDef(decorators)
	}
	if p.cur.Kind == TokName && p.cur.Value == "async" {
		if p.peek.Kind == TokName && p.peek.Value == "def" {
			p.advance() // consume 'async'
			return p.parseFuncDef(decorators)
		}
	}
	if p.cur.Kind == TokName && p.cur.Value == "class" {
		return p.parseClassDef(decorators)
	}
	return nil, p.syntaxErrorf("expected 'def' or 'class' after decorator")
}

// parseSuite parses either an inline simple stmt list or an indented block.
func (p *Parser) parseSuite() ([]Stmt, error) {
	if p.cur.Kind == TokNewline {
		p.advance()
		p.skipNewlines()
		return p.parseStmtList()
	}
	// Inline suite.
	stmts, err := p.parseSimpleStmts()
	if err != nil {
		return nil, err
	}
	return stmts, nil
}

// ---- Simple statements ----

func (p *Parser) parseReturn() (Stmt, error) {
	pos := p.cur.Pos
	p.advance() // consume 'return'
	if p.cur.Kind == TokNewline || p.cur.Kind == TokEOF || p.check(TokOp, ";") {
		return &ReturnStmt{Pos: pos}, nil
	}
	val, err := p.parseTestList()
	if err != nil {
		return nil, err
	}
	return &ReturnStmt{Pos: pos, Value: val}, nil
}

func (p *Parser) parseRaise() (Stmt, error) {
	pos := p.cur.Pos
	p.advance() // consume 'raise'
	if p.cur.Kind == TokNewline || p.cur.Kind == TokEOF || p.check(TokOp, ";") {
		return &RaiseStmt{Pos: pos}, nil
	}
	exc, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	s := &RaiseStmt{Pos: pos, Exc: exc}
	if p.match(TokName, "from") {
		cause, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		s.Cause = cause
	}
	return s, nil
}

func (p *Parser) parseDel() (Stmt, error) {
	pos := p.cur.Pos
	p.advance() // consume 'del'
	targets, err := p.parseExprList()
	if err != nil {
		return nil, err
	}
	return &DelStmt{Pos: pos, Targets: targets}, nil
}

func (p *Parser) parseGlobal() (Stmt, error) {
	pos := p.cur.Pos
	p.advance() // consume 'global'
	var names []string
	nameTok, err := p.expect(TokName, "")
	if err != nil {
		return nil, err
	}
	names = append(names, nameTok.Value)
	for p.match(TokOp, ",") {
		nameTok, err = p.expect(TokName, "")
		if err != nil {
			return nil, err
		}
		names = append(names, nameTok.Value)
	}
	return &GlobalStmt{Pos: pos, Names: names}, nil
}

func (p *Parser) parseNonlocal() (Stmt, error) {
	pos := p.cur.Pos
	p.advance() // consume 'nonlocal'
	var names []string
	nameTok, err := p.expect(TokName, "")
	if err != nil {
		return nil, err
	}
	names = append(names, nameTok.Value)
	for p.match(TokOp, ",") {
		nameTok, err = p.expect(TokName, "")
		if err != nil {
			return nil, err
		}
		names = append(names, nameTok.Value)
	}
	return &NonlocalStmt{Pos: pos, Names: names}, nil
}

func (p *Parser) parseAssert() (Stmt, error) {
	pos := p.cur.Pos
	p.advance() // consume 'assert'
	test, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	s := &AssertStmt{Pos: pos, Test: test}
	if p.match(TokOp, ",") {
		msg, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		s.Msg = msg
	}
	return s, nil
}

func (p *Parser) parseImport() (Stmt, error) {
	pos := p.cur.Pos
	p.advance() // consume 'import'
	var names []ImportName
	name, err := p.parseDottedName()
	if err != nil {
		return nil, err
	}
	alias := ""
	if p.match(TokName, "as") {
		aliasTok, err := p.expect(TokName, "")
		if err != nil {
			return nil, err
		}
		alias = aliasTok.Value
	}
	names = append(names, ImportName{Name: name, Alias: alias})
	for p.match(TokOp, ",") {
		name, err = p.parseDottedName()
		if err != nil {
			return nil, err
		}
		alias = ""
		if p.match(TokName, "as") {
			aliasTok, err := p.expect(TokName, "")
			if err != nil {
				return nil, err
			}
			alias = aliasTok.Value
		}
		names = append(names, ImportName{Name: name, Alias: alias})
	}
	return &ImportStmt{Pos: pos, Names: names}, nil
}

func (p *Parser) parseFromImport() (Stmt, error) {
	pos := p.cur.Pos
	p.advance() // consume 'from'

	// Relative imports: leading dots.
	var dots strings.Builder
	for p.check(TokOp, ".") || p.check(TokOp, "...") {
		dots.WriteString(p.cur.Value)
		p.advance()
	}

	modName := ""
	if p.cur.Kind == TokName && p.cur.Value != "import" {
		var err error
		modName, err = p.parseDottedName()
		if err != nil {
			return nil, err
		}
	}
	module := dots.String() + modName

	if _, err := p.expect(TokName, "import"); err != nil {
		return nil, err
	}

	var names []ImportName
	if p.check(TokOp, "*") {
		p.advance()
		names = []ImportName{{Name: "*"}}
	} else if p.match(TokOp, "(") {
		var err error
		names, err = p.parseImportAsNames()
		if err != nil {
			return nil, err
		}
		p.match(TokOp, ",") // trailing comma
		if _, err := p.expect(TokOp, ")"); err != nil {
			return nil, err
		}
	} else {
		var err error
		names, err = p.parseImportAsNames()
		if err != nil {
			return nil, err
		}
	}

	return &ImportFromStmt{Pos: pos, Module: module, Names: names}, nil
}

func (p *Parser) parseImportAsNames() ([]ImportName, error) {
	var names []ImportName
	nameTok, err := p.expect(TokName, "")
	if err != nil {
		return nil, err
	}
	alias := ""
	if p.match(TokName, "as") {
		aliasTok, err := p.expect(TokName, "")
		if err != nil {
			return nil, err
		}
		alias = aliasTok.Value
	}
	names = append(names, ImportName{Name: nameTok.Value, Alias: alias})
	for p.match(TokOp, ",") {
		if p.cur.Kind != TokName {
			break
		}
		nameTok, err = p.expect(TokName, "")
		if err != nil {
			return nil, err
		}
		alias = ""
		if p.match(TokName, "as") {
			aliasTok, err := p.expect(TokName, "")
			if err != nil {
				return nil, err
			}
			alias = aliasTok.Value
		}
		names = append(names, ImportName{Name: nameTok.Value, Alias: alias})
	}
	return names, nil
}

func (p *Parser) parseDottedName() (string, error) {
	nameTok, err := p.expect(TokName, "")
	if err != nil {
		return "", err
	}
	name := nameTok.Value
	for p.check(TokOp, ".") {
		p.advance()
		part, err := p.expect(TokName, "")
		if err != nil {
			return "", err
		}
		name += "." + part.Value
	}
	return name, nil
}

// parseAssignOrExprStmt handles assignments and expression statements.
func (p *Parser) parseAssignOrExprStmt() (Stmt, error) {
	pos := p.cur.Pos

	// Parse the first expression (possibly a comma-separated tuple).
	first, err := p.parseTestlistStarExpr()
	if err != nil {
		return nil, err
	}

	// Augmented assignment?
	if isAugOp(p.cur) {
		op := p.cur.Value
		p.advance()
		rhs, err := p.parseTestList()
		if err != nil {
			return nil, err
		}
		return &AugAssignStmt{Pos: pos, Target: first, Op: op, Value: rhs}, nil
	}

	// Annotated assignment?
	if p.check(TokOp, ":") {
		p.advance()
		ann, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		s := &AnnAssignStmt{Pos: pos, Target: first, Annotation: ann}
		if p.match(TokOp, "=") {
			val, err := p.parseTestList()
			if err != nil {
				return nil, err
			}
			s.Value = val
		}
		return s, nil
	}

	// Regular assignment chain: a = b = c
	if p.check(TokOp, "=") {
		targets := []Expr{first}
		for p.match(TokOp, "=") {
			var rhs Expr
			// Allow yield/yield from on the RHS of assignment.
			if p.cur.Kind == TokName && p.cur.Value == "yield" {
				rhs, err = p.parseYieldExpr()
			} else {
				rhs, err = p.parseTestlistStarExpr()
			}
			if err != nil {
				return nil, err
			}
			targets = append(targets, rhs)
		}
		// Last element is the value.
		value := targets[len(targets)-1]
		return &AssignStmt{Pos: pos, Targets: targets[:len(targets)-1], Value: value}, nil
	}

	return &ExprStmt{Pos: pos, Value: first}, nil
}

func isAugOp(t Token) bool {
	if t.Kind != TokOp {
		return false
	}
	switch t.Value {
	case "+=", "-=", "*=", "/=", "//=", "%=", "**=", "&=", "|=", "^=", "<<=", ">>=", "@=":
		return true
	}
	return false
}

// ---- Function argument parsing ----

// parseFuncArgs parses the argument specification inside def f(...).
func (p *Parser) parseFuncArgs() (*Arguments, error) {
	args := &Arguments{}

	afterStar := false
	bareStarSeen := false

	for !p.check(TokOp, ")") && p.cur.Kind != TokEOF {
		p.skipNewlines()
		if p.check(TokOp, ")") {
			break
		}

		// **kwargs
		if p.match(TokOp, "**") {
			nameTok, err := p.expect(TokName, "")
			if err != nil {
				return nil, err
			}
			args.Kwarg = nameTok.Value
			p.match(TokOp, ",")
			break
		}

		// *args or bare *
		if p.match(TokOp, "*") {
			if p.check(TokOp, ",") || p.check(TokOp, ")") {
				// bare *
				bareStarSeen = true
				afterStar = true
			} else {
				nameTok, err := p.expect(TokName, "")
				if err != nil {
					return nil, err
				}
				args.Vararg = nameTok.Value
				afterStar = true
			}
			_ = bareStarSeen
			if p.check(TokOp, ",") {
				p.advance()
				continue
			}
			break
		}

		// Regular arg or kwonly arg.
		nameTok, err := p.expect(TokName, "")
		if err != nil {
			return nil, err
		}

		// Optional type annotation.
		if p.match(TokOp, ":") {
			_, err = p.parseExpr() // discard annotation
			if err != nil {
				return nil, err
			}
		}

		var defaultVal Expr
		if p.match(TokOp, "=") {
			defaultVal, err = p.parseExpr()
			if err != nil {
				return nil, err
			}
		}

		if afterStar {
			args.KwOnly = append(args.KwOnly, nameTok.Value)
			args.KwDefaults = append(args.KwDefaults, defaultVal)
		} else {
			args.Args = append(args.Args, nameTok.Value)
			args.Defaults = append(args.Defaults, defaultVal)
		}

		if !p.match(TokOp, ",") {
			break
		}
	}
	return args, nil
}

// ---- Expression parsing ----

// parseExpr parses a single expression (handles ternary).
func (p *Parser) parseExpr() (Expr, error) {
	return p.parseTernary()
}

// parseTernary: boolOr ('if' boolOr 'else' ternary)?
func (p *Parser) parseTernary() (Expr, error) {
	body, err := p.parseBoolOr()
	if err != nil {
		return nil, err
	}
	if p.cur.Kind == TokName && p.cur.Value == "if" {
		pos := p.cur.Pos
		p.advance()
		test, err := p.parseBoolOr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokName, "else"); err != nil {
			return nil, err
		}
		orelse, err := p.parseTernary()
		if err != nil {
			return nil, err
		}
		return &IfExp{Pos: pos, Test: test, Body: body, Orelse: orelse}, nil
	}
	return body, nil
}

// parseBoolOr: boolAnd ('or' boolAnd)*
func (p *Parser) parseBoolOr() (Expr, error) {
	left, err := p.parseBoolAnd()
	if err != nil {
		return nil, err
	}
	for p.cur.Kind == TokName && p.cur.Value == "or" {
		pos := p.cur.Pos
		p.advance()
		right, err := p.parseBoolAnd()
		if err != nil {
			return nil, err
		}
		if bo, ok := left.(*BoolOp); ok && bo.Op == "or" {
			bo.Values = append(bo.Values, right)
		} else {
			left = &BoolOp{Pos: pos, Op: "or", Values: []Expr{left, right}}
		}
	}
	return left, nil
}

// parseBoolAnd: boolNot ('and' boolNot)*
func (p *Parser) parseBoolAnd() (Expr, error) {
	left, err := p.parseBoolNot()
	if err != nil {
		return nil, err
	}
	for p.cur.Kind == TokName && p.cur.Value == "and" {
		pos := p.cur.Pos
		p.advance()
		right, err := p.parseBoolNot()
		if err != nil {
			return nil, err
		}
		if bo, ok := left.(*BoolOp); ok && bo.Op == "and" {
			bo.Values = append(bo.Values, right)
		} else {
			left = &BoolOp{Pos: pos, Op: "and", Values: []Expr{left, right}}
		}
	}
	return left, nil
}

// parseBoolNot: 'not' boolNot | comparison
func (p *Parser) parseBoolNot() (Expr, error) {
	if p.cur.Kind == TokName && p.cur.Value == "not" {
		pos := p.cur.Pos
		p.advance()
		operand, err := p.parseBoolNot()
		if err != nil {
			return nil, err
		}
		return &UnaryOp{Pos: pos, Op: "not", Operand: operand}, nil
	}
	return p.parseComparison()
}

// parseComparison: bitor (cmpop bitor)*
func (p *Parser) parseComparison() (Expr, error) {
	left, err := p.parseBitOr()
	if err != nil {
		return nil, err
	}
	pos := p.cur.Pos
	var ops []string
	var comparators []Expr

	for {
		op, ok := p.peekCmpOp()
		if !ok {
			break
		}
		right, err := p.parseBitOr()
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
		comparators = append(comparators, right)
	}

	if len(ops) == 0 {
		return left, nil
	}
	return &Compare{Pos: pos, Left: left, Ops: ops, Comparators: comparators}, nil
}

// peekCmpOp checks for a comparison operator and advances if found.
func (p *Parser) peekCmpOp() (string, bool) {
	if p.cur.Kind == TokOp {
		switch p.cur.Value {
		case "==", "!=", "<", ">", "<=", ">=":
			op := p.cur.Value
			p.advance()
			return op, true
		}
	}
	if p.cur.Kind == TokName {
		switch p.cur.Value {
		case "in":
			p.advance()
			return "in", true
		case "is":
			p.advance()
			if p.cur.Kind == TokName && p.cur.Value == "not" {
				p.advance()
				return "is not", true
			}
			return "is", true
		case "not":
			if p.peek.Kind == TokName && p.peek.Value == "in" {
				p.advance() // consume 'not'
				p.advance() // consume 'in'
				return "not in", true
			}
		}
	}
	return "", false
}

// parseBitOr: bitxor ('|' bitxor)*
func (p *Parser) parseBitOr() (Expr, error) {
	left, err := p.parseBitXor()
	if err != nil {
		return nil, err
	}
	for p.check(TokOp, "|") {
		pos := p.cur.Pos
		p.advance()
		right, err := p.parseBitXor()
		if err != nil {
			return nil, err
		}
		left = &BinOp{Pos: pos, Left: left, Right: right, Op: "|"}
	}
	return left, nil
}

// parseBitXor: bitand ('^' bitand)*
func (p *Parser) parseBitXor() (Expr, error) {
	left, err := p.parseBitAnd()
	if err != nil {
		return nil, err
	}
	for p.check(TokOp, "^") {
		pos := p.cur.Pos
		p.advance()
		right, err := p.parseBitAnd()
		if err != nil {
			return nil, err
		}
		left = &BinOp{Pos: pos, Left: left, Right: right, Op: "^"}
	}
	return left, nil
}

// parseBitAnd: shift ('&' shift)*
func (p *Parser) parseBitAnd() (Expr, error) {
	left, err := p.parseShift()
	if err != nil {
		return nil, err
	}
	for p.check(TokOp, "&") {
		pos := p.cur.Pos
		p.advance()
		right, err := p.parseShift()
		if err != nil {
			return nil, err
		}
		left = &BinOp{Pos: pos, Left: left, Right: right, Op: "&"}
	}
	return left, nil
}

// parseShift: arith (('<<' | '>>') arith)*
func (p *Parser) parseShift() (Expr, error) {
	left, err := p.parseArith()
	if err != nil {
		return nil, err
	}
	for p.cur.Kind == TokOp && (p.cur.Value == "<<" || p.cur.Value == ">>") {
		op := p.cur.Value
		pos := p.cur.Pos
		p.advance()
		right, err := p.parseArith()
		if err != nil {
			return nil, err
		}
		left = &BinOp{Pos: pos, Left: left, Right: right, Op: op}
	}
	return left, nil
}

// parseArith: term (('+' | '-') term)*
func (p *Parser) parseArith() (Expr, error) {
	left, err := p.parseTerm()
	if err != nil {
		return nil, err
	}
	for p.cur.Kind == TokOp && (p.cur.Value == "+" || p.cur.Value == "-") {
		op := p.cur.Value
		pos := p.cur.Pos
		p.advance()
		right, err := p.parseTerm()
		if err != nil {
			return nil, err
		}
		left = &BinOp{Pos: pos, Left: left, Right: right, Op: op}
	}
	return left, nil
}

// parseTerm: factor (('*' | '/' | '//' | '%' | '@') factor)*
func (p *Parser) parseTerm() (Expr, error) {
	left, err := p.parseFactor()
	if err != nil {
		return nil, err
	}
	for p.cur.Kind == TokOp {
		op := p.cur.Value
		if op != "*" && op != "/" && op != "//" && op != "%" && op != "@" {
			break
		}
		pos := p.cur.Pos
		p.advance()
		right, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		left = &BinOp{Pos: pos, Left: left, Right: right, Op: op}
	}
	return left, nil
}

// parseFactor: ('+' | '-' | '~') factor | power
func (p *Parser) parseFactor() (Expr, error) {
	if p.cur.Kind == TokOp {
		switch p.cur.Value {
		case "+", "-", "~":
			op := p.cur.Value
			pos := p.cur.Pos
			p.advance()
			operand, err := p.parseFactor()
			if err != nil {
				return nil, err
			}
			return &UnaryOp{Pos: pos, Op: op, Operand: operand}, nil
		}
	}
	return p.parsePower()
}

// parsePower: postfix ('**' factor)?  (right-associative)
func (p *Parser) parsePower() (Expr, error) {
	base, err := p.parseAwait()
	if err != nil {
		return nil, err
	}
	if p.check(TokOp, "**") {
		pos := p.cur.Pos
		p.advance()
		exp, err := p.parseFactor()
		if err != nil {
			return nil, err
		}
		return &BinOp{Pos: pos, Left: base, Right: exp, Op: "**"}, nil
	}
	return base, nil
}

// parseAwait: 'await' postfix | postfix
func (p *Parser) parseAwait() (Expr, error) {
	if p.cur.Kind == TokName && p.cur.Value == "await" {
		p.advance() // consume 'await' — treat as no-op for now
	}
	return p.parsePostfix()
}

// parsePostfix: primary ('.' Name | '[' subscript ']' | '(' arglist ')')*
func (p *Parser) parsePostfix() (Expr, error) {
	node, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}

	for {
		if p.check(TokOp, ".") {
			pos := p.cur.Pos
			p.advance()
			nameTok, err := p.expect(TokName, "")
			if err != nil {
				return nil, err
			}
			node = &AttributeExpr{Pos: pos, Value: node, Attr: nameTok.Value}
			continue
		}
		if p.check(TokOp, "[") {
			pos := p.cur.Pos
			p.advance()
			slice, err := p.parseSubscript()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(TokOp, "]"); err != nil {
				return nil, err
			}
			node = &SubscriptExpr{Pos: pos, Value: node, Slice: slice}
			continue
		}
		if p.check(TokOp, "(") {
			pos := p.cur.Pos
			p.advance()
			args, keywords, err := p.parseCallArgs()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(TokOp, ")"); err != nil {
				return nil, err
			}
			node = &CallExpr{Pos: pos, Func: node, Args: args, Keywords: keywords}
			continue
		}
		break
	}
	return node, nil
}

// parseSubscript parses a subscript expression (possibly a slice).
func (p *Parser) parseSubscript() (Expr, error) {
	// Check for bare slice: :upper, ::step, etc.
	if p.check(TokOp, ":") {
		return p.parseSliceSuffix(nil)
	}
	// Could be expr or slice starting with expr.
	first, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.check(TokOp, ":") {
		return p.parseSliceSuffix(first)
	}
	// Tuple subscript: a[1, 2]
	if p.check(TokOp, ",") {
		elts := []Expr{first}
		for p.match(TokOp, ",") {
			if p.check(TokOp, "]") {
				break
			}
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			elts = append(elts, e)
		}
		if len(elts) == 1 {
			return elts[0], nil
		}
		return &TupleExpr{Pos: first.nodePos(), Elts: elts}, nil
	}
	return first, nil
}

func (p *Parser) parseSliceSuffix(lower Expr) (Expr, error) {
	pos := p.cur.Pos
	if lower != nil {
		pos = lower.nodePos()
	}
	if _, err := p.expect(TokOp, ":"); err != nil {
		return nil, err
	}
	var upper Expr
	if !p.check(TokOp, ":") && !p.check(TokOp, "]") && !p.check(TokOp, ",") {
		var err error
		upper, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
	}
	var step Expr
	if p.match(TokOp, ":") {
		if !p.check(TokOp, "]") && !p.check(TokOp, ",") {
			var err error
			step, err = p.parseExpr()
			if err != nil {
				return nil, err
			}
		}
	}
	return &SliceExpr{Pos: pos, Lower: lower, Upper: upper, Step: step}, nil
}

// parsePrimary parses a primary expression.
func (p *Parser) parsePrimary() (Expr, error) {
	pos := p.cur.Pos

	switch p.cur.Kind {
	case TokName:
		name := p.cur.Value
		p.advance()
		switch name {
		case "True":
			return &Constant{Pos: pos, Value: true}, nil
		case "False":
			return &Constant{Pos: pos, Value: false}, nil
		case "None":
			return &Constant{Pos: pos, Value: nil}, nil
		case "lambda":
			return p.parseLambda()
		case "yield":
			return p.parseYieldExpr()
		}
		return &NameExpr{Pos: pos, Id: name}, nil

	case TokInt:
		val, err := parseIntLiteral(p.cur.Value)
		if err != nil {
			return nil, p.syntaxErrorf("invalid integer literal %q: %v", p.cur.Value, err)
		}
		p.advance()
		return &Constant{Pos: pos, Value: val}, nil

	case TokFloat:
		val, err := parseFloatLiteral(p.cur.Value)
		if err != nil {
			return nil, p.syntaxErrorf("invalid float literal %q: %v", p.cur.Value, err)
		}
		p.advance()
		return &Constant{Pos: pos, Value: val}, nil

	case TokString:
		// Adjacent string concatenation.
		val := p.cur.Value
		p.advance()
		for p.cur.Kind == TokString {
			val += p.cur.Value
			p.advance()
		}
		return &Constant{Pos: pos, Value: val}, nil

	case TokBytes:
		val := []byte(p.cur.Value)
		p.advance()
		for p.cur.Kind == TokBytes {
			val = append(val, []byte(p.cur.Value)...)
			p.advance()
		}
		return &Constant{Pos: pos, Value: val}, nil

	case TokOp:
		switch p.cur.Value {
		case "(":
			return p.parseParenExpr()
		case "[":
			return p.parseListExpr()
		case "{":
			return p.parseDictOrSetExpr()
		case "*":
			// Starred expression in assignment target.
			p.advance()
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			return &Starred{Pos: pos, Value: val}, nil
		}
	}

	return nil, p.syntaxErrorf("unexpected token %s %q", tokenKindString(p.cur.Kind), p.cur.Value)
}

// parseParenExpr parses a parenthesized expression, tuple, generator, or yield.
func (p *Parser) parseParenExpr() (Expr, error) {
	pos := p.cur.Pos
	p.advance() // consume '('

	// Empty tuple.
	if p.check(TokOp, ")") {
		p.advance()
		return &TupleExpr{Pos: pos, Elts: nil}, nil
	}

	// yield expression inside parens.
	if p.cur.Kind == TokName && p.cur.Value == "yield" {
		e, err := p.parseYieldExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokOp, ")"); err != nil {
			return nil, err
		}
		return e, nil
	}

	first, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	// Generator expression.
	if p.cur.Kind == TokName && p.cur.Value == "for" {
		gens, err := p.parseComprehensions()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokOp, ")"); err != nil {
			return nil, err
		}
		return &GeneratorExp{Pos: pos, Elt: first, Generators: gens}, nil
	}

	// Tuple or single expression.
	if p.check(TokOp, ",") {
		elts := []Expr{first}
		for p.match(TokOp, ",") {
			if p.check(TokOp, ")") {
				break
			}
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			elts = append(elts, e)
		}
		if _, err := p.expect(TokOp, ")"); err != nil {
			return nil, err
		}
		return &TupleExpr{Pos: pos, Elts: elts}, nil
	}

	if _, err := p.expect(TokOp, ")"); err != nil {
		return nil, err
	}
	return first, nil
}

// parseListExpr parses a list literal or list comprehension.
func (p *Parser) parseListExpr() (Expr, error) {
	pos := p.cur.Pos
	p.advance() // consume '['

	if p.check(TokOp, "]") {
		p.advance()
		return &ListExpr{Pos: pos, Elts: nil}, nil
	}

	first, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	// List comprehension.
	if p.cur.Kind == TokName && p.cur.Value == "for" {
		gens, err := p.parseComprehensions()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokOp, "]"); err != nil {
			return nil, err
		}
		return &ListComp{Pos: pos, Elt: first, Generators: gens}, nil
	}

	elts := []Expr{first}
	for p.match(TokOp, ",") {
		if p.check(TokOp, "]") {
			break
		}
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		elts = append(elts, e)
	}
	if _, err := p.expect(TokOp, "]"); err != nil {
		return nil, err
	}
	return &ListExpr{Pos: pos, Elts: elts}, nil
}

// parseDictOrSetExpr parses a dict literal, set literal, dict comp, or set comp.
func (p *Parser) parseDictOrSetExpr() (Expr, error) {
	pos := p.cur.Pos
	p.advance() // consume '{'

	if p.check(TokOp, "}") {
		p.advance()
		return &DictExpr{Pos: pos}, nil
	}

	// **unpack at start means dict.
	if p.check(TokOp, "**") {
		p.advance()
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		keys := []Expr{nil}
		vals := []Expr{val}
		for p.match(TokOp, ",") {
			if p.check(TokOp, "}") {
				break
			}
			if p.match(TokOp, "**") {
				v, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				keys = append(keys, nil)
				vals = append(vals, v)
			} else {
				k, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				if _, err := p.expect(TokOp, ":"); err != nil {
					return nil, err
				}
				v, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				keys = append(keys, k)
				vals = append(vals, v)
			}
		}
		if _, err := p.expect(TokOp, "}"); err != nil {
			return nil, err
		}
		return &DictExpr{Pos: pos, Keys: keys, Values: vals}, nil
	}

	first, err := p.parseExpr()
	if err != nil {
		return nil, err
	}

	// Dict (key: value) or dict comp.
	if p.check(TokOp, ":") {
		p.advance()
		firstVal, err := p.parseExpr()
		if err != nil {
			return nil, err
		}

		// Dict comprehension.
		if p.cur.Kind == TokName && p.cur.Value == "for" {
			gens, err := p.parseComprehensions()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(TokOp, "}"); err != nil {
				return nil, err
			}
			return &DictComp{Pos: pos, Key: first, Value: firstVal, Generators: gens}, nil
		}

		// Dict literal.
		keys := []Expr{first}
		vals := []Expr{firstVal}
		for p.match(TokOp, ",") {
			if p.check(TokOp, "}") {
				break
			}
			if p.match(TokOp, "**") {
				v, err := p.parseExpr()
				if err != nil {
					return nil, err
				}
				keys = append(keys, nil)
				vals = append(vals, v)
				continue
			}
			k, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(TokOp, ":"); err != nil {
				return nil, err
			}
			v, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			keys = append(keys, k)
			vals = append(vals, v)
		}
		if _, err := p.expect(TokOp, "}"); err != nil {
			return nil, err
		}
		return &DictExpr{Pos: pos, Keys: keys, Values: vals}, nil
	}

	// Set or set comprehension.
	if p.cur.Kind == TokName && p.cur.Value == "for" {
		gens, err := p.parseComprehensions()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokOp, "}"); err != nil {
			return nil, err
		}
		return &SetComp{Pos: pos, Elt: first, Generators: gens}, nil
	}

	// Set literal.
	elts := []Expr{first}
	for p.match(TokOp, ",") {
		if p.check(TokOp, "}") {
			break
		}
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		elts = append(elts, e)
	}
	if _, err := p.expect(TokOp, "}"); err != nil {
		return nil, err
	}
	return &SetExpr{Pos: pos, Elts: elts}, nil
}

// parseComprehensions parses one or more 'for target in iter (if cond)*' clauses.
func (p *Parser) parseComprehensions() ([]*Comprehension, error) {
	var gens []*Comprehension
	for p.cur.Kind == TokName && (p.cur.Value == "for" || p.cur.Value == "async") {
		if p.cur.Value == "async" {
			p.advance() // skip 'async'
		}
		if _, err := p.expect(TokName, "for"); err != nil {
			return nil, err
		}
		target, err := p.parseTargetList()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(TokName, "in"); err != nil {
			return nil, err
		}
		iter, err := p.parseBoolOr() // avoid consuming trailing 'for'/'if'
		if err != nil {
			return nil, err
		}
		// Handle comma-separated iterables: for x in a, b — wrap in tuple.
		if p.check(TokOp, ",") {
			iters := []Expr{iter}
			for p.match(TokOp, ",") {
				if p.cur.Kind == TokName && (p.cur.Value == "for" || p.cur.Value == "if" || p.cur.Value == "async") {
					break
				}
				if p.check(TokOp, "]") || p.check(TokOp, ")") || p.check(TokOp, "}") {
					break
				}
				e, err := p.parseBoolOr()
				if err != nil {
					return nil, err
				}
				iters = append(iters, e)
			}
			if len(iters) > 1 {
				iter = &TupleExpr{Pos: iter.nodePos(), Elts: iters}
			}
		}

		comp := &Comprehension{Target: target, Iter: iter}
		for p.cur.Kind == TokName && p.cur.Value == "if" {
			p.advance()
			cond, err := p.parseBoolNot() // if condition: no ternary
			if err != nil {
				return nil, err
			}
			comp.Ifs = append(comp.Ifs, cond)
		}
		gens = append(gens, comp)
	}
	return gens, nil
}

// parseLambda parses a lambda expression (after 'lambda' has been consumed by parsePrimary).
func (p *Parser) parseLambda() (Expr, error) {
	pos := p.cur.Pos
	// parsePrimary already consumed 'lambda'
	var args *Arguments
	var err error
	if !p.check(TokOp, ":") {
		args, err = p.parseLambdaArgs()
		if err != nil {
			return nil, err
		}
	} else {
		args = &Arguments{}
	}
	if _, err := p.expect(TokOp, ":"); err != nil {
		return nil, err
	}
	body, err := p.parseTernary()
	if err != nil {
		return nil, err
	}
	return &Lambda{Pos: pos, Args: args, Body: body}, nil
}

// parseLambdaArgs parses simplified lambda argument list (no annotations, no defaults… well, defaults yes).
func (p *Parser) parseLambdaArgs() (*Arguments, error) {
	args := &Arguments{}
	afterStar := false
	for !p.check(TokOp, ":") && p.cur.Kind != TokEOF {
		if p.match(TokOp, "**") {
			nameTok, err := p.expect(TokName, "")
			if err != nil {
				return nil, err
			}
			args.Kwarg = nameTok.Value
			p.match(TokOp, ",")
			break
		}
		if p.match(TokOp, "*") {
			if p.check(TokOp, ",") || p.check(TokOp, ":") {
				afterStar = true
			} else {
				nameTok, err := p.expect(TokName, "")
				if err != nil {
					return nil, err
				}
				args.Vararg = nameTok.Value
				afterStar = true
			}
			if p.check(TokOp, ",") {
				p.advance()
				continue
			}
			break
		}
		nameTok, err := p.expect(TokName, "")
		if err != nil {
			return nil, err
		}
		var defaultVal Expr
		if p.match(TokOp, "=") {
			defaultVal, err = p.parseTernary()
			if err != nil {
				return nil, err
			}
		}
		if afterStar {
			args.KwOnly = append(args.KwOnly, nameTok.Value)
			args.KwDefaults = append(args.KwDefaults, defaultVal)
		} else {
			args.Args = append(args.Args, nameTok.Value)
			args.Defaults = append(args.Defaults, defaultVal)
		}
		if !p.match(TokOp, ",") {
			break
		}
	}
	return args, nil
}

// parseYieldExpr parses a yield or yield from expression.
// Called after 'yield' keyword has been identified but NOT consumed.
func (p *Parser) parseYieldExpr() (Expr, error) {
	pos := p.cur.Pos
	p.advance() // consume 'yield'

	if p.cur.Kind == TokName && p.cur.Value == "from" {
		p.advance()
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &YieldFrom{Pos: pos, Value: val}, nil
	}

	if p.cur.Kind == TokNewline || p.cur.Kind == TokEOF ||
		p.check(TokOp, ")") || p.check(TokOp, "]") || p.check(TokOp, "}") ||
		p.check(TokOp, ";") || p.check(TokOp, ",") {
		return &Yield{Pos: pos}, nil
	}

	val, err := p.parseTestList()
	if err != nil {
		return nil, err
	}
	return &Yield{Pos: pos, Value: val}, nil
}

// parseCallArgs parses the argument list in a function call.
func (p *Parser) parseCallArgs() ([]Expr, []*Keyword, error) {
	var args []Expr
	var keywords []*Keyword

	for !p.check(TokOp, ")") && p.cur.Kind != TokEOF {
		p.skipNewlines()
		if p.check(TokOp, ")") {
			break
		}

		// **kwargs
		if p.match(TokOp, "**") {
			val, err := p.parseExpr()
			if err != nil {
				return nil, nil, err
			}
			keywords = append(keywords, &Keyword{Arg: "", Value: val})
			if !p.match(TokOp, ",") {
				break
			}
			continue
		}

		// *args
		if p.match(TokOp, "*") {
			val, err := p.parseExpr()
			if err != nil {
				return nil, nil, err
			}
			args = append(args, &Starred{Pos: val.nodePos(), Value: val})
			if !p.match(TokOp, ",") {
				break
			}
			continue
		}

		// yield inside call.
		if p.cur.Kind == TokName && p.cur.Value == "yield" {
			e, err := p.parseYieldExpr()
			if err != nil {
				return nil, nil, err
			}
			args = append(args, e)
			if !p.match(TokOp, ",") {
				break
			}
			continue
		}

		expr, err := p.parseExpr()
		if err != nil {
			return nil, nil, err
		}

		// Generator expression as sole argument: f(x*x for x in iter)
		if p.cur.Kind == TokName && p.cur.Value == "for" {
			gens, err := p.parseComprehensions()
			if err != nil {
				return nil, nil, err
			}
			args = append(args, &GeneratorExp{Pos: expr.nodePos(), Elt: expr, Generators: gens})
			// generator expression must be the only argument
			break
		}

		// Keyword argument: name=value
		if p.check(TokOp, "=") {
			nameExpr, ok := expr.(*NameExpr)
			if !ok {
				return nil, nil, p.syntaxErrorf("keyword argument must be a name")
			}
			p.advance() // consume '='
			val, err := p.parseExpr()
			if err != nil {
				return nil, nil, err
			}
			keywords = append(keywords, &Keyword{Arg: nameExpr.Id, Value: val})
		} else {
			args = append(args, expr)
		}

		if !p.match(TokOp, ",") {
			break
		}
	}

	return args, keywords, nil
}

// parseArgList parses a comma-separated list of expressions (for class bases, etc.).
func (p *Parser) parseArgList() ([]Expr, error) {
	var exprs []Expr
	for {
		if p.check(TokOp, ")") || p.cur.Kind == TokEOF {
			break
		}
		if p.match(TokOp, "**") {
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			exprs = append(exprs, &Starred{Pos: val.nodePos(), Value: val})
		} else if p.match(TokOp, "*") {
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			exprs = append(exprs, &Starred{Pos: val.nodePos(), Value: val})
		} else {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			// Skip keyword args (name=value) in class bases.
			if p.check(TokOp, "=") {
				p.advance()
				_, err = p.parseExpr()
				if err != nil {
					return nil, err
				}
				// Don't add keyword arguments to bases list.
			} else {
				exprs = append(exprs, e)
			}
		}
		if !p.match(TokOp, ",") {
			break
		}
	}
	return exprs, nil
}

// parseTestList parses a comma-separated list of expressions (possibly a tuple).
func (p *Parser) parseTestList() (Expr, error) {
	pos := p.cur.Pos
	first, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if !p.check(TokOp, ",") {
		return first, nil
	}
	elts := []Expr{first}
	for p.match(TokOp, ",") {
		if isEndOfExprList(p.cur) {
			break
		}
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		elts = append(elts, e)
	}
	return &TupleExpr{Pos: pos, Elts: elts}, nil
}

// parseTestlistStarExpr parses a comma-separated expression list that may
// include starred expressions.
func (p *Parser) parseTestlistStarExpr() (Expr, error) {
	pos := p.cur.Pos

	var first Expr
	var err error
	if p.check(TokOp, "*") {
		starPos := p.cur.Pos
		p.advance()
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		first = &Starred{Pos: starPos, Value: val}
	} else {
		first, err = p.parseExpr()
		if err != nil {
			return nil, err
		}
	}

	if !p.check(TokOp, ",") {
		return first, nil
	}

	elts := []Expr{first}
	for p.match(TokOp, ",") {
		if isEndOfExprList(p.cur) {
			break
		}
		var e Expr
		if p.check(TokOp, "*") {
			starPos := p.cur.Pos
			p.advance()
			val, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			e = &Starred{Pos: starPos, Value: val}
		} else {
			e, err = p.parseExpr()
			if err != nil {
				return nil, err
			}
		}
		elts = append(elts, e)
	}
	return &TupleExpr{Pos: pos, Elts: elts}, nil
}

// parseExprList parses a comma-separated list of expressions (for del etc.).
func (p *Parser) parseExprList() ([]Expr, error) {
	var exprs []Expr
	for {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		exprs = append(exprs, e)
		if !p.match(TokOp, ",") {
			break
		}
		if isEndOfExprList(p.cur) {
			break
		}
	}
	return exprs, nil
}

// parseTargetList parses a for-loop target (possibly a tuple).
// We use parseBitOr rather than parseExpr to avoid consuming the 'in' keyword
// that follows the target in for-loops and comprehensions.
func (p *Parser) parseTargetList() (Expr, error) {
	pos := p.cur.Pos
	first, err := p.parseTargetExpr()
	if err != nil {
		return nil, err
	}
	if !p.check(TokOp, ",") {
		return first, nil
	}
	elts := []Expr{first}
	for p.match(TokOp, ",") {
		if p.cur.Kind == TokName && p.cur.Value == "in" {
			break
		}
		if isEndOfExprList(p.cur) {
			break
		}
		e, err := p.parseTargetExpr()
		if err != nil {
			return nil, err
		}
		elts = append(elts, e)
	}
	if len(elts) == 1 {
		return elts[0], nil
	}
	return &TupleExpr{Pos: pos, Elts: elts}, nil
}

// parseTargetExpr parses a single for-loop target element without consuming 'in'.
// Valid targets: names, attributes, subscripts, starred, parenthesised tuples/lists.
func (p *Parser) parseTargetExpr() (Expr, error) {
	// Handle starred: *x
	if p.check(TokOp, "*") {
		pos := p.cur.Pos
		p.advance()
		inner, err := p.parseBitOr()
		if err != nil {
			return nil, err
		}
		return &Starred{Pos: pos, Value: inner}, nil
	}
	// Parenthesised or bracketed targets
	if p.check(TokOp, "(") || p.check(TokOp, "[") {
		open := p.cur.Value
		close_ := ")"
		if open == "[" {
			close_ = "]"
		}
		pos := p.cur.Pos
		p.advance()
		var elts []Expr
		for !p.check(TokOp, close_) && p.cur.Kind != TokEOF {
			e, err := p.parseTargetExpr()
			if err != nil {
				return nil, err
			}
			elts = append(elts, e)
			if !p.match(TokOp, ",") {
				break
			}
		}
		if _, err := p.expect(TokOp, close_); err != nil {
			return nil, err
		}
		if open == "[" {
			return &ListExpr{Pos: pos, Elts: elts}, nil
		}
		if len(elts) == 1 {
			return elts[0], nil
		}
		return &TupleExpr{Pos: pos, Elts: elts}, nil
	}
	// Otherwise parse as postfix expression (name, attr, subscript)
	return p.parsePostfix()
}

// isEndOfExprList returns true if the token ends a comma-separated expression list.
func isEndOfExprList(t Token) bool {
	if t.Kind == TokEOF || t.Kind == TokNewline || t.Kind == TokDedent {
		return true
	}
	if t.Kind == TokOp {
		switch t.Value {
		case ")", "]", "}", ";", "=", ":":
			return true
		}
	}
	if t.Kind == TokName {
		switch t.Value {
		case "for", "in", "if", "else", "elif":
			return true
		}
	}
	return false
}

// ---- Literal parsing helpers ----

// parseIntLiteral converts a Python integer literal string to int64 or *big.Int.
func parseIntLiteral(s string) (interface{}, error) {
	// Remove underscores.
	s = strings.ReplaceAll(s, "_", "")
	if s == "" {
		return int64(0), nil
	}

	base := 10
	orig := s
	if len(s) >= 2 && s[0] == '0' {
		switch s[1] {
		case 'x', 'X':
			base = 16
			s = s[2:]
		case 'o', 'O':
			base = 8
			s = s[2:]
		case 'b', 'B':
			base = 2
			s = s[2:]
		}
	}

	// Try int64 first.
	if n, err := strconv.ParseInt(s, base, 64); err == nil {
		return n, nil
	}
	// Try uint64 (only if the value fits in int64 to avoid silent wrap-around).
	if n, err := strconv.ParseUint(s, base, 64); err == nil && n <= math.MaxInt64 {
		return int64(n), nil
	}
	// Fall back to big.Int.
	bi := new(big.Int)
	if _, ok := bi.SetString(orig, 0); ok {
		if bi.IsInt64() {
			return bi.Int64(), nil
		}
		return bi, nil
	}
	return nil, fmt.Errorf("cannot parse integer %q", orig)
}

// parseFloatLiteral converts a Python float literal string to float64.
func parseFloatLiteral(s string) (float64, error) {
	// Remove underscores and 'j'/'J' suffix (complex).
	s = strings.ReplaceAll(s, "_", "")
	s = strings.TrimRight(s, "jJ")
	return strconv.ParseFloat(s, 64)
}

// ensure imports are used
var _ = utf8.RuneLen
var _ = big.NewInt
