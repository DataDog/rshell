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
	"os"
	"strings"

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
		return r.readDirHandler(r.handlerCtx(r.ectx, todoPos), s)
	}
}

// maxCmdSubstOutput is the maximum number of bytes a command substitution
// can capture before being truncated. This prevents memory exhaustion from
// commands that produce unbounded output.
const maxCmdSubstOutput = 1 << 20 // 1 MiB

// cmdSubst handles command substitution ($(...) and `...`).
// It runs the commands in a subshell and writes their stdout to w.
func (r *Runner) cmdSubst(w io.Writer, cs *syntax.CmdSubst) error {
	if len(cs.Stmts) == 0 {
		return nil
	}

	// $(<file) shortcut: read file contents directly without a subshell.
	if word := catShortcutArg(cs.Stmts[0]); word != nil && len(cs.Stmts) == 1 {
		path := r.literal(word)
		f, err := r.open(r.ectx, path, os.O_RDONLY, 0, true)
		if err != nil {
			// r.open already printed the error; set exit status and
			// return nil so the expand layer does not double-report.
			r.lastExpandExit = exitStatus{code: 1}
			r.lastExit = r.lastExpandExit
			return nil
		}
		defer f.Close()
		// If the path is a directory, silently produce empty output (matches bash).
		if st, ok := f.(interface{ Stat() (fs.FileInfo, error) }); ok {
			if fi, serr := st.Stat(); serr == nil && fi.IsDir() {
				r.lastExpandExit = exitStatus{code: 0}
				r.lastExit = r.lastExpandExit
				return nil
			}
		}
		_, err = io.Copy(w, io.LimitReader(f, maxCmdSubstOutput))
		var exitCode uint8
		if err != nil {
			exitCode = 1
		}
		r.lastExpandExit = exitStatus{code: exitCode}
		r.lastExit = r.lastExpandExit
		return err
	}

	// General case: run statements in a subshell, capturing stdout.
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

// catShortcutArg detects the $(<file) pattern: a single statement with no
// command and exactly one input redirection. Returns the redirect word if
// matched, nil otherwise.
func catShortcutArg(stmt *syntax.Stmt) *syntax.Word {
	if stmt.Cmd != nil || stmt.Negated || stmt.Background || stmt.Coprocess || stmt.Disown {
		return nil
	}
	if len(stmt.Redirs) != 1 {
		return nil
	}
	rd := stmt.Redirs[0]
	if rd.Op != syntax.RdrIn {
		return nil
	}
	return rd.Word
}

// limitWriter wraps a writer and stops writing after limit bytes.
type limitWriter struct {
	w     io.Writer
	limit int64
	n     int64
}

func (lw *limitWriter) Write(p []byte) (int, error) {
	if lw.n >= lw.limit {
		return len(p), nil // silently discard excess
	}
	remaining := lw.limit - lw.n
	if int64(len(p)) > remaining {
		if _, err := lw.w.Write(p[:remaining]); err != nil {
			return int(remaining), err
		}
		lw.n = lw.limit
		return len(p), nil // report all bytes consumed to avoid short-write errors
	}
	n, err := lw.w.Write(p)
	lw.n += int64(n)
	return n, err
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
		// Defense in depth: if the expand package encounters a command
		// substitution that our handler cannot process, treat it as fatal.
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
