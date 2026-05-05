// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

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
			rLeft.stdout = pw
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
	default:
		r.exit.fatal(fmt.Errorf("unsupported command node: %T", cm))
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

// argvMatchesAllowedPattern reports whether args satisfies any of the
// configured AllowedCommandPatterns. A pattern is shaped like
// (command [, subcommand_path...]) and matches when:
//
//  1. args[0] equals pattern[0] exactly (the command name).
//  2. The leading structural tokens of args[1..] equal pattern[1..],
//     where "structural tokens" are extracted by skipping flag tokens
//     according to the CommandSpec registered for args[0]. See
//     [CommandSpec] for the classification rules.
//
// Single-token patterns trivially match on argv[0] alone — no spec is
// consulted. Multi-token patterns require a spec for args[0]; New()
// rejects multi-token patterns whose command lacks a spec, so by the time
// this method runs we expect the lookup to succeed.
//
// args is expected to be the full argv with the command name at args[0]
// (the same shape passed to call()). Callers that hold the command name and
// arguments separately must reconstruct the full argv before invoking this
// matcher.
//
// The matcher is called after shell expansion, so command-substitution-
// derived argv elements are already resolved — this is the architectural
// guarantee of the feature.
//
// Why structural matching matters: a naive presence-only matcher would
// admit "ip addr show" against a pattern of (ip, route) if the literal
// token "route" appeared anywhere in argv (e.g. as a positional value
// like "ip addr show route"). The structural matcher checks pattern[1..]
// against the leading subcommand-path tokens only, so positional values
// at later positions cannot satisfy pattern slots.
func (r *Runner) argvMatchesAllowedPattern(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for _, pattern := range r.allowedCommandPatterns {
		if len(pattern) == 0 {
			// Defensive: option validator already rejects empty
			// patterns, so we never expect to see one here.
			continue
		}
		// First token must match args[0] exactly.
		if args[0] != pattern[0] {
			continue
		}
		// Single-token pattern: argv[0] match is sufficient.
		if len(pattern) == 1 {
			return true
		}
		// Multi-token pattern: walk argv[1..] and extract structural
		// tokens using the spec for args[0]. validateAllowedCommandPatterns
		// guarantees the spec exists; defensive check returns false if it
		// somehow doesn't (e.g. spec was unregistered between option
		// processing and dispatch).
		spec, ok := r.commandSpecs[args[0]]
		if !ok {
			continue
		}
		structural := extractStructuralTokens(args[1:], spec)
		if len(pattern)-1 > len(structural) {
			continue
		}
		matched := true
		for i, ptok := range pattern[1:] {
			if structural[i] != ptok {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// extractStructuralTokens returns the structural-token sequence (subcommand
// path followed by positional arguments) derived from the trailing-args
// portion of argv (i.e. args[1:] in the caller's view), using spec to
// classify flags. See [CommandSpec] for the classification rules.
//
// Tokens are returned in the order they appear in input, with classified
// flag tokens (and the next-token values of recognised value flags)
// elided. The matcher consumes only the leading prefix of the result.
func extractStructuralTokens(args []string, spec CommandSpec) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		tok := args[i]
		if !strings.HasPrefix(tok, "-") || tok == "-" || tok == "--" {
			// Plain positional / subcommand token. (A bare "-" or "--"
			// is a positional separator in shell convention, not a
			// flag.)
			out = append(out, tok)
			continue
		}
		// Flag of some kind.
		if strings.Contains(tok, "=") {
			// "--flag=value" or "-f=value" form: the value is bundled
			// into this single token, so we don't need to consume the
			// next argv token regardless of spec classification.
			continue
		}
		if spec.ValueFlags[tok] {
			// Skip the flag and its value (next argv token, if any).
			if i+1 < len(args) {
				i++
			}
			continue
		}
		// BooleanFlags or unknown flag: skip just the flag token. We
		// treat unknown flags as boolean to avoid false negatives if
		// the spec is incomplete; the trade-off is a possible false
		// negative if the flag actually takes a value (its assumed
		// "value" would then be misclassified as a structural token).
		_ = spec.BooleanFlags // documented behaviour, no branch needed
	}
	return out
}

func (r *Runner) call(ctx context.Context, pos syntax.Pos, args []string) {
	name := args[0]
	r.totalCount++

	// Evaluate both policy checks upfront so the span tags reflect the
	// independent facts about the command name regardless of which gate
	// short-circuits dispatch.
	isAllowed := r.allowAllCommands || r.allowedCommands[name] || r.argvMatchesAllowedPattern(args)
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
			// Pattern matching expects full argv with the command name at
			// args[0]. cmdArgs by convention excludes cmdName, so we
			// reconstruct the canonical argv before consulting patterns.
			fullArgv := make([]string, 0, len(cmdArgs)+1)
			fullArgv = append(fullArgv, cmdName)
			fullArgv = append(fullArgv, cmdArgs...)
			if !r.allowAllCommands && !r.allowedCommands[cmdName] && !r.argvMatchesAllowedPattern(fullArgv) {
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
				CommandAllowed: func(n string, args []string) bool {
					return r.allowAllCommands || r.allowedCommands[n] || r.argvMatchesAllowedPattern(args)
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
			CommandAllowed: func(cmdName string, args []string) bool {
				return r.allowAllCommands || r.allowedCommands[cmdName] || r.argvMatchesAllowedPattern(args)
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
