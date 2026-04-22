// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"sync"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

func (r *Runner) fillExpandConfig(ctx context.Context) {
	r.ectx = ctx
	r.ecfg = &expand.Config{
		Env:      expandEnv{r},
		CmdSubst: r.cmdSubst,
	}
	r.updateExpandOpts()
}

func (r *Runner) updateExpandOpts() {
	r.ecfg.ReadDir2 = func(s string) ([]fs.DirEntry, error) {
		if r.globReadDirCount != nil {
			if r.globReadDirCount.Add(1) > MaxGlobReadDirCalls {
				return nil, fmt.Errorf("glob expansion exceeded maximum number of directory reads (%d)", MaxGlobReadDirCalls)
			}
		}
		ctx := r.handlerCtx(r.ectx, todoPos)
		if r.readDirHandler != nil {
			return r.readDirHandler(ctx, s)
		}
		// Fallback when a custom openHandler was set without a readDirHandler.
		return r.sandbox.ReadDirForGlob(s, HandlerCtx(ctx).Dir)
	}
}

// maxCmdSubstOutput is the maximum number of bytes a command substitution
// can capture before being truncated. This prevents memory exhaustion from
// commands that produce unbounded output.
const maxCmdSubstOutput = 1 << 20 // 1 MiB

// maxStdoutBytes is the maximum number of bytes a script can write to stdout
// before further output is silently discarded. This caps total script output
// to prevent memory exhaustion from runaway commands (e.g. infinite loops
// writing to stdout).
const maxStdoutBytes = 10 * 1024 * 1024 // 10 MiB

// MaxGlobReadDirCalls is the maximum number of ReadDirForGlob invocations
// allowed per Run() call. This prevents memory exhaustion from scripts that
// trigger an excessive number of glob expansions (e.g. millions of unquoted
// * tokens, or deeply nested glob patterns in loops).
const MaxGlobReadDirCalls = 10_000

// cmdSubst handles command substitution ($(...) and `...`).
// It runs the commands in a subshell and writes their stdout to w.
//
// The $(<file) POSIX shortcut is intentionally NOT implemented here: it
// reads file contents without invoking any command and thus bypasses the
// AllowedCommands allowlist. Scripts that need file contents must invoke
// an allowed file-reading command explicitly, e.g. $(cat file). Bare
// input redirection without a command is rejected at validation time by
// validateRedirectHasCommand.
func (r *Runner) cmdSubst(w io.Writer, cs *syntax.CmdSubst) error {
	if len(cs.Stmts) == 0 {
		return nil
	}

	var buf bytes.Buffer
	r2 := r.subshell(false)
	r2.stdout = &limitWriter{w: &buf, limit: maxCmdSubstOutput}
	r2.stmts(r.ectx, cs.Stmts)
	r2.exit.exiting = false
	r.lastExpandExit = r2.exit
	r.lastExit = r.lastExpandExit
	if r2.exit.fatalExit {
		// Propagate the fatal state to the parent runner so that
		// callers (e.g. for loops iterating over $(…)) cannot
		// silently continue after a context cancellation or other
		// fatal error in the subshell.
		r.exit.fatal(r2.exit.err)
		return r2.exit.err
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// limitWriter wraps a writer and stops writing after limit bytes.
// When the limit is exceeded, exceeded is set to true and further writes
// are silently discarded so that callers do not see spurious short-write
// errors mid-execution. The exceeded flag can be checked after execution
// via isExceeded to surface the event as an error.
//
// limitWriter is safe for concurrent use: the mutex serialises writes so
// that the byte counter and exceeded flag are always consistent, even when
// background goroutines write to the same writer concurrently.
type limitWriter struct {
	mu       sync.Mutex
	w        io.Writer
	limit    int64
	n        int64
	exceeded bool
}

func (lw *limitWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	if lw.n >= lw.limit {
		lw.exceeded = true
		return len(p), nil // silently discard excess
	}
	remaining := lw.limit - lw.n
	if int64(len(p)) > remaining {
		if _, err := lw.w.Write(p[:remaining]); err != nil {
			return int(remaining), err
		}
		lw.n = lw.limit
		lw.exceeded = true
		return len(p), nil // report all bytes consumed to avoid short-write errors
	}
	n, err := lw.w.Write(p)
	lw.n += int64(n)
	return n, err
}

// isExceeded reports whether any write has exceeded the byte limit.
// It is safe to call concurrently with Write.
func (lw *limitWriter) isExceeded() bool {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	return lw.exceeded
}

func (r *Runner) expandErr(err error) {
	if err == nil {
		return
	}
	errMsg := err.Error()
	fmt.Fprintln(r.stderr, errMsg)
	var storageErr *errTotalVarStorageExceeded
	switch {
	case errors.As(err, &expand.UnsetParameterError{}):
	case errors.As(err, &expand.UnexpectedCommandError{}):
		// Defense in depth: if the expand package encounters a command
		// substitution that our handler cannot process, treat it as fatal.
	case errors.As(err, &storageErr):
		// Total variable storage exhaustion via parameter expansion (e.g.
		// ${var:=value}) must abort the script, just as direct assignment
		// through setVar does.  Without this arm the error falls through to
		// the default case, which only sets exit code 1 and lets the script
		// continue — a cap bypass.
	case errMsg == "invalid indirect expansion":
		// TODO: These errors are treated as fatal by bash.
		// Make the error type reflect that.
	case strings.HasSuffix(errMsg, "not supported"):
		// TODO: This "has suffix" is a temporary measure until the expand
		// package supports all syntax nodes like extended globbing.
	default:
		// Non-fatal expansion errors (e.g. assignment to a readonly variable):
		// set non-zero exit status so the failure is visible, but do not exit
		// the script — bash continues execution in this case.
		r.exit.code = 1
		return
	}
	r.exit.code = 1
	r.exit.exiting = true
}

func (r *Runner) fields(words ...*syntax.Word) []string {
	strs, err := expand.Fields(r.ecfg, words...)
	r.expandErr(err)
	return strs
}

func (r *Runner) literal(word *syntax.Word) string {
	str, err := expand.Literal(r.ecfg, word)
	r.expandErr(err)
	return str
}

func (r *Runner) document(word *syntax.Word) string {
	str, err := expand.Document(r.ecfg, word)
	r.expandErr(err)
	return str
}

// expandEnv exposes [Runner]'s variables to the expand package.
type expandEnv struct {
	r *Runner
}

var _ expand.WriteEnviron = expandEnv{}

func (e expandEnv) Get(name string) expand.Variable {
	return e.r.lookupVar(name)
}

func (e expandEnv) Set(name string, vr expand.Variable) error {
	if err := e.r.setVarErr(name, vr); err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return nil
}

func (e expandEnv) Each(fn func(name string, vr expand.Variable) bool) {
	e.r.writeEnv.Each(fn)
}
