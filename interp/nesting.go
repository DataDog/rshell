// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"fmt"
	"strings"

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

// Frame kinds tracked by the lexical pre-scan's context stack. The top frame
// determines the scanner's mode; each frame also records whether it is a true
// nesting level (counted toward depth) or merely a lexical context.
type nestKind uint8

const (
	kindParen          nestKind = iota // ( ... ) / $( ... ) — counted
	kindParam                          // ${ ... } — counted
	kindBrace                          // { ... ; } command group — counted
	kindBacktick                       // ` ... ` — counted
	kindIf                             // if ... (closed by fi) — counted
	kindElif                           // elif ... (part of an if ladder) — counted
	kindLoop                           // while/until/for/select (closed by done) — counted
	kindCase                           // case ... (closed by esac) — counted
	kindSingle                         // ' ... ' — lexical only
	kindDouble                         // " ... " — lexical only
	kindHeredocExpand                  // unquoted heredoc body — lexical only
	kindHeredocLiteral                 // quoted heredoc body — lexical only
	kindNone           = 0xff          // sentinel for "empty stack"
)

// counted reports whether a frame kind is a real nesting level that the parser
// descends into (and therefore must be bounded), as opposed to a quoting or
// heredoc context that only changes how subsequent bytes are interpreted.
func (k nestKind) counted() bool {
	switch k {
	case kindSingle, kindDouble, kindHeredocExpand, kindHeredocLiteral:
		return false
	}
	return true
}

type heredocInfo struct {
	delim     string
	stripTabs bool // <<- form: strip leading tabs before comparing the delimiter
	expand    bool // unquoted delimiter: body still performs $(...)/${...}/`...` expansion
}

// checkScriptNestingDepth is a lexical pre-scan run by [ParseScript] before the
// script text is handed to the recursive-descent parser. Its job is to stop the
// parser itself from overflowing the stack on deeply nested constructs —
// subshells and substitutions "(" / "$(" / "${" / "$((", backquotes, brace
// command groups "{ … ; }", and the compound keyword commands if/elif,
// while/until/for/select, and case — each of which the parser descends into
// once per nesting level, before any AST (and therefore the post-parse
// [checkNestingDepth] guard) exists. Pipelines and &&/|| lists are not counted:
// the parser builds those iteratively, so they cannot overflow it; their depth
// is bounded after parsing by [checkNestingDepth].
//
// It is a quoting- and context-aware state machine over a stack of frames. The
// top frame selects the mode, so that bytes are only treated as structural
// syntax where the parser would treat them so:
//
//   - single quotes and quoted heredoc bodies are fully literal;
//   - double quotes and unquoted heredoc bodies are literal except for "$(",
//     "${" and backquotes, which still open a nested command context;
//   - reserved words (if/fi/while/done/case/esac/…) and brace command groups
//     ("{" / "}") are recognised only in command position, so a keyword- or
//     brace-looking *argument* (echo fi, echo }) is not mistaken for a closer,
//     and heredoc bodies cannot clear the tracked nesting.
//
// These contexts make the counter track the parser's own recursion, so the scan
// neither undercounts real nesting (which would let a crashing script through)
// nor overcounts literal text (which would reject a valid script). Unambiguous
// metacharacters — "(", "$(", "${", backquote — are always counted outside the
// literal contexts above; "<<" inside arithmetic is the one accepted
// imprecision (it is treated as a heredoc operator), which can only over-skip
// and never undercount.
func checkScriptNestingDepth(script string) error {
	kinds := make([]nestKind, 0, 64)
	heredocs := make([]heredocInfo, 0, 4) // parallel stack for heredoc-body frames
	var pending []heredocInfo             // heredocs declared on the current line, bodies not yet consumed
	depth := 0

	top := func() nestKind {
		if len(kinds) == 0 {
			return kindNone
		}
		return kinds[len(kinds)-1]
	}
	pushCounted := func(k nestKind) error {
		kinds = append(kinds, k)
		depth++
		if depth > MaxNestingDepth {
			return errNestingTooDeep()
		}
		return nil
	}
	pushLex := func(k nestKind) { kinds = append(kinds, k) }
	pushHeredoc := func(k nestKind, info heredocInfo) {
		kinds = append(kinds, k)
		heredocs = append(heredocs, info)
	}
	pop := func() {
		k := kinds[len(kinds)-1]
		kinds = kinds[:len(kinds)-1]
		if k == kindHeredocExpand || k == kindHeredocLiteral {
			heredocs = heredocs[:len(heredocs)-1]
		} else if k.counted() {
			depth--
		}
	}
	// startNextHeredoc begins the body of the next pending heredoc, if any and
	// if a heredoc body is not already open on top. Bodies are consumed in the
	// order declared.
	startNextHeredoc := func() {
		if len(pending) == 0 {
			return
		}
		if t := top(); t == kindHeredocExpand || t == kindHeredocLiteral {
			return
		}
		info := pending[0]
		pending = pending[1:]
		if info.expand {
			pushHeredoc(kindHeredocExpand, info)
		} else {
			pushHeredoc(kindHeredocLiteral, info)
		}
	}

	n := len(script)
	i := 0
	lineStart := true
	cmdPos := true // next word begins a command (so reserved words are recognised)

	for i < n {
		k := top()

		// Heredoc bodies and delimiter lines are handled at line starts.
		if lineStart && (k == kindHeredocExpand || k == kindHeredocLiteral) {
			eol := i
			for eol < n && script[eol] != '\n' {
				eol++
			}
			line := script[i:eol]
			info := heredocs[len(heredocs)-1]
			cmp := line
			if info.stripTabs {
				cmp = strings.TrimLeft(cmp, "\t")
			}
			if cmp == info.delim {
				pop()
				i = eol
				if i < n {
					i++ // consume the newline after the delimiter
				}
				lineStart = true
				startNextHeredoc()
				continue
			}
			if k == kindHeredocLiteral {
				// Entire body line is literal: skip it.
				i = eol
				if i < n {
					i++
				}
				lineStart = true
				continue
			}
			// Expanding body: fall through to per-byte handling of this line,
			// which treats it like a double-quoted context (only $(, ${ and
			// backquote open nested levels) until the newline.
		}
		lineStart = false

		switch k {
		case kindSingle:
			if script[i] == '\'' {
				pop()
			}
			i++
			continue
		case kindDouble, kindHeredocExpand:
			switch script[i] {
			case '\\':
				i++ // escape next byte
			case '"':
				if k == kindDouble {
					pop()
				}
			case '`':
				if err := pushCounted(kindBacktick); err != nil {
					return err
				}
				cmdPos = true
			case '$':
				if i+1 < n && script[i+1] == '(' {
					if err := pushCounted(kindParen); err != nil {
						return err
					}
					i += 2
					cmdPos = true
					continue
				}
				if i+1 < n && script[i+1] == '{' {
					if err := pushCounted(kindParam); err != nil {
						return err
					}
					i += 2
					continue
				}
			case '\n':
				lineStart = true
			}
			i++
			continue
		}

		// Command context (base, or a command/grouping frame on top).
		c := script[i]
		switch {
		case c == '\\':
			i += 2
			cmdPos = false
			continue
		case c == '\'':
			pushLex(kindSingle)
			cmdPos = false
		case c == '"':
			pushLex(kindDouble)
			cmdPos = false
		case c == '`':
			if top() == kindBacktick {
				pop()
			} else if err := pushCounted(kindBacktick); err != nil {
				return err
			}
			cmdPos = true
		case c == '#' && cmdPosOrSep(script, i):
			for i < n && script[i] != '\n' {
				i++
			}
			continue
		case c == '$':
			if i+1 < n && script[i+1] == '(' {
				if err := pushCounted(kindParen); err != nil {
					return err
				}
				i += 2
				cmdPos = true
				continue
			}
			if i+1 < n && script[i+1] == '{' {
				if err := pushCounted(kindParam); err != nil {
					return err
				}
				i += 2
				cmdPos = false
				continue
			}
			cmdPos = false
		case c == '(':
			// An empty "()" (optionally spaced) is a function-definition
			// parameter list — the body that follows is a command, so stay in
			// command position so that its "{ … ; }" group is counted. (An
			// empty subshell matches too; treating what follows as a command
			// is harmless.)
			j := i + 1
			for j < n && (script[j] == ' ' || script[j] == '\t') {
				j++
			}
			if j < n && script[j] == ')' {
				i = j + 1
				cmdPos = true
				continue
			}
			if err := pushCounted(kindParen); err != nil {
				return err
			}
			cmdPos = true
		case c == ')':
			if top() == kindParen {
				pop()
			}
			cmdPos = false
		case c == '{':
			if cmdPos && (i+1 >= n || isWordSep(script[i+1])) {
				if err := pushCounted(kindBrace); err != nil {
					return err
				}
				cmdPos = true
			} else {
				cmdPos = false
			}
		case c == '}':
			if top() == kindParam {
				pop()
			} else if cmdPos && top() == kindBrace {
				pop()
			}
			cmdPos = false
		case c == '<':
			i = scanRedirOrHeredoc(script, i, &pending)
			cmdPos = false
			continue
		case c == '>':
			i++
			if i < n && script[i] == '>' {
				i++
			}
			cmdPos = false
			continue
		case c == ';' || c == '&' || c == '|':
			i++
			cmdPos = true
			continue
		case c == '\n':
			i++
			cmdPos = true
			lineStart = true
			startNextHeredoc()
			continue
		case c == ' ' || c == '\t' || c == '\r':
			i++
			continue
		case isASCIILetter(c):
			j := i + 1
			for j < n && isASCIILetter(script[j]) {
				j++
			}
			word := script[i:j]
			standalone := j >= n || isWordSep(script[j])
			if cmdPos && standalone {
				if err := applyKeyword(word, &kinds, &cmdPos, pushCounted, pop); err != nil {
					return err
				}
			} else {
				cmdPos = false
			}
			i = j
			continue
		default:
			cmdPos = false
		}
		i++
	}
	return nil
}

func isASCIILetter(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isWordSep reports whether c terminates a shell word, so that a letter run
// bounded by separators can be recognised as a standalone reserved word.
func isWordSep(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', ';', '&', '|', '(', ')', '<', '>', '`':
		return true
	}
	return false
}

// cmdPosOrSep reports whether the '#' at index i begins a comment: true at the
// start of input or when preceded by an unquoted separator. Being conservative
// here only ever skips genuine comments; a '#' glued to a word (a#b, ${#x}) is
// not treated as a comment.
func cmdPosOrSep(script string, i int) bool {
	if i == 0 {
		return true
	}
	switch script[i-1] {
	case ' ', '\t', '\n', '\r', ';', '&', '|', '(':
		return true
	}
	return false
}

// scanRedirOrHeredoc handles a '<' at index i. It recognises "<<-"/"<<" heredoc
// operators (recording the body delimiter in pending so the following lines are
// treated as a heredoc body) and the "<<<" here-string and plain "<" redirects
// (which have no body). It returns the index just past what it consumed.
func scanRedirOrHeredoc(script string, i int, pending *[]heredocInfo) int {
	n := len(script)
	if i+1 >= n || script[i+1] != '<' {
		return i + 1 // plain "<" redirect
	}
	if i+2 < n && script[i+2] == '<' {
		return i + 3 // "<<<" here-string: no heredoc body
	}
	// Heredoc: "<<" or "<<-".
	j := i + 2
	stripTabs := false
	if j < n && script[j] == '-' {
		stripTabs = true
		j++
	}
	for j < n && (script[j] == ' ' || script[j] == '\t') {
		j++
	}
	delim, expand, end := readHeredocDelim(script, j)
	if delim != "" {
		*pending = append(*pending, heredocInfo{delim: delim, stripTabs: stripTabs, expand: expand})
	}
	return end
}

// readHeredocDelim reads a heredoc delimiter word starting at i, following the
// quoting that decides whether the body expands. A delimiter containing any
// quote or backslash makes the body literal (expand == false); a bare delimiter
// leaves $(...)/${...}/`...` active in the body. It returns the unquoted
// delimiter text, whether the body expands, and the index past the word.
func readHeredocDelim(script string, i int) (string, bool, int) {
	n := len(script)
	var b strings.Builder
	expand := true
	for i < n {
		c := script[i]
		switch c {
		case ' ', '\t', '\n', '\r', ';', '&', '|', '<', '>', '(', ')':
			return b.String(), expand, i
		case '\'':
			expand = false
			i++
			for i < n && script[i] != '\'' {
				b.WriteByte(script[i])
				i++
			}
			if i < n {
				i++ // closing quote
			}
		case '"':
			expand = false
			i++
			for i < n && script[i] != '"' {
				b.WriteByte(script[i])
				i++
			}
			if i < n {
				i++
			}
		case '\\':
			expand = false
			i++
			if i < n {
				b.WriteByte(script[i])
				i++
			}
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String(), expand, i
}

// applyKeyword updates the context stack for a standalone reserved word seen in
// command position. Compound-command openers push a counted frame; their
// closers pop the matching frame; continuation words (then/do/else/in) only
// restore command position for the word that follows. Anything else is an
// ordinary command name.
func applyKeyword(word string, stack *[]nestKind, cmdPos *bool, push func(nestKind) error, pop func()) error {
	topIs := func(k nestKind) bool { return len(*stack) > 0 && (*stack)[len(*stack)-1] == k }
	switch word {
	case "if":
		*cmdPos = true
		return push(kindIf)
	case "elif":
		*cmdPos = true
		return push(kindElif)
	case "while", "until":
		*cmdPos = true
		return push(kindLoop)
	case "for", "select":
		*cmdPos = false // followed by a loop variable name, not a command
		return push(kindLoop)
	case "case":
		*cmdPos = false // followed by the word being matched
		return push(kindCase)
	case "fi":
		// Close an if ladder: pop any elif frames, then the matching if.
		for topIs(kindElif) {
			pop()
		}
		if topIs(kindIf) {
			pop()
		}
		*cmdPos = false
	case "done":
		if topIs(kindLoop) {
			pop()
		}
		*cmdPos = false
	case "esac":
		if topIs(kindCase) {
			pop()
		}
		*cmdPos = false
	case "then", "do", "else", "in":
		*cmdPos = true
	default:
		*cmdPos = false // ordinary command name
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
