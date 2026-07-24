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
	"sync"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/datadog-agent/pkg/fleet/installer/telemetry"
	"github.com/DataDog/rshell/allowedpaths"
	"github.com/DataDog/rshell/builtins"
)

func allowedPathsList(sb *allowedpaths.Sandbox) []builtins.AllowedPath {
	if sb == nil {
		return nil
	}
	paths := sb.PathAccesses()
	out := make([]builtins.AllowedPath, len(paths))
	for i, path := range paths {
		access := builtins.AllowedPathReadOnly
		if path.ReadWrite {
			access = builtins.AllowedPathReadWrite
		}
		out[i] = builtins.AllowedPath{
			Path:   path.Path,
			Access: access,
		}
	}
	return out
}

func toBuiltinFileSystemInfo(info allowedpaths.FileSystemInfo) builtins.FileSystemInfo {
	return builtins.FileSystemInfo{
		ID:                   info.ID,
		IDAvailable:          info.IDAvailable,
		NameMax:              info.NameMax,
		NameMaxAvailable:     info.NameMaxAvailable,
		TypeID:               info.TypeID,
		TypeIDAvailable:      info.TypeIDAvailable,
		TypeName:             info.TypeName,
		IOBlockSize:          info.IOBlockSize,
		FundamentalBlockSize: info.FundamentalBlockSize,
		Blocks:               info.Blocks,
		BlocksFree:           info.BlocksFree,
		BlocksAvailable:      info.BlocksAvailable,
		Files:                info.Files,
		FilesFree:            info.FilesFree,
		FilesAvailable:       info.FilesAvailable,
	}
}

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
		seenRestore := map[string]bool{}

		for _, as := range cm.Assigns {
			name := as.Name.Value
			prev := r.lookupVar(name)

			vr := r.assignVal(prev, as, "")
			// Inline command vars are always exported.
			vr.Exported = true

			// Only the first prev for a given name is the true
			// pre-command value; later ones capture the intermediate
			// assigned by an earlier iteration of this loop.
			if !seenRestore[name] {
				restores = append(restores, restoreVar{name, prev})
				seenRestore[name] = true
			}

			r.setVar(name, vr)
		}

		defer func() {
			// cd intentionally writes $PWD and $OLDPWD as part of
			// its semantics. Reverting those after a successful cd
			// would leave the env vars disagreeing with the shell's
			// tracked working directory — bash skips the revert in
			// the same case (e.g. `PWD=/bogus cd b` keeps PWD at
			// the new dir afterwards). The skip is scoped to a
			// successful cd so a cd that errored still gets its
			// temp PWD assignment reverted normally.
			isCd := len(fields) > 0 && fields[0] == "cd" && r.exit.ok()
			for _, restore := range restores {
				if isCd && (restore.name == "PWD" || restore.name == "OLDPWD") {
					continue
				}
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
			rLeft.stdout = pw
			rLeft.stderr = safeStderr
			rLeft.inPipeline = true
			// Pipeline stages inherit the parent's loop context only when the
			// stage is a simple command or another pipeline. Bash silently
			// no-ops a bare `break`/`continue` invoked as an entire pipeline
			// stage, but when the stage is a compound command (`{...}`,
			// `(...)`, an if chain, etc.) bash prints the "only useful in
			// a loop" diagnostic AND keeps executing the rest of the stage.
			// Inheriting inLoop unconditionally would suppress the
			// diagnostic for compound stages and — worse — let the
			// stage-internal break/continue counter abort the rest of the
			// stage's statements, dropping commands that bash still runs.
			// A nested pipeline must inherit so the recursive case applies
			// the same per-stage rule to its own leaves; otherwise non-
			// rightmost stages of 3+ stage pipelines (`break | cat | cat`)
			// would lose loop context and emit spurious diagnostics.
			if pipelineStageInheritsInLoop(cm.X) {
				rLeft.inLoop = r.inLoop
			}
			rRight := r.subshell(true)
			rRight.stdin = pr
			rRight.stderr = safeStderr
			rRight.inPipeline = true
			if pipelineStageInheritsInLoop(cm.Y) {
				rRight.inLoop = r.inLoop
			}
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

// pipelineStageInheritsInLoop reports whether a pipeline stage should inherit
// the parent's loop context. Bash silently no-ops a bare `break`/`continue`
// invoked as the entirety of a pipeline stage, but prints the "only useful in
// a loop" diagnostic when the stage is compound (block, subshell, if chain,
// loop) — so loop context is propagated for simple commands only.
//
// A nested pipeline (e.g. the `a | b` inside `a | b | c`) must also inherit
// the loop context: the recursive Pipe case re-runs this check on each leaf
// stage, so without propagation through intermediate pipeline nodes any non-
// rightmost stage of a 3+ stage pipeline would lose its loop context and
// `break`/`continue` would emit spurious diagnostics.
func pipelineStageInheritsInLoop(st *syntax.Stmt) bool {
	if st == nil {
		return false
	}
	switch c := st.Cmd.(type) {
	case *syntax.CallExpr:
		return true
	case *syntax.BinaryCmd:
		return c.Op == syntax.Pipe
	}
	return false
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
		span.SetTag("rshell.while.iteration_count", iterationCount)
		span.SetTag("rshell.while.broke_early", brokeEarly)
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

	var lastBody exitStatus
	// condBroke is set when the loop terminates because of a `break` invoked
	// inside the condition list, OR because `continue` invoked in the cond
	// short-circuits the cond list with status 0 and the cond-status check
	// then says "exit loop" (e.g. `until ...; continue` — until exits when
	// cond is 0). In those cases the loop's exit status must be the
	// builtin's exit status (typically 0), NOT the previous body's
	// last-command status — this matches bash. Without the flag, we would
	// overwrite the cond's status with stale lastBody at loop exit.
	condBroke := false
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

		// `break` invoked inside the cond list short-circuits the rest of
		// the cond and exits the loop regardless of cond's last status.
		// Preserve break's exit status (typically 0) as the loop's exit.
		if r.breakEnclosing > 0 {
			r.breakEnclosing--
			brokeEarly = true
			condBroke = true
			break
		}

		// `continue` invoked inside the cond list. Order matters here vs.
		// the cond-status check: when `continue` is invoked in an `until`
		// cond, continue's status (0) means until's cond-status check says
		// "exit loop", and bash then exits the loop rather than re-
		// evaluating. We mirror that by deferring to the cond-status check
		// after consuming nesting levels.
		if r.contnEnclosing > 0 {
			r.contnEnclosing--
			if r.contnEnclosing > 0 {
				// continue targets a loop further out.
				if !oldInLoop {
					// outermost: clamp excess (treat as continue 1).
					r.contnEnclosing = 0
				} else {
					// nested: propagate to outer regardless of cond
					// status. The outer loop will see contnEnclosing>0
					// and continue its own iteration.
					brokeEarly = true
					break
				}
			}
			// continue 1 (or clamped excess at outermost). Cond status
			// decides: if cond says exit loop (e.g. until + status 0),
			// exit and preserve continue's status. Otherwise re-evaluate
			// cond, skipping the body for this iteration.
			if r.exit.ok() == cm.Until {
				condBroke = true
				break
			}
			continue
		}

		// while: run body when cond.ok(); until: run body when !cond.ok().
		// Equivalently, exit the loop when ok() == cm.Until.
		if r.exit.ok() == cm.Until {
			break
		}
		// Reset the cond's exit so the body sees a clean status (mirrors how
		// r.stmt() resets r.exit before each statement).
		r.exit = exitStatus{}

		broken := r.loopStmtsBroken(loopCtx, cm.Do)
		iterationCount++
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
	// status/state propagates upward. If the loop exited via a break in the
	// condition list, r.exit currently holds the break builtin's exit status
	// — leave it alone so the cond-break status (matching bash) is preserved
	// rather than overwritten by the stale previous-body status.
	if r.exit.exiting || r.exit.fatalExit {
		return
	}
	if condBroke {
		return
	}
	if iterationCount > 0 {
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
		var runCmdWithStdin func(context.Context, string, string, []string, io.Reader) (uint8, error)
		runCmdWithStdin = func(ctx context.Context, dir string, cmdName string, cmdArgs []string, childStdin io.Reader) (uint8, error) {
			if !r.allowAllCommands && !r.allowedCommands[cmdName] {
				return 127, fmt.Errorf("rshell: %s: command not allowed", cmdName)
			}
			cmdFn, ok := builtins.Lookup(cmdName)
			if !ok {
				return 127, fmt.Errorf("rshell: %s: unknown command", cmdName)
			}
			child := &builtins.CallContext{
				Stdout:  r.stdout,
				Stderr:  r.stderr,
				WorkDir: func() string { return dir },
				HostPrefix: func() string {
					// Return the sandbox's normalized prefix (filepath.Clean'd
					// in SetHostPrefix) rather than the raw user-supplied
					// value. A caller-provided trailing slash or "."/".."
					// segment would otherwise break prefix-matching in
					// builtins that consume this value.
					if r.sandbox != nil {
						return r.sandbox.HostPrefix()
					}
					return r.hostPrefix
				},
				CanonicalizeRootPrefix: func(absPath string) string {
					if r.sandbox == nil {
						return absPath
					}
					return r.sandbox.CanonicalizeRootPrefix(absPath)
				},
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
				FileSystemStat: func(ctx context.Context, path string) (builtins.FileSystemInfo, error) {
					info, err := r.sandbox.StatFS(path, dir)
					return toBuiltinFileSystemInfo(info), err
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
				AuthorizeSystemd:          r.authorizeSystemd,
				AuthorizeSystemServices:   r.authorizeSystemServices,
				ReadableSystemServices:    r.readableSystemServices,
				AllowedSystemServicesList: r.allowedSystemServicesList,
				AllowedPathsList: func() []builtins.AllowedPath {
					return allowedPathsList(r.sandbox)
				},
				// ChangeDir is intentionally nil for RunCommand children
				// (find -exec, find -execdir, xargs). bash forks a child
				// process for each invocation, so cd inside such a child
				// can never propagate to the parent shell. We model the
				// same isolation by making cd unavailable in this path —
				// the cd handler returns "cd: not supported in this
				// runner" rather than silently mutating the top-level
				// r.Dir, which would have leaked the child's directory
				// change back into the caller (the bug Codex flagged).
				LookupEnvVar: r.lookupEnvVar,
				RunCommand: func(ctx context.Context, dir string, name string, args []string) (uint8, error) {
					// Inherit the parent's overridden stdin so grandchildren
					// dispatched via RunCommand (the no-stdin variant) stay
					// isolated from the top-level r.stdin. When the parent
					// has no override (childStdin == nil), this is the same
					// fallback as before — the grandchild's CallContext
					// picks up r.stdin via the default branch below.
					return runCmdWithStdin(ctx, dir, name, args, childStdin)
				},
				RunCommandWithStdin: runCmdWithStdin,
				// Intentionally not exposing SetVar / GetVar in the
				// child CallContext used for find -exec / -execdir
				// grandchildren. find treats each invocation as a
				// separate command (bash forks and execs a new
				// process), so any environment mutation must not
				// leak back to the calling shell. State-mutating
				// builtins like read detect the absent SetVar and
				// refuse to run with "variable access is not
				// available", which is the closest analogue to bash's
				// "exec read fails because read is a builtin, not an
				// executable on PATH" behaviour.
				Proc:            r.proc,
				Systemd:         r.systemd,
				RemediationMode: r.remediationMode,
			}
			if r.remediationMode && r.sandbox != nil {
				child.Truncate = func(ctx context.Context, path string, size int64, create bool) error {
					return r.sandbox.Truncate(path, dir, size, create)
				}
				child.TruncateToZeroIfAtLeast = func(ctx context.Context, path string, minSize int64, dryRun bool) (int64, bool, error) {
					return r.sandbox.TruncateToZeroIfAtLeast(path, dir, minSize, dryRun)
				}
				child.Remove = func(ctx context.Context, path string) error {
					return r.sandbox.Remove(path, dir)
				}
			}
			if childStdin != nil {
				child.Stdin = childStdin
			} else if r.stdin != nil {
				child.Stdin = r.stdin
			}
			result := cmdFn(ctx, child, cmdArgs)
			return result.Code, nil
		}
		runCmd := func(ctx context.Context, dir string, cmdName string, cmdArgs []string) (uint8, error) {
			return runCmdWithStdin(ctx, dir, cmdName, cmdArgs, nil)
		}
		call := &builtins.CallContext{
			Stdout:       r.stdout,
			Stderr:       r.stderr,
			InLoop:       r.inLoop,
			LastExitCode: r.lastExit.code,
			WorkDir: func() string {
				return HandlerCtx(r.handlerCtx(ctx, todoPos)).Dir
			},
			HostPrefix: func() string {
				// Return the sandbox's normalized prefix (filepath.Clean'd
				// in SetHostPrefix) rather than the raw user-supplied
				// value. A caller-provided trailing slash or "."/".."
				// segment would otherwise break prefix-matching in
				// builtins that consume this value.
				if r.sandbox != nil {
					return r.sandbox.HostPrefix()
				}
				return r.hostPrefix
			},
			CanonicalizeRootPrefix: func(absPath string) string {
				if r.sandbox == nil {
					return absPath
				}
				return r.sandbox.CanonicalizeRootPrefix(absPath)
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
			FileSystemStat: func(ctx context.Context, path string) (builtins.FileSystemInfo, error) {
				info, err := r.sandbox.StatFS(path, HandlerCtx(r.handlerCtx(ctx, todoPos)).Dir)
				return toBuiltinFileSystemInfo(info), err
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
			AuthorizeSystemd:          r.authorizeSystemd,
			AuthorizeSystemServices:   r.authorizeSystemServices,
			ReadableSystemServices:    r.readableSystemServices,
			AllowedSystemServicesList: r.allowedSystemServicesList,
			AllowedPathsList: func() []builtins.AllowedPath {
				return allowedPathsList(r.sandbox)
			},
			ChangeDir:           r.changeDir,
			LookupEnvVar:        r.lookupEnvVar,
			RunCommand:          runCmd,
			RunCommandWithStdin: runCmdWithStdin,
			SetVar: func(name, value string) error {
				if len(value) > MaxVarBytes {
					return fmt.Errorf("%s: value too large (limit %d bytes)", name, MaxVarBytes)
				}
				err := r.setVarErr(name, expand.Variable{Set: true, Kind: expand.String, Str: value})
				if err == nil {
					return nil
				}
				// Total-storage exhaustion is script-aborting (matches the
				// AST setVar behaviour in interp/vars.go). Translate the
				// internal sentinel to the public builtins.ErrVarStorageExceeded
				// so state-mutating builtins can surface Result.Exiting=true
				// without needing access to the private interp type.
				var storageErr *errTotalVarStorageExceeded
				if errors.As(err, &storageErr) {
					return fmt.Errorf("%s: %w", err, builtins.ErrVarStorageExceeded)
				}
				return err
			},
			GetVar: func(name string) (string, bool) {
				vr := r.writeEnv.Get(name)
				return vr.Str, vr.IsSet()
			},
			Proc:            r.proc,
			Systemd:         r.systemd,
			RemediationMode: r.remediationMode,
		}
		if r.remediationMode && r.sandbox != nil {
			call.Truncate = func(ctx context.Context, path string, size int64, create bool) error {
				return r.sandbox.Truncate(path, r.Dir, size, create)
			}
			call.TruncateToZeroIfAtLeast = func(ctx context.Context, path string, minSize int64, dryRun bool) (int64, bool, error) {
				return r.sandbox.TruncateToZeroIfAtLeast(path, r.Dir, minSize, dryRun)
			}
			call.Remove = func(ctx context.Context, path string) error {
				return r.sandbox.Remove(path, r.Dir)
			}
		}
		if r.stdin != nil { // do not assign a typed nil into the io.Reader interface
			call.Stdin = r.stdin
		}
		result := fn(ctx, call, args[1:])
		r.exit.code = result.Code
		r.exit.exiting = result.Exiting
		r.breakEnclosing = result.BreakN
		r.contnEnclosing = result.ContinueN
		// If the run-level context was cancelled while the builtin was
		// blocked (MaxExecutionTime, CLI --timeout, parent cancellation),
		// surface that error to Run() so callers can distinguish a
		// timeout-driven termination from an ordinary failing exit code.
		// Without this, a long-blocking builtin that happens to be the
		// last command in a script would leave Run() returning the
		// builtin's status (e.g. `read -t`'s 142) instead of the
		// underlying context.DeadlineExceeded that cmd/rshell main checks
		// for to print "execution timed out".
		if err := ctx.Err(); err != nil {
			r.exit.fatal(err)
		}
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
