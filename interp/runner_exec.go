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
	"sync"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"

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
			rRight := r.subshell(true)
			rRight.stdin = pr
			rRight.stderr = safeStderr
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer func() {
					if rec := recover(); rec != nil {
						rLeft.exit.fatal(fmt.Errorf("internal error: %v", rec))
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
			if rLeft.exit.fatalExit {
				r.exit.fatal(rLeft.exit.err)
			}
		}
	case *syntax.IfClause:
		r.stmts(ctx, cm.Cond)
		if r.exit.exiting || r.breakEnclosing > 0 || r.contnEnclosing > 0 {
			break
		}
		if r.exit.ok() {
			r.stmts(ctx, cm.Then)
		} else {
			r.exit = exitStatus{}
			if cm.Else != nil {
				r.cmd(ctx, cm.Else)
			}
		}
	case *syntax.ForClause:
		switch y := cm.Loop.(type) {
		case *syntax.WordIter:
			name := y.Name.Value
			items := r.Params // for i; do ...

			inToken := y.InPos.IsValid()
			if inToken {
				items = r.fields(y.Items...) // for i in ...; do ...
			}

			for _, field := range items {
				if err := ctx.Err(); err != nil {
					r.exit.fatal(err)
					break
				}
				r.setVarString(name, field)
				if r.loopStmtsBroken(ctx, cm.Do) {
					// Excess continue at outermost loop: clamp and keep iterating
					// (bash treats "continue 99" in a single loop like "continue 1").
					if r.contnEnclosing > 0 && !r.inLoop {
						r.contnEnclosing = 0
						continue
					}
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

func (r *Runner) call(ctx context.Context, pos syntax.Pos, args []string) {
	if r.stop(ctx) {
		return
	}
	name := args[0]
	if fn, ok := builtins.Lookup(name); ok {
		call := &builtins.CallContext{
			Stdout:       r.stdout,
			Stderr:       r.stderr,
			InLoop:       r.inLoop,
			LastExitCode: r.lastExit.code,
			OpenFile: func(ctx context.Context, path string, flags int, mode os.FileMode) (io.ReadWriteCloser, error) {
				return r.open(ctx, path, flags, mode, false)
			},
			ReadDir: func(ctx context.Context, path string) ([]fs.DirEntry, error) {
				return r.sandbox.ReadDir(path, HandlerCtx(r.handlerCtx(ctx, todoPos)).Dir)
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
			AccessFile: func(ctx context.Context, path string, mode uint32) error {
				return r.sandbox.Access(path, HandlerCtx(r.handlerCtx(ctx, todoPos)).Dir, mode)
			},
			PortableErr: allowedpaths.PortableErrMsg,
			Now:         time.Now,
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
	r.exec(ctx, pos, args)
}

func (r *Runner) exec(ctx context.Context, pos syntax.Pos, args []string) {
	r.exit.fromHandlerError(r.execHandler(r.handlerCtx(ctx, pos), args))
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
