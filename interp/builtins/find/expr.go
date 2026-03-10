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
	args  []string
	pos   int
	depth int
	nodes int
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

// errorf creates an error with fmt.Sprintf formatting.
func errorf(format string, args ...any) error {
	return errors.New(fmt.Sprintf(format, args...))
}

// parseExpression parses the find expression from args. Returns nil if no
// expression is provided (meaning match everything).
func parseExpression(args []string) (*expr, error) {
	if len(args) == 0 {
		return nil, nil
	}

	p := &parser{args: args}
	e, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.args) {
		return nil, errorf("find: unexpected argument '%s'", p.args[p.pos])
	}
	return e, nil
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
		return errorf("find: expected '%s'", s)
	}
	if p.args[p.pos] != s {
		return errorf("find: expected '%s', got '%s'", s, p.args[p.pos])
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
		return nil, errorf("find: %s: %s", tok, reason)
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
	case "-true":
		return &expr{kind: exprTrue}, nil
	case "-false":
		return &expr{kind: exprFalse}, nil
	default:
		return nil, errorf("find: unknown predicate '%s'", tok)
	}
}

func (p *parser) parseStringPredicate(kind exprKind) (*expr, error) {
	if p.pos >= len(p.args) {
		return nil, errorf("find: missing argument for %s", kindName(kind))
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
	// Validate type character(s). GNU find allows comma-separated types.
	for i := 0; i < len(val); i++ {
		switch val[i] {
		case 'f', 'd', 'l', 'p', 's', ',':
		default:
			return nil, errorf("find: Unknown argument to -type: %s", val)
		}
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
		return nil, errorf("find: missing argument for %s", kindName(kind))
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
	n, err := strconv.Atoi(numStr)
	if err != nil {
		return nil, errorf("find: invalid argument '%s' to %s", val, kindName(kind))
	}
	return &expr{kind: kind, numVal: int64(n), numCmp: cmp}, nil
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
		return sizeUnit{}, errorf("find: invalid argument '%s' to -size", s)
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
		return sizeUnit{}, errorf("find: invalid argument '%s' to -size", s)
	}

	n, err := strconv.Atoi(numStr)
	if err != nil {
		return sizeUnit{}, errorf("find: invalid argument '%s' to -size", s)
	}
	if n < 0 {
		return sizeUnit{}, errorf("find: invalid argument '%s' to -size", s)
	}
	su.n = int64(n)
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
