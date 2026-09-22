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
	callCtx       *builtins.CallContext
	prog          []*sedCmd
	labelMap      map[string]labelLocation // precomputed label locations for O(1) branch lookup
	suppressPrint bool
	lineNum       int64
	lastLine      bool
	patternSpace  string
	holdSpace     string
	// holdSpaceChomped mirrors patternSpaceChomped for the hold space: it
	// tracks whether the physical line currently occupying the hold
	// space's trailing edge was itself newline-terminated, so that
	// h/H/g/G/x (which move content between pattern and hold space) carry
	// the correct termination state along with the content they move,
	// rather than leaving whatever the destination's chomp state happened
	// to be beforehand. Starts true (GNU sed's line.chomped default before
	// any input is read — confirmed empirically: `sed -i 'g' file`, where
	// file's single unterminated line is replaced by the still-empty
	// initial hold space, still produces a properly newline-terminated
	// empty line, not an unterminated one). Only meaningful when
	// trackMissingNewline is set (i.e. only for -i).
	holdSpaceChomped bool
	appendQueue      []string       // text queued by 'a' command, flushed after auto-print
	appendQueueBytes int            // total bytes in appendQueue for limit checking
	subMade          bool           // set when s/// succeeds (cleared on new input line)
	lastRe           *regexp.Regexp // last regex used (for empty pattern in s///)
	emptyReErr       bool           // set when // address has no previous regex
	isRegularFile    bool
	isLastFile       bool // whether we are processing the last file in the argument list

	// Missing-final-newline tracking, used only by -i (trackMissingNewline).
	// The default streaming mode intentionally diverges from GNU sed here
	// (see MaxLineBytes doc / tests/scenarios/cmd/sed/edge/no_trailing_newline.yaml):
	// it always terminates output with \n for consistent AI-agent-facing
	// stdout, regardless of whether the input's last line had one. -i
	// cannot make that same trade-off: it writes back to a real file that
	// other tools read afterward, so silently appending a byte the input
	// never had is a correctness bug, not a formatting choice. When
	// trackMissingNewline is true, these fields replicate GNU sed's own
	// chomped/output_missing_newline mechanism (sed/execute.c) closely
	// enough to match it for the common cases (auto-print, p/P, n/N,
	// s///p): patternSpaceChomped records whether the physical input line
	// currently occupying the trailing edge of the pattern space was
	// itself newline-terminated, and pendingMissingNewline records that
	// the previous pattern-space print omitted its trailing newline and a
	// deferred one must be flushed before the next byte of any kind is
	// written, so two logical lines are never concatenated.
	trackMissingNewline     bool
	finalRecordUnterminated bool // true when the last physical line has no trailing \n
	patternSpaceChomped     bool // true unless the current pattern space's trailing line is finalRecordUnterminated
	pendingMissingNewline   bool
}

// flushPendingMissingNewline writes the deferred newline recorded by a prior
// unterminated pattern-space print, if any, before new output reaches the
// stream — mirroring GNU sed's output_missing_newline. A no-op whenever
// trackMissingNewline is false (the default streaming mode) or nothing is
// pending.
func (eng *engine) flushPendingMissingNewline() {
	if eng.pendingMissingNewline {
		eng.callCtx.Out("\n")
		eng.pendingMissingNewline = false
	}
}

// writeLine flushes any pending missing newline, writes s, and then either
// terminates it with \n or — only when trackMissingNewline is true and s is
// the unterminated final input line — defers the newline via
// pendingMissingNewline instead of writing it, matching GNU sed's handling
// of a source file with no trailing newline. chomped must be true for any
// output that is not a direct reflection of the current pattern space's
// trailing physical line (i.e. everything except the auto-print/p/P/n/N/
// s///p pattern-space prints), since generated text (a/i/c/=/l) always ends
// with its own newline regardless of the input's own termination.
func (eng *engine) writeLine(s string, chomped bool) {
	eng.flushPendingMissingNewline()
	eng.callCtx.Out(s)
	if !eng.trackMissingNewline || chomped {
		eng.callCtx.Out("\n")
		return
	}
	eng.pendingMissingNewline = true
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

// resetForNewFile clears the per-file stream state — line numbering, the
// current line's addressing state, any in-progress two-address ranges, and
// the hold space — so that a subsequent file is processed as an independent
// stream, matching GNU sed's -s/--separate semantics (used unconditionally
// by -i, since editing multiple files in place always treats each one
// separately). This mirrors GNU sed's own read_pattern_space, which clears
// hold.length alongside the line number and address-range state whenever
// reset_at_next_file fires (sed/execute.c) — confirmed empirically against
// GNU sed 4.9: `sed -s '/keepme/h; $G' a.txt b.txt` does NOT carry a.txt's
// hold-space value into b.txt.
//
// The last-used regex (for an empty // address/s/// pattern) is deliberately
// left untouched: GNU sed's per-file reset does not clear it, and
// `sed -s '/foo/ s//bar/' a.txt b.txt` (each file containing just "foo")
// confirms the empty pattern in b.txt still reuses a.txt's last regex.
func (eng *engine) resetForNewFile() {
	eng.lineNum = 0
	eng.lastLine = false
	eng.patternSpace = ""
	eng.holdSpace = ""
	eng.holdSpaceChomped = true
	eng.subMade = false
	eng.appendQueue = eng.appendQueue[:0]
	eng.appendQueueBytes = 0
	eng.trackMissingNewline = false
	eng.finalRecordUnterminated = false
	eng.patternSpaceChomped = true
	eng.pendingMissingNewline = false
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

// processFileInPlace rewrites a single file for -i.
//
// Unlike the streaming default mode, the entire original file is read into
// memory up front (readAllBounded, capped at MaxInPlaceOutputBytes) rather
// than consumed incrementally by the scanner. This is deliberate, not just
// an implementation convenience: consuming the file incrementally (e.g. via
// io.TeeReader alongside the scanner) only captures the bytes the scanner
// actually read before returning, so a script that quits early via q/Q on a
// file larger than the scanner's read-ahead would capture only a prefix of
// the original — silently corrupting the restore-on-failure path below.
// Reading the whole file up front guarantees the in-memory original is
// always complete before any destructive write is attempted, regardless of
// where the script stops.
//
// Every write that would normally reach the real stdout is captured into a
// second bounded in-memory buffer instead (via a shallow CallContext copy
// with Stdout swapped out). Output is only written back to the file — via
// writeBack — once processing completes without an unrecoverable error.
// This sandbox has no atomic rename/replace primitive, so the write-back
// cannot be a single atomic filesystem operation the way GNU sed's real
// temp-file-then-rename strategy is; see writeBack and
// Sandbox.WriteRegularFile for how the destructive write, its target-type
// validation, and its failure recovery are kept race-free despite that.
//
// A q/Q command still commits the file: GNU sed's -i writes out everything
// produced up to the quit point and only then stops processing later files,
// so the caller must still treat *quitError as "commit, then stop", not
// "discard". Any other error leaves the file unmodified (or restored, per
// writeBack), matching GNU sed's behaviour of not replacing the original on
// a hard failure.
func (eng *engine) processFileInPlace(ctx context.Context, callCtx *builtins.CallContext, file string) error {
	eng.resetForNewFile()

	// readHandle is kept open across the whole read-process-write sequence
	// (see readAllBounded's doc) and is only closed once write-back has
	// fully finished, successfully or not.
	original, identity, readHandle, err := readAllBounded(ctx, callCtx, file, MaxInPlaceOutputBytes)
	if err != nil {
		return err
	}
	defer readHandle.Close()

	// The read is already fully bounded by readAllBounded above, so treat
	// the in-memory reader as a regular-file source: lr.checkLimit's
	// separate MaxTotalReadBytes cap exists for non-regular streaming
	// sources (FIFOs, /dev/zero) that readAllBounded never sees here.
	eng.isRegularFile = true

	// Enable missing-final-newline tracking for this file: unlike the
	// streaming mode's intentional divergence (see the engine struct's
	// doc), -i must reproduce GNU sed's on-disk byte-for-byte, including a
	// source file whose last line was never newline-terminated.
	eng.trackMissingNewline = true
	eng.finalRecordUnterminated = len(original) > 0 && original[len(original)-1] != '\n'

	out := &boundedBuffer{maxBytes: MaxInPlaceOutputBytes}
	bufferedCtx := *callCtx
	bufferedCtx.Stdout = out
	eng.callCtx = &bufferedCtx
	defer func() { eng.callCtx = callCtx }()

	processErr := eng.processReader(ctx, bytes.NewReader(original), true)

	var qe *quitError
	isQuit := errors.As(processErr, &qe)
	if processErr != nil && !isQuit {
		return processErr
	}
	if out.overflow {
		return fmt.Errorf("rewritten output exceeded %d bytes", MaxInPlaceOutputBytes)
	}

	// readHandle is still open here (its Close is deferred above), so the
	// original file's inode cannot have been recycled by an unlink+create
	// at the same path since the read — see readAllBounded's doc for why
	// that matters to the identity check writeBack performs.
	if werr := eng.writeBack(ctx, callCtx, file, out.buf.Bytes(), original, identity); werr != nil {
		return werr
	}

	// Surface the quit request to the caller so it stops processing any
	// remaining files, after the write-back above has already committed
	// this file's output.
	return processErr
}

// readAllBounded opens file through callCtx.OpenRegularFile — which opens
// non-blocking, verifies handle identity, and rejects special files and
// descriptor portals (FIFOs, /dev/zero, /dev/fd/N) — rather than the plain
// callCtx.OpenFile this function used before: OpenFile alone would let a
// FIFO target poll indefinitely (bounded only by the execution timeout) or
// force a full MaxInPlaceOutputBytes read from an infinite device before
// this function's own size check could reject it. It then reads the
// entirety of file into memory, refusing anything larger than maxBytes
// rather than allocating an unbounded amount. It reads maxBytes+1 bytes at
// most — via io.LimitReader — so a source that somehow still kept producing
// data could not make this read run unbounded either; the extra byte is
// only used to distinguish "exactly maxBytes" from "more than maxBytes"
// without reading further.
//
// Unlike a typical read helper, the opened handle is returned to the caller
// instead of being closed here, alongside the fs.FileInfo of that exact
// descriptor for use as writeBack/WriteRegularFile's identity pin. The
// caller (processFileInPlace) must keep it open for as long as the pinned
// identity needs to remain trustworthy — that is, through the entire
// write-back sequence, only closing it once writeBack has returned. This
// matters because os.SameFile compares by device+inode: if this function
// closed the descriptor itself, another process could unlink the original
// file and a new file created at the same path could be assigned the exact
// same, now-recycled inode number, and os.SameFile would then wrongly
// accept that unrelated new file as "the same file" at write-back time. An
// open file descriptor is what keeps the kernel from recycling the inode in
// the first place (unlink only removes the directory entry; the inode and
// its data persist as long as any descriptor or link remains), so holding
// this one open across the gap is what makes the later identity check
// meaningful rather than just plausible.
func readAllBounded(ctx context.Context, callCtx *builtins.CallContext, file string, maxBytes int) ([]byte, os.FileInfo, io.Closer, error) {
	f, err := callCtx.OpenRegularFile(ctx, file)
	if err != nil {
		return nil, nil, nil, err
	}

	sf, ok := f.(statCloser)
	if !ok {
		f.Close()
		return nil, nil, nil, fmt.Errorf("%s: cannot verify file identity for in-place edit", file)
	}
	info, err := sf.Stat()
	if err != nil {
		f.Close()
		return nil, nil, nil, err
	}

	data, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)+1))
	if err != nil {
		f.Close()
		return nil, nil, nil, err
	}
	if len(data) > maxBytes {
		f.Close()
		return nil, nil, nil, fmt.Errorf("file too large to edit in place safely (original content exceeded %d bytes)", maxBytes)
	}
	return data, info, f, nil
}

// writeBack commits newContent to file, restoring originalContent on a
// failed write so a transient error (e.g. disk full) does not leave the file
// empty or partially rewritten. expectedIdentity is the fs.FileInfo captured
// by readAllBounded from the exact descriptor that produced originalContent;
// both the primary write and the restore attempt pass it through so
// callCtx.WriteRegularFile can reject a target that was swapped for a
// different file (of the same, otherwise-acceptable regular type) at any
// point since the read — closing the identity gap that the single-fd
// type-check/write sequence inside WriteRegularFile does not, by itself,
// address: that sequence proves the descriptor it opens *at write time* is
// a regular file, but nothing before this pin proved it is the *same*
// regular file the original content came from.
//
// The actual write goes through Sandbox.WriteRegularFile (via
// callCtx.WriteRegularFile), which validates the target is (and remains) a
// regular file and performs the destructive write against a single file
// descriptor — open, fstat, write, truncate — so nothing can be swapped in
// between the type check and the write the way a separate Stat-then-Open
// sequence would allow. If that call fails, a second call attempts to
// restore originalContent through the same primitive (so the restore
// attempt gets the same type-check *and* identity protection as the
// original write); a failed restore is reported alongside the original
// error rather than silently swallowed, since at that point the file's
// on-disk state is genuinely unknown and the caller needs both facts to
// decide how to recover.
func (eng *engine) writeBack(ctx context.Context, callCtx *builtins.CallContext, file string, newContent, originalContent []byte, expectedIdentity os.FileInfo) error {
	// callCtx.WriteRegularFile (backed by Sandbox.WriteRegularFile) is fully
	// synchronous and does not itself watch ctx: once started, a write of up
	// to MaxInPlaceOutputBytes (256 MiB) runs to completion regardless of
	// whether the run has already been cancelled or its deadline has
	// already passed. Check ctx.Err() here, immediately before starting
	// that write, so a run that is already done does not still begin a
	// large, uninterruptible mutation. This check applies only to the
	// primary (destructive) write, deliberately not to the restore attempt
	// below: once that primary write has actually started and possibly
	// partially mutated the file, the restore is cleanup for a mutation
	// already in flight, not a new discretionary write, so it must still be
	// attempted on a best-effort basis even if the context is cancelled by
	// the time the failure is observed — skipping it would leave the file
	// in the exact broken state writeBack exists to prevent.
	if err := ctx.Err(); err != nil {
		return err
	}

	werr := callCtx.WriteRegularFile(ctx, file, newContent, expectedIdentity)
	if werr == nil {
		return nil
	}

	rerr := callCtx.WriteRegularFile(ctx, file, originalContent, expectedIdentity)
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

	return eng.processReader(ctx, rc, isLastFile)
}

// processReader runs the sed script over every line produced by rc, which
// must already reflect eng.isRegularFile correctly (processFile sets it from
// the opened source; processFileInPlace hardcodes it to true since the
// in-memory reader over the already fully-read original file behaves like a
// regular file for MaxTotalReadBytes purposes — the size was already
// bounded by readAllBounded before processReader ever runs).
func (eng *engine) processReader(ctx context.Context, rc io.Reader, isLastFile bool) error {
	eng.isLastFile = isLastFile

	sc := bufio.NewScanner(rc)
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
		eng.patternSpaceChomped = eng.computeChomped(lr)

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

// computeChomped reports whether the line lr just delivered was terminated
// by \n in the original source, for missing-final-newline tracking (see the
// engine struct's doc). Always true unless trackMissingNewline is enabled
// and this is genuinely the last line of the last file with an unterminated
// final record — scanLinesPreserveCR only ever omits the newline for that
// one physical line, at EOF.
func (eng *engine) computeChomped(lr *lineReader) bool {
	if !eng.trackMissingNewline {
		return true
	}
	return !(lr.isLast() && eng.isLastFile && eng.finalRecordUnterminated)
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
			eng.writeLine(eng.patternSpace, eng.patternSpaceChomped)
		}
		// Flush queued 'a' text after auto-print (even if auto-print was suppressed or deleted).
		for _, text := range eng.appendQueue {
			eng.writeLine(text, true)
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
			eng.writeLine(eng.patternSpace, eng.patternSpaceChomped)

		case cmdDelete:
			return actionDelete, nil

		case cmdPrintFirstLine:
			if idx := strings.IndexByte(eng.patternSpace, '\n'); idx >= 0 {
				// The cut point is an embedded newline that originated from an
				// earlier N join, not the input's own final-line termination,
				// so this fragment is always fully terminated.
				eng.writeLine(eng.patternSpace[:idx], true)
			} else {
				eng.writeLine(eng.patternSpace, eng.patternSpaceChomped)
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
				eng.writeLine(eng.patternSpace, eng.patternSpaceChomped)
			}
			for _, text := range eng.appendQueue {
				eng.writeLine(text, true)
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
			eng.writeLine(cmd.text, true)

		case cmdChange:
			// For range addresses, only output text at the end of the range.
			if cmd.addr2 != nil && cmd.inRange {
				// Still inside the range — delete silently without output.
				return actionDelete, nil
			}
			eng.writeLine(cmd.text, true)
			return actionDelete, nil

		case cmdLineNum:
			eng.writeLine(fmt.Sprintf("%d", eng.lineNum), true)

		case cmdPrintUnambig:
			eng.printUnambiguous()

		case cmdNext:
			if !eng.suppressPrint {
				eng.writeLine(eng.patternSpace, eng.patternSpaceChomped)
			}
			for _, text := range eng.appendQueue {
				eng.writeLine(text, true)
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
				eng.patternSpaceChomped = eng.computeChomped(lr)
				eng.subMade = false // n loads a new input line; reset substitution state
			} else {
				// n already printed the pattern space; suppress auto-print.
				eng.lastLine = eng.isLastFile
				return actionDelete, nil
			}

		case cmdNextAppend:
			// Flush queued 'a' text before reading the next line (GNU sed behaviour).
			for _, text := range eng.appendQueue {
				eng.writeLine(text, true)
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
				// N's newly appended line defines the pattern space's new
				// trailing edge, so its own termination (not the
				// previously-held line's) now governs chomped state.
				eng.patternSpaceChomped = eng.computeChomped(lr)
			} else {
				if !eng.suppressPrint {
					eng.writeLine(eng.patternSpace, eng.patternSpaceChomped)
				}
				return actionDelete, nil
			}

		case cmdHoldCopy:
			eng.holdSpace = eng.patternSpace
			// h replaces the hold space's content wholesale with the
			// pattern space's, so the hold space's trailing edge is now
			// exactly the pattern space's trailing edge.
			eng.holdSpaceChomped = eng.patternSpaceChomped

		case cmdHoldAppend:
			if len(eng.holdSpace)+1+len(eng.patternSpace) > MaxSpaceBytes {
				return actionContinue, errors.New("hold space exceeded size limit")
			}
			eng.holdSpace += "\n" + eng.patternSpace
			// H appends the pattern space, so the hold space's new trailing
			// edge is the pattern space's, not whatever it was before.
			eng.holdSpaceChomped = eng.patternSpaceChomped

		case cmdGetCopy:
			eng.patternSpace = eng.holdSpace
			// g replaces the pattern space's content wholesale with the
			// hold space's, so the pattern space's trailing edge is now
			// exactly the hold space's trailing edge. Verified against real
			// GNU sed 4.9: `sed -i '1h;2g' file` (file's last line
			// unterminated) still produces a properly newline-terminated
			// final line, because line 2's pattern space is replaced by
			// line 1's (terminated) content via g.
			eng.patternSpaceChomped = eng.holdSpaceChomped

		case cmdGetAppend:
			if len(eng.patternSpace)+1+len(eng.holdSpace) > MaxSpaceBytes {
				return actionContinue, errors.New("pattern space exceeded size limit")
			}
			eng.patternSpace += "\n" + eng.holdSpace
			// G appends the hold space, so the pattern space's new trailing
			// edge is the hold space's, not whatever it was before.
			eng.patternSpaceChomped = eng.holdSpaceChomped

		case cmdExchange:
			eng.patternSpace, eng.holdSpace = eng.holdSpace, eng.patternSpace
			// x swaps the content wholesale, so the chomp state must swap
			// with it — each space's trailing edge is now what the other's
			// was.
			eng.patternSpaceChomped, eng.holdSpaceChomped = eng.holdSpaceChomped, eng.patternSpaceChomped

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
			// Substitution does not change whether the underlying input
			// line was itself newline-terminated, so patternSpaceChomped
			// from when the line was read still applies.
			eng.writeLine(eng.patternSpace, eng.patternSpaceChomped)
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
	// l's own trailing $ marker always terminates the record it produces,
	// regardless of whether the underlying pattern space's physical line
	// was itself newline-terminated.
	eng.flushPendingMissingNewline()
	eng.callCtx.Out(sb.String())
	eng.callCtx.Out("\n")
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

// statCloser is implemented by the concrete types callCtx.OpenFile actually
// returns (e.g. *os.File, or a context-cancellation wrapper around one),
// even though the interface it is declared to return (io.ReadWriteCloser)
// does not itself expose Stat.
type statCloser interface {
	Stat() (os.FileInfo, error)
}

// isRegularFile checks whether an io.Reader is backed by a regular file.
func isRegularFile(r any) bool {
	sf, ok := r.(statCloser)
	if !ok {
		return false
	}
	fi, err := sf.Stat()
	return err == nil && fi.Mode().IsRegular()
}
