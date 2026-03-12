// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package sed

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// parser holds state during sed script parsing.
type parser struct {
	input      string
	pos        int
	useERE     bool
	groupDepth int
}

// maxGroupDepth is the maximum nesting depth for {...} groups.
const maxGroupDepth = 256

func parseScript(script string, useERE bool) ([]*sedCmd, error) {
	p := &parser{input: script, useERE: useERE}
	cmds, err := p.parseCommands(false)
	if err != nil {
		return nil, err
	}
	return cmds, nil
}

func (p *parser) parseCommands(inGroup bool) ([]*sedCmd, error) {
	var cmds []*sedCmd
	for p.pos < len(p.input) {
		p.skipWhitespaceAndSemicolons()
		if p.pos >= len(p.input) {
			break
		}
		ch := p.input[p.pos]
		if ch == '}' {
			if inGroup {
				p.pos++ // consume '}'
				return cmds, nil
			}
			return nil, errors.New("unexpected '}'")
		}
		if ch == '#' {
			// Comment — skip to end of line.
			for p.pos < len(p.input) && p.input[p.pos] != '\n' {
				p.pos++
			}
			continue
		}
		cmd, err := p.parseOneCommand()
		if err != nil {
			return nil, err
		}
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if inGroup {
		return nil, errors.New("unterminated '{'")
	}
	return cmds, nil
}

func (p *parser) skipWhitespaceAndSemicolons() {
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == ';' {
			p.pos++
		} else {
			break
		}
	}
}

func (p *parser) skipSpaces() {
	for p.pos < len(p.input) && (p.input[p.pos] == ' ' || p.input[p.pos] == '\t') {
		p.pos++
	}
}

func (p *parser) parseOneCommand() (*sedCmd, error) {
	cmd := &sedCmd{}

	// Parse first address.
	addr1, err := p.parseAddress()
	if err != nil {
		return nil, err
	}
	cmd.addr1 = addr1

	// Check for comma (address range).
	if cmd.addr1 != nil && p.pos < len(p.input) && p.input[p.pos] == ',' {
		p.pos++ // consume ','
		p.skipSpaces()
		addr2, err := p.parseAddress()
		if err != nil {
			return nil, err
		}
		if addr2 == nil {
			return nil, errors.New("expected address after ','")
		}
		cmd.addr2 = addr2
	}

	p.skipSpaces()

	// Check for negation.
	if p.pos < len(p.input) && p.input[p.pos] == '!' {
		cmd.negated = true
		p.pos++
		p.skipSpaces()
	}

	if p.pos >= len(p.input) {
		return nil, errors.New("missing command")
	}

	ch := p.input[p.pos]
	p.pos++

	switch ch {
	case 's':
		return p.parseSubstitute(cmd)
	case 'y':
		return p.parseTransliterate(cmd)
	case 'p':
		cmd.kind = cmdPrint
	case 'P':
		cmd.kind = cmdPrintFirstLine
	case 'd':
		cmd.kind = cmdDelete
	case 'D':
		cmd.kind = cmdDeleteFirstLine
	case 'q':
		cmd.kind = cmdQuit
		cmd.quitCode = p.parseOptionalExitCode()
	case 'Q':
		cmd.kind = cmdQuitNoprint
		cmd.quitCode = p.parseOptionalExitCode()
	case 'a':
		cmd.kind = cmdAppend
		cmd.text = p.parseTextArg()
	case 'i':
		cmd.kind = cmdInsert
		cmd.text = p.parseTextArg()
	case 'c':
		cmd.kind = cmdChange
		cmd.text = p.parseTextArg()
	case '=':
		cmd.kind = cmdLineNum
	case 'l':
		cmd.kind = cmdPrintUnambig
	case 'n':
		cmd.kind = cmdNext
	case 'N':
		cmd.kind = cmdNextAppend
	case 'h':
		cmd.kind = cmdHoldCopy
	case 'H':
		cmd.kind = cmdHoldAppend
	case 'g':
		cmd.kind = cmdGetCopy
	case 'G':
		cmd.kind = cmdGetAppend
	case 'x':
		cmd.kind = cmdExchange
	case 'b':
		cmd.kind = cmdBranch
		cmd.label = p.parseLabelArg()
	case 't':
		cmd.kind = cmdBranchIfSub
		cmd.label = p.parseLabelArg()
	case 'T':
		cmd.kind = cmdBranchIfNoSub
		cmd.label = p.parseLabelArg()
	case ':':
		cmd.kind = cmdLabel
		cmd.label = p.parseLabelArg()
		if cmd.label == "" {
			return nil, errors.New("missing label name for ':'")
		}
	case '{':
		if p.groupDepth >= maxGroupDepth {
			return nil, errors.New("group nesting depth limit exceeded")
		}
		p.groupDepth++
		children, err := p.parseCommands(true)
		p.groupDepth--
		if err != nil {
			return nil, err
		}
		cmd.kind = cmdGroup
		cmd.children = children
	case 'e':
		return nil, errors.New("'e' command is blocked: command execution is not allowed")
	case 'w':
		return nil, errors.New("'w' command is blocked: file writing is not allowed")
	case 'W':
		return nil, errors.New("'W' command is blocked: file writing is not allowed")
	case 'r':
		return nil, errors.New("'r' command is blocked: unsandboxed file reading is not allowed")
	case 'R':
		return nil, errors.New("'R' command is blocked: unsandboxed file reading is not allowed")
	default:
		return nil, errors.New("unknown command: '" + string(ch) + "'")
	}

	return cmd, nil
}

func (p *parser) parseOptionalExitCode() uint8 {
	p.skipSpaces()
	start := p.pos
	for p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
		p.pos++
	}
	if start == p.pos {
		return 0
	}
	n, err := strconv.Atoi(p.input[start:p.pos])
	if err != nil || n < 0 || n > 255 {
		return 0
	}
	return uint8(n)
}

func (p *parser) parseTextArg() string {
	// GNU sed allows: a\text, a text, or a\<newline>text
	if p.pos < len(p.input) && p.input[p.pos] == '\\' {
		p.pos++
		if p.pos < len(p.input) && p.input[p.pos] == '\n' {
			p.pos++ // consume newline after backslash
		}
	} else {
		p.skipSpaces()
	}
	start := p.pos
	for p.pos < len(p.input) && p.input[p.pos] != '\n' {
		p.pos++
	}
	return p.input[start:p.pos]
}

func (p *parser) parseLabelArg() string {
	p.skipSpaces()
	start := p.pos
	for p.pos < len(p.input) && p.input[p.pos] != ' ' && p.input[p.pos] != '\t' &&
		p.input[p.pos] != '\n' && p.input[p.pos] != ';' && p.input[p.pos] != '}' {
		p.pos++
	}
	return p.input[start:p.pos]
}

func (p *parser) parseAddress() (*address, error) {
	if p.pos >= len(p.input) {
		return nil, nil
	}

	ch := p.input[p.pos]

	// Line number.
	if ch >= '0' && ch <= '9' {
		start := p.pos
		for p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
			p.pos++
		}
		// Check for first~step syntax.
		if p.pos < len(p.input) && p.input[p.pos] == '~' {
			first, err := strconv.ParseInt(p.input[start:p.pos], 10, 64)
			if err != nil {
				return nil, errors.New("invalid address: " + p.input[start:p.pos])
			}
			p.pos++ // consume '~'
			stepStart := p.pos
			for p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
				p.pos++
			}
			step, err := strconv.ParseInt(p.input[stepStart:p.pos], 10, 64)
			if err != nil || step <= 0 {
				return nil, errors.New("invalid step in address")
			}
			return &address{kind: addrStep, first: first, step: step}, nil
		}
		n, err := strconv.ParseInt(p.input[start:p.pos], 10, 64)
		if err != nil {
			return nil, errors.New("invalid line number: " + p.input[start:p.pos])
		}
		return &address{kind: addrLine, line: n}, nil
	}

	// Last line.
	if ch == '$' {
		p.pos++
		return &address{kind: addrLast}, nil
	}

	// Regex address.
	if ch == '/' || ch == '\\' {
		var delim byte
		if ch == '\\' {
			p.pos++ // consume '\'
			if p.pos >= len(p.input) {
				return nil, errors.New("expected delimiter after '\\'")
			}
			delim = p.input[p.pos]
		} else {
			delim = '/'
		}
		p.pos++ // consume delimiter
		pattern, err := p.readUntilDelimiter(delim)
		if err != nil {
			return nil, err
		}
		re, err := p.compileRegex(pattern)
		if err != nil {
			return nil, err
		}
		return &address{kind: addrRegexp, re: re}, nil
	}

	return nil, nil
}

func (p *parser) readUntilDelimiter(delim byte) (string, error) {
	var sb strings.Builder
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if ch == '\\' && p.pos+1 < len(p.input) {
			next := p.input[p.pos+1]
			if next == delim {
				sb.WriteByte(delim)
				p.pos += 2
				continue
			}
			sb.WriteByte('\\')
			sb.WriteByte(next)
			p.pos += 2
			continue
		}
		if ch == delim {
			p.pos++ // consume closing delimiter
			return sb.String(), nil
		}
		sb.WriteByte(ch)
		p.pos++
	}
	return "", errors.New("unterminated address regex")
}

func (p *parser) parseSubstitute(cmd *sedCmd) (*sedCmd, error) {
	if p.pos >= len(p.input) {
		return nil, errors.New("missing delimiter for 's' command")
	}
	delim := p.input[p.pos]
	if delim == '\\' || delim == '\n' {
		return nil, errors.New("invalid delimiter for 's' command: '" + string(delim) + "'")
	}
	p.pos++ // consume delimiter

	// Read pattern.
	pattern, err := p.readSubstPart(delim)
	if err != nil {
		return nil, errors.New("unterminated 's' command: " + err.Error())
	}

	// Read replacement.
	replacement, err := p.readSubstPart(delim)
	if err != nil {
		return nil, errors.New("unterminated 's' command: " + err.Error())
	}

	// Read flags.
	cmd.kind = cmdSubstitute
	cmd.subReplacement = replacement
	caseInsensitive := false

	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		switch ch {
		case 'g':
			cmd.subGlobal = true
			p.pos++
		case 'p':
			cmd.subPrint = true
			p.pos++
		case 'i', 'I':
			caseInsensitive = true
			p.pos++
		case 'w':
			return nil, errors.New("'w' flag in 's' command is blocked: file writing is not allowed")
		case 'e':
			return nil, errors.New("'e' flag in 's' command is blocked: command execution is not allowed")
		default:
			if ch == '0' {
				return nil, errors.New("number option to 's' command may not be zero")
			}
			if ch >= '1' && ch <= '9' {
				start := p.pos
				for p.pos < len(p.input) && p.input[p.pos] >= '0' && p.input[p.pos] <= '9' {
					p.pos++
				}
				n, err := strconv.Atoi(p.input[start:p.pos])
				if err != nil || n <= 0 {
					return nil, errors.New("invalid substitution occurrence number")
				}
				cmd.subNth = n
				continue
			}
			// Any other character ends the flag list.
			goto flagsDone
		}
	}
flagsDone:

	if pattern == "" {
		// Empty pattern means "reuse last regex" — defer to runtime.
		// cmd.subRe stays nil to signal this.
		if caseInsensitive {
			cmd.subCaseInsensitive = true
		}
	} else {
		re, err := p.compileRegex(pattern)
		if err != nil {
			return nil, err
		}
		// Apply case-insensitive flag after BRE-to-ERE conversion so (?i) isn't mangled.
		if caseInsensitive {
			re, err = regexp.Compile("(?i)" + re.String())
			if err != nil {
				return nil, errors.New("invalid regex with case-insensitive flag: " + err.Error())
			}
		}
		cmd.subRe = re
	}
	return cmd, nil
}

func (p *parser) readSubstPart(delim byte) (string, error) {
	var sb strings.Builder
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if ch == '\\' && p.pos+1 < len(p.input) {
			next := p.input[p.pos+1]
			if next == delim {
				sb.WriteByte(delim)
				p.pos += 2
				continue
			}
			if next == 'n' {
				sb.WriteByte('\n')
				p.pos += 2
				continue
			}
			if next == 't' {
				sb.WriteByte('\t')
				p.pos += 2
				continue
			}
			if next == 'a' {
				sb.WriteByte('\a')
				p.pos += 2
				continue
			}
			if next == 'b' {
				sb.WriteByte('\b')
				p.pos += 2
				continue
			}
			if next == 'f' {
				sb.WriteByte('\f')
				p.pos += 2
				continue
			}
			if next == 'r' {
				sb.WriteByte('\r')
				p.pos += 2
				continue
			}
			sb.WriteByte('\\')
			sb.WriteByte(next)
			p.pos += 2
			continue
		}
		if ch == delim {
			p.pos++ // consume closing delimiter
			return sb.String(), nil
		}
		sb.WriteByte(ch)
		p.pos++
	}
	return sb.String(), errors.New("unterminated delimiter")
}

func (p *parser) parseTransliterate(cmd *sedCmd) (*sedCmd, error) {
	if p.pos >= len(p.input) {
		return nil, errors.New("missing delimiter for 'y' command")
	}
	delim := p.input[p.pos]
	p.pos++

	srcStr, err := p.readSubstPart(delim)
	if err != nil {
		return nil, err
	}
	dstStr, err := p.readSubstPart(delim)
	if err != nil {
		return nil, err
	}

	src := []rune(srcStr)
	dst := []rune(dstStr)
	if len(src) != len(dst) {
		return nil, errors.New("'y' command: source and destination must have the same length")
	}

	cmd.kind = cmdTransliterate
	cmd.transFrom = src
	cmd.transTo = dst
	return cmd, nil
}

// compileRegex compiles a regex pattern, converting BRE to ERE if needed.
func (p *parser) compileRegex(pattern string) (*regexp.Regexp, error) {
	if !p.useERE {
		pattern = breToERE(pattern)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, errors.New("invalid regex: " + err.Error())
	}
	return re, nil
}

// breToERE converts a basic regular expression to an extended one.
// In BRE: \( \) \{ \} \+ \? are special; ( ) { } + ? are literal.
// In ERE: ( ) { } + ? are special; \( \) etc. are literal.
func breToERE(pattern string) string {
	var sb strings.Builder
	sb.Grow(len(pattern))
	i := 0
	for i < len(pattern) {
		if pattern[i] == '\\' && i+1 < len(pattern) {
			next := pattern[i+1]
			switch next {
			case '(', ')', '{', '}', '+', '?', '|':
				// BRE escaped special → ERE unescaped special.
				sb.WriteByte(next)
				i += 2
			default:
				// Includes backreferences (\1-\9) which RE2 doesn't support
				// but are passed through unchanged.
				sb.WriteByte('\\')
				sb.WriteByte(next)
				i += 2
			}
		} else {
			ch := pattern[i]
			switch ch {
			case '(', ')', '{', '}', '+', '?', '|':
				// In BRE these are literal; escape them for ERE.
				sb.WriteByte('\\')
				sb.WriteByte(ch)
			default:
				sb.WriteByte(ch)
			}
			i++
		}
	}
	return sb.String()
}
