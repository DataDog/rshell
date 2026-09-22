// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package sed

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/DataDog/rshell/builtins"
)

// engine holds the state for executing a sed script.
type engine struct {
	callCtx          *builtins.CallContext
	prog             []*sedCmd
	labelMap         map[string]labelLocation // precomputed label locations for O(1) branch lookup
	suppressPrint    bool
	lineNum          int64
	lastLine         bool
	patternSpace     string
	holdSpace        string
	appendQueue      []string       // text queued by 'a' command, flushed after auto-print
	appendQueueBytes int            // total bytes in appendQueue for limit checking
	subMade          bool           // set when s/// succeeds (cleared on new input line)
	lastRe           *regexp.Regexp // last regex used (for empty pattern in s///)
	emptyReErr       bool           // set when // address has no previous regex
	isRegularFile    bool
	isLastFile       bool // whether we are processing the last file in the argument list
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

// resetForNewFile clears the per-file stream state (line numbering, the
// current line's addressing state, and any in-progress two-address ranges)
// so that a subsequent file is processed as an independent stream, matching
// GNU sed's -s/--separate semantics (used unconditionally by -i, since
// editing multiple files in place always treats each one separately). Hold
// space and the last-used regex are deliberately left untouched: GNU sed
// documents that -s resets line numbers and $ per file but does not clear
// the hold space or the s///-reuse regex across files.
func (eng *engine) resetForNewFile() {
	eng.lineNum = 0
	eng.lastLine = false
	eng.patternSpace = ""
	eng.subMade = false
	eng.appendQueue = eng.appendQueue[:0]
	eng.appendQueueBytes = 0
	resetRangeState(eng.prog)
}

// resetRangeState clears the inRange flag on every two-address command in
// cmds (recursing into { ... } groups), so an address range that was open
// at the end of one file does not leak into the next.
func resetRangeState(cmds []*sedCmd) {
	for _, cmd := range cmds {
		cmd.inRange = false
		if cmd.kind == cmdGroup {
			resetRangeState(cmd.children)
		}
	}
}

// boundedBuffer is a bytes.Buffer that refuses writes once its content
// would exceed maxBytes, instead recording the overflow and discarding the
// write. Used to cap the in-memory size of an -i rewrite: the sandbox has no
// atomic rename/replace primitive, so the entire rewritten contents of a
// file must be buffered before being written back, and an unbounded sed
// script (e.g. a global substitution that expands every line) must not be
// allowed to grow that buffer without limit.
type boundedBuffer struct {
	buf      bytes.Buffer
	maxBytes int
	overflow bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.overflow {
		return len(p), nil
	}
	if b.buf.Len()+len(p) > b.maxBytes {
		b.overflow = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

// processFileInPlace rewrites a single file for -i: the file's own contents
// are read and processed exactly like processFile, except:
//
//  1. Every write that would normally reach the real stdout is captured into
//     an in-memory buffer instead (via a shallow CallContext copy with
//     Stdout swapped out).
//  2. The bytes read from the file are simultaneously captured into a second
//     bounded in-memory backup buffer (via io.TeeReader), so the original
//     content is still available after the read side has been fully
//     consumed and closed.
//
// Output is only written back to the file — by reopening it for writing —
// once processing of that file completes without an unrecoverable error.
// This sandbox has no atomic rename/replace primitive, so the write-back
// cannot be a single atomic filesystem operation the way GNU sed's real
// temp-file-then-rename strategy is. Instead, the destructive write is
// preceded by a fresh non-blocking regular-file check (checkRegularFile) so
// the reopen cannot block on a FIFO that was swapped in after the read side
// observed a regular file, and if the write itself fails partway (e.g.
// ENOSPC), the original bytes captured by the tee above are written back as
// a best-effort restore so a transient write failure does not leave the file
// empty or truncated. See writeBack below for the exact sequencing and its
// residual limits.
//
// A q/Q command still commits the file: GNU sed's -i writes out everything
// produced up to the quit point and only then stops processing later files,
// so the caller must still treat *quitError as "commit, then stop", not
// "discard". Any other error leaves the file unmodified (or restored, per
// writeBack), matching GNU sed's behaviour of not replacing the original on
// a hard failure.
func (eng *engine) processFileInPlace(ctx context.Context, callCtx *builtins.CallContext, file string) error {
	eng.resetForNewFile()

	out := &boundedBuffer{maxBytes: MaxInPlaceOutputBytes}
	backup := &boundedBuffer{maxBytes: MaxInPlaceOutputBytes}
	bufferedCtx := *callCtx
	bufferedCtx.Stdout = out
	eng.callCtx = &bufferedCtx
	defer func() { eng.callCtx = callCtx }()

	processErr := eng.processFileTee(ctx, callCtx, file, backup)

	var qe *quitError
	isQuit := errors.As(processErr, &qe)
	if processErr != nil && !isQuit {
		return processErr
	}
	if out.overflow {
		return fmt.Errorf("rewritten output exceeded %d bytes", MaxInPlaceOutputBytes)
	}
	if backup.overflow {
		// The original file's own content did not fit in the backup buffer,
		// so a failed write-back could not be safely restored. Refuse the
		// edit entirely rather than proceed without a recovery path; the
		// file has not been touched at this point.
		return fmt.Errorf("file too large to edit in place safely (original content exceeded %d bytes)", MaxInPlaceOutputBytes)
	}

	if werr := eng.writeBack(ctx, callCtx, file, out.buf.Bytes(), backup.buf.Bytes()); werr != nil {
		return werr
	}

	// Surface the quit request to the caller so it stops processing any
	// remaining files, after the write-back above has already committed
	// this file's output.
	return processErr
}

// checkRegularFile rejects file if it is not currently a regular file.
// Sandbox.Stat (which backs callCtx.StatFile) is openat-based and never
// blocks, unlike an O_WRONLY open of a FIFO with no attached reader, which
// blocks the shell indefinitely. This mirrors the exact check
// interp.rejectNonRegularRedirectTarget performs before opening a `>`/`>>`
// redirect target in remediation mode (see interp/runner_redir_remediation.go):
// there is a TOCTOU window between this Stat and the subsequent write-open,
// but it is not a sandbox-escape risk, since path containment for the write
// itself is still enforced atomically by the sandbox's openat walk — the
// check exists only to keep the write-open from ever blocking on a
// readerless FIFO, not to guarantee the target's type at the instant of open.
func checkRegularFile(ctx context.Context, callCtx *builtins.CallContext, file string) error {
	info, err := callCtx.StatFile(ctx, file)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s: not a regular file", file)
	}
	return nil
}

// writeBack commits newContent to file, restoring originalContent on a
// failed write so a transient error (e.g. disk full) does not leave the file
// empty or partially rewritten.
//
// Sequencing:
//  1. checkRegularFile re-verifies the target is a regular file immediately
//     before opening for writing (see checkRegularFile's doc for why this
//     specific check, and its accepted TOCTOU window, is needed here).
//  2. The file is opened O_WRONLY|O_TRUNC and newContent is written. This is
//     the point at which the original content is destroyed; everything
//     before this line is non-destructive.
//  3. If the write (or the file's Close) fails, a best-effort restore
//     re-opens the same descriptor's path O_WRONLY|O_TRUNC and writes back
//     originalContent. The restore write is exactly the same size as what
//     was just truncated away, so the same free space that accommodated the
//     original file before step 2 accommodates the restore, absent a
//     concurrent external writer competing for the same freed blocks.
//     Restore failure is reported alongside the original error rather than
//     silently swallowed, since at that point the file's on-disk state is
//     unknown and the caller needs both facts to decide how to recover.
func (eng *engine) writeBack(ctx context.Context, callCtx *builtins.CallContext, file string, newContent, originalContent []byte) error {
	if err := checkRegularFile(ctx, callCtx, file); err != nil {
		return err
	}

	wf, err := callCtx.OpenFile(ctx, file, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	_, werr := wf.Write(newContent)
	cerr := wf.Close()
	if werr == nil && cerr != nil {
		werr = cerr
	}
	if werr == nil {
		return nil
	}

	// The write (or its close) failed after the original content was
	// already truncated away. Attempt to restore it so the file is not left
	// empty or partially rewritten; report both errors if the restore also
	// fails, since the caller then genuinely cannot know the file's state
	// from the write error alone.
	rf, rerr := callCtx.OpenFile(ctx, file, os.O_WRONLY|os.O_TRUNC, 0)
	if rerr != nil {
		return fmt.Errorf("write failed (%w); restore also failed: %w", werr, rerr)
	}
	_, rerr = rf.Write(originalContent)
	if cerr2 := rf.Close(); rerr == nil {
		rerr = cerr2
	}
	if rerr != nil {
		return fmt.Errorf("write failed (%w); restore also failed: %w", werr, rerr)
	}
	return fmt.Errorf("write failed, original content restored: %w", werr)
}

// processFile reads a single file and runs the sed script on each line.
// isLastFile indicates whether this is the last file in the argument list;
// the $ address only matches when it is the last line of the last file
// (GNU sed treats multiple files as one continuous stream).
func (eng *engine) processFile(ctx context.Context, callCtx *builtins.CallContext, file string, isLastFile bool) error {
	return eng.processFileImpl(ctx, callCtx, file, isLastFile, nil)
}

// processFileTee behaves exactly like processFile, except that every byte
// read from the file (not stdin) is additionally copied to tee via
// io.TeeReader as it is consumed by the scanner. Used by processFileInPlace
// to capture the original file content for a possible restore, without a
// separate full read of the file. tee may be nil, in which case this is
// identical to processFile (isLastFile is always true for the -i caller, so
// that parameter is fixed at the processFile wrapper above instead of being
// threaded through here).
func (eng *engine) processFileTee(ctx context.Context, callCtx *builtins.CallContext, file string, tee io.Writer) error {
	return eng.processFileImpl(ctx, callCtx, file, true, tee)
}

func (eng *engine) processFileImpl(ctx context.Context, callCtx *builtins.CallContext, file string, isLastFile bool, tee io.Writer) error {
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

	eng.isLastFile = isLastFile

	var sc *bufio.Scanner
	if tee != nil {
		sc = bufio.NewScanner(io.TeeReader(rc, tee))
	} else {
		sc = bufio.NewScanner(rc)
	}
	buf := make([]byte, 4096)
	sc.Buffer(buf, MaxLineBytes)
	// Use a custom split function that only splits on \n (not \r\n).
	// The default bufio.ScanLines strips \r from \r\n endings, but GNU sed
	// preserves \r as part of the pattern space.
	sc.Split(scanLinesPreserveCR)

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
		eng.lastLine = lr.isLast() && isLastFile

		err := eng.runCycle(ctx, lr)
		if err != nil {
			return err
		}
	}

	if err := lr.sc.Err(); err != nil {
		return err
	}
	return nil
}

// runCycle executes the script for the current input line.
func (eng *engine) runCycle(ctx context.Context, lr *lineReader) error {
	eng.subMade = false
	eng.appendQueue = eng.appendQueue[:0]
	eng.appendQueueBytes = 0
	for {
		action, err := eng.execCommandsFrom(ctx, 0, lr, 0)
		if err != nil {
			return err
		}
		if action == actionRestart {
			// D command requested a cycle restart with the remaining
			// pattern space. subMade is intentionally preserved.
			eng.appendQueue = eng.appendQueue[:0]
			eng.appendQueueBytes = 0
			continue
		}
		if action == actionBranch {
			action = actionContinue
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
			if eng.emptyReErr {
				eng.emptyReErr = false
				return actionContinue, errors.New("no previous regular expression")
			}
			continue
		}
		if eng.emptyReErr {
			eng.emptyReErr = false
			return actionContinue, errors.New("no previous regular expression")
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
				// Note: subMade is intentionally preserved across D restarts
				// (GNU sed behaviour — t/T branching state survives D).
				eng.appendQueue = eng.appendQueue[:0]
				eng.appendQueueBytes = 0
				return actionRestart, nil
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
			eng.patternSpace = eng.transliterate(eng.patternSpace, cmd.transMap)

		case cmdAppend:
			eng.appendQueueBytes += len(cmd.text)
			if eng.appendQueueBytes > MaxAppendQueueBytes {
				return actionContinue, errors.New("append queue exceeded size limit")
			}
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
			eng.appendQueueBytes = 0
			line, ok := lr.readLine()
			if ok {
				if err := lr.checkLimit(); err != nil {
					return actionContinue, err
				}
				eng.lineNum++
				eng.patternSpace = line
				eng.lastLine = lr.isLast() && eng.isLastFile
				eng.subMade = false // n loads a new input line; reset substitution state
			} else {
				// n already printed the pattern space; suppress auto-print.
				eng.lastLine = eng.isLastFile
				return actionDelete, nil
			}

		case cmdNextAppend:
			// Flush queued 'a' text before reading the next line (GNU sed behaviour).
			for _, text := range eng.appendQueue {
				eng.callCtx.Outf("%s\n", text)
			}
			eng.appendQueue = eng.appendQueue[:0]
			eng.appendQueueBytes = 0
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
				eng.lastLine = lr.isLast() && eng.isLastFile
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

// buildLabelMap precomputes the location of every label in the program
// for O(1) branch resolution instead of linear scanning on every branch.
func buildLabelMap(cmds []*sedCmd) map[string]labelLocation {
	m := make(map[string]labelLocation)
	buildLabelMapRecursive(cmds, nil, m)
	return m
}

func buildLabelMapRecursive(cmds []*sedCmd, prefix []int, m map[string]labelLocation) {
	for i, cmd := range cmds {
		currentPath := append(append([]int{}, prefix...), i)
		if cmd.kind == cmdLabel && cmd.label != "" {
			// Last definition wins — GNU sed branches to the most recently
			// defined label when duplicates exist.
			m[cmd.label] = labelLocation{path: currentPath}
		}
		if cmd.kind == cmdGroup {
			buildLabelMapRecursive(cmd.children, currentPath, m)
		}
	}
}

// branchTo resolves a label and continues execution from the command after it.
// An empty label branches to end of script (returns actionBranch).
func (eng *engine) branchTo(ctx context.Context, label string, lr *lineReader, depth int) (actionType, error) {
	if label == "" {
		// Branch to end of script. Return actionBranch so that a branch
		// inside a group properly skips commands after the group.
		return actionBranch, nil
	}
	loc, ok := eng.labelMap[label]
	if !ok {
		// This should not happen — labels are validated at parse time.
		return actionContinue, errors.New("undefined label '" + label + "'")
	}
	action, err := eng.branchToPath(ctx, eng.prog, loc.path, lr, depth)
	if err != nil {
		return action, err
	}
	// Wrap actionContinue as actionBranch so callers (e.g. group execution)
	// know a non-local jump occurred and don't fall through.
	if action == actionContinue {
		return actionBranch, nil
	}
	return action, nil
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
		// Line 0 is special: it only makes sense as the start of a 0,/re/
		// range. It matches on line 1 (the range starts "before line 1")
		// so the regex addr2 can close on line 1. After line 1, it no
		// longer matches so the range doesn't reopen.
		if addr.line == 0 {
			return eng.lineNum == 1
		}
		return eng.lineNum == addr.line
	case addrLast:
		return eng.lastLine
	case addrRegexp:
		re := addr.re
		if re == nil {
			// Empty pattern: reuse last regex.
			if eng.lastRe == nil {
				// GNU sed fails with "no previous regular expression".
				// Store a sentinel so the caller can detect and report this.
				eng.emptyReErr = true
				return false
			}
			re = eng.lastRe
		} else {
			eng.lastRe = re // Record the most recently used regex.
		}
		return re.MatchString(eng.patternSpace)
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
		// Special case: addr1 is line 0 (the GNU 0,/re/ form).
		// Unlike normal ranges, check addr2 on the very first line so the
		// range can close immediately on line 1.
		addr1IsZero := cmd.addr1.kind == addrLine && cmd.addr1.line == 0

		// For regex addr2, GNU sed does not check it on the opening line —
		// the range always extends to at least the next line.
		// Exception: 0,/re/ DOES check addr2 on line 1.
		// For line-number/$ addr2, check immediately for degenerate range.
		if cmd.addr2.kind != addrRegexp || addr1IsZero {
			// Descending numeric range (e.g. 4,2): treat as one-line range.
			if cmd.addr2.kind == addrLine && cmd.addr2.line < eng.lineNum {
				return true // one-line range
			}
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
	// Note: case-insensitive flag (i/I) on empty regexp is rejected at parse
	// time, so we don't need to handle it here.
	re := cmd.subRe
	if re == nil {
		if eng.lastRe == nil {
			return errors.New("no previous regular expression")
		}
		re = eng.lastRe
	}
	eng.lastRe = re

	// Validate backreferences in the replacement against the number of
	// capture groups in the regex. GNU sed rejects invalid references.
	// Skip when cmd.subRe is non-nil — validation was already done at parse time.
	// Only needed for empty-pattern reuse (cmd.subRe == nil), since the
	// previous regex may have a different number of capture groups.
	if cmd.subRe == nil {
		if err := validateBackrefs(cmd.subReplacement, re.NumSubexp()); err != nil {
			return err
		}
	}

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

// validateBackrefs checks that all \N backreferences in the replacement string
// refer to capture groups that exist in the regex. GNU sed errors on invalid
// references like \1 when there are no capture groups.
func validateBackrefs(repl string, numGroups int) error {
	for i := 0; i < len(repl); i++ {
		if repl[i] == '\\' && i+1 < len(repl) {
			next := repl[i+1]
			if next >= '1' && next <= '9' {
				ref := int(next - '0')
				if ref > numGroups {
					return errors.New("invalid reference \\" + string(next) + " on `s' command's RHS")
				}
			}
			i++ // skip next
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
			if next == '0' {
				// \0 is equivalent to & (entire match) in GNU sed.
				sb.WriteString("${0}")
				i++
			} else if next >= '1' && next <= '9' {
				// Use braced form ${N} so that a following digit is not
				// swallowed by Go's regexp replacement parser.
				// e.g. sed's \10 means group-1 then literal '0', not group-10.
				sb.WriteString("${")
				sb.WriteByte(next)
				sb.WriteString("}")
				i++
			} else if next == '&' {
				sb.WriteByte('&')
				i++
			} else if next == '\\' {
				sb.WriteByte('\\')
				i++
			} else {
				// GNU sed drops the backslash for non-special escapes
				// (e.g. \q becomes q).
				sb.WriteByte(next)
				i++
			}
		} else {
			sb.WriteByte(ch)
		}
	}
	return sb.String()
}

func (eng *engine) transliterate(s string, mapping map[rune]rune) string {
	runes := []rune(s)
	for i, r := range runes {
		if replacement, ok := mapping[r]; ok {
			runes[i] = replacement
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

// scanLinesPreserveCR is like bufio.ScanLines but does NOT strip trailing \r
// from \r\n endings. GNU sed treats \r as an ordinary character that is part
// of the pattern space, so we must preserve it.
func scanLinesPreserveCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		// Return the line up to (but not including) the \n.
		return i + 1, data[:i], nil
	}
	// At EOF, deliver the last line without a trailing newline.
	if atEOF {
		return len(data), data, nil
	}
	// Request more data.
	return 0, nil, nil
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
