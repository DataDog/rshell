// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package find

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// AST limits to prevent resource exhaustion.
const (
	maxExprDepth = 64
	maxExprNodes = 256
)

// exprKind identifies the type of expression node.
type exprKind int

const (
	exprName    exprKind = iota // -name pattern
	exprIName                   // -iname pattern
	exprPath                    // -path pattern
	exprIPath                   // -ipath pattern
	exprType                    // -type c
	exprSize                    // -size n[cwbkMG]
	exprEmpty                   // -empty
	exprNewer                   // -newer file
	exprMtime                   // -mtime n
	exprMmin                    // -mmin n
	exprPrint                   // -print
	exprPrint0                  // -print0
	exprPrune                   // -prune
	exprTrue                    // -true
	exprFalse                   // -false
	exprAnd                     // expr -a expr  or  expr expr (implicit)
	exprOr                      // expr -o expr
	exprNot                     // ! expr  or  -not expr
)

// sizeUnit holds a parsed -size predicate value.
type sizeUnit struct {
	n    int64 // magnitude (always positive)
	cmp  int   // -1 = less than, 0 = exact, +1 = greater than
	unit byte  // one of: c w b k M G (default 'b' if omitted)
}

// expr is a node in the find expression AST.
type expr struct {
	kind    exprKind
	strVal  string   // pattern for name/iname/path/ipath, type char, file path for newer
	sizeVal sizeUnit // for -size
	numVal  int64    // for -mtime, -mmin
	numCmp  int      // -1/0/+1 for numeric comparisons
	left    *expr    // for and/or
	right   *expr    // for and/or
	operand *expr    // for not
}

// isAction returns true if this expression is an output action.
func (e *expr) isAction() bool {
	return e.kind == exprPrint || e.kind == exprPrint0
}

// hasAction checks if any node in the expression tree is an action.
func hasAction(e *expr) bool {
	if e == nil {
		return false
	}
	if e.isAction() {
		return true
	}
	return hasAction(e.left) || hasAction(e.right) || hasAction(e.operand)
}

// parser is a recursive-descent parser for find expressions.
type parser struct {
	args     []string
	pos      int
	depth    int
	nodes    int
	maxDepth int // -1 = not specified
	minDepth int // -1 = not specified
}

// parseResult holds the output of parseExpression.
type parseResult struct {
	expr     *expr
	maxDepth int // -1 = not specified
	minDepth int // -1 = not specified
}

// blocked predicates that are forbidden for sandbox safety.
var blockedPredicates = map[string]string{
	"-exec":    "arbitrary command execution is blocked",
	"-execdir": "arbitrary command execution is blocked",
	"-delete":  "file deletion is blocked",
	"-ok":      "interactive execution is blocked",
	"-okdir":   "interactive execution is blocked",
	"-fls":     "file writes are blocked",
	"-fprint":  "file writes are blocked",
	"-fprint0": "file writes are blocked",
	"-fprintf": "file writes are blocked",
	"-regex":   "regular expressions are blocked (ReDoS risk)",
	"-iregex":  "regular expressions are blocked (ReDoS risk)",
}

// parseExpression parses the find expression from args, including
// -maxdepth/-mindepth which are integrated into the recursive-descent parser.
// This avoids the argument-stealing problem: each predicate's own argument
// consumption naturally prevents depth options from capturing tokens that
// belong to other predicates (e.g. "find . -name -maxdepth" correctly treats
// "-maxdepth" as the -name pattern, not as a depth option).
func parseExpression(args []string) (parseResult, error) {
	if len(args) == 0 {
		return parseResult{maxDepth: -1, minDepth: -1}, nil
	}

	p := &parser{args: args, maxDepth: -1, minDepth: -1}
	e, err := p.parseOr()
	if err != nil {
		return parseResult{}, err
	}
	if p.pos < len(p.args) {
		return parseResult{}, fmt.Errorf("find: unexpected argument '%s'", p.args[p.pos])
	}
	return parseResult{expr: e, maxDepth: p.maxDepth, minDepth: p.minDepth}, nil
}

func (p *parser) peek() string {
	if p.pos >= len(p.args) {
		return ""
	}
	return p.args[p.pos]
}

func (p *parser) advance() string {
	s := p.args[p.pos]
	p.pos++
	return s
}

func (p *parser) expect(s string) error {
	if p.pos >= len(p.args) {
		return fmt.Errorf("find: expected '%s'", s)
	}
	if p.args[p.pos] != s {
		return fmt.Errorf("find: expected '%s', got '%s'", s, p.args[p.pos])
	}
	p.pos++
	return nil
}

func (p *parser) addNode() error {
	p.nodes++
	if p.nodes > maxExprNodes {
		return errors.New("find: expression too complex (too many nodes)")
	}
	return nil
}

// parseOr handles: expr -o expr
func (p *parser) parseOr() (*expr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.peek() == "-o" || p.peek() == "-or" {
		p.advance()
		if err := p.addNode(); err != nil {
			return nil, err
		}
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &expr{kind: exprOr, left: left, right: right}
	}
	return left, nil
}

// parseAnd handles: expr -a expr  or  expr expr (implicit AND)
func (p *parser) parseAnd() (*expr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		tok := p.peek()
		if tok == "-a" || tok == "-and" {
			p.advance()
		} else if tok == "" || tok == "-o" || tok == "-or" || tok == ")" {
			break
		}
		if err := p.addNode(); err != nil {
			return nil, err
		}
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &expr{kind: exprAnd, left: left, right: right}
	}
	return left, nil
}

// parseUnary handles: ! expr  or  -not expr  or  ( expr )  or  primary
func (p *parser) parseUnary() (*expr, error) {
	tok := p.peek()
	if tok == "!" || tok == "-not" {
		p.advance()
		p.depth++
		if p.depth > maxExprDepth {
			return nil, errors.New("find: expression too deeply nested")
		}
		if err := p.addNode(); err != nil {
			return nil, err
		}
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		p.depth--
		return &expr{kind: exprNot, operand: operand}, nil
	}
	if tok == "(" {
		p.advance()
		if p.peek() == ")" {
			return nil, errors.New("find: invalid expression; empty parentheses are not allowed.")
		}
		p.depth++
		if p.depth > maxExprDepth {
			return nil, errors.New("find: expression too deeply nested")
		}
		e, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		p.depth--
		if err := p.expect(")"); err != nil {
			return nil, err
		}
		return e, nil
	}
	return p.parsePrimary()
}

// parsePrimary handles leaf predicates.
func (p *parser) parsePrimary() (*expr, error) {
	if p.pos >= len(p.args) {
		return nil, errors.New("find: expected expression")
	}

	if err := p.addNode(); err != nil {
		return nil, err
	}

	tok := p.advance()

	// Check blocked predicates.
	if reason, blocked := blockedPredicates[tok]; blocked {
		return nil, fmt.Errorf("find: %s: %s", tok, reason)
	}

	switch tok {
	case "-name":
		return p.parseStringPredicate(exprName)
	case "-iname":
		return p.parseStringPredicate(exprIName)
	case "-path", "-wholename":
		return p.parseStringPredicate(exprPath)
	case "-ipath", "-iwholename":
		return p.parseStringPredicate(exprIPath)
	case "-type":
		return p.parseTypePredicate()
	case "-size":
		return p.parseSizePredicate()
	case "-empty":
		return &expr{kind: exprEmpty}, nil
	case "-newer":
		return p.parseStringPredicate(exprNewer)
	case "-mtime":
		return p.parseNumericPredicate(exprMtime)
	case "-mmin":
		return p.parseNumericPredicate(exprMmin)
	case "-print":
		return &expr{kind: exprPrint}, nil
	case "-print0":
		return &expr{kind: exprPrint0}, nil
	case "-prune":
		return &expr{kind: exprPrune}, nil
	case "-maxdepth":
		return p.parseDepthOption(true)
	case "-mindepth":
		return p.parseDepthOption(false)
	case "-true":
		return &expr{kind: exprTrue}, nil
	case "-false":
		return &expr{kind: exprFalse}, nil
	default:
		return nil, fmt.Errorf("find: unknown predicate '%s'", tok)
	}
}

func (p *parser) parseStringPredicate(kind exprKind) (*expr, error) {
	if p.pos >= len(p.args) {
		return nil, fmt.Errorf("find: missing argument for %s", kindName(kind))
	}
	val := p.advance()
	return &expr{kind: kind, strVal: val}, nil
}

func (p *parser) parseTypePredicate() (*expr, error) {
	if p.pos >= len(p.args) {
		return nil, errors.New("find: missing argument for -type")
	}
	val := p.advance()
	if len(val) == 0 {
		return nil, errors.New("find: Unknown argument to -type: ")
	}
	// Validate type character(s). GNU find allows comma-separated types
	// like "f,d" but rejects malformed lists like ",", "f,", ",d", or "fd".
	expectType := true
	for i := 0; i < len(val); i++ {
		c := val[i]
		if c == ',' {
			if expectType {
				// Leading or consecutive comma.
				return nil, fmt.Errorf("find: Unknown argument to -type: %s", val)
			}
			expectType = true
			continue
		}
		switch c {
		case 'f', 'd', 'l', 'p', 's':
			if !expectType {
				// Adjacent type chars without comma (e.g. "fd").
				return nil, fmt.Errorf("find: Unknown argument to -type: %s", val)
			}
			expectType = false
		default:
			return nil, fmt.Errorf("find: Unknown argument to -type: %s", val)
		}
	}
	if expectType {
		// Trailing comma.
		return nil, fmt.Errorf("find: Unknown argument to -type: %s", val)
	}
	return &expr{kind: exprType, strVal: val}, nil
}

func (p *parser) parseSizePredicate() (*expr, error) {
	if p.pos >= len(p.args) {
		return nil, errors.New("find: missing argument for -size")
	}
	val := p.advance()
	su, err := parseSize(val)
	if err != nil {
		return nil, err
	}
	return &expr{kind: exprSize, sizeVal: su}, nil
}

func (p *parser) parseNumericPredicate(kind exprKind) (*expr, error) {
	if p.pos >= len(p.args) {
		return nil, fmt.Errorf("find: missing argument for %s", kindName(kind))
	}
	val := p.advance()
	cmp := 0
	numStr := val
	if strings.HasPrefix(numStr, "+") {
		cmp = 1
		numStr = numStr[1:]
	} else if strings.HasPrefix(numStr, "-") {
		cmp = -1
		numStr = numStr[1:]
	}
	n, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("find: invalid argument '%s' to %s", val, kindName(kind))
	}
	return &expr{kind: kind, numVal: n, numCmp: cmp}, nil
}

func (p *parser) parseDepthOption(isMax bool) (*expr, error) {
	name := "-mindepth"
	if isMax {
		name = "-maxdepth"
	}
	if p.pos >= len(p.args) {
		return nil, fmt.Errorf("find: missing argument to '%s'", name)
	}
	val := p.advance()
	// Reject non-decimal forms like "+1" or "-1" that strconv.Atoi accepts.
	// GNU find requires a positive decimal integer.
	if len(val) > 0 && (val[0] == '+' || val[0] == '-') {
		return nil, fmt.Errorf("find: invalid argument '%s' to %s", val, name)
	}
	n, err := strconv.Atoi(val)
	if err != nil || n < 0 {
		return nil, fmt.Errorf("find: invalid argument '%s' to %s", val, name)
	}
	if isMax {
		p.maxDepth = n
	} else {
		p.minDepth = n
	}
	return &expr{kind: exprTrue}, nil
}

// parseSize parses a -size argument like "+10k", "-5M", "100c".
func parseSize(s string) (sizeUnit, error) {
	if len(s) == 0 {
		return sizeUnit{}, errors.New("find: invalid argument '' to -size")
	}
	var su sizeUnit

	numStr := s
	if s[0] == '+' {
		su.cmp = 1
		numStr = s[1:]
	} else if s[0] == '-' {
		su.cmp = -1
		numStr = s[1:]
	}

	if len(numStr) == 0 {
		return sizeUnit{}, fmt.Errorf("find: invalid argument '%s' to -size", s)
	}

	// Check for unit suffix.
	su.unit = 'b' // default: 512-byte blocks
	last := numStr[len(numStr)-1]
	switch last {
	case 'c', 'w', 'b', 'k', 'M', 'G':
		su.unit = last
		numStr = numStr[:len(numStr)-1]
	}

	if len(numStr) == 0 {
		return sizeUnit{}, fmt.Errorf("find: invalid argument '%s' to -size", s)
	}

	n, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		return sizeUnit{}, fmt.Errorf("find: invalid argument '%s' to -size", s)
	}
	if n < 0 {
		return sizeUnit{}, fmt.Errorf("find: invalid argument '%s' to -size", s)
	}
	su.n = n
	return su, nil
}

func kindName(k exprKind) string {
	switch k {
	case exprName:
		return "-name"
	case exprIName:
		return "-iname"
	case exprPath:
		return "-path"
	case exprIPath:
		return "-ipath"
	case exprMtime:
		return "-mtime"
	case exprMmin:
		return "-mmin"
	case exprNewer:
		return "-newer"
	default:
		return "unknown"
	}
}
