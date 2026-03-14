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
	"strings"
	"sync"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// procSubstEntry tracks state for a single process substitution.
type procSubstEntry struct {
	path      string         // /dev/fd/N path returned to the outer command
	outerFile *os.File       // pipe end the outer command reads/writes
	wg        sync.WaitGroup // goroutine for the inner subshell
}

func (r *Runner) fillExpandConfig(ctx context.Context) {
	r.ectx = ctx
	r.ecfg = &expand.Config{
		Env: expandEnv{r},
		CmdSubst: func(w io.Writer, cs *syntax.CmdSubst) error {
			return r.cmdSubst(ctx, w, cs)
		},
		ProcSubst: func(ps *syntax.ProcSubst) (string, error) {
			return r.procSubst(ctx, ps)
		},
	}
	r.updateExpandOpts()
}

// cmdSubst implements command substitution: $(cmd).
// It runs the statements in a subshell with stdout captured into w.
// The output is bounded by MaxVarBytes to prevent unbounded memory use.
func (r *Runner) cmdSubst(ctx context.Context, w io.Writer, cs *syntax.CmdSubst) error {
	r2 := r.subshell(false)
	r2.stdout = &limitWriter{w: w, n: MaxVarBytes}
	r2.stmts(ctx, cs.Stmts)
	// Propagate exit code but not the exiting flag — exit inside $(...)
	// only terminates the subshell, not the parent shell.
	r.lastExpandExit = exitStatus{code: r2.exit.code}
	if r2.exit.fatalExit {
		return r2.exit.err
	}
	return nil
}

// procSubst implements process substitution: <(cmd) and >(cmd).
// It creates an OS pipe, runs the subshell in a background goroutine,
// and returns the /dev/fd/N path for the pipe end the outer command uses.
func (r *Runner) procSubst(ctx context.Context, ps *syntax.ProcSubst) (string, error) {
	if !devFDSupported {
		return "", fmt.Errorf("process substitution is not supported on this platform")
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return "", err
	}
	r2 := r.subshell(true)

	var outerFile, innerFile *os.File
	if ps.Op == syntax.CmdIn {
		// <(cmd): subshell writes to pipe, outer command reads
		r2.stdout = pw
		outerFile = pr
		innerFile = pw
	} else {
		// >(cmd): subshell reads from pipe, outer command writes
		r2.stdin = pr
		outerFile = pw
		innerFile = pr
	}

	path := fmt.Sprintf("/dev/fd/%d", outerFile.Fd())

	entry := &procSubstEntry{
		path:      path,
		outerFile: outerFile,
	}
	// Wrap stderr in a synchronized writer so the goroutine can write errors
	// without racing with the outer command.
	safeStderr := &syncWriter{w: r.stderr}
	r2.stderr = safeStderr

	entry.wg.Add(1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				fmt.Fprintf(safeStderr, "internal error: %v\n", rec)
			}
			innerFile.Close()
			entry.wg.Done()
		}()
		r2.stmts(ctx, ps.Stmts)
	}()

	r.procSubsts = append(r.procSubsts, entry)
	return path, nil
}

// lookupProcSubstFile returns the *os.File for a process substitution path,
// or nil if the path doesn't match any active process substitution.
func (r *Runner) lookupProcSubstFile(path string) *os.File {
	for _, ps := range r.procSubsts {
		if ps.path == path {
			return ps.outerFile
		}
	}
	return nil
}

// cleanupProcSubst closes pipe files and waits for process substitution
// goroutines to finish. Called after each statement completes.
func (r *Runner) cleanupProcSubst() {
	for _, ps := range r.procSubsts {
		ps.outerFile.Close()
		ps.wg.Wait()
	}
	r.procSubsts = r.procSubsts[:0]
}

func (r *Runner) updateExpandOpts() {
	r.ecfg.ReadDir2 = func(s string) ([]fs.DirEntry, error) {
		return r.readDirHandler(r.handlerCtx(r.ectx, todoPos), s)
	}
}

func (r *Runner) expandErr(err error) {
	if err == nil {
		return
	}
	errMsg := err.Error()
	fmt.Fprintln(r.stderr, errMsg)
	switch {
	case errors.As(err, &expand.UnsetParameterError{}):
	case errors.As(err, &expand.UnexpectedCommandError{}):
		// Defense in depth: should not happen now that CmdSubst is set,
		// but treat as fatal if it somehow leaks through.
	case errMsg == "invalid indirect expansion":
		// TODO: These errors are treated as fatal by bash.
		// Make the error type reflect that.
	case strings.HasSuffix(errMsg, "not supported"):
		// TODO: This "has suffix" is a temporary measure until the expand
		// package supports all syntax nodes like extended globbing.
	default:
		return // other cases do not exit
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
	e.r.setVar(name, vr)
	return nil // TODO: return any errors
}

func (e expandEnv) Each(fn func(name string, vr expand.Variable) bool) {
	e.r.writeEnv.Each(fn)
}

// limitWriter wraps an io.Writer and stops writing after n bytes.
// Excess bytes are silently discarded to prevent unbounded memory use
// in command substitution output.
type limitWriter struct {
	w io.Writer
	n int
}

func (lw *limitWriter) Write(p []byte) (int, error) {
	if lw.n <= 0 {
		return len(p), nil // discard silently
	}
	orig := len(p)
	if len(p) > lw.n {
		p = p[:lw.n]
	}
	n, err := lw.w.Write(p)
	lw.n -= n
	if err != nil {
		return n, err
	}
	// Report all input bytes as consumed even if truncated,
	// so callers (io.Copy) don't retry with the remainder.
	return orig, nil
}
