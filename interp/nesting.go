// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"fmt"

	"mvdan.cc/sh/v3/syntax"
)

// MaxNestingDepth bounds how deeply a script may nest shell constructs
// (parentheses, command/parameter substitutions, brace groups, pipelines,
// logical lists, and compound commands such as if/while/for/case).
//
// The bound exists purely for safety, not for compatibility: the recursive
// descent parser in mvdan.cc/sh, rshell's own recursive AST passes
// ([validateNode] via [syntax.Walk]), and the recursive executor all consume
// Go stack frames per nesting level. A sufficiently deep script — still well
// under [MaxScriptBytes] — grows the goroutine stack past the Go runtime's
// 1 GiB limit, which aborts the process with an *uncatchable* "fatal error:
// stack overflow". That throw is not a panic, so the recover() guard in
// [Runner.Run] cannot intercept it, and it happens before any command, path,
// mode, or timeout policy is consulted.
//
// The pre-parse lexical guard ([checkScriptNestingDepth]) measures this in
// source bracket depth; the post-parse guard ([checkNestingDepth]) measures it
// in AST node depth, which is a small constant multiple of the source nesting
// (each nested construct expands to a few AST levels). 5000 is far below the
// depth at which any of those recursions overflows (empirically the parser
// alone tolerates tens of thousands of bracket levels, and the crash is at
// hundreds of thousands) yet comfortably above the nesting depth of any
// realistic script — including long pipelines — so legitimate input is never
// rejected while the crash is prevented.
const MaxNestingDepth = 5000

// errNestingTooDeep is the error reported when a script exceeds
// [MaxNestingDepth]. It is shared by the pre-parse lexical guard and the
// post-parse AST guard so callers see a consistent message.
func errNestingTooDeep() error {
	return fmt.Errorf("script nesting exceeds maximum depth of %d; simplify deeply nested constructs", MaxNestingDepth)
}

// Frame kinds tracked by the lexical pre-scan's context stack.
type nestKind uint8

const (
	kindParen    nestKind = iota // ( ... )  — subshell, $( ), arithmetic grouping
	kindBrace                    // { ... }  — brace group, ${ }
	kindBacktick                 // ` ... `
	kindIf                       // if ... (closed by fi)
	kindElif                     // elif ... (part of an if ladder, closed by fi)
	kindLoop                     // while/until/for/select ... (closed by done)
	kindCase                     // case ... (closed by esac)
	kindSingle                   // ' ... '  (lexical only, not a nesting level)
	kindDouble                   // " ... "  (lexical only, not a nesting level)
)

// checkScriptNestingDepth is a cheap lexical pre-scan run by [ParseScript]
// before the script text is handed to the recursive-descent parser. Its sole
// job is to stop the parser itself from overflowing the stack on deeply nested
// constructs — subshells and substitutions "(" / "$(" / "${" / "$((",
// backquotes, brace groups "{", and the compound keyword commands if/elif,
// while/until/for/select, and case — each of which the parser descends into
// once per nesting level, before any AST (and therefore the post-parse
// [checkNestingDepth] guard) exists.
//
// It is a small quoting-aware state machine over a context stack: the top
// frame determines whether the scanner is in command context (where brackets
// and reserved words open nested constructs), inside single quotes (fully
// literal), or inside double quotes (where only "$(", "${" and backquotes open
// a nested command context — so ordinary words like for/do/if appearing in a
// quoted string are not miscounted). A command substitution opened inside
// double quotes pushes a command-context frame, so reserved words nested
// within it are still counted.
//
// Pipelines and &&/|| lists are deliberately not counted: the parser builds
// those iteratively (no per-stage stack growth), so they cannot overflow it;
// their depth is bounded after parsing by [checkNestingDepth] instead.
//
// The scan is a conservative upper bound on the parser's recursion depth — it
// may count a construct the parser would treat as literal — so it can only
// ever reject slightly early, never let a crashing script through. Because the
// threshold is far above any real nesting, this has no practical effect on
// legitimate scripts.
func checkScriptNestingDepth(script string) error {
	stack := make([]nestKind, 0, 64)
	depth := 0 // number of stacked frames that are genuine nesting levels

	top := func() (nestKind, bool) {
		if len(stack) == 0 {
			return 0, false
		}
		return stack[len(stack)-1], true
	}
	push := func(k nestKind) error {
		stack = append(stack, k)
		if k != kindSingle && k != kindDouble {
			depth++
			if depth > MaxNestingDepth {
				return errNestingTooDeep()
			}
		}
		return nil
	}
	pop := func() {
		k := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if k != kindSingle && k != kindDouble {
			depth--
		}
	}
	// mode reports the lexing context implied by the top frame.
	inSingle := func() bool { k, ok := top(); return ok && k == kindSingle }
	inDouble := func() bool { k, ok := top(); return ok && k == kindDouble }

	atWordStart := true
	isDelim := func(c byte) bool {
		switch c {
		case ' ', '\t', '\n', '\r', ';', '&', '|', '(', ')', '<', '>', '`':
			return true
		}
		return false
	}

	for i := 0; i < len(script); i++ {
		c := script[i]

		if inSingle() {
			if c == '\'' {
				pop()
			}
			continue
		}

		if inDouble() {
			switch c {
			case '\\':
				i++ // escape next byte
			case '"':
				pop()
			case '`':
				if err := push(kindBacktick); err != nil {
					return err
				}
			case '$':
				if i+1 < len(script) {
					switch script[i+1] {
					case '(':
						if err := push(kindParen); err != nil {
							return err
						}
						i++
					case '{':
						if err := push(kindBrace); err != nil {
							return err
						}
						i++
					}
				}
			}
			continue
		}

		// Command context (base frame or any command-context frame on top).
		switch c {
		case '\\':
			i++ // escape next byte
			atWordStart = false
		case '\'':
			if err := push(kindSingle); err != nil {
				return err
			}
			atWordStart = false
		case '"':
			if err := push(kindDouble); err != nil {
				return err
			}
			atWordStart = false
		case '`':
			if k, ok := top(); ok && k == kindBacktick {
				pop() // close the backquote
			} else if err := push(kindBacktick); err != nil {
				return err
			}
			atWordStart = false
		case '#':
			if atWordStart {
				for i < len(script) && script[i] != '\n' {
					i++
				}
				atWordStart = true
			} else {
				atWordStart = false
			}
		case '(':
			if err := push(kindParen); err != nil {
				return err
			}
			atWordStart = true
		case '{':
			if err := push(kindBrace); err != nil {
				return err
			}
			atWordStart = true
		case ')':
			if k, ok := top(); ok && k == kindParen {
				pop()
			}
			atWordStart = true
		case '}':
			if k, ok := top(); ok && k == kindBrace {
				pop()
			}
			atWordStart = true
		case ' ', '\t', '\n', '\r', ';', '&', '|':
			atWordStart = true
		default:
			if atWordStart && isASCIILetter(c) {
				// Read the maximal run of ASCII letters and decide whether it
				// is a standalone reserved word.
				j := i + 1
				for j < len(script) && isASCIILetter(script[j]) {
					j++
				}
				word := script[i:j]
				following := byte(' ')
				if j < len(script) {
					following = script[j]
				}
				if j == len(script) || isDelim(following) {
					if err := applyKeyword(word, &stack, push, pop); err != nil {
						return err
					}
				}
				i = j - 1 // advance past the letter run
			}
			atWordStart = false
		}
	}
	return nil
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// applyKeyword updates the context stack for a standalone reserved word. Only
// the compound-command keywords that make the parser descend a level are
// significant; other words (then, do, in, else, and any non-keyword) are
// ignored.
func applyKeyword(word string, stack *[]nestKind, push func(nestKind) error, pop func()) error {
	switch word {
	case "if":
		return push(kindIf)
	case "elif":
		return push(kindElif)
	case "while", "until", "for", "select":
		return push(kindLoop)
	case "case":
		return push(kindCase)
	case "fi":
		// Close an if ladder: pop any elif frames, then the matching if.
		for len(*stack) > 0 && (*stack)[len(*stack)-1] == kindElif {
			pop()
		}
		if len(*stack) > 0 && (*stack)[len(*stack)-1] == kindIf {
			pop()
		}
	case "done":
		if len(*stack) > 0 && (*stack)[len(*stack)-1] == kindLoop {
			pop()
		}
	case "esac":
		if len(*stack) > 0 && (*stack)[len(*stack)-1] == kindCase {
			pop()
		}
	}
	return nil
}

// checkNestingDepth bounds the structural depth of a parsed AST. It is called
// from [validateNode] — and therefore on every [Runner.Run] and [Runner.Check]
// path — before the recursive [syntax.Walk] validation pass and before any
// recursive execution, so that none of those recursions can exceed
// [MaxNestingDepth] stack frames.
//
// It is itself iterative: it never recurses deeper than one level (to read a
// node's direct children), so it cannot overflow the stack regardless of how
// deep the AST is. Constructs that the recursive-descent parser accepts
// without recursing per level — notably long pipelines (a|b|c…) and logical
// lists (a&&b&&c…), which build a left-nested BinaryCmd tree whose depth the
// parser does not pay for in stack but whose later walks and execution do —
// are bounded here even though the lexical pre-scan does not see them.
func checkNestingDepth(root syntax.Node) error {
	if root == nil {
		return nil
	}
	type item struct {
		node  syntax.Node
		depth int
	}
	stack := []item{{root, 0}}
	for len(stack) > 0 {
		it := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if it.depth > MaxNestingDepth {
			return errNestingTooDeep()
		}
		for _, child := range directChildren(it.node) {
			stack = append(stack, item{child, it.depth + 1})
		}
	}
	return nil
}

// directChildren returns the immediate child nodes of parent without
// descending further. It uses [syntax.Walk], whose visitor is invoked for the
// node itself first and then for each child; returning true only for parent
// and false for every child stops the walk at depth one, so this call recurses
// a single frame regardless of the overall tree depth.
func directChildren(parent syntax.Node) []syntax.Node {
	var children []syntax.Node
	syntax.Walk(parent, func(n syntax.Node) bool {
		if n == nil {
			return false
		}
		if n == parent {
			return true // descend into parent's direct children
		}
		children = append(children, n)
		return false // but no deeper
	})
	return children
}
