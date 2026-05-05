// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"fmt"
	"strings"
)

// tokenKind enumerates lexical token categories.
type tokenKind uint8

const (
	tkEOF tokenKind = iota
	tkNewline
	tkSemicolon

	tkIdent
	tkFuncName // ident immediately followed by '('
	tkNumber
	tkString
	tkRegex

	tkDollar

	tkLParen
	tkRParen
	tkLBrace
	tkRBrace
	tkLBracket
	tkRBracket

	tkComma
	tkQuestion
	tkColon

	tkAssign
	tkAddAssign
	tkSubAssign
	tkMulAssign
	tkDivAssign
	tkModAssign
	tkPowAssign

	tkPlus
	tkMinus
	tkStar
	tkSlash
	tkPercent
	tkCaret

	tkEq
	tkNe
	tkLt
	tkLe
	tkGt
	tkGe

	tkMatch
	tkNotMatch

	tkAnd
	tkOr
	tkNot

	tkInc
	tkDec

	tkAppend // >>  (always rejected)
	tkPipe   // |   (rejected when used as redirect)

	// Keywords.
	tkBegin
	tkEnd
	tkIf
	tkElse
	tkWhile
	tkFor
	tkDo
	tkIn
	tkBreak
	tkContinue
	tkNext
	tkExit
	tkDelete
	tkReturn
	tkFunction
	tkGetline
	tkPrint
	tkPrintf
)

// keywords maps reserved word text to token kind.
var keywords = map[string]tokenKind{
	"BEGIN":    tkBegin,
	"END":      tkEnd,
	"if":       tkIf,
	"else":     tkElse,
	"while":    tkWhile,
	"for":      tkFor,
	"do":       tkDo,
	"in":       tkIn,
	"break":    tkBreak,
	"continue": tkContinue,
	"next":     tkNext,
	"exit":     tkExit,
	"delete":   tkDelete,
	"return":   tkReturn,
	"function": tkFunction,
	"func":     tkFunction,
	"getline":  tkGetline,
	"print":    tkPrint,
	"printf":   tkPrintf,
}

// tokenName returns a human-readable name for diagnostics.
func tokenName(k tokenKind) string {
	switch k {
	case tkEOF:
		return "end of input"
	case tkNewline:
		return "newline"
	case tkSemicolon:
		return ";"
	case tkIdent, tkFuncName:
		return "identifier"
	case tkNumber:
		return "number"
	case tkString:
		return "string"
	case tkRegex:
		return "regex"
	case tkDollar:
		return "$"
	case tkLParen:
		return "("
	case tkRParen:
		return ")"
	case tkLBrace:
		return "{"
	case tkRBrace:
		return "}"
	case tkLBracket:
		return "["
	case tkRBracket:
		return "]"
	case tkComma:
		return ","
	case tkQuestion:
		return "?"
	case tkColon:
		return ":"
	case tkAssign:
		return "="
	case tkAddAssign:
		return "+="
	case tkSubAssign:
		return "-="
	case tkMulAssign:
		return "*="
	case tkDivAssign:
		return "/="
	case tkModAssign:
		return "%="
	case tkPowAssign:
		return "^="
	case tkPlus:
		return "+"
	case tkMinus:
		return "-"
	case tkStar:
		return "*"
	case tkSlash:
		return "/"
	case tkPercent:
		return "%"
	case tkCaret:
		return "^"
	case tkEq:
		return "=="
	case tkNe:
		return "!="
	case tkLt:
		return "<"
	case tkLe:
		return "<="
	case tkGt:
		return ">"
	case tkGe:
		return ">="
	case tkMatch:
		return "~"
	case tkNotMatch:
		return "!~"
	case tkAnd:
		return "&&"
	case tkOr:
		return "||"
	case tkNot:
		return "!"
	case tkInc:
		return "++"
	case tkDec:
		return "--"
	case tkAppend:
		return ">>"
	case tkPipe:
		return "|"
	}
	for name, kk := range keywords {
		if kk == k {
			return name
		}
	}
	return "unknown"
}

// token holds a lexed token.
type token struct {
	kind tokenKind
	val  string  // textual value (identifier, number lexeme, string content, regex source)
	num  float64 // pre-parsed numeric value for tkNumber
	line int
}

// lexer scans an awk program.
type lexer struct {
	src    string
	pos    int
	line   int
	prev   tokenKind
	tokens []token
}

// lex tokenises the program text. It returns an error on the first invalid
// token (unterminated string, unterminated regex, unknown character).
func lex(src string) ([]token, error) {
	l := &lexer{src: src, line: 1}
	for {
		t, err := l.next()
		if err != nil {
			return nil, err
		}
		l.tokens = append(l.tokens, t)
		if t.kind == tkEOF {
			return l.tokens, nil
		}
		l.prev = t.kind
	}
}

// regexExpected reports whether the previous token leaves us in a position
// where '/' starts a regex literal rather than a division. Awk's classic rule.
func (l *lexer) regexExpected() bool {
	switch l.prev {
	case 0, // start of input
		tkNewline, tkSemicolon,
		tkLBrace, tkRBrace, tkLParen, tkLBracket,
		tkComma, tkQuestion, tkColon,
		tkAssign, tkAddAssign, tkSubAssign, tkMulAssign,
		tkDivAssign, tkModAssign, tkPowAssign,
		tkPlus, tkMinus, tkStar, tkSlash, tkPercent, tkCaret,
		tkEq, tkNe, tkLt, tkLe, tkGt, tkGe,
		tkMatch, tkNotMatch,
		tkAnd, tkOr, tkNot,
		tkBegin, tkEnd,
		tkIf, tkElse, tkWhile, tkFor, tkDo,
		tkBreak, tkContinue, tkNext, tkExit, tkDelete, tkReturn,
		tkPrint, tkPrintf, tkGetline,
		tkIn,
		tkDollar:
		return true
	}
	return false
}

// next produces the next token.
func (l *lexer) next() (token, error) {
	// Skip horizontal whitespace and line continuations and comments.
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		switch c {
		case ' ', '\t':
			l.pos++
			continue
		case '\\':
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '\n' {
				l.pos += 2
				l.line++
				continue
			}
		case '#':
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
			continue
		}
		break
	}

	if l.pos >= len(l.src) {
		return token{kind: tkEOF, line: l.line}, nil
	}

	startLine := l.line
	c := l.src[l.pos]

	// Newline is significant in awk (statement terminator).
	if c == '\n' {
		l.pos++
		l.line++
		return token{kind: tkNewline, line: startLine}, nil
	}

	// Identifiers / keywords.
	if isIdentStart(c) {
		j := l.pos + 1
		for j < len(l.src) && isIdentCont(l.src[j]) {
			j++
		}
		name := l.src[l.pos:j]
		l.pos = j
		if k, ok := keywords[name]; ok {
			return token{kind: k, val: name, line: startLine}, nil
		}
		// Distinguish identifier-as-function: name followed immediately by '('.
		kind := tkIdent
		if j < len(l.src) && l.src[j] == '(' {
			kind = tkFuncName
		}
		return token{kind: kind, val: name, line: startLine}, nil
	}

	// Numeric literals.
	if c == '.' && l.pos+1 < len(l.src) && isDigit(l.src[l.pos+1]) {
		return l.lexNumber()
	}
	if isDigit(c) {
		return l.lexNumber()
	}

	// String literals.
	if c == '"' {
		return l.lexString()
	}

	// Regex literal vs slash.
	if c == '/' && l.regexExpected() {
		return l.lexRegex()
	}

	return l.lexSymbol()
}

// lexNumber reads a decimal numeric literal.
func (l *lexer) lexNumber() (token, error) {
	startLine := l.line
	start := l.pos
	for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
		l.pos++
	}
	if l.pos < len(l.src) && l.src[l.pos] == '.' {
		l.pos++
		for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
			l.pos++
		}
	}
	if l.pos < len(l.src) && (l.src[l.pos] == 'e' || l.src[l.pos] == 'E') {
		l.pos++
		if l.pos < len(l.src) && (l.src[l.pos] == '+' || l.src[l.pos] == '-') {
			l.pos++
		}
		for l.pos < len(l.src) && isDigit(l.src[l.pos]) {
			l.pos++
		}
	}
	lex := l.src[start:l.pos]
	f := parseAwkNumber(lex)
	return token{kind: tkNumber, val: lex, num: f, line: startLine}, nil
}

// lexString reads a "..." literal, processing C-style escapes.
func (l *lexer) lexString() (token, error) {
	startLine := l.line
	l.pos++ // skip opening "
	var sb strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '"' {
			l.pos++
			return token{kind: tkString, val: sb.String(), line: startLine}, nil
		}
		if c == '\n' {
			return token{}, fmt.Errorf("line %d: unterminated string literal", startLine)
		}
		if c == '\\' && l.pos+1 < len(l.src) {
			esc := l.src[l.pos+1]
			l.pos += 2
			switch esc {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '\\':
				sb.WriteByte('\\')
			case '"':
				sb.WriteByte('"')
			case '/':
				sb.WriteByte('/')
			case 'a':
				sb.WriteByte('\a')
			case 'b':
				sb.WriteByte('\b')
			case 'f':
				sb.WriteByte('\f')
			case 'v':
				sb.WriteByte('\v')
			case '0', '1', '2', '3', '4', '5', '6', '7':
				// Octal escape: 1–3 digits.
				v := int(esc - '0')
				for i := 0; i < 2 && l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '7'; i++ {
					v = v*8 + int(l.src[l.pos]-'0')
					l.pos++
				}
				sb.WriteByte(byte(v))
			case 'x':
				// Hex escape: \xNN (1 or 2 hex digits), gawk/mawk compatible.
				if l.pos < len(l.src) && isHexDigit(l.src[l.pos]) {
					v := hexDigitVal(l.src[l.pos])
					l.pos++
					if l.pos < len(l.src) && isHexDigit(l.src[l.pos]) {
						v = v*16 + hexDigitVal(l.src[l.pos])
						l.pos++
					}
					sb.WriteByte(byte(v))
				} else {
					sb.WriteByte('\\')
					sb.WriteByte('x')
				}
			default:
				// Unknown escape: keep backslash and char (matches awk behaviour).
				sb.WriteByte('\\')
				sb.WriteByte(esc)
			}
			continue
		}
		sb.WriteByte(c)
		if sb.Len() > MaxStringBytes {
			return token{}, fmt.Errorf("line %d: string literal exceeds maximum length %d", startLine, MaxStringBytes)
		}
		l.pos++
	}
	return token{}, fmt.Errorf("line %d: unterminated string literal", startLine)
}

// lexRegex reads a /.../ regex literal. Awk regex syntax allows almost
// anything inside; we forward the source text to regexp.Compile later.
// Backslash escapes the next character.
func (l *lexer) lexRegex() (token, error) {
	startLine := l.line
	l.pos++ // skip leading /
	var sb strings.Builder
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '/' {
			l.pos++
			return token{kind: tkRegex, val: sb.String(), line: startLine}, nil
		}
		if c == '\n' {
			return token{}, fmt.Errorf("line %d: unterminated regex", startLine)
		}
		if c == '\\' && l.pos+1 < len(l.src) {
			next := l.src[l.pos+1]
			if next == '/' {
				sb.WriteByte('/')
				l.pos += 2
				continue
			}
			// Pass other backslash escapes through to regexp.Compile.
			sb.WriteByte('\\')
			sb.WriteByte(next)
			l.pos += 2
			continue
		}
		sb.WriteByte(c)
		if sb.Len() > MaxStringBytes {
			return token{}, fmt.Errorf("line %d: regex literal exceeds maximum length %d", startLine, MaxStringBytes)
		}
		l.pos++
	}
	return token{}, fmt.Errorf("line %d: unterminated regex", startLine)
}

// lexSymbol reads a punctuation/operator token.
func (l *lexer) lexSymbol() (token, error) {
	startLine := l.line
	c := l.src[l.pos]
	twoOf := func(k tokenKind) (token, error) {
		l.pos += 2
		return token{kind: k, line: startLine}, nil
	}
	one := func(k tokenKind) (token, error) {
		l.pos++
		return token{kind: k, line: startLine}, nil
	}
	peek := func() byte {
		if l.pos+1 < len(l.src) {
			return l.src[l.pos+1]
		}
		return 0
	}
	switch c {
	case ';':
		return one(tkSemicolon)
	case '$':
		return one(tkDollar)
	case '(':
		return one(tkLParen)
	case ')':
		return one(tkRParen)
	case '{':
		return one(tkLBrace)
	case '}':
		return one(tkRBrace)
	case '[':
		return one(tkLBracket)
	case ']':
		return one(tkRBracket)
	case ',':
		return one(tkComma)
	case '?':
		return one(tkQuestion)
	case ':':
		return one(tkColon)
	case '+':
		switch peek() {
		case '+':
			return twoOf(tkInc)
		case '=':
			return twoOf(tkAddAssign)
		}
		return one(tkPlus)
	case '-':
		switch peek() {
		case '-':
			return twoOf(tkDec)
		case '=':
			return twoOf(tkSubAssign)
		}
		return one(tkMinus)
	case '*':
		switch peek() {
		case '*':
			// Treat ** as ^ (gawk/mawk extension). Then check **=.
			if l.pos+2 < len(l.src) && l.src[l.pos+2] == '=' {
				l.pos += 3
				return token{kind: tkPowAssign, line: startLine}, nil
			}
			l.pos += 2
			return token{kind: tkCaret, line: startLine}, nil
		case '=':
			return twoOf(tkMulAssign)
		}
		return one(tkStar)
	case '/':
		if peek() == '=' {
			return twoOf(tkDivAssign)
		}
		return one(tkSlash)
	case '%':
		if peek() == '=' {
			return twoOf(tkModAssign)
		}
		return one(tkPercent)
	case '^':
		if peek() == '=' {
			return twoOf(tkPowAssign)
		}
		return one(tkCaret)
	case '=':
		if peek() == '=' {
			return twoOf(tkEq)
		}
		return one(tkAssign)
	case '!':
		switch peek() {
		case '=':
			return twoOf(tkNe)
		case '~':
			return twoOf(tkNotMatch)
		}
		return one(tkNot)
	case '<':
		if peek() == '=' {
			return twoOf(tkLe)
		}
		return one(tkLt)
	case '>':
		switch peek() {
		case '=':
			return twoOf(tkGe)
		case '>':
			return twoOf(tkAppend)
		}
		return one(tkGt)
	case '~':
		return one(tkMatch)
	case '&':
		if peek() == '&' {
			return twoOf(tkAnd)
		}
		return token{}, fmt.Errorf("line %d: unexpected character %q", startLine, c)
	case '|':
		if peek() == '|' {
			return twoOf(tkOr)
		}
		return one(tkPipe)
	}
	return token{}, fmt.Errorf("line %d: unexpected character %q", startLine, c)
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isIdentCont(c byte) bool {
	return isIdentStart(c) || isDigit(c)
}
func isDigit(c byte) bool {
	return c >= '0' && c <= '9'
}
func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}
func hexDigitVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	default: // A-F
		return int(c-'A') + 10
	}
}
