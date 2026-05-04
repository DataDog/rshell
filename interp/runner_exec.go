// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/datadog-agent/pkg/fleet/installer/telemetry"
	"github.com/DataDog/rshell/allowedpaths"
	"github.com/DataDog/rshell/builtins"
)

func (r *Runner) stmt(ctx context.Context, st *syntax.Stmt) {
	if r.stop(ctx) {
		return
	}
	r.exit = exitStatus{}
	r.stmtSync(ctx, st)
	r.lastExit = r.exit
}

func (r *Runner) stmtSync(ctx context.Context, st *syntax.Stmt) {
	oldIn, oldOut, oldErr := r.stdin, r.stdout, r.stderr
	for _, rd := range st.Redirs {
		cls, err := r.redir(ctx, rd)
		if err != nil {
			r.exit.code = 1
			break
		}
		if cls != nil {
			defer cls.Close()
		}
	}
	if r.exit.ok() && st.Cmd != nil {
		r.cmd(ctx, st.Cmd)
	}
	if st.Negated && !r.exit.exiting {
		wasOk := r.exit.ok()
		r.exit = exitStatus{}
		r.exit.oneIf(wasOk)
	}
	r.stdin, r.stdout, r.stderr = oldIn, oldOut, oldErr
}

func (r *Runner) cmd(ctx context.Context, cm syntax.Command) {
	if r.stop(ctx) {
		return
	}

	switch cm := cm.(type) {
	case *syntax.Subshell:
		r2 := r.subshell(false)
		// A pipeline inside (…) should get its own pipeline span, so
		// clear the flag that suppresses nested pipeline spans.
		r2.inPipeline = false
		r2.stmts(ctx, cm.Stmts)
		r.exit = r2.exit
		r.exit.exiting = false
		r.totalCount += r2.totalCount
		r.dispatchedCount += r2.dispatchedCount
		r.unallowedCount += r2.unallowedCount
		r.unknownCount += r2.unknownCount
	case *syntax.Block:
		r.stmts(ctx, cm.Stmts)
	case *syntax.CallExpr:
		args := cm.Args
		r.lastExpandExit = exitStatus{}
		fields := r.fields(args...)
		if len(fields) == 0 {
			for _, as := range cm.Assigns {
				prev := r.lookupVar(as.Name.Value)
				prev.Local = false

				vr := r.assignVal(prev, as, "")
				r.setVarWithIndex(prev, as.Name.Value, as.Index, vr)
			}
			// If interpreting the last expansion like $(foo) failed,
			// and the expansion and assignments otherwise succeeded,
			// we need to surface that last exit code.
			if r.exit.ok() {
				r.exit = r.lastExpandExit
			}
			break
		}

		type restoreVar struct {
			name string
			vr   expand.Variable
		}
		var restores []restoreVar

		for _, as := range cm.Assigns {
			name := as.Name.Value
			prev := r.lookupVar(name)

			vr := r.assignVal(prev, as, "")
			// Inline command vars are always exported.
			vr.Exported = true

			restores = append(restores, restoreVar{name, prev})

			r.setVar(name, vr)
		}

		defer func() {
			for _, restore := range restores {
				r.setVarRestore(restore.name, restore.vr)
			}
		}()
		if r.exit.ok() {
			r.call(ctx, cm.Args[0].Pos(), fields)
		}
	case *syntax.BinaryCmd:
		switch cm.Op {
		case syntax.AndStmt, syntax.OrStmt:
			r.stmt(ctx, cm.X)
			if r.breakEnclosing > 0 || r.contnEnclosing > 0 || r.exit.exiting {
				break
			}
			if r.exit.ok() == (cm.Op == syntax.AndStmt) {
				r.stmt(ctx, cm.Y)
			}
		case syntax.Pipe:
			if !r.inPipeline {
				var span *telemetry.Span
				span, ctx = telemetry.StartSpanFromContext(ctx, "control_flow")
				span.SetResourceName("pipeline")
				span.SetTag("rshell.pipeline.stage_count", countPipelineStages(cm))
				defer func() {
					span.SetTag("rshell.pipeline.exit_code", int(r.exit.code))
					span.Finish(nil)
				}()
			}
			pr, pw, err := os.Pipe()
			if err != nil {
				r.exit.fatal(err) // not being able to create a pipe is rare but critical
				return
			}
			// Wrap stderr in a synchronized writer so both sides of the
			// pipe can write to it concurrently without a data race.
			safeStderr := &syncWriter{w: r.stderr}
			rLeft := r.subshell(true)
			// Allocate a shared pipeBroken flag for the producer pipeline
			// stage. Wrap the producer's stdout in a writer that flips this
			// flag when a write returns EPIPE; r.stop() then terminates the
			// producer (and any of its subshells, since the *bool is
			// inherited via subshell()).
			//
			// Without this, an unbounded producer
			// (e.g. `while true; do echo x; done | head`) keeps running
			// after the consumer closes its read end — bash terminates the
			// producer via SIGPIPE, but we are a single Go process and must
			// turn the broken-pipe error into a graceful exit signal.
			//
			// Save the inherited pipeBroken (from an outer pipeline stage, if
			// any) as parentPipeBroken before overwriting it. r.stop() checks
			// both so that a chained producer like `while true | cat | head`
			// also stops when the outer consumer closes — the while runner's
			// own pipeBroken is never set in that case because cat keeps reading
			// the inner pipe, but the outer pipeBroken (set when cat's write to
			// head fails) is visible through parentPipeBroken.
			pipeBroken := new(atomic.Bool)
			rLeft.parentPipeBroken = rLeft.pipeBroken
			rLeft.pipeBroken = pipeBroken
			rLeft.stdout = &pipeBrokenWriter{w: pw, flag: pipeBroken}
			rLeft.stderr = safeStderr
			rLeft.inPipeline = true
			rRight := r.subshell(true)
			rRight.stdin = pr
			rRight.stderr = safeStderr
			rRight.inPipeline = true
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer func() {
					if rec := recover(); rec != nil {
						panicOut := io.Writer(io.Discard)
						if rLeft.stderr != nil {
							panicOut = rLeft.stderr
						}
						func() {
							defer func() { recover() }()
							fmt.Fprintf(panicOut, "rshell: internal panic: %v\n", rec)
						}()
						rLeft.exit.fatal(fmt.Errorf("internal error"))
					}
					pw.Close()
					wg.Done()
				}()
				rLeft.stmt(ctx, cm.X)
				rLeft.exit.exiting = false
			}()
			rRight.stmt(ctx, cm.Y)
			r.exit = rRight.exit
			r.exit.exiting = false
			pr.Close()
			wg.Wait()
			// Roll each pipeline stage's per-run counters up to the
			// parent so the run-span totals reflect commands dispatched,
			// blocked, or unknown across every stage.
			r.totalCount += rLeft.totalCount + rRight.totalCount
			r.dispatchedCount += rLeft.dispatchedCount + rRight.dispatchedCount
			r.unallowedCount += rLeft.unallowedCount + rRight.unallowedCount
			r.unknownCount += rLeft.unknownCount + rRight.unknownCount
			if rLeft.exit.fatalExit {
				r.exit.fatal(rLeft.exit.err)
			}
		}
	case *syntax.IfClause:
		r.execIfChain(ctx, cm)
	case *syntax.ForClause:
		span, forCtx := telemetry.StartSpanFromContext(ctx, "control_flow")
		span.SetResourceName("for")
		iterationCount := 0
		brokeEarly := false
		var varName string
		defer func() {
			span.SetTag("rshell.for.variable_name", varName)
			span.SetTag("rshell.for.iteration_count", iterationCount)
			span.SetTag("rshell.for.broke_early", brokeEarly)
			span.Finish(nil)
		}()
		switch y := cm.Loop.(type) {
		case *syntax.WordIter:
			varName = y.Name.Value
			items := r.Params // for i; do ...

			inToken := y.InPos.IsValid()
			if inToken {
				items = r.fields(y.Items...) // for i in ...; do ...
			}

			for _, field := range items {
				if err := forCtx.Err(); err != nil {
					r.exit.fatal(err)
					break
				}
				r.setVarString(varName, field)
				iterSpan, iterCtx := telemetry.StartSpanFromContext(forCtx, "control_flow")
				iterSpan.SetResourceName("for.iteration")
				iterSpan.SetTag("rshell.for.iteration.index", iterationCount)
				broken := r.loopStmtsBroken(iterCtx, cm.Do)
				iterSpan.Finish(nil)
				iterationCount++
				if broken {
					// Excess continue at outermost loop: clamp and keep iterating
					// (bash treats "continue 99" in a single loop like "continue 1").
					if r.contnEnclosing > 0 && !r.inLoop {
						r.contnEnclosing = 0
						continue
					}
					brokeEarly = true
					break
				}
			}
			// Clamp excess break/continue levels at the outermost loop.
			// Bash discards excess levels (e.g. "break 99" with 1 loop).
			if !r.inLoop {
				r.breakEnclosing = 0
				r.contnEnclosing = 0
			}
		default:
			r.exit.fatal(fmt.Errorf("unsupported loop type: %T", cm.Loop))
		}
	case *syntax.WhileClause:
		r.execWhileClause(ctx, cm)
	default:
		r.exit.fatal(fmt.Errorf("unsupported command node: %T", cm))
	}
}

// execWhileClause runs a while or until loop. Both share the same AST node;
// cm.Until inverts the condition's exit-status check (`while` runs the body
// while the condition's last command exits 0; `until` runs the body while it
// exits non-zero).
//
// Per POSIX 1003.1-2024 §2.9.4.1/§2.9.4.2, the loop's exit status is the exit
// status of the last command of the last body iteration, or 0 if the body
// never executed.
//
// Termination is bounded by the runner's existing safety machinery: every
// r.stmt() invocation calls r.stop(ctx), which short-circuits on shell exit
// or context cancellation. We also explicitly check loopCtx.Err() at the top
// of each iteration so a cancelled context exits the loop with a fatal error
// even if the body and cond happen to be fast no-ops.
//
// Per-iteration spans are deliberately omitted (unlike for `for`): while/until
// can iterate unboundedly and emitting one span per iteration would be a
// memory cliff on long-running loops. Iteration count and broke-early are
// reported on the outer span instead.
func (r *Runner) execWhileClause(ctx context.Context, cm *syntax.WhileClause) {
	kind := "while"
	if cm.Until {
		kind = "until"
	}
	// Resource name encodes the loop kind (while/until); no separate kind tag
	// is needed.
	span, loopCtx := telemetry.StartSpanFromContext(ctx, "control_flow")
	span.SetResourceName(kind)
	iterationCount := 0
	brokeEarly := false
	defer func() {
		span.SetTag("rshell.loop.iteration_count", iterationCount)
		span.SetTag("rshell.loop.broke_early", brokeEarly)
		span.Finish(nil)
	}()

	// The condition list is part of the loop's lexical scope, so `break` and
	// `continue` invoked inside it are valid (they should not error out as
	// "only useful in a loop"). Mark the runner as in-loop for the duration
	// of the entire while/until evaluation; loopStmtsBroken redundantly re-
	// sets this flag for the body, which is harmless.
	oldInLoop := r.inLoop
	r.inLoop = true
	defer func() { r.inLoop = oldInLoop }()

	// ranBody tracks whether the body executed at least once, independently
	// of iterationCount (which is informational/telemetry). Using a bool
	// here avoids a theoretical wraparound: if iterationCount overflows int,
	// "iterationCount > 0" would silently flip false and the loop's exit
	// status would be reset to 0 instead of the last body's exit.
	ranBody := false
	var lastBody exitStatus
	for {
		if err := loopCtx.Err(); err != nil {
			r.exit.fatal(err)
			break
		}
		// Evaluate the condition list. Per POSIX, only the trailing exit
		// status decides whether to enter the body.
		r.stmts(loopCtx, cm.Cond)
		if r.exit.exiting || r.exit.fatalExit {
			break
		}

		// break/continue inside the condition list: consume one nesting level
		// for this loop, then either restart cond (continue targeting this
		// loop) or propagate outward.
		if r.breakEnclosing > 0 {
			r.breakEnclosing--
			brokeEarly = true
			break
		}
		if r.contnEnclosing > 0 {
			r.contnEnclosing--
			if r.contnEnclosing > 0 {
				// continue targets a loop further out. If we are the
				// outermost loop, clamp the excess level to 0 and
				// re-evaluate cond — this matches bash's "continue 99"
				// behaviour at the outermost loop. Otherwise, propagate
				// outward.
				if !oldInLoop {
					r.contnEnclosing = 0
					continue
				}
				brokeEarly = true
				break
			}
			// continue targets this loop: re-evaluate cond.
			continue
		}

		// while: run body when cond.ok(); until: run body when !cond.ok().
		// Equivalently, exit the loop when ok() == cm.Until.
		if r.exit.ok() == cm.Until {
			break
		}

		broken := r.loopStmtsBroken(loopCtx, cm.Do)
		iterationCount++
		ranBody = true
		// Capture the body's last-command exit; per POSIX this becomes the
		// loop's exit status if no further iteration runs.
		lastBody = r.exit

		if broken {
			// Excess continue at the outermost loop: clamp and keep iterating
			// (bash treats "continue 99" in a single loop like "continue 1").
			// oldInLoop is the in-loop flag from BEFORE this loop started, so
			// it tells us whether we're nested inside another loop.
			if r.contnEnclosing > 0 && !oldInLoop {
				r.contnEnclosing = 0
				continue
			}
			brokeEarly = true
			break
		}
	}
	// Clamp excess break/continue levels at the outermost loop, matching
	// bash's behaviour of discarding excess levels (e.g. "break 99" in a
	// single loop).
	if !oldInLoop {
		r.breakEnclosing = 0
		r.contnEnclosing = 0
	}
	// Loop's exit status (POSIX 2.9.4): exit status of the last command of the
	// last body iteration, or 0 if the body never ran. If the loop is exiting
	// via `exit` or a fatal error, we leave r.exit alone so the exit
	// status/state propagates upward.
	if r.exit.exiting || r.exit.fatalExit {
		return
	}
	if ranBody {
		r.exit = lastBody
	} else {
		r.exit = exitStatus{}
	}
}

func (r *Runner) stmts(ctx context.Context, stmts []*syntax.Stmt) {
	for _, stmt := range stmts {
		r.stmt(ctx, stmt)
		if r.exit.exiting || r.breakEnclosing > 0 || r.contnEnclosing > 0 {
			return
		}
	}
}

func (r *Runner) loopStmtsBroken(ctx context.Context, stmts []*syntax.Stmt) bool {
	oldInLoop := r.inLoop
	r.inLoop = true
	defer func() { r.inLoop = oldInLoop }()
	for _, stmt := range stmts {
		r.stmt(ctx, stmt)
		if r.exit.exiting {
			return true
		}
		if r.contnEnclosing > 0 {
			r.contnEnclosing--
			return r.contnEnclosing > 0
		}
		if r.breakEnclosing > 0 {
			r.breakEnclosing--
			return true
		}
	}
	return false
}

func (r *Runner) call(ctx context.Context, pos syntax.Pos, args []string) {
	name := args[0]
	r.totalCount++

	// Evaluate both policy checks upfront so the span tags reflect the
	// independent facts about the command name regardless of which gate
	// short-circuits dispatch.
	isAllowed := r.allowAllCommands || r.allowedCommands[name]
	fn, isKnown := builtins.Lookup(name)

	span, ctx := telemetry.StartSpanFromContext(ctx, "command")
	span.SetResourceName(name)
	span.SetTag("rshell.command.name", name)
	span.SetTag("rshell.command.argc", len(args)-1)
	span.SetTag("rshell.command.is_allowed", isAllowed)
	span.SetTag("rshell.command.is_known", isKnown)
	// has_stdin_pipe / has_output_redirect reflect whether the command's
	// stdin/stdout were reassigned from the Runner's originals — true for
	// both pipeline stages and file redirects.
	span.SetTag("rshell.command.has_stdin_pipe", r.stdin != r.runStdin)
	span.SetTag("rshell.command.has_output_redirect", r.stdout != r.runStdout)
	defer func() {
		span.SetTag("rshell.command.exit_code", int(r.exit.code))
		span.Finish(nil)
	}()

	if r.stop(ctx) {
		return
	}

	// Increment independently — is_allowed and is_known are orthogonal
	// facts about the command name, so a command that is both blocked and
	// missing from the registry bumps both counters. Mirrors the semantics
	// of the per-command rshell.command.is_allowed / is_known tags.
	if !isAllowed {
		r.unallowedCount++
	}
	if !isKnown {
		r.unknownCount++
	}

	if !isAllowed {
		r.errf("rshell: %s: command not allowed\n", name)
		if r.allowedCommands["help"] {
			r.errf("Run 'help' to see allowed commands.\n")
		}
		r.exit.code = 127
		return
	}

	if isKnown {
		r.dispatchedCount++
		var runCmd func(context.Context, string, string, []string) (uint8, error)
		runCmd = func(ctx context.Context, dir string, cmdName string, cmdArgs []string) (uint8, error) {
			if !r.allowAllCommands && !r.allowedCommands[cmdName] {
				return 127, fmt.Errorf("rshell: %s: command not allowed", cmdName)
			}
			cmdFn, ok := builtins.Lookup(cmdName)
			if !ok {
				return 127, fmt.Errorf("rshell: %s: unknown command", cmdName)
			}
			child := &builtins.CallContext{
				Stdout:     r.stdout,
				Stderr:     r.stderr,
				WorkDir:    func() string { return dir },
				RunCommand: runCmd,
				OpenFile: func(ctx context.Context, path string, flags int, mode os.FileMode) (io.ReadWriteCloser, error) {
					f, err := r.sandbox.Open(path, dir, flags, mode)
					if err != nil {
						return nil, err
					}
					return allowedpaths.WithContextClose(ctx, f), nil
				},
				ReadDir: func(ctx context.Context, path string) ([]fs.DirEntry, error) {
					return r.sandbox.ReadDir(path, dir)
				},
				OpenDir: func(ctx context.Context, path string) (fs.ReadDirFile, error) {
					return r.sandbox.OpenDir(path, dir)
				},
				IsDirEmpty: func(ctx context.Context, path string) (bool, error) {
					return r.sandbox.IsDirEmpty(path, dir)
				},
				ReadDirLimited: func(ctx context.Context, path string, offset, maxRead int) ([]fs.DirEntry, bool, error) {
					return r.sandbox.ReadDirLimited(path, dir, offset, maxRead)
				},
				StatFile: func(ctx context.Context, path string) (fs.FileInfo, error) {
					return r.sandbox.Stat(path, dir)
				},
				LstatFile: func(ctx context.Context, path string) (fs.FileInfo, error) {
					return r.sandbox.Lstat(path, dir)
				},
				ReadlinkFile: func(ctx context.Context, path string) (string, error) {
					return r.sandbox.Readlink(path, dir)
				},
				AccessFile: func(ctx context.Context, path string, mode uint32) error {
					return r.sandbox.Access(path, dir, mode)
				},
				PortableErr: allowedpaths.PortableErrMsg,
				Now:         r.startTime,
				FileIdentity: func(path string, info fs.FileInfo) (builtins.FileID, bool) {
					absPath := path
					if !filepath.IsAbs(absPath) {
						absPath = filepath.Join(dir, absPath)
					}
					dev, ino, ok := allowedpaths.FileIdentity(absPath, info, r.sandbox)
					if !ok {
						return builtins.FileID{}, false
					}
					return builtins.FileID{Dev: dev, Ino: ino}, true
				},
				CommandAllowed: func(n string) bool {
					return r.allowAllCommands || r.allowedCommands[n]
				},
			}
			if r.stdin != nil {
				child.Stdin = r.stdin
			}
			result := cmdFn(ctx, child, cmdArgs)
			return result.Code, nil
		}
		call := &builtins.CallContext{
			Stdout:       r.stdout,
			Stderr:       r.stderr,
			InLoop:       r.inLoop,
			LastExitCode: r.lastExit.code,
			WorkDir: func() string {
				return HandlerCtx(r.handlerCtx(ctx, todoPos)).Dir
			},
			OpenFile: func(ctx context.Context, path string, flags int, mode os.FileMode) (io.ReadWriteCloser, error) {
				f, err := r.open(ctx, path, flags, mode, false)
				if err != nil {
					return nil, err
				}
				return allowedpaths.WithContextClose(ctx, f), nil
			},
			ReadDir: func(ctx context.Context, path string) ([]fs.DirEntry, error) {
				return r.sandbox.ReadDir(path, HandlerCtx(r.handlerCtx(ctx, todoPos)).Dir)
			},
			OpenDir: func(ctx context.Context, path string) (fs.ReadDirFile, error) {
				return r.sandbox.OpenDir(path, HandlerCtx(r.handlerCtx(ctx, todoPos)).Dir)
			},
			IsDirEmpty: func(ctx context.Context, path string) (bool, error) {
				return r.sandbox.IsDirEmpty(path, HandlerCtx(r.handlerCtx(ctx, todoPos)).Dir)
			},
			ReadDirLimited: func(ctx context.Context, path string, offset, maxRead int) ([]fs.DirEntry, bool, error) {
				return r.sandbox.ReadDirLimited(path, HandlerCtx(r.handlerCtx(ctx, todoPos)).Dir, offset, maxRead)
			},
			StatFile: func(ctx context.Context, path string) (fs.FileInfo, error) {
				return r.sandbox.Stat(path, HandlerCtx(r.handlerCtx(ctx, todoPos)).Dir)
			},
			LstatFile: func(ctx context.Context, path string) (fs.FileInfo, error) {
				return r.sandbox.Lstat(path, HandlerCtx(r.handlerCtx(ctx, todoPos)).Dir)
			},
			ReadlinkFile: func(ctx context.Context, path string) (string, error) {
				return r.sandbox.Readlink(path, HandlerCtx(r.handlerCtx(ctx, todoPos)).Dir)
			},
			AccessFile: func(ctx context.Context, path string, mode uint32) error {
				return r.sandbox.Access(path, HandlerCtx(r.handlerCtx(ctx, todoPos)).Dir, mode)
			},
			PortableErr: allowedpaths.PortableErrMsg,
			Now:         r.startTime,
			FileIdentity: func(path string, info fs.FileInfo) (builtins.FileID, bool) {
				absPath := path
				if !filepath.IsAbs(absPath) {
					absPath = filepath.Join(r.Dir, absPath)
				}
				dev, ino, ok := allowedpaths.FileIdentity(absPath, info, r.sandbox)
				if !ok {
					return builtins.FileID{}, false
				}
				return builtins.FileID{Dev: dev, Ino: ino}, true
			},
			CommandAllowed: func(cmdName string) bool {
				return r.allowAllCommands || r.allowedCommands[cmdName]
			},
			RunCommand: runCmd,
			Proc:       r.proc,
		}
		if r.stdin != nil { // do not assign a typed nil into the io.Reader interface
			call.Stdin = r.stdin
		}
		result := fn(ctx, call, args[1:])
		r.exit.code = result.Code
		r.exit.exiting = result.Exiting
		r.breakEnclosing = result.BreakN
		r.contnEnclosing = result.ContinueN
		return
	}
	// Allowed but not known: the default execHandler (noExecHandler) will
	// reject with exit 127. unknownCount was already incremented above.
	r.exec(ctx, pos, args)
}

func (r *Runner) exec(ctx context.Context, pos syntax.Pos, args []string) {
	r.exit.fromHandlerError(r.execHandler(r.handlerCtx(ctx, pos), args))
}

// execIfChain runs an if/elif/else chain iteratively (rather than recursing
// through cmd() on each elif as the AST's Else pointer suggests) so the whole
// chain is covered by a single rshell.if span. The parser encodes "else" as a
// trailing *IfClause with no ThenPos set and an empty Cond.
func (r *Runner) execIfChain(ctx context.Context, cm *syntax.IfClause) {
	span, ctx := telemetry.StartSpanFromContext(ctx, "control_flow")
	span.SetResourceName("if")
	branchCount := 0
	for cur := cm; cur != nil; cur = cur.Else {
		branchCount++
	}
	branchTaken := -2 // -2 = none matched, -1 = else, >=0 = if/elif index
	defer func() {
		span.SetTag("rshell.if.branch_taken", branchTaken)
		span.SetTag("rshell.if.branch_count", branchCount)
		span.Finish(nil)
	}()

	idx := 0
	for cur := cm; cur != nil; cur = cur.Else {
		// A trailing "else" branch has no "then" token.
		if !cur.ThenPos.IsValid() {
			branchTaken = -1
			r.stmts(ctx, cur.Then)
			return
		}
		r.stmts(ctx, cur.Cond)
		if r.exit.exiting || r.breakEnclosing > 0 || r.contnEnclosing > 0 {
			return
		}
		if r.exit.ok() {
			branchTaken = idx
			r.stmts(ctx, cur.Then)
			return
		}
		r.exit = exitStatus{}
		idx++
	}
}

// countPipelineStages walks a left-recursive Pipe-BinaryCmd tree (e.g. a|b|c
// is BinaryCmd(Pipe, BinaryCmd(Pipe, a, b), c)) and returns the total number
// of stages.
func countPipelineStages(cm *syntax.BinaryCmd) int {
	n := 1 // Y is one stage
	if inner, ok := cm.X.Cmd.(*syntax.BinaryCmd); ok && inner.Op == syntax.Pipe {
		n += countPipelineStages(inner)
	} else {
		n += 1
	}
	return n
}

// syncWriter wraps an io.Writer with a mutex so concurrent writes are safe.
// Used to protect stderr when both sides of a pipe write to it.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (sw *syncWriter) Write(p []byte) (int, error) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.w.Write(p)
}

// pipeBrokenWriter wraps the producer side of an internal pipe and turns a
// broken-pipe error into a graceful exit signal on the producer's runner. In
// bash, the kernel delivers SIGPIPE to a writer when the read end is closed;
// the default action is to terminate the writer. We approximate that here by
// flagging the runner as exiting on the next r.stop(ctx) call once a write
// has returned EPIPE.
//
// We do NOT propagate the EPIPE error back to the caller (so existing
// builtins that ignore write errors continue to work unchanged), but we do
// preserve the byte count and other error types.
type pipeBrokenWriter struct {
	w    io.Writer
	flag *atomic.Bool // shared with the producer runner and its subshells
}

func (p *pipeBrokenWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	if err != nil && isBrokenPipeErr(err) {
		// Set the durable pipeBroken flag — r.stop() will pick it up on the
		// next statement boundary and terminate the producer. We don't set
		// r.exit.exiting directly here because that field is overwritten by
		// each builtin's Result.Exiting (interp/runner_exec.go:619). The
		// flag is shared via *atomic.Bool with subshells of the producer so
		// that nested constructs (e.g. `while true; do (while ...); done | head`)
		// also unwind, and so writes from one goroutine are visible to reads
		// in another (e.g. chained pipelines where the inner while and outer
		// cat run in different goroutines).
		if p.flag != nil {
			p.flag.Store(true)
		}
		// Suppress the EPIPE error — the runner-level signal is what matters.
		// Report len(b) bytes written to satisfy the io.Writer contract
		// (returning n < len(b) with nil error would be a short-write violation).
		return len(b), nil
	}
	return n, err
}

// isBrokenPipeErr reports whether err is the broken-pipe error returned by
// writing to a pipe whose read end has been closed. Cross-platform:
//   - Unix: syscall.EPIPE (errno 32).
//   - Windows: ERROR_BROKEN_PIPE (errno 109) OR ERROR_NO_DATA (errno 232).
//     Go's own os/pipe_test.go special-cases both; os.File.Write does NOT
//     normalise either to syscall.EPIPE.
//
// The numeric Windows errno values are guarded by runtime.GOOS == "windows"
// so we don't misidentify unrelated errors on other platforms (Linux errno 109
// is ENOPROTOOPT; macOS errno 109 is ENOATTR — neither is pipe-related). The
// constants are hardcoded rather than referenced symbolically because
// syscall.ERROR_BROKEN_PIPE / syscall.ERROR_NO_DATA are Windows-only.
func isBrokenPipeErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EPIPE) {
		return true
	}
	if runtime.GOOS == "windows" {
		var errno syscall.Errno
		if errors.As(err, &errno) {
			// 109 = ERROR_BROKEN_PIPE; 232 = ERROR_NO_DATA ("the pipe is
			// being closed"). See Go src os/pipe_test.go.
			return errno == 109 || errno == 232
		}
	}
	return false
}
