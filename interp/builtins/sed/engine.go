// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package sed

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/DataDog/rshell/interp/builtins"
)

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
	subMade       bool           // set when s/// succeeds (cleared on new input line)
	lastRe        *regexp.Regexp // last regex used (for empty pattern in s///)
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
		// Add +1 to account for the newline delimiter stripped by Scanner.
		lr.totalRead += int64(len(lr.sc.Bytes())) + 1
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

// processFile reads a single file and runs the sed script on each line.
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
			for _, text := range eng.appendQueue {
				eng.callCtx.Outf("%s\n", text)
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
			// For range addresses, only output text at the end of the range.
			if cmd.addr2 != nil && cmd.inRange {
				// Still inside the range — delete silently without output.
				return actionDelete, nil
			}
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
				// n already printed the pattern space; suppress auto-print.
				eng.lastLine = true
				return actionDelete, nil
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
			eng.subMade = false

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

// labelLocation describes where a label was found as a path through the
// command tree. path[0] is the index in the top-level command list,
// path[1] is the index inside the first-level group's children, etc.
type labelLocation struct {
	path []int // indices at each nesting level; nil means not found
}

func (l labelLocation) found() bool { return l.path != nil }

func findLabel(cmds []*sedCmd, label string) labelLocation {
	return findLabelRecursive(cmds, label, nil)
}

func findLabelRecursive(cmds []*sedCmd, label string, prefix []int) labelLocation {
	for i, cmd := range cmds {
		currentPath := append(append([]int{}, prefix...), i)
		if cmd.kind == cmdLabel && cmd.label == label {
			return labelLocation{path: currentPath}
		}
		if cmd.kind == cmdGroup {
			if loc := findLabelRecursive(cmd.children, label, currentPath); loc.found() {
				return loc
			}
		}
	}
	return labelLocation{}
}

// branchTo resolves a label and continues execution from the command after it.
// An empty label branches to end of script (returns actionContinue).
func (eng *engine) branchTo(ctx context.Context, label string, lr *lineReader, depth int) (actionType, error) {
	if label == "" {
		return actionContinue, nil
	}
	loc := findLabel(eng.prog, label)
	if !loc.found() {
		return actionContinue, errors.New("undefined label '" + label + "'")
	}
	return eng.branchToPath(ctx, eng.prog, loc.path, lr, depth)
}

// branchToPath executes commands starting from the label described by path.
// path[0] is the index in cmds; if len(path) > 1, cmds[path[0]] is a group
// and we recurse into its children with path[1:].
func (eng *engine) branchToPath(ctx context.Context, cmds []*sedCmd, path []int, lr *lineReader, depth int) (actionType, error) {
	if len(path) == 1 {
		// Label is at this level — continue from path[0]+1.
		return eng.execCmds(ctx, cmds, path[0]+1, lr, depth+1)
	}
	// Label is inside a nested group at cmds[path[0]].
	group := cmds[path[0]]
	action, err := eng.branchToPath(ctx, group.children, path[1:], lr, depth)
	if err != nil || action != actionContinue {
		return action, err
	}
	// After the nested group finishes, continue with commands after it.
	return eng.execCmds(ctx, cmds, path[0]+1, lr, depth+1)
}

// --- Address matching ---

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
	return eng.matchRange(cmd)
}

func (eng *engine) matchAddr(addr *address) bool {
	switch addr.kind {
	case addrLine:
		return eng.lineNum == addr.line
	case addrLast:
		return eng.lastLine
	case addrRegexp:
		if addr.re.MatchString(eng.patternSpace) {
			eng.lastRe = addr.re
			return true
		}
		return false
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
		// For regex addr2, GNU sed does not check it on the opening line —
		// the range always extends to at least the next line.
		// For line-number/$ addr2, check immediately for degenerate range.
		if cmd.addr2.kind != addrRegexp {
			if eng.matchAddr(cmd.addr2) {
				return true // one-line range, don't enter inRange state
			}
		}
		cmd.inRange = true
		return true
	}
	return false
}

// --- Command implementations ---

func (eng *engine) execSubstitute(cmd *sedCmd) error {
	// Resolve the regex: nil means "reuse last regex".
	re := cmd.subRe
	if re == nil {
		if eng.lastRe == nil {
			return errors.New("no previous regular expression")
		}
		re = eng.lastRe
		if cmd.subCaseInsensitive {
			var err error
			re, err = regexp.Compile("(?i)" + re.String())
			if err != nil {
				return errors.New("invalid regex with case-insensitive flag: " + err.Error())
			}
		}
	}
	eng.lastRe = re

	var result string
	var matched bool
	if cmd.subGlobal && cmd.subNth > 0 {
		// Combined Nth + global: replace from the Nth match onward.
		count := 0
		expanded := expandReplacement(cmd.subReplacement)
		result = re.ReplaceAllStringFunc(eng.patternSpace, func(match string) string {
			count++
			if count >= cmd.subNth {
				matched = true
				return re.ReplaceAllString(match, expanded)
			}
			return match
		})
	} else if cmd.subGlobal {
		expanded := expandReplacement(cmd.subReplacement)
		matched = re.MatchString(eng.patternSpace)
		result = re.ReplaceAllString(eng.patternSpace, expanded)
	} else if cmd.subNth > 0 {
		count := 0
		expanded := expandReplacement(cmd.subReplacement)
		result = re.ReplaceAllStringFunc(eng.patternSpace, func(match string) string {
			count++
			if count == cmd.subNth {
				matched = true
				return re.ReplaceAllString(match, expanded)
			}
			return match
		})
	} else {
		loc := re.FindStringIndex(eng.patternSpace)
		if loc != nil {
			matched = true
			m := eng.patternSpace[loc[0]:loc[1]]
			replacement := re.ReplaceAllString(m, expandReplacement(cmd.subReplacement))
			result = eng.patternSpace[:loc[0]] + replacement + eng.patternSpace[loc[1]:]
		} else {
			return nil
		}
	}
	if matched {
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
		} else if ch == '$' {
			// Escape literal $ so Go's regexp engine doesn't interpret $1, $2, etc.
			sb.WriteString("$$")
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
			if r > 127 {
				// Output non-ASCII bytes as octal escapes like GNU sed.
				for _, b := range []byte(string(r)) {
					s += fmt.Sprintf("\\%03o", b)
				}
			} else {
				s = string(r)
			}
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
