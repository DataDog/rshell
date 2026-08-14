// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import "fmt"

const (
	maxParserDepth        = 512
	maxFunctionArguments  = 256
	maxFunctionParameters = 256

	precAssign  = 10
	precTernary = 15
	precOr      = 20
	precAnd     = 30
	precCompare = 40
	precConcat  = 50
	precAdd     = 60
	precMul     = 70
	precPrefix  = 80
	precPower   = 90
	precPostfix = 100
)

var unsupportedBuiltinFunctions = map[string]struct{}{
	"and":            {},
	"asort":          {},
	"atan2":          {},
	"bindtextdomain": {},
	"compl":          {},
	"cos":            {},
	"dcgettext":      {},
	"dcngettext":     {},
	"exp":            {},
	"fflush":         {},
	"isarray":        {},
	"log":            {},
	"lshift":         {},
	"mktime":         {},
	"or":             {},
	"patsplit":       {},
	"rand":           {},
	"rshift":         {},
	"sin":            {},
	"sqrt":           {},
	"srand":          {},
	"strftime":       {},
	"system":         {},
	"systime":        {},
	"typeof":         {},
	"xor":            {},
}

var supportedBuiltinFunctions = map[string]struct{}{
	"close":    {},
	"asorti":   {},
	"gensub":   {},
	"gsub":     {},
	"index":    {},
	"int":      {},
	"length":   {},
	"match":    {},
	"split":    {},
	"sprintf":  {},
	"strtonum": {},
	"sub":      {},
	"substr":   {},
	"tolower":  {},
	"toupper":  {},
}

type parser struct {
	toks              []token
	pos               int
	stopPrintRedirect bool
	nestingDepth      int
}

func (p *parser) enterNesting() error {
	if p.nestingDepth >= maxParserDepth {
		return parserNestingError()
	}
	p.nestingDepth++
	return nil
}

func (p *parser) leaveNesting() {
	p.nestingDepth--
}

func parserNestingError() error {
	return fmt.Errorf("parser nesting depth limit exceeded (maximum %d)", maxParserDepth)
}

type syntaxNodeDepth struct {
	node  any
	depth int
}

func validateProgramNesting(prog *program) error {
	stack := make([]syntaxNodeDepth, 0)
	pushExpr := func(x expr, depth int) {
		if x != nil {
			stack = append(stack, syntaxNodeDepth{node: x, depth: depth})
		}
	}
	pushStmt := func(s stmt, depth int) {
		if s != nil {
			stack = append(stack, syntaxNodeDepth{node: s, depth: depth})
		}
	}
	for _, r := range prog.rules {
		pushExpr(r.pattern, 1)
		for _, s := range r.action {
			pushStmt(s, 1)
		}
	}
	for _, fn := range prog.functions {
		for _, s := range fn.body {
			pushStmt(s, 1)
		}
	}
	for len(stack) > 0 {
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if item.depth > maxParserDepth {
			return parserNestingError()
		}
		childDepth := item.depth + 1
		switch n := item.node.(type) {
		case *printStmt:
			for _, x := range n.args {
				pushExpr(x, childDepth)
			}
			pushExpr(n.pipe, childDepth)
		case *printfStmt:
			for _, x := range n.args {
				pushExpr(x, childDepth)
			}
			pushExpr(n.pipe, childDepth)
		case *ifStmt:
			pushExpr(n.cond, childDepth)
			for _, s := range n.thenStmts {
				pushStmt(s, childDepth)
			}
			for _, s := range n.elseStmts {
				pushStmt(s, childDepth)
			}
		case *forInStmt:
			for _, s := range n.body {
				pushStmt(s, childDepth)
			}
		case *forStmt:
			pushExpr(n.init, childDepth)
			pushExpr(n.cond, childDepth)
			pushExpr(n.post, childDepth)
			for _, s := range n.body {
				pushStmt(s, childDepth)
			}
		case *whileStmt:
			pushExpr(n.cond, childDepth)
			for _, s := range n.body {
				pushStmt(s, childDepth)
			}
		case *exitStmt:
			pushExpr(n.status, childDepth)
		case *returnStmt:
			pushExpr(n.value, childDepth)
		case *deleteStmt:
			for _, x := range n.indices {
				pushExpr(x, childDepth)
			}
		case *exprStmt:
			pushExpr(n.x, childDepth)
		case *arrayRefExpr:
			for _, x := range n.indices {
				pushExpr(x, childDepth)
			}
		case *compositeExpr:
			for _, x := range n.parts {
				pushExpr(x, childDepth)
			}
		case *fieldExpr:
			pushExpr(n.index, childDepth)
		case *groupedExpr:
			pushExpr(n.x, childDepth)
		case *unaryExpr:
			pushExpr(n.x, childDepth)
		case *binaryExpr:
			pushExpr(n.left, childDepth)
			pushExpr(n.right, childDepth)
		case *ternaryExpr:
			pushExpr(n.cond, childDepth)
			pushExpr(n.then, childDepth)
			pushExpr(n.els, childDepth)
		case *rangeExpr:
			pushExpr(n.start, childDepth)
			pushExpr(n.end, childDepth)
		case *assignExpr:
			pushExpr(n.left, childDepth)
			pushExpr(n.right, childDepth)
		case *incDecExpr:
			pushExpr(n.x, childDepth)
		case *callExpr:
			for _, x := range n.args {
				pushExpr(x, childDepth)
			}
		case *getlineExpr:
			pushExpr(n.target, childDepth)
			pushExpr(n.source, childDepth)
		case *nextStmt, *breakStmt, *continueStmt, *numberExpr, *stringExpr, *regexExpr, *varExpr:
		default:
			return fmt.Errorf("unknown syntax node")
		}
	}
	return nil
}

func parseProgram(src string) (*program, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	prog := &program{functions: make(map[string]*functionDef)}
	p.skipSeparators()
	for !p.at(tokEOF) {
		if p.atIdent("function") {
			fn, err := p.parseFunctionDefinition()
			if err != nil {
				return nil, err
			}
			if _, exists := prog.functions[fn.name]; exists {
				return nil, fmt.Errorf("function %q is already defined", fn.name)
			}
			prog.functions[fn.name] = fn
			p.skipSeparators()
			continue
		}
		r, err := p.parseRule()
		if err != nil {
			return nil, err
		}
		prog.rules = append(prog.rules, r)
		p.skipSeparators()
	}
	if err := validateProgramNesting(prog); err != nil {
		return nil, err
	}
	if err := validateLoopControlStatements(prog); err != nil {
		return nil, err
	}
	if err := validateUserFunctionNameReferences(prog); err != nil {
		return nil, err
	}
	return prog, nil
}

func (p *parser) parseFunctionDefinition() (*functionDef, error) {
	p.advance()
	if p.cur().kind != tokIdent {
		return nil, fmt.Errorf("expected function name")
	}
	name := p.cur().lit
	if err := validateFunctionName(name); err != nil {
		return nil, err
	}
	p.advance()
	if !p.match(tokLParen) {
		return nil, fmt.Errorf("expected ( after function name")
	}
	params := []string{}
	seen := make(map[string]int)
	p.skipSeparators()
	if !p.match(tokRParen) {
		for {
			p.skipSeparators()
			if p.cur().kind != tokIdent {
				return nil, fmt.Errorf("expected function parameter")
			}
			if len(params) >= maxFunctionParameters {
				return nil, fmt.Errorf("function %q has too many parameters (maximum %d)", name, maxFunctionParameters)
			}
			param := p.cur().lit
			if err := validateFunctionParameterName(name, param); err != nil {
				return nil, err
			}
			if first, ok := seen[param]; ok {
				return nil, fmt.Errorf("function %q parameter #%d, %q, duplicates parameter #%d", name, len(params)+1, param, first)
			}
			seen[param] = len(params) + 1
			params = append(params, param)
			p.advance()
			p.skipSeparators()
			if p.match(tokRParen) {
				break
			}
			if !p.match(tokComma) {
				return nil, fmt.Errorf("expected , or ) in function parameter list")
			}
		}
	}
	p.skipSeparators()
	body, err := p.parseAction()
	if err != nil {
		return nil, err
	}
	return &functionDef{name: name, params: params, body: body}, nil
}

func (p *parser) parseRule() (rule, error) {
	if p.atIdent("BEGIN") {
		p.advance()
		action, err := p.parseAction()
		return rule{kind: ruleBegin, action: action}, err
	}
	if p.atIdent("END") {
		p.advance()
		action, err := p.parseAction()
		return rule{kind: ruleEnd, action: action}, err
	}
	if p.at(tokLBrace) {
		action, err := p.parseAction()
		return rule{kind: ruleNormal, action: action}, err
	}
	pattern, err := p.parseExpression(0)
	if err != nil {
		return rule{}, err
	}
	if p.at(tokComma) {
		p.advance()
		end, err := p.parseExpression(0)
		if err != nil {
			return rule{}, err
		}
		pattern = &rangeExpr{start: pattern, end: end}
	}
	if p.at(tokLBrace) {
		action, err := p.parseAction()
		return rule{kind: ruleNormal, pattern: pattern, action: action}, err
	}
	return rule{kind: ruleNormal, pattern: pattern}, nil
}

func (p *parser) parseAction() ([]stmt, error) {
	if !p.match(tokLBrace) {
		return nil, fmt.Errorf("expected action")
	}
	return p.parseStatementList()
}

func (p *parser) parseStatementList() ([]stmt, error) {
	stmts := []stmt{}
	p.skipSeparators()
	for !p.at(tokRBrace) {
		if p.at(tokEOF) {
			return nil, fmt.Errorf("unterminated action")
		}
		st, err := p.parseStatement()
		if err != nil {
			return nil, err
		}
		stmts = append(stmts, st)
		if !p.at(tokRBrace) && !p.at(tokEOF) && !isSeparator(p.cur().kind) && !statementEndsBlock(st) {
			return nil, fmt.Errorf("expected statement separator")
		}
		p.skipSeparators()
	}
	p.advance()
	return stmts, nil
}

func statementEndsBlock(st stmt) bool {
	switch s := st.(type) {
	case *ifStmt:
		return s.endsBlock
	case *forStmt:
		return s.endsBlock
	case *forInStmt:
		return s.endsBlock
	case *whileStmt:
		return s.endsBlock
	default:
		return false
	}
}

func (p *parser) parseStatement() (stmt, error) {
	if err := p.enterNesting(); err != nil {
		return nil, err
	}
	defer p.leaveNesting()

	if p.atIdent("if") {
		return p.parseIf()
	}
	if p.atIdent("for") {
		return p.parseFor()
	}
	if p.atIdent("while") {
		return p.parseWhile()
	}
	if p.atIdent("next") {
		p.advance()
		return &nextStmt{}, nil
	}
	if p.atIdent("exit") {
		return p.parseExit()
	}
	if p.atIdent("return") {
		return p.parseReturn()
	}
	if p.atIdent("break") {
		p.advance()
		return &breakStmt{}, nil
	}
	if p.atIdent("continue") {
		p.advance()
		return &continueStmt{}, nil
	}
	if p.atIdent("print") {
		return p.parsePrint()
	}
	if p.atIdent("printf") {
		return p.parsePrintf()
	}
	if p.atIdent("nextfile") {
		return nil, fmt.Errorf("control flow statements are not supported")
	}
	if p.atIdent("delete") {
		return p.parseDelete()
	}
	x, err := p.parseExpression(0)
	if err != nil {
		return nil, err
	}
	return &exprStmt{x: x}, nil
}

func (p *parser) parseExit() (stmt, error) {
	p.advance()
	if p.at(tokRBrace) || p.at(tokEOF) || isSeparator(p.cur().kind) {
		return &exitStmt{}, nil
	}
	status, err := p.parseExpression(0)
	if err != nil {
		return nil, err
	}
	return &exitStmt{status: status}, nil
}

func (p *parser) parseReturn() (stmt, error) {
	p.advance()
	if p.at(tokRBrace) || p.at(tokEOF) || isSeparator(p.cur().kind) {
		return &returnStmt{}, nil
	}
	x, err := p.parseExpression(0)
	if err != nil {
		return nil, err
	}
	return &returnStmt{value: x}, nil
}

func (p *parser) parseFor() (stmt, error) {
	p.advance()
	if !p.match(tokLParen) {
		return nil, fmt.Errorf("expected ( after for")
	}
	p.skipNewlines()
	if p.cur().kind == tokIdent && p.peek(1).kind == tokIdent && p.peek(1).lit == "in" {
		varName := p.cur().lit
		if err := validateIdentifierReference(varName); err != nil {
			return nil, err
		}
		p.advance()
		p.advance()
		if p.cur().kind != tokIdent {
			return nil, fmt.Errorf("expected array name in for loop")
		}
		arrayName := p.cur().lit
		if err := validateIdentifierReference(arrayName); err != nil {
			return nil, err
		}
		p.advance()
		p.skipSeparators()
		if !p.match(tokRParen) {
			return nil, fmt.Errorf("expected ) after for loop")
		}
		body, braced, err := p.parseStatementGroup()
		if err != nil {
			return nil, err
		}
		return &forInStmt{varName: varName, arrayName: arrayName, body: body, endsBlock: braced}, nil
	}
	init, err := p.parseOptionalForExpr(tokSemicolon)
	if err != nil {
		return nil, err
	}
	if !p.match(tokSemicolon) {
		return nil, fmt.Errorf("expected ; in for loop")
	}
	cond, err := p.parseOptionalForExpr(tokSemicolon)
	if err != nil {
		return nil, err
	}
	if !p.match(tokSemicolon) {
		return nil, fmt.Errorf("expected ; in for loop")
	}
	post, err := p.parseOptionalForExpr(tokRParen)
	if err != nil {
		return nil, err
	}
	if !p.match(tokRParen) {
		return nil, fmt.Errorf("expected ) after for loop")
	}
	body, braced, err := p.parseStatementGroup()
	if err != nil {
		return nil, err
	}
	return &forStmt{init: init, cond: cond, post: post, body: body, endsBlock: braced}, nil
}

func (p *parser) parseOptionalForExpr(end tokenKind) (expr, error) {
	p.skipNewlines()
	if p.at(end) {
		return nil, nil
	}
	x, err := p.parseExpression(0)
	if err != nil {
		return nil, err
	}
	p.skipNewlines()
	return x, nil
}

func (p *parser) parseWhile() (stmt, error) {
	p.advance()
	if !p.match(tokLParen) {
		return nil, fmt.Errorf("expected ( after while")
	}
	cond, err := p.parseExpression(0)
	if err != nil {
		return nil, err
	}
	if !p.match(tokRParen) {
		return nil, fmt.Errorf("expected ) after while condition")
	}
	body, braced, err := p.parseStatementGroup()
	if err != nil {
		return nil, err
	}
	return &whileStmt{cond: cond, body: body, endsBlock: braced}, nil
}

func (p *parser) parseIf() (stmt, error) {
	p.advance()
	if !p.match(tokLParen) {
		return nil, fmt.Errorf("expected ( after if")
	}
	cond, err := p.parseExpression(0)
	if err != nil {
		return nil, err
	}
	if !p.match(tokRParen) {
		return nil, fmt.Errorf("expected ) after if condition")
	}
	thenStmts, thenBraced, err := p.parseStatementGroup()
	if err != nil {
		return nil, err
	}
	save := p.pos
	p.skipSeparators()
	var elseStmts []stmt
	endsBlock := thenBraced
	if p.atIdent("else") {
		p.advance()
		var elseBraced bool
		elseStmts, elseBraced, err = p.parseStatementGroup()
		if err != nil {
			return nil, err
		}
		endsBlock = elseBraced
	} else {
		p.pos = save
	}
	return &ifStmt{cond: cond, thenStmts: thenStmts, elseStmts: elseStmts, endsBlock: endsBlock}, nil
}

func (p *parser) parseStatementGroup() ([]stmt, bool, error) {
	p.skipNewlines()
	if p.at(tokSemicolon) {
		return nil, false, nil
	}
	if p.match(tokLBrace) {
		stmts, err := p.parseStatementList()
		return stmts, true, err
	}
	st, err := p.parseStatement()
	if err != nil {
		return nil, false, err
	}
	return []stmt{st}, false, nil
}

func (p *parser) parseDelete() (stmt, error) {
	p.advance()
	if p.cur().kind != tokIdent {
		return nil, fmt.Errorf("delete requires an array name")
	}
	name := p.cur().lit
	if err := validateIdentifierReference(name); err != nil {
		return nil, err
	}
	p.advance()
	if !p.match(tokLBracket) {
		return &deleteStmt{name: name, all: true}, nil
	}
	indices, err := p.parseArrayIndices()
	if err != nil {
		return nil, err
	}
	return &deleteStmt{name: name, indices: indices}, nil
}

func (p *parser) parsePrint() (stmt, error) {
	p.advance()
	ps := &printStmt{}
	if p.at(tokRBrace) || p.at(tokEOF) || isSeparator(p.cur().kind) {
		return ps, nil
	}
	if p.at(tokPipe) {
		pipe, err := p.parseOutputPipe()
		if err != nil {
			return nil, err
		}
		ps.pipe = pipe
		return ps, nil
	}
	old := p.stopPrintRedirect
	p.stopPrintRedirect = true
	defer func() { p.stopPrintRedirect = old }()
	for {
		x, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		ps.args = append(ps.args, x)
		if p.at(tokGT) || p.at(tokAppend) {
			return nil, fmt.Errorf("print redirection is not supported")
		}
		if p.at(tokPipe) {
			pipe, err := p.parseOutputPipe()
			if err != nil {
				return nil, err
			}
			ps.pipe = pipe
			return ps, nil
		}
		if !p.match(tokComma) {
			break
		}
		p.skipSeparators()
	}
	return ps, nil
}

func (p *parser) parsePrintf() (stmt, error) {
	p.advance()
	ps := &printfStmt{}
	parenthesized := p.match(tokLParen)
	if parenthesized {
		p.skipSeparators()
	}
	if p.at(tokRBrace) || p.at(tokEOF) || isSeparator(p.cur().kind) || p.at(tokRParen) {
		return nil, fmt.Errorf("printf requires a format expression")
	}
	old := p.stopPrintRedirect
	p.stopPrintRedirect = true
	defer func() { p.stopPrintRedirect = old }()
	for {
		x, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		ps.args = append(ps.args, x)
		if p.at(tokGT) || p.at(tokAppend) {
			return nil, fmt.Errorf("print redirection is not supported")
		}
		if p.at(tokPipe) {
			pipe, err := p.parseOutputPipe()
			if err != nil {
				return nil, err
			}
			ps.pipe = pipe
			return ps, nil
		}
		if parenthesized {
			p.skipSeparators()
			if p.match(tokRParen) {
				break
			}
			if !p.match(tokComma) {
				return nil, fmt.Errorf("expected , or ) in printf")
			}
			p.skipSeparators()
			continue
		}
		if !p.match(tokComma) {
			break
		}
		p.skipSeparators()
	}
	if p.at(tokGT) || p.at(tokAppend) {
		return nil, fmt.Errorf("print redirection is not supported")
	}
	if p.at(tokPipe) {
		pipe, err := p.parseOutputPipe()
		if err != nil {
			return nil, err
		}
		ps.pipe = pipe
	}
	return ps, nil
}

func (p *parser) parseOutputPipe() (expr, error) {
	if !p.match(tokPipe) {
		return nil, fmt.Errorf("expected |")
	}
	old := p.stopPrintRedirect
	p.stopPrintRedirect = false
	defer func() { p.stopPrintRedirect = old }()
	return p.parseExpression(0)
}

func (p *parser) skipNewlines() {
	for p.at(tokNewline) {
		p.advance()
	}
}

func (p *parser) parseExpression(minPrec int) (expr, error) {
	if err := p.enterNesting(); err != nil {
		return nil, err
	}
	defer p.leaveNesting()

	left, err := p.parsePrefix()
	if err != nil {
		return nil, err
	}
	for {
		if p.at(tokQuestion) {
			if precTernary < minPrec {
				break
			}
			p.advance()
			thenExpr, err := p.parseExpression(0)
			if err != nil {
				return nil, err
			}
			if !p.match(tokColon) {
				return nil, fmt.Errorf("expected : in conditional expression")
			}
			elseExpr, err := p.parseExpression(precAssign)
			if err != nil {
				return nil, err
			}
			left = &ternaryExpr{cond: left, then: thenExpr, els: elseExpr}
			continue
		}
		if p.at(tokInc) || p.at(tokDec) {
			if precPostfix < minPrec {
				break
			}
			op := p.cur().lit
			p.advance()
			if !isAssignableExpr(left) {
				return nil, fmt.Errorf("syntax error: increment and decrement require variables")
			}
			left = &incDecExpr{op: op, x: left}
			continue
		}
		if p.stopPrintRedirect && (p.at(tokGT) || p.at(tokAppend) || p.at(tokPipe)) {
			break
		}
		if p.at(tokPipe) && p.peek(1).kind == tokIdent && p.peek(1).lit == "getline" {
			if precCompare < minPrec {
				break
			}
			next, err := p.parseCommandGetline(left)
			if err != nil {
				return nil, err
			}
			left = next
			continue
		}
		if op, prec, assoc, ok := p.binaryOp(); ok {
			if prec < minPrec {
				break
			}
			p.advance()
			nextMin := prec + 1
			if assoc == "right" {
				nextMin = prec
			}
			right, err := p.parseExpression(nextMin)
			if err != nil {
				return nil, err
			}
			if isComparisonOp(op) {
				if b, ok := left.(*binaryExpr); ok && isComparisonOp(b.op) {
					return nil, fmt.Errorf("chained comparisons are not supported")
				}
			}
			if isAssignOp(op) {
				if !isAssignableExpr(left) {
					return nil, fmt.Errorf("assignment requires a variable")
				}
				left = &assignExpr{op: op, left: left, right: right}
			} else {
				left = &binaryExpr{op: op, left: left, right: right}
			}
			continue
		}
		if minPrec <= precConcat && p.canStartConcatenation() {
			right, err := p.parseExpression(precConcat + 1)
			if err != nil {
				return nil, err
			}
			left = &binaryExpr{op: "concat", left: left, right: right}
			continue
		}
		break
	}
	return left, nil
}

func (p *parser) parsePrefix() (expr, error) {
	tok := p.cur()
	switch tok.kind {
	case tokNumber:
		p.advance()
		n, ok := parseAwkFloat(tok.lit)
		if !ok {
			return nil, fmt.Errorf("invalid number %q", tok.lit)
		}
		return &numberExpr{text: tok.lit, num: n}, nil
	case tokString:
		p.advance()
		return &stringExpr{value: tok.lit}, nil
	case tokRegex:
		p.advance()
		return &regexExpr{pattern: tok.lit}, nil
	case tokIdent:
		p.advance()
		if tok.lit == "getline" {
			return p.parseGetline(nil)
		}
		if p.at(tokLParen) && (tokensAdjacent(tok, p.cur()) || isKnownBuiltinFunction(tok.lit)) {
			return p.parseFunctionCall(tok.lit)
		}
		if tok.lit == "length" {
			return &callExpr{name: tok.lit}, nil
		}
		if err := validateIdentifierReference(tok.lit); err != nil {
			return nil, err
		}
		if p.at(tokLBracket) {
			return p.parseArrayRef(tok.lit)
		}
		return &varExpr{name: tok.lit}, nil
	case tokDollar:
		return p.parseFieldRef()
	case tokLParen:
		p.advance()
		old := p.stopPrintRedirect
		p.stopPrintRedirect = false
		x, err := p.parseExpression(0)
		p.stopPrintRedirect = old
		if err != nil {
			return nil, err
		}
		if p.match(tokComma) {
			parts := []expr{x}
			for {
				p.skipSeparators()
				part, err := p.parseExpression(0)
				if err != nil {
					return nil, err
				}
				parts = append(parts, part)
				p.skipSeparators()
				if p.match(tokRParen) {
					return &compositeExpr{parts: parts}, nil
				}
				if !p.match(tokComma) {
					return nil, fmt.Errorf("expected , or ) in expression list")
				}
			}
		}
		if !p.match(tokRParen) {
			return nil, fmt.Errorf("expected )")
		}
		return &groupedExpr{x: x}, nil
	case tokPlus, tokMinus, tokBang:
		p.advance()
		x, err := p.parseExpression(precPrefix)
		if err != nil {
			return nil, err
		}
		return &unaryExpr{op: tok.lit, x: x}, nil
	case tokInc, tokDec:
		p.advance()
		x, err := p.parseExpression(precPrefix)
		if err != nil {
			return nil, err
		}
		if !isAssignableExpr(x) {
			return nil, fmt.Errorf("increment and decrement require variables")
		}
		return &incDecExpr{op: tok.lit, x: x, prefix: true}, nil
	default:
		return nil, fmt.Errorf("expected expression")
	}
}

func (p *parser) parseCommandGetline(source expr) (expr, error) {
	if !p.match(tokPipe) {
		return nil, fmt.Errorf("expected |")
	}
	if !p.atIdent("getline") {
		return nil, fmt.Errorf("expected getline")
	}
	p.advance()
	return p.parseGetline(source)
}

func (p *parser) parseGetline(command expr) (expr, error) {
	g := &getlineExpr{source: command}
	if command != nil {
		g.kind = getlineCommand
	} else {
		g.kind = getlineMain
	}
	if command == nil && p.at(tokLT) {
		source, err := p.parseGetlineRedirection()
		if err != nil {
			return nil, err
		}
		g.kind = getlineFile
		g.source = source
		return g, nil
	}
	if p.canStartGetlineTarget() {
		target, err := p.parseGetlineTarget()
		if err != nil {
			return nil, err
		}
		g.target = target
	}
	if command == nil && p.at(tokLT) {
		source, err := p.parseGetlineRedirection()
		if err != nil {
			return nil, err
		}
		g.kind = getlineFile
		g.source = source
	}
	return g, nil
}

func (p *parser) parseGetlineRedirection() (expr, error) {
	if !p.match(tokLT) {
		return nil, fmt.Errorf("expected <")
	}
	return p.parseExpression(precConcat + 1)
}

func (p *parser) canStartGetlineTarget() bool {
	return p.at(tokIdent) || p.at(tokDollar)
}

func (p *parser) parseGetlineTarget() (expr, error) {
	switch tok := p.cur(); tok.kind {
	case tokIdent:
		p.advance()
		if err := validateIdentifierReference(tok.lit); err != nil {
			return nil, err
		}
		if p.at(tokLBracket) {
			return p.parseArrayRef(tok.lit)
		}
		return &varExpr{name: tok.lit}, nil
	case tokDollar:
		return p.parseFieldRef()
	default:
		return nil, fmt.Errorf("syntax error: getline requires an assignable target")
	}
}

func tokensAdjacent(left, right token) bool {
	return left.pos+len(left.lit) == right.pos
}

func isKnownBuiltinFunction(name string) bool {
	if name == "system" {
		return true
	}
	if _, ok := supportedBuiltinFunctions[name]; ok {
		return true
	}
	_, ok := unsupportedBuiltinFunctions[name]
	return ok
}

func (p *parser) parseArrayRef(name string) (expr, error) {
	p.advance()
	indices, err := p.parseArrayIndices()
	if err != nil {
		return nil, err
	}
	return &arrayRefExpr{name: name, indices: indices}, nil
}

func (p *parser) parseArrayIndices() ([]expr, error) {
	indices := []expr{}
	for {
		p.skipSeparators()
		index, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		indices = append(indices, index)
		p.skipSeparators()
		if p.match(tokRBracket) {
			return indices, nil
		}
		if !p.match(tokComma) {
			return nil, fmt.Errorf("expected , or ] after array index")
		}
	}
}

func (p *parser) parseFunctionCall(name string) (expr, error) {
	if msg, ok := unsupportedExpressionKeyword(name); ok {
		return nil, fmt.Errorf("%s", msg)
	}
	if name == "system" {
		return nil, fmt.Errorf("system() is not supported")
	}
	_, supportedBuiltin := supportedBuiltinFunctions[name]
	if _, ok := unsupportedBuiltinFunctions[name]; ok {
		return nil, fmt.Errorf("function calls are not supported")
	}
	p.advance()
	args := []expr{}
	p.skipSeparators()
	if p.match(tokRParen) {
		if supportedBuiltin {
			if err := validateBuiltinCallArity(name, len(args)); err != nil {
				return nil, err
			}
		} else if !validVarName(name) {
			return nil, fmt.Errorf("invalid function name %q", name)
		}
		return &callExpr{name: name}, nil
	}
	for {
		p.skipSeparators()
		if len(args) >= maxFunctionArguments {
			return nil, fmt.Errorf("function %q has too many arguments (maximum %d)", name, maxFunctionArguments)
		}
		arg, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		p.skipSeparators()
		if p.match(tokRParen) {
			break
		}
		if !p.match(tokComma) {
			return nil, fmt.Errorf("expected , or ) in function call")
		}
	}
	if supportedBuiltin {
		if err := validateBuiltinCallArity(name, len(args)); err != nil {
			return nil, err
		}
	} else if !validVarName(name) {
		return nil, fmt.Errorf("invalid function name %q", name)
	}
	return &callExpr{name: name, args: args}, nil
}

func validateFunctionName(name string) error {
	if !validVarName(name) {
		return fmt.Errorf("invalid function name %q", name)
	}
	if _, ok := supportedBuiltinFunctions[name]; ok {
		return fmt.Errorf("%q is a built-in function, it cannot be redefined", name)
	}
	if _, ok := unsupportedBuiltinFunctions[name]; ok {
		return fmt.Errorf("%q is a built-in function, it cannot be redefined", name)
	}
	if isReservedAwkVariableName(name) {
		return fmt.Errorf("function name %q uses a reserved awk variable name", name)
	}
	return nil
}

func validateFunctionParameterName(functionName, param string) error {
	if !validVarName(param) {
		return fmt.Errorf("invalid function parameter %q", param)
	}
	if functionName == param {
		return fmt.Errorf("function %q cannot use function name as parameter name", functionName)
	}
	if isReservedAwkVariableName(param) {
		return fmt.Errorf("parameter %q uses a reserved awk variable name", param)
	}
	if _, ok := supportedBuiltinFunctions[param]; ok {
		return fmt.Errorf("parameter %q uses a built-in function name", param)
	}
	if _, ok := unsupportedBuiltinFunctions[param]; ok {
		return fmt.Errorf("parameter %q uses a built-in function name", param)
	}
	if msg, ok := unsupportedExpressionKeyword(param); ok {
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func validateLoopControlStatements(prog *program) error {
	for _, r := range prog.rules {
		if err := validateStmtListLoopControl(r.action, 0); err != nil {
			return err
		}
	}
	for _, fn := range prog.functions {
		if err := validateStmtListLoopControl(fn.body, 0); err != nil {
			return err
		}
	}
	return nil
}

func validateStmtListLoopControl(stmts []stmt, loopDepth int) error {
	for _, st := range stmts {
		if err := validateStmtLoopControl(st, loopDepth); err != nil {
			return err
		}
	}
	return nil
}

func validateStmtLoopControl(st stmt, loopDepth int) error {
	switch s := st.(type) {
	case *ifStmt:
		if err := validateStmtListLoopControl(s.thenStmts, loopDepth); err != nil {
			return err
		}
		return validateStmtListLoopControl(s.elseStmts, loopDepth)
	case *forInStmt:
		return validateStmtListLoopControl(s.body, loopDepth+1)
	case *forStmt:
		return validateStmtListLoopControl(s.body, loopDepth+1)
	case *whileStmt:
		return validateStmtListLoopControl(s.body, loopDepth+1)
	case *breakStmt:
		if loopDepth == 0 {
			return fmt.Errorf("break is not allowed outside a loop")
		}
	case *continueStmt:
		if loopDepth == 0 {
			return fmt.Errorf("continue is not allowed outside a loop")
		}
	}
	return nil
}

func validateUserFunctionNameReferences(prog *program) error {
	for _, r := range prog.rules {
		if err := validateExprUserFunctionNameReferences(r.pattern, prog.functions, nil); err != nil {
			return err
		}
		if err := validateStmtListUserFunctionNameReferences(r.action, prog.functions, nil); err != nil {
			return err
		}
	}
	for _, fn := range prog.functions {
		locals := make(map[string]struct{}, len(fn.params))
		for _, param := range fn.params {
			locals[param] = struct{}{}
		}
		if err := validateStmtListUserFunctionNameReferences(fn.body, prog.functions, locals); err != nil {
			return err
		}
	}
	return nil
}

func validateStmtListUserFunctionNameReferences(stmts []stmt, functions map[string]*functionDef, locals map[string]struct{}) error {
	for _, st := range stmts {
		if err := validateStmtUserFunctionNameReferences(st, functions, locals); err != nil {
			return err
		}
	}
	return nil
}

func validateStmtUserFunctionNameReferences(st stmt, functions map[string]*functionDef, locals map[string]struct{}) error {
	switch s := st.(type) {
	case *printStmt:
		if err := validateExprListUserFunctionNameReferences(s.args, functions, locals); err != nil {
			return err
		}
		return validateExprUserFunctionNameReferences(s.pipe, functions, locals)
	case *printfStmt:
		if err := validateExprListUserFunctionNameReferences(s.args, functions, locals); err != nil {
			return err
		}
		return validateExprUserFunctionNameReferences(s.pipe, functions, locals)
	case *ifStmt:
		if err := validateExprUserFunctionNameReferences(s.cond, functions, locals); err != nil {
			return err
		}
		if err := validateStmtListUserFunctionNameReferences(s.thenStmts, functions, locals); err != nil {
			return err
		}
		return validateStmtListUserFunctionNameReferences(s.elseStmts, functions, locals)
	case *forInStmt:
		if err := validateNameNotUserFunction(s.varName, functions, locals); err != nil {
			return err
		}
		if err := validateNameNotUserFunction(s.arrayName, functions, locals); err != nil {
			return err
		}
		return validateStmtListUserFunctionNameReferences(s.body, functions, locals)
	case *forStmt:
		if err := validateExprUserFunctionNameReferences(s.init, functions, locals); err != nil {
			return err
		}
		if err := validateExprUserFunctionNameReferences(s.cond, functions, locals); err != nil {
			return err
		}
		if err := validateExprUserFunctionNameReferences(s.post, functions, locals); err != nil {
			return err
		}
		return validateStmtListUserFunctionNameReferences(s.body, functions, locals)
	case *whileStmt:
		if err := validateExprUserFunctionNameReferences(s.cond, functions, locals); err != nil {
			return err
		}
		return validateStmtListUserFunctionNameReferences(s.body, functions, locals)
	case *exitStmt:
		return validateExprUserFunctionNameReferences(s.status, functions, locals)
	case *returnStmt:
		return validateExprUserFunctionNameReferences(s.value, functions, locals)
	case *deleteStmt:
		if err := validateNameNotUserFunction(s.name, functions, locals); err != nil {
			return err
		}
		return validateExprListUserFunctionNameReferences(s.indices, functions, locals)
	case *exprStmt:
		return validateExprUserFunctionNameReferences(s.x, functions, locals)
	default:
		return nil
	}
}

func validateExprListUserFunctionNameReferences(exprs []expr, functions map[string]*functionDef, locals map[string]struct{}) error {
	for _, x := range exprs {
		if err := validateExprUserFunctionNameReferences(x, functions, locals); err != nil {
			return err
		}
	}
	return nil
}

func validateExprUserFunctionNameReferences(x expr, functions map[string]*functionDef, locals map[string]struct{}) error {
	switch e := x.(type) {
	case nil, *numberExpr, *stringExpr, *regexExpr:
		return nil
	case *varExpr:
		return validateNameNotUserFunction(e.name, functions, locals)
	case *arrayRefExpr:
		if err := validateNameNotUserFunction(e.name, functions, locals); err != nil {
			return err
		}
		return validateExprListUserFunctionNameReferences(e.indices, functions, locals)
	case *compositeExpr:
		return validateExprListUserFunctionNameReferences(e.parts, functions, locals)
	case *fieldExpr:
		return validateExprUserFunctionNameReferences(e.index, functions, locals)
	case *groupedExpr:
		return validateExprUserFunctionNameReferences(e.x, functions, locals)
	case *unaryExpr:
		return validateExprUserFunctionNameReferences(e.x, functions, locals)
	case *binaryExpr:
		if err := validateExprUserFunctionNameReferences(e.left, functions, locals); err != nil {
			return err
		}
		return validateExprUserFunctionNameReferences(e.right, functions, locals)
	case *ternaryExpr:
		if err := validateExprUserFunctionNameReferences(e.cond, functions, locals); err != nil {
			return err
		}
		if err := validateExprUserFunctionNameReferences(e.then, functions, locals); err != nil {
			return err
		}
		return validateExprUserFunctionNameReferences(e.els, functions, locals)
	case *rangeExpr:
		if err := validateExprUserFunctionNameReferences(e.start, functions, locals); err != nil {
			return err
		}
		return validateExprUserFunctionNameReferences(e.end, functions, locals)
	case *assignExpr:
		if err := validateExprUserFunctionNameReferences(e.left, functions, locals); err != nil {
			return err
		}
		return validateExprUserFunctionNameReferences(e.right, functions, locals)
	case *incDecExpr:
		return validateExprUserFunctionNameReferences(e.x, functions, locals)
	case *callExpr:
		if _, ok := locals[e.name]; ok {
			return fmt.Errorf("parameter %q cannot be called as a function", e.name)
		}
		if _, builtin := supportedBuiltinFunctions[e.name]; !builtin {
			if _, ok := functions[e.name]; !ok {
				return fmt.Errorf("function %q not defined", e.name)
			}
		}
		return validateExprListUserFunctionNameReferences(e.args, functions, locals)
	case *getlineExpr:
		if err := validateExprUserFunctionNameReferences(e.target, functions, locals); err != nil {
			return err
		}
		return validateExprUserFunctionNameReferences(e.source, functions, locals)
	default:
		return nil
	}
}

func validateNameNotUserFunction(name string, functions map[string]*functionDef, locals map[string]struct{}) error {
	if _, ok := locals[name]; ok {
		return nil
	}
	if _, ok := functions[name]; ok {
		return fmt.Errorf("function %q cannot be used as a variable or array", name)
	}
	return nil
}

func validateBuiltinCallArity(name string, argc int) error {
	switch name {
	case "length":
		if argc > 1 {
			return fmt.Errorf("length expects at most 1 argument")
		}
	case "substr":
		if argc != 2 && argc != 3 {
			return fmt.Errorf("substr expects 2 or 3 arguments")
		}
	case "index":
		if argc != 2 {
			return fmt.Errorf("index expects 2 arguments")
		}
	case "split":
		if argc != 2 && argc != 3 {
			return fmt.Errorf("split expects 2 or 3 arguments")
		}
	case "sub", "gsub":
		if argc != 2 && argc != 3 {
			return fmt.Errorf("%s expects 2 or 3 arguments", name)
		}
	case "match":
		if argc != 2 && argc != 3 {
			return fmt.Errorf("match expects 2 or 3 arguments")
		}
	case "sprintf":
		if argc < 1 {
			return fmt.Errorf("sprintf expects at least 1 argument")
		}
	case "gensub":
		if argc != 3 && argc != 4 {
			return fmt.Errorf("gensub expects 3 or 4 arguments")
		}
	case "strtonum":
		if argc != 1 {
			return fmt.Errorf("strtonum expects 1 argument")
		}
	case "asorti":
		if argc != 1 && argc != 2 {
			return fmt.Errorf("asorti expects 1 or 2 arguments")
		}
	case "close":
		if argc != 1 {
			return fmt.Errorf("close expects 1 argument")
		}
	case "tolower", "toupper", "int":
		if argc != 1 {
			return fmt.Errorf("%s expects 1 argument", name)
		}
	}
	return nil
}

func isAssignableExpr(x expr) bool {
	switch x.(type) {
	case *varExpr, *arrayRefExpr, *fieldExpr:
		return true
	default:
		return false
	}
}

func validateIdentifierReference(name string) error {
	if msg, ok := unsupportedExpressionKeyword(name); ok {
		return fmt.Errorf("%s", msg)
	}
	if name == "system" {
		return fmt.Errorf("system() is not supported")
	}
	if _, ok := supportedBuiltinFunctions[name]; ok {
		return fmt.Errorf("function calls are not supported")
	}
	if _, ok := unsupportedBuiltinFunctions[name]; ok {
		return fmt.Errorf("function calls are not supported")
	}
	return nil
}

func unsupportedExpressionKeyword(name string) (string, bool) {
	switch name {
	case "BEGIN", "END":
		return "BEGIN and END are reserved patterns", true
	case "if", "while", "for", "next", "nextfile", "exit", "break", "continue", "return", "function":
		return "control flow statements are not supported", true
	case "delete":
		return "arrays are not supported", true
	case "printf":
		return "printf is not supported", true
	case "print":
		return "print is not supported in expressions", true
	default:
		return "", false
	}
}

func isComparisonOp(op string) bool {
	switch op {
	case "==", "!=", "<", ">", "<=", ">=":
		return true
	default:
		return false
	}
}

func (p *parser) parseFieldRef() (expr, error) {
	p.advance()
	switch tok := p.cur(); tok.kind {
	case tokNumber:
		p.advance()
		n, ok := parseAwkFloat(tok.lit)
		if !ok {
			return nil, fmt.Errorf("invalid field number %q", tok.lit)
		}
		return &fieldExpr{index: &numberExpr{text: tok.lit, num: n}}, nil
	case tokIdent:
		p.advance()
		if err := validateIdentifierReference(tok.lit); err != nil {
			return nil, err
		}
		return &fieldExpr{index: &varExpr{name: tok.lit}}, nil
	case tokLParen:
		p.advance()
		x, err := p.parseExpression(0)
		if err != nil {
			return nil, err
		}
		if !p.match(tokRParen) {
			return nil, fmt.Errorf("expected ) after field expression")
		}
		return &fieldExpr{index: x}, nil
	default:
		return nil, fmt.Errorf("expected field reference")
	}
}

func (p *parser) binaryOp() (string, int, string, bool) {
	if p.atIdent("in") {
		return "in", precCompare, "left", true
	}
	switch p.cur().kind {
	case tokAssign:
		return "=", precAssign, "right", true
	case tokPlusAssign:
		return "+=", precAssign, "right", true
	case tokMinusAssign:
		return "-=", precAssign, "right", true
	case tokStarAssign:
		return "*=", precAssign, "right", true
	case tokSlashAssign:
		return "/=", precAssign, "right", true
	case tokPercentAssign:
		return "%=", precAssign, "right", true
	case tokCaretAssign:
		return "^=", precAssign, "right", true
	case tokOr:
		return "||", precOr, "left", true
	case tokAnd:
		return "&&", precAnd, "left", true
	case tokEQ:
		return "==", precCompare, "left", true
	case tokNE:
		return "!=", precCompare, "left", true
	case tokLT:
		return "<", precCompare, "left", true
	case tokGT:
		return ">", precCompare, "left", true
	case tokLE:
		return "<=", precCompare, "left", true
	case tokGE:
		return ">=", precCompare, "left", true
	case tokMatch:
		return "~", precCompare, "left", true
	case tokNotMatch:
		return "!~", precCompare, "left", true
	case tokPlus:
		return "+", precAdd, "left", true
	case tokMinus:
		return "-", precAdd, "left", true
	case tokStar:
		return "*", precMul, "left", true
	case tokSlash:
		return "/", precMul, "left", true
	case tokPercent:
		return "%", precMul, "left", true
	case tokCaret:
		return "^", precPower, "right", true
	default:
		return "", 0, "", false
	}
}

func (p *parser) canStartConcatenation() bool {
	switch p.cur().kind {
	case tokIdent, tokNumber, tokString, tokRegex, tokDollar, tokLParen:
		return true
	default:
		return false
	}
}

func isAssignOp(op string) bool {
	switch op {
	case "=", "+=", "-=", "*=", "/=", "%=", "^=":
		return true
	default:
		return false
	}
}

func (p *parser) skipSeparators() {
	for isSeparator(p.cur().kind) {
		p.advance()
	}
}

func isSeparator(k tokenKind) bool {
	return k == tokNewline || k == tokSemicolon
}

func (p *parser) cur() token {
	if p.pos >= len(p.toks) {
		return token{kind: tokEOF}
	}
	return p.toks[p.pos]
}

func (p *parser) peek(n int) token {
	idx := p.pos + n
	if idx >= len(p.toks) {
		return token{kind: tokEOF}
	}
	return p.toks[idx]
}

func (p *parser) at(k tokenKind) bool {
	return p.cur().kind == k
}

func (p *parser) atIdent(s string) bool {
	return p.cur().kind == tokIdent && p.cur().lit == s
}

func (p *parser) match(k tokenKind) bool {
	if !p.at(k) {
		return false
	}
	p.advance()
	return true
}

func (p *parser) advance() {
	if p.pos < len(p.toks) {
		p.pos++
	}
}
