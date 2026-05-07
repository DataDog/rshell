// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"fmt"
	"strconv"
)

const (
	precAssign  = 10
	precOr      = 20
	precAnd     = 30
	precCompare = 40
	precConcat  = 50
	precAdd     = 60
	precMul     = 70
	precPrefix  = 80
	precPostfix = 90
)

var unsupportedBuiltinFunctions = map[string]struct{}{
	"and":            {},
	"asort":          {},
	"asorti":         {},
	"atan2":          {},
	"bindtextdomain": {},
	"close":          {},
	"compl":          {},
	"cos":            {},
	"dcgettext":      {},
	"dcngettext":     {},
	"exp":            {},
	"fflush":         {},
	"gensub":         {},
	"gsub":           {},
	"index":          {},
	"int":            {},
	"isarray":        {},
	"length":         {},
	"log":            {},
	"lshift":         {},
	"match":          {},
	"mktime":         {},
	"or":             {},
	"patsplit":       {},
	"rand":           {},
	"rshift":         {},
	"sin":            {},
	"split":          {},
	"sprintf":        {},
	"sqrt":           {},
	"srand":          {},
	"strftime":       {},
	"strtonum":       {},
	"sub":            {},
	"substr":         {},
	"system":         {},
	"systime":        {},
	"tolower":        {},
	"toupper":        {},
	"typeof":         {},
	"xor":            {},
}

type parser struct {
	toks              []token
	pos               int
	stopPrintRedirect bool
}

func parseProgram(src string) (*program, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks}
	prog := &program{}
	p.skipSeparators()
	for !p.at(tokEOF) {
		r, err := p.parseRule()
		if err != nil {
			return nil, err
		}
		prog.rules = append(prog.rules, r)
		p.skipSeparators()
	}
	if len(prog.rules) == 0 {
		return nil, fmt.Errorf("empty program")
	}
	return prog, nil
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
		return rule{}, fmt.Errorf("range patterns are not supported")
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
		if !p.at(tokRBrace) && !p.at(tokEOF) && !isSeparator(p.cur().kind) {
			return nil, fmt.Errorf("expected statement separator")
		}
		p.skipSeparators()
	}
	p.advance()
	return stmts, nil
}

func (p *parser) parseStatement() (stmt, error) {
	if p.atIdent("print") {
		return p.parsePrint()
	}
	if p.atIdent("printf") {
		return nil, fmt.Errorf("printf is not supported")
	}
	if p.atIdent("if") || p.atIdent("while") || p.atIdent("for") ||
		p.atIdent("next") || p.atIdent("nextfile") || p.atIdent("exit") ||
		p.atIdent("break") || p.atIdent("continue") {
		return nil, fmt.Errorf("control flow statements are not supported")
	}
	if p.atIdent("delete") {
		return nil, fmt.Errorf("arrays are not supported")
	}
	if p.atIdent("getline") {
		return nil, fmt.Errorf("getline is not supported")
	}
	x, err := p.parseExpression(0)
	if err != nil {
		return nil, err
	}
	return &exprStmt{x: x}, nil
}

func (p *parser) parsePrint() (stmt, error) {
	p.advance()
	ps := &printStmt{}
	if p.at(tokRBrace) || p.at(tokEOF) || isSeparator(p.cur().kind) {
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
		if p.at(tokGT) || p.at(tokAppend) || p.at(tokPipe) {
			return nil, fmt.Errorf("print redirection and command pipes are not supported")
		}
		if !p.match(tokComma) {
			break
		}
	}
	return ps, nil
}

func (p *parser) parseExpression(minPrec int) (expr, error) {
	left, err := p.parsePrefix()
	if err != nil {
		return nil, err
	}
	for {
		if p.at(tokInc) || p.at(tokDec) {
			if precPostfix < minPrec {
				break
			}
			op := p.cur().lit
			p.advance()
			if _, ok := left.(*varExpr); !ok {
				return nil, fmt.Errorf("increment and decrement require scalar variables")
			}
			left = &incDecExpr{op: op, x: left}
			continue
		}
		if p.stopPrintRedirect && (p.at(tokGT) || p.at(tokAppend) || p.at(tokPipe)) {
			break
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
			if isAssignOp(op) {
				if _, ok := left.(*fieldExpr); ok {
					return nil, fmt.Errorf("field assignment is not supported")
				}
				if _, ok := left.(*varExpr); !ok {
					return nil, fmt.Errorf("assignment requires a scalar variable")
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
		n, err := strconv.ParseFloat(tok.lit, 64)
		if err != nil {
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
		if msg, ok := unsupportedExpressionKeyword(tok.lit); ok {
			return nil, fmt.Errorf("%s", msg)
		}
		if tok.lit == "system" {
			return nil, fmt.Errorf("system() is not supported")
		}
		if _, ok := unsupportedBuiltinFunctions[tok.lit]; ok {
			return nil, fmt.Errorf("function calls are not supported")
		}
		if p.at(tokLParen) {
			return nil, fmt.Errorf("function calls are not supported")
		}
		if p.at(tokLBracket) {
			return nil, fmt.Errorf("arrays are not supported")
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
		if !p.match(tokRParen) {
			return nil, fmt.Errorf("expected )")
		}
		return x, nil
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
		if _, ok := x.(*varExpr); !ok {
			return nil, fmt.Errorf("increment and decrement require scalar variables")
		}
		return &incDecExpr{op: tok.lit, x: x, prefix: true}, nil
	default:
		return nil, fmt.Errorf("expected expression")
	}
}

func unsupportedExpressionKeyword(name string) (string, bool) {
	switch name {
	case "if", "while", "for", "next", "nextfile", "exit", "break", "continue":
		return "control flow statements are not supported", true
	case "delete":
		return "arrays are not supported", true
	case "getline":
		return "getline is not supported", true
	case "printf":
		return "printf is not supported", true
	case "print":
		return "print is not supported in expressions", true
	default:
		return "", false
	}
}

func (p *parser) parseFieldRef() (expr, error) {
	p.advance()
	switch tok := p.cur(); tok.kind {
	case tokNumber:
		p.advance()
		n, err := strconv.ParseFloat(tok.lit, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid field number %q", tok.lit)
		}
		return &fieldExpr{index: &numberExpr{text: tok.lit, num: n}}, nil
	case tokIdent:
		p.advance()
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
	case "=", "+=", "-=", "*=", "/=", "%=":
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
