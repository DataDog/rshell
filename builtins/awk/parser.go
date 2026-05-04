// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"fmt"
	"regexp"
)

// builtinFuncs is the set of builtin function names recognised at parse time.
// Each maps to (minArgs, maxArgs); -1 for maxArgs means unbounded.
var builtinFuncs = map[string][2]int{
	"length":  {0, 1},
	"substr":  {2, 3},
	"index":   {2, 2},
	"split":   {2, 3},
	"sub":     {2, 3},
	"gsub":    {2, 3},
	"match":   {2, 2},
	"sprintf": {1, -1},
	"tolower": {1, 1},
	"toupper": {1, 1},
	"int":     {1, 1},
	"sqrt":    {1, 1},
	"exp":     {1, 1},
	"log":     {1, 1},
	"sin":     {1, 1},
	"cos":     {1, 1},
	"atan2":   {2, 2},
	"rand":    {0, 0},
	"srand":   {0, 1},
}

// blockedNames are identifiers / functions that we explicitly reject at parse
// time for security or scope reasons.
var blockedNames = map[string]string{
	"system":   "system() (command execution) is blocked in the sandboxed shell",
	"ENVIRON":  "ENVIRON (environment exposure) is blocked in the sandboxed shell",
	"ARGV":     "ARGV (command-line argument vector) is not populated in the sandboxed shell",
	"ARGC":     "ARGC (command-line argument count) is not populated in the sandboxed shell",
	"close":    "close() is not supported (no I/O redirects allowed)",
	"fflush":   "fflush() is not supported (no I/O redirects allowed)",
	"getline":  "getline is not supported in this awk implementation",
	"function": "user-defined functions are not supported in this awk implementation",
}

// parseProgram is the top-level entry point.
func parseProgram(src string) (*program, error) {
	tokens, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{tokens: tokens}
	return p.parse()
}

type parser struct {
	tokens      []token
	pos         int
	inPrintList bool // suppress > / >> / | as relational/value operators
	depth       int  // recursion depth, capped to prevent stack-overflow DoS
}

// maxParseDepth caps recursive descent to prevent stack-overflow attacks via
// deeply nested expressions (e.g. ((((((1)))))) or $$$$x).
const maxParseDepth = 256

// enterDepth must be called at the entry of every recursive parser method.
// On overflow it returns an error so the parser bails out cleanly instead of
// crashing the goroutine.
func (p *parser) enterDepth() error {
	p.depth++
	if p.depth > maxParseDepth {
		return p.errorf("expression too deeply nested (max %d)", maxParseDepth)
	}
	return nil
}
func (p *parser) leaveDepth() { p.depth-- }

// peek returns the current token without consuming.
func (p *parser) peek() token { return p.tokens[p.pos] }

// peekAt returns the token at offset n from current position.
func (p *parser) peekAt(n int) token {
	idx := p.pos + n
	if idx >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1] // EOF
	}
	return p.tokens[idx]
}

// advance consumes the current token and returns it.
func (p *parser) advance() token {
	t := p.tokens[p.pos]
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	return t
}

// expect consumes a token of the required kind or returns an error.
func (p *parser) expect(k tokenKind) (token, error) {
	t := p.peek()
	if t.kind != k {
		return token{}, p.errorf("expected %s but got %s", tokenName(k), tokenName(t.kind))
	}
	return p.advance(), nil
}

// accept consumes and returns true if the current token matches kind.
func (p *parser) accept(k tokenKind) bool {
	if p.peek().kind == k {
		p.advance()
		return true
	}
	return false
}

// skipTerminators consumes any sequence of newlines/semicolons.
func (p *parser) skipTerminators() {
	for p.peek().kind == tkNewline || p.peek().kind == tkSemicolon {
		p.advance()
	}
}

// errorf builds a parse error annotated with the current line.
func (p *parser) errorf(format string, args ...any) error {
	return fmt.Errorf("line %d: %s", p.peek().line, fmt.Sprintf(format, args...))
}

// parse consumes tokens and returns a program.
func (p *parser) parse() (*program, error) {
	prog := &program{}
	p.skipTerminators()
	for p.peek().kind != tkEOF {
		// Reject user-defined functions at the top level.
		if p.peek().kind == tkFunction {
			return nil, p.errorf("user-defined functions are not supported")
		}
		r, err := p.parseRule()
		if err != nil {
			return nil, err
		}
		prog.rules = append(prog.rules, r)
		// A rule may be followed by a terminator, EOF, or directly by the
		// start of another rule (classic awk: "BEGIN {…} {…} END {…}" on
		// one line).
		switch p.peek().kind {
		case tkEOF:
			// done
		case tkNewline, tkSemicolon:
			p.skipTerminators()
		case tkBegin, tkEnd, tkLBrace, tkRegex,
			tkIdent, tkFuncName, tkNumber, tkString,
			tkDollar, tkLParen, tkNot, tkMinus, tkPlus:
			// Implicit rule boundary.
		default:
			return nil, p.errorf("expected newline or ';' after rule, got %s", tokenName(p.peek().kind))
		}
	}
	return prog, nil
}

// parseRule parses a pattern-action pair. Either part may be omitted.
func (p *parser) parseRule() (rule, error) {
	r := rule{}
	// Pattern.
	switch p.peek().kind {
	case tkLBrace:
		// No pattern; action only.
		r.pat = &alwaysPattern{}
	case tkBegin:
		p.advance()
		r.pat = &beginPattern{}
	case tkEnd:
		p.advance()
		r.pat = &endPattern{}
	default:
		first, err := p.parsePatternExpr()
		if err != nil {
			return rule{}, err
		}
		if p.accept(tkComma) {
			second, err := p.parsePatternExpr()
			if err != nil {
				return rule{}, err
			}
			r.pat = &rangePattern{start: first, end: second}
		} else {
			r.pat = first
		}
	}
	// Action.
	switch p.peek().kind {
	case tkLBrace:
		body, err := p.parseBlock()
		if err != nil {
			return rule{}, err
		}
		r.action = body
	default:
		// Default action depends on pattern kind:
		// - BEGIN/END require an action; reject if missing.
		switch r.pat.(type) {
		case *beginPattern, *endPattern:
			return rule{}, p.errorf("BEGIN/END require an action block")
		}
		// Pattern-only rule: implicit { print }.
		r.action = nil
	}
	return r, nil
}

// parsePatternExpr parses a single pattern (excluding BEGIN/END/range), which
// is either a regex literal or a general expression.
func (p *parser) parsePatternExpr() (pattern, error) {
	if p.peek().kind == tkRegex {
		t := p.advance()
		re, err := compileERE(t.val)
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid regex /%s/: %v", t.line, t.val, err)
		}
		return &regexPattern{re: re, src: t.val}, nil
	}
	// General expression.
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &exprPattern{e: e}, nil
}

// parseBlock parses a {...} block.
func (p *parser) parseBlock() (*blockStmt, error) {
	if _, err := p.expect(tkLBrace); err != nil {
		return nil, err
	}
	body, err := p.parseStmtList(tkRBrace)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tkRBrace); err != nil {
		return nil, err
	}
	return &blockStmt{body: body}, nil
}

// parseStmtList reads statements until it sees the terminator token kind.
func (p *parser) parseStmtList(end tokenKind) ([]stmt, error) {
	var stmts []stmt
	p.skipTerminators()
	for p.peek().kind != end && p.peek().kind != tkEOF {
		s, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, s)
		p.skipTerminators()
	}
	return stmts, nil
}

// parseStmt parses a single statement.
func (p *parser) parseStmt() (stmt, error) {
	if err := p.enterDepth(); err != nil {
		return nil, err
	}
	defer p.leaveDepth()
	t := p.peek()
	switch t.kind {
	case tkLBrace:
		return p.parseBlock()
	case tkIf:
		return p.parseIf()
	case tkWhile:
		return p.parseWhile()
	case tkDo:
		return p.parseDoWhile()
	case tkFor:
		return p.parseFor()
	case tkBreak:
		p.advance()
		return &breakStmt{}, nil
	case tkContinue:
		p.advance()
		return &continueStmt{}, nil
	case tkNext:
		p.advance()
		return &nextStmt{}, nil
	case tkExit:
		p.advance()
		st := &exitStmt{}
		if !p.atStmtTerminator() {
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			st.code = e
		}
		return st, nil
	case tkDelete:
		return p.parseDelete()
	case tkPrint:
		return p.parsePrint()
	case tkPrintf:
		return p.parsePrintf()
	case tkReturn:
		return nil, p.errorf("'return' is only valid inside user-defined functions (not supported)")
	case tkGetline:
		return nil, p.errorf("getline is not supported in this awk implementation")
	case tkFunction:
		return nil, p.errorf("user-defined functions are not supported")
	case tkSemicolon, tkNewline:
		p.advance()
		return &blockStmt{}, nil
	}
	// Expression statement.
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &exprStmt{expr: e}, nil
}

func (p *parser) atStmtTerminator() bool {
	switch p.peek().kind {
	case tkNewline, tkSemicolon, tkEOF, tkRBrace:
		return true
	}
	return false
}

// parseIf parses if (cond) stmt [else stmt].
func (p *parser) parseIf() (stmt, error) {
	p.advance() // 'if'
	if _, err := p.expect(tkLParen); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tkRParen); err != nil {
		return nil, err
	}
	p.skipTerminators()
	body, err := p.parseStmt()
	if err != nil {
		return nil, err
	}
	is := &ifStmt{cond: cond, then: body}
	// optional else
	p.skipTerminators()
	if p.accept(tkElse) {
		p.skipTerminators()
		els, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		is.else_ = els
	}
	return is, nil
}

// parseWhile parses while (cond) stmt.
func (p *parser) parseWhile() (stmt, error) {
	p.advance() // 'while'
	if _, err := p.expect(tkLParen); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tkRParen); err != nil {
		return nil, err
	}
	p.skipTerminators()
	body, err := p.parseStmt()
	if err != nil {
		return nil, err
	}
	return &whileStmt{cond: cond, body: body}, nil
}

// parseDoWhile parses do stmt while (cond).
func (p *parser) parseDoWhile() (stmt, error) {
	p.advance() // 'do'
	p.skipTerminators()
	body, err := p.parseStmt()
	if err != nil {
		return nil, err
	}
	p.skipTerminators()
	if _, err := p.expect(tkWhile); err != nil {
		return nil, err
	}
	if _, err := p.expect(tkLParen); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tkRParen); err != nil {
		return nil, err
	}
	return &doWhileStmt{cond: cond, body: body}, nil
}

// parseFor parses both forms: classic C-style and "for (var in array)".
func (p *parser) parseFor() (stmt, error) {
	p.advance() // 'for'
	if _, err := p.expect(tkLParen); err != nil {
		return nil, err
	}

	// Detect "for (var in array)" — needs lookahead.
	if p.peek().kind == tkIdent && p.peekAt(1).kind == tkIn {
		loopVarTok := p.advance()
		v := loopVarTok.val
		// Validate the loop variable against blockedNames for consistency with
		// the array-variable check below (e.g. "for (ENVIRON in arr)" should error).
		if reason, blocked := blockedNames[v]; blocked {
			return nil, fmt.Errorf("line %d: %s", loopVarTok.line, reason)
		}
		p.advance() // 'in'
		arr, err := p.expect(tkIdent)
		if err != nil {
			return nil, err
		}
		if reason, blocked := blockedNames[arr.val]; blocked {
			return nil, fmt.Errorf("line %d: %s", arr.line, reason)
		}
		if _, err := p.expect(tkRParen); err != nil {
			return nil, err
		}
		p.skipTerminators()
		body, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		return &forInStmt{loopVar: v, arrayVar: arr.val, body: body}, nil
	}

	// Classic for(init; cond; post) body.
	var initStmt stmt
	if p.peek().kind != tkSemicolon {
		s, err := p.parseSimpleStmtForFor()
		if err != nil {
			return nil, err
		}
		initStmt = s
	}
	if _, err := p.expect(tkSemicolon); err != nil {
		return nil, err
	}
	var cond expr
	if p.peek().kind != tkSemicolon {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		cond = e
	}
	if _, err := p.expect(tkSemicolon); err != nil {
		return nil, err
	}
	var postStmt stmt
	if p.peek().kind != tkRParen {
		s, err := p.parseSimpleStmtForFor()
		if err != nil {
			return nil, err
		}
		postStmt = s
	}
	if _, err := p.expect(tkRParen); err != nil {
		return nil, err
	}
	p.skipTerminators()
	body, err := p.parseStmt()
	if err != nil {
		return nil, err
	}
	return &forStmt{init: initStmt, cond: cond, post: postStmt, body: body}, nil
}

// parseSimpleStmtForFor parses an expression statement for use in for-init / for-post.
func (p *parser) parseSimpleStmtForFor() (stmt, error) {
	e, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &exprStmt{expr: e}, nil
}

// parseDelete parses delete arr[i] or delete arr.
func (p *parser) parseDelete() (stmt, error) {
	p.advance() // 'delete'
	id, err := p.expect(tkIdent)
	if err != nil {
		return nil, err
	}
	if reason, blocked := blockedNames[id.val]; blocked {
		return nil, fmt.Errorf("line %d: %s", id.line, reason)
	}
	st := &deleteStmt{arrayVar: id.val}
	if p.accept(tkLBracket) {
		args, err := p.parseExprList(tkRBracket)
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tkRBracket); err != nil {
			return nil, err
		}
		st.indices = args
	}
	return st, nil
}

// parsePrint parses a print statement.
func (p *parser) parsePrint() (stmt, error) {
	args, err := p.parsePrintLike("print", false)
	if err != nil {
		return nil, err
	}
	return &printStmt{args: args}, nil
}

// parsePrintf parses a printf statement.
func (p *parser) parsePrintf() (stmt, error) {
	args, err := p.parsePrintLike("printf", true)
	if err != nil {
		return nil, err
	}
	return &printfStmt{args: args}, nil
}

// parsePrintLike implements the shared body of print and printf parsing. The
// only behavioural difference is that printf demands at least one argument
// (the format string).
func (p *parser) parsePrintLike(name string, requireFormat bool) ([]expr, error) {
	p.advance() // print/printf keyword
	if p.atStmtTerminator() {
		if requireFormat {
			return nil, p.errorf("%s requires at least a format argument", name)
		}
		return nil, nil
	}
	if err := p.checkNoRedirect(name); err != nil {
		return nil, err
	}
	args, err := p.parsePrintArgList()
	if err != nil {
		return nil, err
	}
	if requireFormat && len(args) == 0 {
		return nil, p.errorf("%s requires at least a format argument", name)
	}
	if err := p.checkNoRedirect(name); err != nil {
		return nil, err
	}
	return args, nil
}

// checkNoRedirect rejects > / >> / | redirect operators on print/printf.
// Called both before and after parsing the argument list to catch any redirect.
func (p *parser) checkNoRedirect(stmtName string) error {
	switch p.peek().kind {
	case tkGt:
		return p.errorf("%s output redirection ('> file') is not supported in the sandboxed shell", stmtName)
	case tkAppend:
		return p.errorf("%s output redirection ('>> file') is not supported in the sandboxed shell", stmtName)
	case tkPipe:
		return p.errorf("%s pipe ('| cmd') is not supported in the sandboxed shell", stmtName)
	}
	return nil
}

// parsePrintArgList parses a comma-separated expression list for print/printf.
// We do not allow the relational operators or '>' here because '>' would be
// ambiguous with output redirection — and we are blocking output redirection
// anyway. To keep error messages clear, we forbid '>' explicitly via the
// surrounding checkNoRedirect.
func (p *parser) parsePrintArgList() ([]expr, error) {
	prev := p.inPrintList
	p.inPrintList = true
	defer func() { p.inPrintList = prev }()
	var args []expr
	for {
		e, err := p.parseExprNoIn()
		if err != nil {
			return nil, err
		}
		args = append(args, e)
		if !p.accept(tkComma) {
			break
		}
		p.skipTerminators()
	}
	return args, nil
}

// parseExprList parses a comma-separated expression list (e.g. function args).
func (p *parser) parseExprList(end tokenKind) ([]expr, error) {
	var args []expr
	if p.peek().kind == end {
		return args, nil
	}
	for {
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, e)
		if !p.accept(tkComma) {
			break
		}
		p.skipTerminators()
	}
	return args, nil
}

// =====================================================================
// Expression precedence (low to high), POSIX awk:
//
//   assignment       = += -= *= /= %= ^=
//   ternary          ? :
//   logical or       ||
//   logical and      &&
//   in               (k in arr)
//   match            ~ !~
//   relational       < <= > >= != ==
//   concat           (juxtaposition)
//   range            (n/a)
//   addsub           + -
//   mul/div/mod      * / %
//   exponent         ^ **      (right-assoc)
//   prefix           ! - +
//   incr/decr        ++ --
//   field            $
//   group/call/index ( ) [ ]
//
// "in" appears in two places: as an operator binding tighter than match, and
// as a special form "(idx in arr)" inside parens. We have already special-
// cased "for (v in arr)".
// =====================================================================

// parseExpr is the top-level expression parser.
func (p *parser) parseExpr() (expr, error) {
	if err := p.enterDepth(); err != nil {
		return nil, err
	}
	defer p.leaveDepth()
	return p.parseAssignment()
}

// parseExprNoIn is identical to parseExpr; we keep the alias because gawk
// has a distinction in some contexts. We do not implement that distinction
// here.
func (p *parser) parseExprNoIn() (expr, error) {
	return p.parseAssignment()
}

func (p *parser) parseAssignment() (expr, error) {
	lhs, err := p.parseTernary()
	if err != nil {
		return nil, err
	}
	switch p.peek().kind {
	case tkAssign, tkAddAssign, tkSubAssign, tkMulAssign, tkDivAssign, tkModAssign, tkPowAssign:
		op := p.advance().kind
		if !isLValue(lhs) {
			return nil, p.errorf("invalid assignment target")
		}
		rhs, err := p.parseAssignment()
		if err != nil {
			return nil, err
		}
		return &assignExpr{op: op, left: lhs, right: rhs}, nil
	}
	return lhs, nil
}

func isLValue(e expr) bool {
	switch e.(type) {
	case *identExpr, *indexExpr, *fieldExpr:
		return true
	}
	return false
}

func (p *parser) parseTernary() (expr, error) {
	c, err := p.parseLogicalOr()
	if err != nil {
		return nil, err
	}
	if p.accept(tkQuestion) {
		t, err := p.parseTernary()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tkColon); err != nil {
			return nil, err
		}
		e, err := p.parseTernary()
		if err != nil {
			return nil, err
		}
		return &condExpr{cond: c, then: t, else_: e}, nil
	}
	return c, nil
}

func (p *parser) parseLogicalOr() (expr, error) {
	left, err := p.parseLogicalAnd()
	if err != nil {
		return nil, err
	}
	for p.accept(tkOr) {
		p.skipTerminators()
		right, err := p.parseLogicalAnd()
		if err != nil {
			return nil, err
		}
		left = &binaryExpr{op: tkOr, left: left, right: right}
	}
	return left, nil
}

func (p *parser) parseLogicalAnd() (expr, error) {
	left, err := p.parseInExpr()
	if err != nil {
		return nil, err
	}
	for p.accept(tkAnd) {
		p.skipTerminators()
		right, err := p.parseInExpr()
		if err != nil {
			return nil, err
		}
		left = &binaryExpr{op: tkAnd, left: left, right: right}
	}
	return left, nil
}

// parseInExpr handles the "expr in array" form, evaluating left-to-right.
// We accept `expr in name` where name is a plain identifier (the array).
func (p *parser) parseInExpr() (expr, error) {
	left, err := p.parseMatch()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tkIn {
		p.advance()
		arr, err := p.expect(tkIdent)
		if err != nil {
			return nil, err
		}
		if reason, blocked := blockedNames[arr.val]; blocked {
			return nil, fmt.Errorf("line %d: %s", arr.line, reason)
		}
		left = &inExpr{keys: []expr{left}, arrayVar: arr.val}
	}
	return left, nil
}

func (p *parser) parseMatch() (expr, error) {
	left, err := p.parseRelational()
	if err != nil {
		return nil, err
	}
	for p.peek().kind == tkMatch || p.peek().kind == tkNotMatch {
		op := p.advance().kind
		// RHS is either a regex literal (compile now) or any expression.
		me := &matchExpr{negate: op == tkNotMatch, left: left}
		if p.peek().kind == tkRegex {
			t := p.advance()
			re, err := compileERE(t.val)
			if err != nil {
				return nil, fmt.Errorf("line %d: invalid regex /%s/: %v", t.line, t.val, err)
			}
			me.re = re
		} else {
			r, err := p.parseRelational()
			if err != nil {
				return nil, err
			}
			me.right = r
		}
		left = me
	}
	return left, nil
}

func (p *parser) parseRelational() (expr, error) {
	left, err := p.parseConcat()
	if err != nil {
		return nil, err
	}
	for {
		k := p.peek().kind
		// In print/printf argument lists, '>' / '>>' / '|' are reserved for
		// (blocked) output redirection. Stop here so the surrounding
		// checkNoRedirect can produce a clear error. Note: '>=' (tkGe) is a
		// normal relational operator and must NOT be treated as a redirect.
		if p.inPrintList && (k == tkGt || k == tkAppend || k == tkPipe) {
			break
		}
		switch k {
		case tkLt, tkLe, tkGt, tkGe, tkEq, tkNe:
			op := p.advance().kind
			right, err := p.parseConcat()
			if err != nil {
				return nil, err
			}
			left = &binaryExpr{op: op, left: left, right: right}
			continue
		}
		break
	}
	return left, nil
}

// parseConcat handles juxtaposition. Concatenation has no operator — it just
// applies when an expression is followed by the start of another expression
// at this precedence level.
func (p *parser) parseConcat() (expr, error) {
	left, err := p.parseAddSub()
	if err != nil {
		return nil, err
	}
	for canStartConcat(p.peek().kind) {
		right, err := p.parseAddSub()
		if err != nil {
			return nil, err
		}
		switch lc := left.(type) {
		case *concatExpr:
			lc.parts = append(lc.parts, right)
		default:
			left = &concatExpr{parts: []expr{left, right}}
		}
	}
	return left, nil
}

// canStartConcat reports whether the given token can start a value-producing
// expression at concat precedence (so the previous expression should be
// concatenated with it).
func canStartConcat(k tokenKind) bool {
	switch k {
	case tkIdent, tkFuncName, tkNumber, tkString,
		tkDollar, tkLParen, tkNot:
		return true
	}
	return false
}

func (p *parser) parseAddSub() (expr, error) {
	left, err := p.parseMulDiv()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek().kind {
		case tkPlus, tkMinus:
			op := p.advance().kind
			right, err := p.parseMulDiv()
			if err != nil {
				return nil, err
			}
			left = &binaryExpr{op: op, left: left, right: right}
			continue
		}
		break
	}
	return left, nil
}

func (p *parser) parseMulDiv() (expr, error) {
	left, err := p.parsePrefix()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek().kind {
		case tkStar, tkSlash, tkPercent:
			op := p.advance().kind
			right, err := p.parsePrefix()
			if err != nil {
				return nil, err
			}
			left = &binaryExpr{op: op, left: left, right: right}
			continue
		}
		break
	}
	return left, nil
}

// parseExp implements right-associative ^ (exponentiation).
// Per awk's operator precedence, ^ binds tighter than unary minus:
// -x^2 is -(x^2), not (-x)^2. To achieve this, parseExp is called
// from parseMulDiv (via parsePrefix→parseExp for the unary case),
// and unary operators call parseExp for their operand rather than
// calling themselves recursively.
func (p *parser) parseExp() (expr, error) {
	left, err := p.parseField()
	if err != nil {
		return nil, err
	}
	if p.peek().kind == tkCaret {
		p.advance()
		// Right-associative: recurse through parsePrefix so that
		// the right operand can itself start with a unary operator.
		right, err := p.parsePrefix()
		if err != nil {
			return nil, err
		}
		return &binaryExpr{op: tkCaret, left: left, right: right}, nil
	}
	return left, nil
}

func (p *parser) parsePrefix() (expr, error) {
	switch p.peek().kind {
	case tkNot, tkMinus, tkPlus:
		op := p.advance().kind
		// Unary minus/plus/not bind LOOSER than ^ (exponentiation).
		// Delegate to parseExp so that -x^2 is parsed as -(x^2).
		operand, err := p.parseExp()
		if err != nil {
			return nil, err
		}
		return &unaryExpr{op: op, operand: operand}, nil
	case tkInc, tkDec:
		op := p.advance().kind
		operand, err := p.parseField()
		if err != nil {
			return nil, err
		}
		if !isLValue(operand) {
			return nil, p.errorf("invalid target for %s", tokenName(op))
		}
		return &incrExpr{post: false, op: op, expr: operand}, nil
	}
	return p.parseExp()
}

// parseField handles $ and post-increment/decrement.
func (p *parser) parseField() (expr, error) {
	if err := p.enterDepth(); err != nil {
		return nil, err
	}
	defer p.leaveDepth()
	if p.accept(tkDollar) {
		operand, err := p.parseField()
		if err != nil {
			return nil, err
		}
		return p.maybePostfix(&fieldExpr{index: operand})
	}
	primary, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	return p.maybePostfix(primary)
}

// maybePostfix handles x++ / x-- after a primary.
func (p *parser) maybePostfix(e expr) (expr, error) {
	switch p.peek().kind {
	case tkInc, tkDec:
		op := p.advance().kind
		if !isLValue(e) {
			return nil, p.errorf("invalid target for %s", tokenName(op))
		}
		return &incrExpr{post: true, op: op, expr: e}, nil
	}
	return e, nil
}

// parsePrimary handles literals, identifiers, function calls, parens.
func (p *parser) parsePrimary() (expr, error) {
	t := p.peek()
	switch t.kind {
	case tkNumber:
		p.advance()
		return &numExpr{val: t.num, src: t.val}, nil
	case tkString:
		p.advance()
		return &strExpr{val: t.val}, nil
	case tkRegex:
		p.advance()
		re, err := compileERE(t.val)
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid regex /%s/: %v", t.line, t.val, err)
		}
		return &regexExpr{re: re, src: t.val}, nil
	case tkLParen:
		p.advance()
		// "(a,b,...) in arr" form.
		// We parse an expression list; if followed by ')' and 'in', it's a
		// multi-key in-test. Otherwise it's a parenthesised expression.
		// Inside parens, output redirection is impossible — clear inPrintList.
		prev := p.inPrintList
		p.inPrintList = false
		exprs, err := p.parseExprList(tkRParen)
		p.inPrintList = prev
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tkRParen); err != nil {
			return nil, err
		}
		if p.peek().kind == tkIn && len(exprs) > 0 {
			p.advance()
			arr, err := p.expect(tkIdent)
			if err != nil {
				return nil, err
			}
			if reason, blocked := blockedNames[arr.val]; blocked {
				return nil, fmt.Errorf("line %d: %s", arr.line, reason)
			}
			return &inExpr{keys: exprs, arrayVar: arr.val}, nil
		}
		if len(exprs) != 1 {
			return nil, p.errorf("unexpected expression list inside parentheses")
		}
		return exprs[0], nil
	case tkFuncName:
		return p.parseCall()
	case tkIdent:
		p.advance()
		// Reject blocked identifiers before any further usage.
		if reason, blocked := blockedNames[t.val]; blocked {
			return nil, fmt.Errorf("line %d: %s", t.line, reason)
		}
		// GNU awk allows whitespace between a built-in function name and its '('.
		// The lexer only emits tkFuncName when '(' is immediate; handle the
		// spaced case here by re-dispatching to parseCall.
		if _, isBuiltin := builtinFuncs[t.val]; isBuiltin && p.peek().kind == tkLParen {
			// Re-insert token and let parseCall handle it.
			p.pos-- // unconsume the tkIdent
			p.tokens[p.pos] = token{kind: tkFuncName, val: t.val, line: t.line}
			return p.parseCall()
		}
		// POSIX/gawk: bare `length` (no parentheses) means length($0).
		// Unlike user-defined functions, `length x` does NOT pass x as the
		// argument: gawk and mawk parse it as concat(length($0), x), so we emit
		// a no-arg call and let the concatenation context consume any following
		// identifier token.  Only `length(arr)` with explicit parens computes
		// the length of an array.
		if t.val == "length" && p.peek().kind != tkLBracket && p.peek().kind != tkLParen {
			return &callExpr{name: "length"}, nil
		}
		// Array subscript?
		if p.accept(tkLBracket) {
			args, err := p.parseExprList(tkRBracket)
			if err != nil {
				return nil, err
			}
			if _, err := p.expect(tkRBracket); err != nil {
				return nil, err
			}
			if len(args) == 0 {
				return nil, p.errorf("array subscript cannot be empty")
			}
			return &indexExpr{name: t.val, indices: args}, nil
		}
		return &identExpr{name: t.val}, nil
	}
	return nil, p.errorf("unexpected token %s", tokenName(t.kind))
}

// parseCall parses fname(args).
func (p *parser) parseCall() (expr, error) {
	id := p.advance() // function name
	if reason, blocked := blockedNames[id.val]; blocked {
		return nil, fmt.Errorf("line %d: %s", id.line, reason)
	}
	if _, ok := builtinFuncs[id.val]; !ok {
		return nil, fmt.Errorf("line %d: unknown function %q", id.line, id.val)
	}
	if _, err := p.expect(tkLParen); err != nil {
		return nil, err
	}
	args, err := p.parseExprList(tkRParen)
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tkRParen); err != nil {
		return nil, err
	}
	if minMax, ok := builtinFuncs[id.val]; ok {
		min, max := minMax[0], minMax[1]
		if len(args) < min || (max >= 0 && len(args) > max) {
			return nil, fmt.Errorf("line %d: %s() called with wrong number of arguments", id.line, id.val)
		}
	}
	return &callExpr{name: id.val, args: args}, nil
}

// compileERE compiles an extended regular expression using Go's RE2 engine.
// We translate awk-specific quirks to Go regex where reasonable; awk's POSIX
// ERE is largely a subset of Go's regex syntax.
func compileERE(src string) (*regexp.Regexp, error) {
	return regexp.Compile(src)
}
