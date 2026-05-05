// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"context"
	"fmt"
	"io"
	"os"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/allowedpaths"
)

var todoPos syntax.Pos // for handlerCtx callers where we don't yet have a position

func (r *Runner) handlerCtx(ctx context.Context, pos syntax.Pos) context.Context {
	hc := HandlerContext{
		Env:    &overlayEnviron{parent: r.writeEnv},
		Dir:    r.Dir,
		Pos:    pos,
		Stdout: r.stdout,
		Stderr: r.stderr,
	}
	if r.stdin != nil { // do not leave hc.Stdin as a typed nil
		hc.Stdin = r.stdin
	}
	return context.WithValue(ctx, handlerCtxKey{}, hc)
}

func (r *Runner) errf(format string, a ...any) {
	fmt.Fprintf(r.stderr, format, a...)
}

// applyNewWorkDir is invoked by call() after a builtin (cd) returns a
// non-empty Result.NewWorkDir on a successful exit. It rotates the
// previous directory into $OLDPWD, installs the new directory as r.Dir,
// and refreshes $PWD so that subsequent path resolution and parameter
// expansion reflect the change.
//
// For OLDPWD, bash uses the current shell $PWD variable value (not the
// internal runner directory r.Dir). This matters when scripts assign PWD
// inline before calling cd, e.g. `PWD=/sentinel cd sub` — bash records
// /sentinel as OLDPWD. We therefore read $PWD from the shell variables
// rather than r.Dir.
//
// The distinction between unset and empty $PWD matters: bash uses the
// empty string as-is for OLDPWD when $PWD is set-but-empty (e.g. after
// `PWD="" cd sub`), but falls back to internal bookkeeping only when
// $PWD is truly unset.
//
// The builtin is expected to have already validated newDir against the
// sandbox; this method only performs state mutation.
//
// All three state updates (OLDPWD, r.Dir, PWD) are committed atomically:
// if OLDPWD is written successfully but the subsequent PWD write fails,
// OLDPWD is rolled back to its previous value so that the variable store
// remains consistent. r.Dir is not changed until both variable writes
// succeed, preventing the runner's working directory from diverging from $PWD.
func (r *Runner) applyNewWorkDir(newDir string) {
	// Prefer the shell $PWD variable as the old directory to record in
	// OLDPWD, matching bash's behaviour for inline PWD assignments.
	// Use the ok boolean to distinguish truly-unset $PWD (fall back to
	// r.Dir) from set-but-empty $PWD (use the empty string as-is,
	// matching bash: `PWD="" cd x` leaves OLDPWD empty).
	old, ok := r.lookupVarString("PWD")
	if !ok {
		// $PWD is truly unset: fall back to internal dir but skip the
		// OLDPWD update when that is also empty (runner has no prior dir).
		old = r.Dir
	}
	// Write OLDPWD and PWD atomically: if PWD write fails after OLDPWD
	// succeeds, roll back OLDPWD so that the variable store remains
	// consistent (no partial update where OLDPWD changed but PWD did not).
	// Always set OLDPWD when $PWD was explicitly set (even to empty),
	// matching bash: `PWD="" cd sub` sets OLDPWD="". Only skip the
	// OLDPWD update when $PWD was unset AND the fallback is also empty.
	var prevOLDPWD string
	var prevOLDPWDSet bool
	wroteOLDPWD := false
	if ok || old != "" {
		// Capture prior OLDPWD before overwriting it so we can roll back.
		prevOLDPWD, prevOLDPWDSet = r.lookupVarString("OLDPWD")
		if err := r.setVarErr("OLDPWD", expand.Variable{Set: true, Kind: expand.String, Str: old}); err != nil {
			r.errf("OLDPWD: %v\n", err)
			r.exit.code = 1
			return
		}
		wroteOLDPWD = true
	}
	if err := r.setVarErr("PWD", expand.Variable{Set: true, Kind: expand.String, Str: newDir}); err != nil {
		r.errf("PWD: %v\n", err)
		r.exit.code = 1
		// Roll back OLDPWD so the variable store stays consistent:
		// the cd did not complete, so OLDPWD should be unchanged.
		if wroteOLDPWD {
			_ = r.setVarErr("OLDPWD", expand.Variable{Set: prevOLDPWDSet, Kind: expand.String, Str: prevOLDPWD})
		}
		return
	}
	// Only update the internal directory after both variable writes succeeded.
	r.Dir = newDir
}

// lookupVarString returns the string value of a shell variable and a
// boolean indicating whether it was set. It is the bridge between
// builtins.CallContext.LookupVar and Runner.lookupVar so the closure does
// not need to be duplicated at every CallContext construction site.
func (r *Runner) lookupVarString(name string) (string, bool) {
	vr := r.lookupVar(name)
	if !vr.IsSet() {
		return "", false
	}
	return vr.String(), true
}

func (r *Runner) stop(ctx context.Context) bool {
	if r.exit.exiting {
		return true
	}
	if err := ctx.Err(); err != nil {
		r.exit.fatal(err)
		return true
	}
	return false
}

func (r *Runner) open(ctx context.Context, path string, flags int, mode os.FileMode, print bool) (io.ReadWriteCloser, error) {
	f, err := r.openHandler(r.handlerCtx(ctx, todoPos), path, flags, mode)
	// TODO: support wrapped PathError returned from openHandler.
	switch err.(type) {
	case nil:
		return f, nil
	case *os.PathError:
		err = allowedpaths.PortablePathError(err)
		if print {
			r.errf("%v\n", err)
		}
	default: // handler's custom fatal error
		r.exit.fatal(err)
	}
	return nil, err
}
