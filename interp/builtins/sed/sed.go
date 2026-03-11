// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package sed implements the sed builtin command.
//
// sed — stream editor for filtering and transforming text
//
// Usage: sed [OPTION]... [script] [FILE]...
//
//	sed [OPTION]... -e script [-e script]... [FILE]...
//
// sed reads input files (or standard input if no files are given, or when
// FILE is -), applies editing commands from the script, and writes the
// result to standard output.
//
// Accepted flags:
//
//	-n, --quiet, --silent
//	    Suppress automatic printing of pattern space. Only lines
//	    explicitly printed via the p command are output.
//
//	-e script, --expression=script
//	    Add the script commands to the set of commands to execute.
//	    Multiple -e options are allowed; they are concatenated in order.
//
//	-E, --regexp-extended
//	    Use extended regular expressions (ERE) rather than basic (BRE).
//
//	-r
//	    GNU alias for -E (extended regular expressions).
//
//	-h, --help
//	    Print this usage message to stdout and exit 0.
//
// Supported sed commands:
//
//	s/regex/replacement/[flags]   Substitute matches of regex with replacement.
//	                              Flags: g (global), p (print), i/I (case-insensitive),
//	                              N (replace Nth match).
//	p                             Print the current pattern space.
//	d                             Delete pattern space, start next cycle.
//	q [code]                      Quit with optional exit code (prints pattern space first).
//	Q [code]                      Quit with optional exit code (does not print).
//	y/src/dst/                    Transliterate characters from src to dst.
//	a\text  /  a text             Append text after the current line.
//	i\text  /  i text             Insert text before the current line.
//	c\text  /  c text             Replace line(s) with text.
//	=                             Print the current line number.
//	l                             Print pattern space unambiguously.
//	n                             Read next input line into pattern space.
//	N                             Append next input line to pattern space.
//	h                             Copy pattern space to hold space.
//	H                             Append pattern space to hold space.
//	g                             Copy hold space to pattern space.
//	G                             Append hold space to pattern space.
//	x                             Exchange pattern and hold spaces.
//	b [label]                     Branch to label (or end of script).
//	: label                       Define a label for branching.
//	t [label]                     Branch to label if s/// made a substitution.
//	T [label]                     Branch to label if s/// did NOT make a substitution.
//	{...}                         Group commands.
//	!command                      Negate the address (apply to non-matching lines).
//
// Addressing:
//
//	N           Line number (1-based).
//	$           Last line.
//	/regex/     Lines matching regex.
//	addr1,addr2 Range of lines.
//	first~step  Every step-th line starting from first (GNU extension).
//
// Rejected commands (blocked for safety):
//
//	e           Execute pattern space as shell command (blocked: command execution).
//	w file      Write pattern space to file (blocked: file write).
//	W file      Write first line to file (blocked: file write).
//	r file      Read file contents (blocked: unsandboxed file read).
//	R file      Read one line from file (blocked: unsandboxed file read).
//
// Rejected flags:
//
//	-i, --in-place    Edit files in place (blocked: file write).
//	-f, --file        Read script from file (not implemented).
//	-s, --separate    Treat files as separate streams (not implemented).
//	-z, --null-data   NUL-separated input (not implemented).
//
// Exit codes:
//
//	0  Success (or custom code via q/Q command).
//	1  Invalid script syntax, missing file, or other error.
//
// Memory safety:
//
//	Input is processed line-by-line via a buffered scanner with a per-line
//	cap of 1 MiB (MaxLineBytes). Pattern space and hold space are each
//	bounded to MaxSpaceBytes (1 MiB). Branch loops are capped at
//	MaxBranchIterations (10 000) per input line to prevent infinite loops.
//	Non-regular-file inputs are subject to a MaxTotalReadBytes (256 MiB)
//	limit to guard against infinite sources.
//
// Regex safety:
//
//	All regular expressions use Go's regexp package, which implements RE2
//	(guaranteed linear-time matching, no backtracking). This prevents ReDoS
//	attacks. BRE patterns are converted to ERE syntax before compilation.
package sed

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/DataDog/rshell/interp/builtins"
)

// Cmd is the sed builtin command descriptor.
var Cmd = builtins.Command{Name: "sed", MakeFlags: registerFlags}

// MaxLineBytes is the per-line buffer cap for the line scanner.
const MaxLineBytes = 1 << 20 // 1 MiB

// MaxSpaceBytes is the maximum size for pattern space and hold space.
const MaxSpaceBytes = 1 << 20 // 1 MiB

// MaxBranchIterations is the maximum number of branch iterations per
// input line to prevent infinite loops.
const MaxBranchIterations = 10_000

// MaxTotalReadBytes is the maximum total bytes consumed from a single
// non-regular-file input source.
const MaxTotalReadBytes = 256 << 20 // 256 MiB

// expressionSlice collects multiple -e values.
type expressionSlice []string

func (e *expressionSlice) String() string { return strings.Join(*e, "\n") }
func (e *expressionSlice) Set(val string) error {
	*e = append(*e, val)
	return nil
}
func (e *expressionSlice) Type() string { return "string" }

// registerFlags sets up sed flags and returns the handler.
func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := fs.BoolP("help", "h", false, "print usage and exit")
	quiet := fs.BoolP("quiet", "n", false, "suppress automatic printing of pattern space")
	fs.Lookup("quiet").NoOptDefVal = "true"
	// --silent is an alias for --quiet.
	silent := fs.Bool("silent", false, "alias for --quiet")
	fs.Lookup("silent").NoOptDefVal = "true"

	var expressions expressionSlice
	fs.VarP(&expressions, "expression", "e", "add script commands")

	extendedE := fs.BoolP("regexp-extended", "E", false, "use extended regular expressions")
	extendedR := fs.BoolP("regexp-extended-r", "r", false, "use extended regular expressions (GNU alias for -E)")
	fs.Lookup("regexp-extended-r").Hidden = true

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: sed [OPTION]... [script] [FILE]...\n")
			callCtx.Out("Stream editor for filtering and transforming text.\n")
			callCtx.Out("With no FILE, or when FILE is -, read standard input.\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}

		suppressPrint := *quiet || *silent
		useERE := *extendedE || *extendedR

		// Determine script and files.
		var scriptParts []string
		var files []string

		if len(expressions) > 0 {
			scriptParts = []string(expressions)
			files = args
		} else if len(args) > 0 {
			scriptParts = []string{args[0]}
			files = args[1:]
		} else {
			callCtx.Errf("sed: no script command has been specified\n")
			return builtins.Result{Code: 1}
		}

		// Parse the sed script.
		prog, err := parseScript(strings.Join(scriptParts, "\n"), useERE)
		if err != nil {
			callCtx.Errf("sed: %s\n", err)
			return builtins.Result{Code: 1}
		}

		if len(files) == 0 {
			files = []string{"-"}
		}

		// Create the execution engine.
		eng := &engine{
			callCtx:       callCtx,
			prog:          prog,
			suppressPrint: suppressPrint,
		}

		var failed bool
		for _, file := range files {
			if ctx.Err() != nil {
				break
			}
			if err := eng.processFile(ctx, callCtx, file); err != nil {
				var qe *quitError
				if errors.As(err, &qe) {
					// q command: print pattern space if requested, then exit.
					return builtins.Result{Code: qe.code}
				}
				name := file
				if file == "-" {
					name = "standard input"
				}
				callCtx.Errf("sed: %s: %s\n", name, callCtx.PortableErr(err))
				failed = true
			}
		}

		if failed {
			return builtins.Result{Code: 1}
		}
		return builtins.Result{}
	}
}

// --- Error types ---

// quitError signals a q or Q command with an exit code.
type quitError struct {
	code uint8
}

func (e *quitError) Error() string {
	return fmt.Sprintf("quit with code %d", e.code)
}

// --- Address types ---

// addrType distinguishes different address kinds.
type addrType int

const (
	addrNone    addrType = iota
	addrLine             // specific line number
	addrLast             // $ (last line)
	addrRegexp           // /regex/
	addrStep             // first~step (GNU extension)
)

// address represents a sed address (line number, regex, or $).
type address struct {
	kind  addrType
	line  int64          // for addrLine
	re    *regexp.Regexp // for addrRegexp
	first int64          // for addrStep
	step  int64          // for addrStep
}

// --- Command types ---

// cmdType identifies the sed command.
type cmdType int

const (
	cmdSubstitute cmdType = iota
	cmdPrint
	cmdDelete
	cmdQuit
	cmdQuitNoprint
	cmdTransliterate
	cmdAppend
	cmdInsert
	cmdChange
	cmdLineNum
	cmdPrintUnambig
	cmdNext
	cmdNextAppend
	cmdHoldCopy
	cmdHoldAppend
	cmdGetCopy
	cmdGetAppend
	cmdExchange
	cmdBranch
	cmdLabel
	cmdBranchIfSub
	cmdBranchIfNoSub
	cmdPrintFirstLine  // P: print up to first embedded newline
	cmdDeleteFirstLine // D: delete up to first embedded newline, restart cycle
	cmdGroup
	cmdNoop
)

// sedCmd represents a single parsed sed command.
type sedCmd struct {
	addr1    *address
	addr2    *address
	negated  bool
	inRange  bool // stateful: tracks whether we're inside a two-address range
	kind     cmdType

	// For s command:
	subRe          *regexp.Regexp
	subReplacement string
	subGlobal      bool
	subPrint       bool
	subNth         int

	// For y command:
	transFrom []rune
	transTo   []rune

	// For a, i, c commands:
	text string

	// For q, Q commands:
	quitCode uint8

	// For b, t, T commands:
	label string

	// For { ... } grouping:
	children []*sedCmd
}

// --- Parser ---

// parser holds state during sed script parsing.
type parser struct {
	input  string
	pos    int
	useERE bool
}

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
		children, err := p.parseCommands(true)
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
	for p.pos < len(p.input) && p.input[p.pos] != '\n' && p.input[p.pos] != ';' {
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
	return sb.String(), nil
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

// --- Execution Engine ---

// engine holds the state for executing a sed script.
type engine struct {
	callCtx       *builtins.CallContext
	prog          []*sedCmd
	suppressPrint bool
	lineNum       int64
	lastLine      bool
	patternSpace  string
	holdSpace     string
	appendQueue   []string // text queued by 'a' command, flushed after auto-print
	subMade       bool     // set when s/// succeeds (cleared on new input line)
	totalRead     int64
	isRegularFile bool
}

// lineReader wraps a scanner with one-line look-ahead so we can determine
// whether the current line is the last one, while still allowing n/N commands
// to consume lines from the same scanner.
type lineReader struct {
	sc            *bufio.Scanner
	nextLine      string
	hasNext       bool
	totalRead     int64
	isRegularFile bool
}

func newLineReader(sc *bufio.Scanner, isRegular bool) *lineReader {
	lr := &lineReader{sc: sc, isRegularFile: isRegular}
	lr.advance() // prime the look-ahead
	return lr
}

func (lr *lineReader) advance() bool {
	if lr.sc.Scan() {
		lr.nextLine = lr.sc.Text()
		lr.totalRead += int64(len(lr.sc.Bytes()))
		lr.hasNext = true
		return true
	}
	lr.hasNext = false
	return false
}

func (lr *lineReader) readLine() (string, bool) {
	if !lr.hasNext {
		return "", false
	}
	line := lr.nextLine
	lr.advance()
	return line, true
}

func (lr *lineReader) isLast() bool {
	return !lr.hasNext
}

func (lr *lineReader) checkLimit() error {
	if !lr.isRegularFile && lr.totalRead > MaxTotalReadBytes {
		return errors.New("input too large: read limit exceeded")
	}
	return nil
}

func (eng *engine) processFile(ctx context.Context, callCtx *builtins.CallContext, file string) error {
	var rc io.ReadCloser
	if file == "-" {
		if callCtx.Stdin == nil {
			return nil
		}
		eng.isRegularFile = isRegularFile(callCtx.Stdin)
		rc = io.NopCloser(callCtx.Stdin)
	} else {
		f, err := callCtx.OpenFile(ctx, file, os.O_RDONLY, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		eng.isRegularFile = isRegularFile(f)
		rc = f
	}

	sc := bufio.NewScanner(rc)
	buf := make([]byte, 4096)
	sc.Buffer(buf, MaxLineBytes)

	lr := newLineReader(sc, eng.isRegularFile)

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		line, ok := lr.readLine()
		if !ok {
			break
		}
		if err := lr.checkLimit(); err != nil {
			return err
		}

		eng.lineNum++
		eng.patternSpace = line
		eng.lastLine = lr.isLast()

		err := eng.runCycle(ctx, lr)
		if err != nil {
			return err
		}
	}

	if err := sc.Err(); err != nil {
		return err
	}
	return nil
}

// runCycle executes the script for the current input line.
func (eng *engine) runCycle(ctx context.Context, lr *lineReader) error {
	eng.subMade = false
	eng.appendQueue = eng.appendQueue[:0]
	action, err := eng.execCommandsFrom(ctx, 0, lr, 0)
	if err != nil {
		return err
	}
	if action != actionDelete && !eng.suppressPrint {
		eng.callCtx.Outf("%s\n", eng.patternSpace)
	}
	// Flush queued 'a' text after auto-print (even if auto-print was suppressed or deleted).
	for _, text := range eng.appendQueue {
		eng.callCtx.Outf("%s\n", text)
	}
	return nil
}

// actionType signals how to proceed after executing a command.
type actionType int

const (
	actionContinue actionType = iota
	actionDelete              // d/D command: skip auto-print, start next cycle
)

// execCommandsFrom executes commands starting from index startIdx in the given
// command list. For branching, it always searches the full eng.prog for labels
// and restarts from there to handle backward branches correctly.
func (eng *engine) execCommandsFrom(ctx context.Context, startIdx int, lr *lineReader, depth int) (actionType, error) {
	return eng.execCmds(ctx, eng.prog, startIdx, lr, depth)
}

func (eng *engine) execCmds(ctx context.Context, cmds []*sedCmd, startIdx int, lr *lineReader, depth int) (actionType, error) {
	if depth > MaxBranchIterations {
		return actionContinue, errors.New("branch loop limit exceeded")
	}

	for i := startIdx; i < len(cmds); i++ {
		if ctx.Err() != nil {
			return actionContinue, ctx.Err()
		}

		cmd := cmds[i]

		if cmd.kind == cmdLabel {
			continue
		}

		if !eng.addressMatch(cmd) {
			continue
		}

		switch cmd.kind {
		case cmdSubstitute:
			if err := eng.execSubstitute(cmd); err != nil {
				return actionContinue, err
			}

		case cmdPrint:
			eng.callCtx.Outf("%s\n", eng.patternSpace)

		case cmdDelete:
			return actionDelete, nil

		case cmdPrintFirstLine:
			if idx := strings.IndexByte(eng.patternSpace, '\n'); idx >= 0 {
				eng.callCtx.Outf("%s\n", eng.patternSpace[:idx])
			} else {
				eng.callCtx.Outf("%s\n", eng.patternSpace)
			}

		case cmdDeleteFirstLine:
			if idx := strings.IndexByte(eng.patternSpace, '\n'); idx >= 0 {
				eng.patternSpace = eng.patternSpace[idx+1:]
				// Restart the cycle with the remaining pattern space.
				eng.subMade = false
				eng.appendQueue = eng.appendQueue[:0]
				return eng.execCommandsFrom(ctx, 0, lr, depth+1)
			}
			return actionDelete, nil

		case cmdQuit:
			if !eng.suppressPrint {
				eng.callCtx.Outf("%s\n", eng.patternSpace)
			}
			return actionContinue, &quitError{code: cmd.quitCode}

		case cmdQuitNoprint:
			return actionContinue, &quitError{code: cmd.quitCode}

		case cmdTransliterate:
			eng.patternSpace = eng.transliterate(eng.patternSpace, cmd.transFrom, cmd.transTo)

		case cmdAppend:
			eng.appendQueue = append(eng.appendQueue, cmd.text)

		case cmdInsert:
			eng.callCtx.Outf("%s\n", cmd.text)

		case cmdChange:
			eng.callCtx.Outf("%s\n", cmd.text)
			return actionDelete, nil

		case cmdLineNum:
			eng.callCtx.Outf("%d\n", eng.lineNum)

		case cmdPrintUnambig:
			eng.printUnambiguous()

		case cmdNext:
			if !eng.suppressPrint {
				eng.callCtx.Outf("%s\n", eng.patternSpace)
			}
			for _, text := range eng.appendQueue {
				eng.callCtx.Outf("%s\n", text)
			}
			eng.appendQueue = eng.appendQueue[:0]
			line, ok := lr.readLine()
			if ok {
				if err := lr.checkLimit(); err != nil {
					return actionContinue, err
				}
				eng.lineNum++
				eng.patternSpace = line
				eng.lastLine = lr.isLast()
			} else {
				eng.lastLine = true
				return actionContinue, nil
			}

		case cmdNextAppend:
			line, ok := lr.readLine()
			if ok {
				if err := lr.checkLimit(); err != nil {
					return actionContinue, err
				}
				eng.lineNum++
				if len(eng.patternSpace)+1+len(line) > MaxSpaceBytes {
					return actionContinue, errors.New("pattern space exceeded size limit")
				}
				eng.patternSpace += "\n" + line
				eng.lastLine = lr.isLast()
			} else {
				if !eng.suppressPrint {
					eng.callCtx.Outf("%s\n", eng.patternSpace)
				}
				return actionDelete, nil
			}

		case cmdHoldCopy:
			eng.holdSpace = eng.patternSpace

		case cmdHoldAppend:
			if len(eng.holdSpace)+1+len(eng.patternSpace) > MaxSpaceBytes {
				return actionContinue, errors.New("hold space exceeded size limit")
			}
			eng.holdSpace += "\n" + eng.patternSpace

		case cmdGetCopy:
			eng.patternSpace = eng.holdSpace

		case cmdGetAppend:
			if len(eng.patternSpace)+1+len(eng.holdSpace) > MaxSpaceBytes {
				return actionContinue, errors.New("pattern space exceeded size limit")
			}
			eng.patternSpace += "\n" + eng.holdSpace

		case cmdExchange:
			eng.patternSpace, eng.holdSpace = eng.holdSpace, eng.patternSpace

		case cmdBranch:
			return eng.branchTo(ctx, cmd.label, lr, depth)

		case cmdBranchIfSub:
			if eng.subMade {
				eng.subMade = false
				return eng.branchTo(ctx, cmd.label, lr, depth)
			}

		case cmdBranchIfNoSub:
			if !eng.subMade {
				return eng.branchTo(ctx, cmd.label, lr, depth)
			}

		case cmdGroup:
			action, err := eng.execCmds(ctx, cmd.children, 0, lr, depth)
			if err != nil || action != actionContinue {
				return action, err
			}

		case cmdNoop, cmdLabel:
			// Do nothing.
		}
	}

	return actionContinue, nil
}

func findLabel(cmds []*sedCmd, label string) int {
	for i, cmd := range cmds {
		if cmd.kind == cmdLabel && cmd.label == label {
			return i
		}
		if cmd.kind == cmdGroup {
			// Labels inside groups are visible from the top level in GNU sed.
			if idx := findLabel(cmd.children, label); idx >= 0 {
				// Return the group's index since we can't index into children from here.
				return i
			}
		}
	}
	return -1
}

// branchTo resolves a label and continues execution from the command after it.
// An empty label branches to end of script (returns actionContinue).
func (eng *engine) branchTo(ctx context.Context, label string, lr *lineReader, depth int) (actionType, error) {
	if label == "" {
		return actionContinue, nil
	}
	target := findLabel(eng.prog, label)
	if target < 0 {
		return actionContinue, errors.New("undefined label '" + label + "'")
	}
	return eng.execCmds(ctx, eng.prog, target+1, lr, depth+1)
}

// addressMatch checks whether the current line matches the command's address.
func (eng *engine) addressMatch(cmd *sedCmd) bool {
	match := eng.rawAddressMatch(cmd)
	if cmd.negated {
		return !match
	}
	return match
}

func (eng *engine) rawAddressMatch(cmd *sedCmd) bool {
	if cmd.addr1 == nil {
		return true // no address means match all
	}

	if cmd.addr2 == nil {
		// Single address.
		return eng.matchAddr(cmd.addr1)
	}

	// Two-address range: match from addr1 to addr2 inclusive.
	// We use a simple approach: check if current line is >= addr1 and <= addr2.
	// For regex addresses, this is more complex. We use a stateful approach
	// via the command itself to track whether we're inside the range.
	return eng.matchRange(cmd)
}

func (eng *engine) matchAddr(addr *address) bool {
	switch addr.kind {
	case addrLine:
		return eng.lineNum == addr.line
	case addrLast:
		return eng.lastLine
	case addrRegexp:
		return addr.re.MatchString(eng.patternSpace)
	case addrStep:
		if addr.first == 0 {
			return eng.lineNum%addr.step == 0
		}
		return eng.lineNum >= addr.first && (eng.lineNum-addr.first)%addr.step == 0
	}
	return false
}

func (eng *engine) matchRange(cmd *sedCmd) bool {
	if cmd.inRange {
		// We're inside the range. Check if addr2 closes it.
		if eng.matchAddr(cmd.addr2) {
			cmd.inRange = false
			return true // addr2 line is still part of the range
		}
		return true
	}
	// Not in range — check if addr1 opens it.
	if eng.matchAddr(cmd.addr1) {
		// Check if addr2 also matches on the same line (degenerate range).
		if eng.matchAddr(cmd.addr2) {
			return true // one-line range, don't enter inRange state
		}
		cmd.inRange = true
		return true
	}
	return false
}

func (eng *engine) execSubstitute(cmd *sedCmd) error {
	var result string
	if cmd.subGlobal {
		result = cmd.subRe.ReplaceAllString(eng.patternSpace, expandReplacement(cmd.subReplacement))
	} else if cmd.subNth > 0 {
		count := 0
		result = cmd.subRe.ReplaceAllStringFunc(eng.patternSpace, func(match string) string {
			count++
			if count == cmd.subNth {
				return cmd.subRe.ReplaceAllString(match, expandReplacement(cmd.subReplacement))
			}
			return match
		})
	} else {
		loc := cmd.subRe.FindStringIndex(eng.patternSpace)
		if loc != nil {
			matched := eng.patternSpace[loc[0]:loc[1]]
			replacement := cmd.subRe.ReplaceAllString(matched, expandReplacement(cmd.subReplacement))
			result = eng.patternSpace[:loc[0]] + replacement + eng.patternSpace[loc[1]:]
		} else {
			return nil
		}
	}
	if result != eng.patternSpace {
		if len(result) > MaxSpaceBytes {
			return errors.New("pattern space exceeded size limit")
		}
		eng.subMade = true
		eng.patternSpace = result
		if cmd.subPrint {
			eng.callCtx.Outf("%s\n", eng.patternSpace)
		}
	}
	return nil
}

// expandReplacement converts sed replacement syntax to Go regexp replacement.
// In sed, & means the whole match. In Go regexp, that's ${0} or $0.
// Sed uses \1-\9 for groups, Go uses $1-$9.
func expandReplacement(repl string) string {
	var sb strings.Builder
	sb.Grow(len(repl))
	for i := 0; i < len(repl); i++ {
		ch := repl[i]
		if ch == '&' {
			sb.WriteString("${0}")
		} else if ch == '\\' && i+1 < len(repl) {
			next := repl[i+1]
			if next >= '1' && next <= '9' {
				sb.WriteByte('$')
				sb.WriteByte(next)
				i++
			} else if next == '&' {
				sb.WriteByte('&')
				i++
			} else if next == '\\' {
				sb.WriteByte('\\')
				i++
			} else if next == 'n' {
				sb.WriteByte('\n')
				i++
			} else if next == 't' {
				sb.WriteByte('\t')
				i++
			} else {
				sb.WriteByte('\\')
				sb.WriteByte(next)
				i++
			}
		} else {
			sb.WriteByte(ch)
		}
	}
	return sb.String()
}

func (eng *engine) transliterate(s string, from, to []rune) string {
	runes := []rune(s)
	for i, r := range runes {
		for j, fr := range from {
			if r == fr {
				runes[i] = to[j]
				break
			}
		}
	}
	return string(runes)
}

func (eng *engine) printUnambiguous() {
	// l command: print pattern space showing non-printing characters.
	var sb strings.Builder
	col := 0
	for _, r := range eng.patternSpace {
		var s string
		switch {
		case r == '\\':
			s = "\\\\"
		case r == '\a':
			s = "\\a"
		case r == '\b':
			s = "\\b"
		case r == '\f':
			s = "\\f"
		case r == '\r':
			s = "\\r"
		case r == '\t':
			s = "\\t"
		case r == '\n':
			s = "\\n"
		case r < 32 || r == 127:
			s = fmt.Sprintf("\\%03o", r)
		default:
			s = string(r)
		}
		if col+len(s) >= 70 {
			sb.WriteString("\\\n")
			col = 0
		}
		sb.WriteString(s)
		col += len(s)
	}
	sb.WriteByte('$')
	sb.WriteByte('\n')
	eng.callCtx.Out(sb.String())
}

// isRegularFile checks whether an io.Reader is backed by a regular file.
func isRegularFile(r any) bool {
	type stater interface{ Stat() (os.FileInfo, error) }
	sf, ok := r.(stater)
	if !ok {
		return false
	}
	fi, err := sf.Stat()
	return err == nil && fi.Mode().IsRegular()
}
