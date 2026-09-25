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

// errf writes an interpreter-level diagnostic (a policy rejection, redirect
// setup failure, expansion error, and similar) to the current statement's
// stderr — falling back to the pre-elevation stream while a statement's own
// write-target redirect is elevated (see currentStderr).
//
// errf is exclusively the interpreter's own meta-channel: every builtin's
// actual output goes through its own CallContext.Stdout/Stderr, a value
// passed by reference from r.stdout/r.stderr at dispatch time, never through
// errf. Falling back here closes a finding beyond what currentStderr and
// (*Runner).subshell already address: a statement can carry more than one
// redirect (e.g. "sudo true 2>>/allowed/log >"/some/expanded/path""), and
// once an earlier redirect on the SAME statement has elevated and installed
// its file as r.stderr, any later diagnostic on that statement — a
// subsequent redirect's own setup failure (which can embed that redirect's
// expanded, potentially attacker-influenced target path, including embedded
// newlines) or a command-argument expansion error — would otherwise be
// written through the already-elevated descriptor merely because r.stderr
// happens to currently be it, not because the diagnostic itself was ever
// authorized to elevate. Restricting the elevated descriptor's content to
// exactly what the authorized command itself writes through CallContext
// keeps the elevated write surface as narrow as SelectiveElevation's
// contract promises.
func (r *Runner) errf(format string, a ...any) {
	fmt.Fprintf(r.currentStderr(), format, a...)
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
