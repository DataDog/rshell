// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package pyruntime

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// TokenKind identifies the type of a lexical token.
type TokenKind int

const (
	TokEOF     TokenKind = iota
	TokNewline           // logical newline
	TokIndent            // indent increase
	TokDedent            // indent decrease
	TokName              // identifier or keyword
	TokInt               // integer literal
	TokFloat             // float literal
	TokString            // string literal
	TokBytes             // bytes literal
	TokOp                // operator or punctuation
	TokComment           // comment (callers typically ignore)
)

// Token is a single lexical token.
type Token struct {
	Kind  TokenKind
	Value string
	Pos   Pos
}

// pythonKeywords is the set of Python keywords (emitted as TokName).
var pythonKeywords = map[string]bool{
	"if": true, "elif": true, "else": true, "for": true, "while": true,
	"def": true, "class": true, "return": true, "import": true, "from": true,
	"as": true, "with": true, "try": true, "except": true, "finally": true,
	"raise": true, "pass": true, "break": true, "continue": true,
	"and": true, "or": true, "not": true, "in": true, "is": true,
	"global": true, "nonlocal": true, "del": true, "assert": true,
	"lambda": true, "yield": true, "True": true, "False": true, "None": true,
	"async": true, "await": true,
}

// Lexer tokenizes Python source code.
type Lexer struct {
	src     []rune
	pos     int // current position in src
	line    int // 1-based
	col     int // 1-based
	paren   int // nesting depth of ( [ {
	pending []Token
	// indent stack: first entry is always ""
	indentStack []string
	// atLineStart tracks whether we need to process indentation on the next token
	atLineStart bool
	// afterNewline tracks whether the last emitted logical token was a newline
	// (used to decide whether to emit INDENT/DEDENT)
	lastWasNewline bool
}

// NewLexer creates a new Lexer for the given source string.
func NewLexer(src string) *Lexer {
	l := &Lexer{
		src:         []rune(src),
		line:        1,
		col:         1,
		indentStack: []string{""},
		atLineStart: true,
	}
	return l
}

// Next consumes and returns the next token.
func (l *Lexer) Next() Token {
	t := l.next()
	return t
}

// Peek returns the next token without consuming it.
func (l *Lexer) Peek() Token {
	return l.PeekN(0)
}

// PeekN peeks n tokens ahead (0 = next token).
func (l *Lexer) PeekN(n int) Token {
	for len(l.pending) <= n {
		l.pending = append(l.pending, l.next())
	}
	return l.pending[n]
}

// next reads the next token from the stream.
func (l *Lexer) next() Token {
	if len(l.pending) > 0 {
		t := l.pending[0]
		l.pending = l.pending[1:]
		return t
	}
	return l.readToken()
}

// readToken is the core tokenizer.
func (l *Lexer) readToken() Token {
	for {
		// At start of a new line (when not inside brackets), handle indentation.
		if l.atLineStart && l.paren == 0 {
			l.atLineStart = false
			toks := l.handleIndent()
			if len(toks) > 0 {
				// Queue all but first.
				if len(toks) > 1 {
					l.pending = append(toks[1:], l.pending...)
				}
				return toks[0]
			}
		}

		if l.pos >= len(l.src) {
			// Emit pending DEDENTs before EOF.
			if len(l.indentStack) > 1 {
				l.indentStack = l.indentStack[:len(l.indentStack)-1]
				pos := l.curPos()
				// If we haven't emitted a newline before dedents, emit one.
				if !l.lastWasNewline {
					l.lastWasNewline = true
					// queue the dedent
					l.pending = append(l.pending, Token{Kind: TokDedent, Pos: pos})
					return Token{Kind: TokNewline, Pos: pos}
				}
				return Token{Kind: TokDedent, Pos: pos}
			}
			return Token{Kind: TokEOF, Pos: l.curPos()}
		}

		ch := l.src[l.pos]

		// Line continuation.
		if ch == '\\' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '\n' {
			l.pos += 2
			l.line++
			l.col = 1
			continue
		}

		// Skip whitespace (not newlines, unless inside parens).
		if ch == ' ' || ch == '\t' || ch == '\r' {
			l.pos++
			if ch == '\t' {
				// tab advances to next multiple of 8
				l.col = ((l.col-1)/8+1)*8 + 1
			} else {
				l.col++
			}
			continue
		}

		// Comment.
		if ch == '#' {
			pos := l.curPos()
			start := l.pos
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
				l.col++
			}
			_ = pos
			_ = start
			// skip comment, don't emit
			continue
		}

		// Newline.
		if ch == '\n' {
			pos := l.curPos()
			l.pos++
			l.line++
			l.col = 1
			if l.paren > 0 {
				// Inside brackets: implicit line continuation, skip newline.
				continue
			}
			l.atLineStart = true
			l.lastWasNewline = true
			return Token{Kind: TokNewline, Value: "\n", Pos: pos}
		}

		// String literals.
		if isStringStart(l.src, l.pos) {
			return l.readStringOrBytes()
		}

		// Numbers.
		if unicode.IsDigit(ch) || (ch == '.' && l.pos+1 < len(l.src) && unicode.IsDigit(l.src[l.pos+1])) {
			return l.readNumber()
		}

		// Identifiers and keywords.
		if ch == '_' || unicode.IsLetter(ch) {
			return l.readName()
		}

		// Operators and punctuation.
		return l.readOp()
	}
}

func (l *Lexer) curPos() Pos {
	return Pos{Line: l.line, Col: l.col}
}

// handleIndent processes indentation at the start of a line.
// Returns a (possibly empty) list of tokens to emit.
func (l *Lexer) handleIndent() []Token {
	// Count leading whitespace.
	indentStr := l.measureIndent()

	// Skip blank lines and comment-only lines.
	pos := l.pos + len([]rune(indentStr))
	if pos < len(l.src) {
		ch := l.src[pos]
		if ch == '\n' || ch == '#' || ch == '\r' {
			// Blank or comment line — consume the indent whitespace and the line.
			l.advanceBy(len([]rune(indentStr)))
			return nil
		}
	} else {
		// End of file after whitespace — no indentation token needed.
		l.advanceBy(len([]rune(indentStr)))
		return nil
	}

	// Consume the indent characters.
	l.advanceBy(len([]rune(indentStr)))

	top := l.indentStack[len(l.indentStack)-1]
	pos2 := l.curPos()

	if indentStr == top {
		// Same level — no token.
		return nil
	}

	if strings.HasPrefix(indentStr, top) && indentStr != top {
		// Deeper — emit INDENT.
		l.indentStack = append(l.indentStack, indentStr)
		l.lastWasNewline = false
		return []Token{{Kind: TokIndent, Pos: pos2}}
	}

	// Shallower — find the matching level and emit DEDENTs.
	var toks []Token
	for len(l.indentStack) > 1 {
		l.indentStack = l.indentStack[:len(l.indentStack)-1]
		toks = append(toks, Token{Kind: TokDedent, Pos: pos2})
		if l.indentStack[len(l.indentStack)-1] == indentStr {
			break
		}
	}
	if l.indentStack[len(l.indentStack)-1] != indentStr {
		// Indentation error — we'll surface this as a dedent mismatch.
		// For robustness, just emit what we have.
	}
	l.lastWasNewline = false
	return toks
}

// measureIndent returns the leading whitespace string of the current line
// without advancing the lexer position.
func (l *Lexer) measureIndent() string {
	var buf strings.Builder
	i := l.pos
	for i < len(l.src) {
		ch := l.src[i]
		if ch == ' ' || ch == '\t' {
			buf.WriteRune(ch)
			i++
		} else {
			break
		}
	}
	return buf.String()
}

// advanceBy moves the lexer position forward by n runes, updating col.
func (l *Lexer) advanceBy(n int) {
	for i := 0; i < n && l.pos < len(l.src); i++ {
		ch := l.src[l.pos]
		l.pos++
		if ch == '\t' {
			l.col = ((l.col-1)/8+1)*8 + 1
		} else {
			l.col++
		}
	}
}

// isStringStart returns true if the position starts a string literal.
func isStringStart(src []rune, pos int) bool {
	if pos >= len(src) {
		return false
	}
	ch := src[pos]
	if ch == '"' || ch == '\'' {
		return true
	}
	// Check for string prefixes: r, b, f, u, rb, br, etc.
	if (ch == 'r' || ch == 'R' || ch == 'b' || ch == 'B' ||
		ch == 'f' || ch == 'F' || ch == 'u' || ch == 'U') &&
		pos+1 < len(src) {
		next := src[pos+1]
		if next == '"' || next == '\'' {
			return true
		}
		// Two-character prefix: rb, br, fr, rf
		if (ch == 'r' || ch == 'R' || ch == 'b' || ch == 'B' ||
			ch == 'f' || ch == 'F') && pos+2 < len(src) {
			if next == 'b' || next == 'B' || next == 'r' || next == 'R' || next == 'f' || next == 'F' {
				if src[pos+2] == '"' || src[pos+2] == '\'' {
					return true
				}
			}
		}
	}
	return false
}

// readStringOrBytes reads a string or bytes literal.
func (l *Lexer) readStringOrBytes() Token {
	pos := l.curPos()

	// Collect prefix.
	var prefix strings.Builder
	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		if ch == 'r' || ch == 'R' || ch == 'b' || ch == 'B' ||
			ch == 'f' || ch == 'F' || ch == 'u' || ch == 'U' {
			prefix.WriteRune(ch)
			l.pos++
			l.col++
		} else {
			break
		}
	}
	prefixStr := strings.ToLower(prefix.String())
	isRaw := strings.ContainsRune(prefixStr, 'r')
	isBytes := strings.ContainsRune(prefixStr, 'b')

	if l.pos >= len(l.src) {
		return Token{Kind: TokString, Value: "", Pos: pos}
	}

	quote := l.src[l.pos]
	l.pos++
	l.col++

	// Check for triple quote.
	triple := false
	if l.pos+1 < len(l.src) && l.src[l.pos] == quote && l.src[l.pos+1] == quote {
		triple = true
		l.pos += 2
		l.col += 2
	}

	var buf strings.Builder
	for l.pos < len(l.src) {
		ch := l.src[l.pos]

		if triple {
			if ch == quote && l.pos+2 < len(l.src) && l.src[l.pos+1] == quote && l.src[l.pos+2] == quote {
				l.pos += 3
				l.col += 3
				break
			}
		} else {
			if ch == quote {
				l.pos++
				l.col++
				break
			}
			if ch == '\n' {
				// Unterminated string.
				break
			}
		}

		if ch == '\\' && !isRaw {
			l.pos++
			l.col++
			if l.pos >= len(l.src) {
				break
			}
			esc := l.src[l.pos]
			l.pos++
			l.col++
			switch esc {
			case 'n':
				buf.WriteByte('\n')
			case 't':
				buf.WriteByte('\t')
			case 'r':
				buf.WriteByte('\r')
			case '\\':
				buf.WriteByte('\\')
			case '\'':
				buf.WriteByte('\'')
			case '"':
				buf.WriteByte('"')
			case '0':
				buf.WriteByte(0)
			case 'a':
				buf.WriteByte('\a')
			case 'b':
				buf.WriteByte('\b')
			case 'f':
				buf.WriteByte('\f')
			case 'v':
				buf.WriteByte('\v')
			case '\n':
				// line continuation inside string
				l.line++
				l.col = 1
			case 'x':
				// \xNN
				if l.pos+1 < len(l.src) {
					hexStr := string(l.src[l.pos : l.pos+2])
					if v, err := strconv.ParseUint(hexStr, 16, 8); err == nil {
						buf.WriteByte(byte(v))
						l.pos += 2
						l.col += 2
					} else {
						buf.WriteByte('\\')
						buf.WriteRune('x')
					}
				}
			case 'u':
				// \uNNNN
				if l.pos+3 < len(l.src) {
					hexStr := string(l.src[l.pos : l.pos+4])
					if v, err := strconv.ParseUint(hexStr, 16, 16); err == nil {
						buf.WriteRune(rune(v))
						l.pos += 4
						l.col += 4
					} else {
						buf.WriteByte('\\')
						buf.WriteRune('u')
					}
				}
			case 'U':
				// \UNNNNNNNN
				if l.pos+7 < len(l.src) {
					hexStr := string(l.src[l.pos : l.pos+8])
					if v, err := strconv.ParseUint(hexStr, 16, 32); err == nil && v <= unicode.MaxRune {
						buf.WriteRune(rune(v))
						l.pos += 8
						l.col += 8
					} else {
						buf.WriteByte('\\')
						buf.WriteRune('U')
					}
				}
			case 'N':
				// \N{name} — unicode name, skip for now
				buf.WriteByte('\\')
				buf.WriteRune('N')
			default:
				buf.WriteByte('\\')
				buf.WriteRune(esc)
			}
		} else {
			if ch == '\n' {
				l.line++
				l.col = 1
			} else {
				l.col++
			}
			buf.WriteRune(ch)
			l.pos++
		}
	}

	kind := TokString
	if isBytes {
		kind = TokBytes
	}
	return Token{Kind: kind, Value: buf.String(), Pos: pos}
}

// readNumber reads an integer or float literal.
func (l *Lexer) readNumber() Token {
	pos := l.curPos()
	start := l.pos

	// Check for special bases.
	if l.src[l.pos] == '0' && l.pos+1 < len(l.src) {
		next := l.src[l.pos+1]
		if next == 'x' || next == 'X' {
			return l.readHex(pos, start)
		}
		if next == 'o' || next == 'O' {
			return l.readOctal(pos, start)
		}
		if next == 'b' || next == 'B' {
			return l.readBinary(pos, start)
		}
	}

	// Decimal integer or float.
	isFloat := false
	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		if unicode.IsDigit(ch) || ch == '_' {
			l.pos++
			l.col++
		} else if ch == '.' && !isFloat {
			// Check that the next char is not another '.' (e.g. range operator in some langs)
			if l.pos+1 < len(l.src) && l.src[l.pos+1] == '.' {
				break
			}
			isFloat = true
			l.pos++
			l.col++
		} else if (ch == 'e' || ch == 'E') && !isFloat {
			isFloat = true
			l.pos++
			l.col++
			if l.pos < len(l.src) && (l.src[l.pos] == '+' || l.src[l.pos] == '-') {
				l.pos++
				l.col++
			}
		} else if (ch == 'e' || ch == 'E') && isFloat {
			l.pos++
			l.col++
			if l.pos < len(l.src) && (l.src[l.pos] == '+' || l.src[l.pos] == '-') {
				l.pos++
				l.col++
			}
		} else if ch == 'j' || ch == 'J' {
			// complex literal — treat as float for now
			l.pos++
			l.col++
			isFloat = true
			break
		} else {
			break
		}
	}

	// Handle float starting with '.'.
	val := string(l.src[start:l.pos])
	if isFloat {
		return Token{Kind: TokFloat, Value: val, Pos: pos}
	}
	return Token{Kind: TokInt, Value: val, Pos: pos}
}

func (l *Lexer) readHex(pos Pos, start int) Token {
	// consume 0x
	l.pos += 2
	l.col += 2
	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		if isHexDigit(ch) || ch == '_' {
			l.pos++
			l.col++
		} else {
			break
		}
	}
	return Token{Kind: TokInt, Value: string(l.src[start:l.pos]), Pos: pos}
}

func (l *Lexer) readOctal(pos Pos, start int) Token {
	l.pos += 2
	l.col += 2
	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		if (ch >= '0' && ch <= '7') || ch == '_' {
			l.pos++
			l.col++
		} else {
			break
		}
	}
	return Token{Kind: TokInt, Value: string(l.src[start:l.pos]), Pos: pos}
}

func (l *Lexer) readBinary(pos Pos, start int) Token {
	l.pos += 2
	l.col += 2
	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		if ch == '0' || ch == '1' || ch == '_' {
			l.pos++
			l.col++
		} else {
			break
		}
	}
	return Token{Kind: TokInt, Value: string(l.src[start:l.pos]), Pos: pos}
}

func isHexDigit(ch rune) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

// readName reads an identifier or keyword.
func (l *Lexer) readName() Token {
	pos := l.curPos()
	start := l.pos
	for l.pos < len(l.src) {
		ch := l.src[l.pos]
		if ch == '_' || unicode.IsLetter(ch) || unicode.IsDigit(ch) {
			l.pos++
			l.col++
		} else {
			break
		}
	}
	val := string(l.src[start:l.pos])
	return Token{Kind: TokName, Value: val, Pos: pos}
}

// readOp reads an operator or punctuation token.
func (l *Lexer) readOp() Token {
	pos := l.curPos()
	ch := l.src[l.pos]

	// Track paren depth for indent/dedent logic.
	switch ch {
	case '(':
		l.paren++
		l.pos++
		l.col++
		return Token{Kind: TokOp, Value: "(", Pos: pos}
	case ')':
		if l.paren > 0 {
			l.paren--
		}
		l.pos++
		l.col++
		return Token{Kind: TokOp, Value: ")", Pos: pos}
	case '[':
		l.paren++
		l.pos++
		l.col++
		return Token{Kind: TokOp, Value: "[", Pos: pos}
	case ']':
		if l.paren > 0 {
			l.paren--
		}
		l.pos++
		l.col++
		return Token{Kind: TokOp, Value: "]", Pos: pos}
	case '{':
		l.paren++
		l.pos++
		l.col++
		return Token{Kind: TokOp, Value: "{", Pos: pos}
	case '}':
		if l.paren > 0 {
			l.paren--
		}
		l.pos++
		l.col++
		return Token{Kind: TokOp, Value: "}", Pos: pos}
	}

	// Try multi-character operators first.
	if l.pos+2 < len(l.src) {
		three := string(l.src[l.pos : l.pos+3])
		switch three {
		case "<<=", ">>=", "**=", "//=":
			l.pos += 3
			l.col += 3
			return Token{Kind: TokOp, Value: three, Pos: pos}
		}
	}

	if l.pos+1 < len(l.src) {
		two := string(l.src[l.pos : l.pos+2])
		switch two {
		case "**", "//", "<<", ">>", "<=", ">=", "!=", "==",
			"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=",
			"->", ":=":
			l.pos += 2
			l.col += 2
			return Token{Kind: TokOp, Value: two, Pos: pos}
		}
	}

	// Single character operators.
	l.pos++
	l.col++
	return Token{Kind: TokOp, Value: string(ch), Pos: pos}
}

// tokenKindString returns a human-readable name for a token kind.
func tokenKindString(k TokenKind) string {
	switch k {
	case TokEOF:
		return "EOF"
	case TokNewline:
		return "NEWLINE"
	case TokIndent:
		return "INDENT"
	case TokDedent:
		return "DEDENT"
	case TokName:
		return "NAME"
	case TokInt:
		return "INT"
	case TokFloat:
		return "FLOAT"
	case TokString:
		return "STRING"
	case TokBytes:
		return "BYTES"
	case TokOp:
		return "OP"
	case TokComment:
		return "COMMENT"
	default:
		return fmt.Sprintf("Token(%d)", int(k))
	}
}

// ensure utf8 is used (for utf8.RuneLen in potential future use)
var _ = utf8.RuneLen
var _ = unicode.IsLetter

// ensure strings is used
var _ = strings.Builder{}
