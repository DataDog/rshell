// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awkcore

import (
	"fmt"
	"strings"
)

// lexer tokenizes awk program source text.
type lexer struct {
	src    string
	pos    int
	tokens []token
}

func lex(src string) ([]token, error) {
	l := &lexer{src: src}
	if err := l.run(); err != nil {
		return nil, err
	}
	l.tokens = append(l.tokens, token{typ: tokEOF, pos: l.pos})
	return l.tokens, nil
}

func (l *lexer) run() error {
	for l.pos < len(l.src) {
		ch := l.src[l.pos]

		// Skip spaces and tabs.
		if ch == ' ' || ch == '\t' {
			l.pos++
			continue
		}

		// Skip comments.
		if ch == '#' {
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
			continue
		}

		// Backslash-newline continuation.
		if ch == '\\' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '\n' {
			l.pos += 2
			continue
		}

		// Newline.
		if ch == '\n' || ch == '\r' {
			l.emit(tokNEWLINE, l.pos, l.pos+1)
			l.pos++
			if ch == '\r' && l.pos < len(l.src) && l.src[l.pos] == '\n' {
				l.pos++
			}
			continue
		}

		if ch == ';' {
			l.emit(tokSEMI, l.pos, l.pos+1)
			l.pos++
			continue
		}
		if ch == '{' {
			l.emit(tokLBRACE, l.pos, l.pos+1)
			l.pos++
			continue
		}
		if ch == '}' {
			l.emit(tokRBRACE, l.pos, l.pos+1)
			l.pos++
			continue
		}
		if ch == '(' {
			l.emit(tokLPAREN, l.pos, l.pos+1)
			l.pos++
			continue
		}
		if ch == ')' {
			l.emit(tokRPAREN, l.pos, l.pos+1)
			l.pos++
			continue
		}
		if ch == '[' {
			l.emit(tokLBRACKET, l.pos, l.pos+1)
			l.pos++
			continue
		}
		if ch == ']' {
			l.emit(tokRBRACKET, l.pos, l.pos+1)
			l.pos++
			continue
		}
		if ch == ',' {
			l.emit(tokCOMMA, l.pos, l.pos+1)
			l.pos++
			continue
		}
		if ch == '$' {
			l.emit(tokDOLLAR, l.pos, l.pos+1)
			l.pos++
			continue
		}
		if ch == '?' {
			l.emit(tokQUESTION, l.pos, l.pos+1)
			l.pos++
			continue
		}
		if ch == ':' {
			l.emit(tokCOLON, l.pos, l.pos+1)
			l.pos++
			continue
		}

		// Two-character operators.
		if ch == '+' {
			if l.peek(1) == '+' {
				l.emit(tokINCR, l.pos, l.pos+2)
				l.pos += 2
			} else if l.peek(1) == '=' {
				l.emit(tokPLUSASSIGN, l.pos, l.pos+2)
				l.pos += 2
			} else {
				l.emit(tokPLUS, l.pos, l.pos+1)
				l.pos++
			}
			continue
		}
		if ch == '-' {
			if l.peek(1) == '-' {
				l.emit(tokDECR, l.pos, l.pos+2)
				l.pos += 2
			} else if l.peek(1) == '=' {
				l.emit(tokMINUSASSIGN, l.pos, l.pos+2)
				l.pos += 2
			} else {
				l.emit(tokMINUS, l.pos, l.pos+1)
				l.pos++
			}
			continue
		}
		if ch == '*' {
			if l.peek(1) == '=' {
				l.emit(tokSTARASSIGN, l.pos, l.pos+2)
				l.pos += 2
			} else {
				l.emit(tokSTAR, l.pos, l.pos+1)
				l.pos++
			}
			continue
		}
		if ch == '%' {
			if l.peek(1) == '=' {
				l.emit(tokPERCENTASSIGN, l.pos, l.pos+2)
				l.pos += 2
			} else {
				l.emit(tokPERCENT, l.pos, l.pos+1)
				l.pos++
			}
			continue
		}
		if ch == '^' {
			if l.peek(1) == '=' {
				l.emit(tokPOWERASSIGN, l.pos, l.pos+2)
				l.pos += 2
			} else {
				l.emit(tokPOWER, l.pos, l.pos+1)
				l.pos++
			}
			continue
		}
		if ch == '<' {
			if l.peek(1) == '=' {
				l.emit(tokLE, l.pos, l.pos+2)
				l.pos += 2
			} else {
				l.emit(tokLT, l.pos, l.pos+1)
				l.pos++
			}
			continue
		}
		if ch == '>' {
			if l.peek(1) == '=' {
				l.emit(tokGE, l.pos, l.pos+2)
				l.pos += 2
			} else if l.peek(1) == '>' {
				l.emit(tokAPPEND, l.pos, l.pos+2)
				l.pos += 2
			} else {
				l.emit(tokGT, l.pos, l.pos+1)
				l.pos++
			}
			continue
		}
		if ch == '=' {
			if l.peek(1) == '=' {
				l.emit(tokEQ, l.pos, l.pos+2)
				l.pos += 2
			} else {
				l.emit(tokASSIGN, l.pos, l.pos+1)
				l.pos++
			}
			continue
		}
		if ch == '!' {
			if l.peek(1) == '=' {
				l.emit(tokNE, l.pos, l.pos+2)
				l.pos += 2
			} else if l.peek(1) == '~' {
				l.emit(tokNOTMATCH, l.pos, l.pos+2)
				l.pos += 2
			} else {
				l.emit(tokNOT, l.pos, l.pos+1)
				l.pos++
			}
			continue
		}
		if ch == '~' {
			l.emit(tokMATCH, l.pos, l.pos+1)
			l.pos++
			continue
		}
		if ch == '&' {
			if l.peek(1) == '&' {
				l.emit(tokAND, l.pos, l.pos+2)
				l.pos += 2
			} else {
				return fmt.Errorf("unexpected character '&' at position %d", l.pos)
			}
			continue
		}
		if ch == '|' {
			if l.peek(1) == '|' {
				l.emit(tokOR, l.pos, l.pos+2)
				l.pos += 2
			} else {
				l.emit(tokPIPE, l.pos, l.pos+1)
				l.pos++
			}
			continue
		}

		// String literal.
		if ch == '"' {
			if err := l.lexString(); err != nil {
				return err
			}
			continue
		}

		// Regex literal — context-dependent: only after certain tokens.
		if ch == '/' && l.canStartRegex() {
			if err := l.lexRegex(); err != nil {
				return err
			}
			continue
		}
		if ch == '/' {
			if l.peek(1) == '=' {
				l.emit(tokSLASHASSIGN, l.pos, l.pos+2)
				l.pos += 2
			} else {
				l.emit(tokSLASH, l.pos, l.pos+1)
				l.pos++
			}
			continue
		}

		// Number literal.
		if ch >= '0' && ch <= '9' || (ch == '.' && l.pos+1 < len(l.src) && l.src[l.pos+1] >= '0' && l.src[l.pos+1] <= '9') {
			l.lexNumber()
			continue
		}

		// Identifier or keyword.
		if isIdentStart(ch) {
			l.lexIdent()
			continue
		}

		return fmt.Errorf("unexpected character %q at position %d", ch, l.pos)
	}
	return nil
}

func (l *lexer) peek(offset int) byte {
	i := l.pos + offset
	if i >= len(l.src) {
		return 0
	}
	return l.src[i]
}

func (l *lexer) emit(typ tokenType, start, end int) {
	l.tokens = append(l.tokens, token{typ: typ, val: l.src[start:end], pos: start})
}

func (l *lexer) canStartRegex() bool {
	// A '/' starts a regex if preceded by an operator or start of expression,
	// NOT after a value (number, string, identifier, closing paren/bracket).
	for i := len(l.tokens) - 1; i >= 0; i-- {
		t := l.tokens[i]
		if t.typ == tokNEWLINE || t.typ == tokSEMI {
			return true
		}
		switch t.typ {
		case tokNUMBER, tokSTRING, tokIDENT, tokRPAREN, tokRBRACKET, tokINCR, tokDECR:
			return false
		default:
			return true
		}
	}
	return true // start of input
}

func (l *lexer) lexString() error {
	start := l.pos
	l.pos++ // skip opening quote
	var sb strings.Builder
	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		if ch == '"' {
			l.pos++
			l.tokens = append(l.tokens, token{typ: tokSTRING, val: sb.String(), pos: start})
			return nil
		}
		if ch == '\\' && l.pos+1 < len(l.src) {
			l.pos++
			esc := l.src[l.pos]
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
			case 'a':
				sb.WriteByte('\a')
			case 'b':
				sb.WriteByte('\b')
			case 'f':
				sb.WriteByte('\f')
			case 'v':
				sb.WriteByte('\v')
			case '/':
				sb.WriteByte('/')
			default:
				sb.WriteByte('\\')
				sb.WriteByte(esc)
			}
			l.pos++
			continue
		}
		if ch == '\n' {
			return fmt.Errorf("unterminated string at position %d", start)
		}
		sb.WriteByte(ch)
		l.pos++
	}
	return fmt.Errorf("unterminated string at position %d", start)
}

func (l *lexer) lexRegex() error {
	start := l.pos
	l.pos++ // skip opening /
	var sb strings.Builder
	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		if ch == '/' {
			l.pos++
			l.tokens = append(l.tokens, token{typ: tokREGEX, val: sb.String(), pos: start})
			return nil
		}
		if ch == '\\' && l.pos+1 < len(l.src) {
			next := l.src[l.pos+1]
			if next == '/' {
				sb.WriteByte('/')
				l.pos += 2
				continue
			}
			sb.WriteByte('\\')
			sb.WriteByte(next)
			l.pos += 2
			continue
		}
		if ch == '\n' {
			return fmt.Errorf("unterminated regex at position %d", start)
		}
		sb.WriteByte(ch)
		l.pos++
	}
	return fmt.Errorf("unterminated regex at position %d", start)
}

func (l *lexer) lexNumber() {
	start := l.pos
	if l.src[l.pos] == '0' && l.pos+1 < len(l.src) && (l.src[l.pos+1] == 'x' || l.src[l.pos+1] == 'X') {
		l.pos += 2
		for l.pos < len(l.src) && isHexDigit(l.src[l.pos]) {
			l.pos++
		}
	} else {
		for l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
			l.pos++
		}
		if l.pos < len(l.src) && l.src[l.pos] == '.' {
			l.pos++
			for l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
				l.pos++
			}
		}
		if l.pos < len(l.src) && (l.src[l.pos] == 'e' || l.src[l.pos] == 'E') {
			l.pos++
			if l.pos < len(l.src) && (l.src[l.pos] == '+' || l.src[l.pos] == '-') {
				l.pos++
			}
			for l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
				l.pos++
			}
		}
	}
	l.tokens = append(l.tokens, token{typ: tokNUMBER, val: l.src[start:l.pos], pos: start})
}

func (l *lexer) lexIdent() {
	start := l.pos
	for l.pos < len(l.src) && isIdentCont(l.src[l.pos]) {
		l.pos++
	}
	word := l.src[start:l.pos]
	if typ, ok := keywords[word]; ok {
		l.tokens = append(l.tokens, token{typ: typ, val: word, pos: start})
	} else {
		l.tokens = append(l.tokens, token{typ: tokIDENT, val: word, pos: start})
	}
}

func isIdentStart(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_'
}

func isIdentCont(ch byte) bool {
	return isIdentStart(ch) || (ch >= '0' && ch <= '9')
}

func isHexDigit(ch byte) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}
