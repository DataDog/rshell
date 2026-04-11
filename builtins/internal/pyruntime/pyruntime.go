// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package pyruntime implements a sandboxed Python 3 interpreter for the
// python builtin shell command.
//
// # Security sandbox
//
// The interpreter is a from-scratch tree-walking evaluator that provides
// safety-by-design through:
//
//   - A module whitelist: only approved modules are importable.
//   - Read-only file access: open() is sandboxed to AllowedPaths via callCtx.OpenFile.
//   - Write/append/create modes in open() raise PermissionError.
//   - Dangerous modules (subprocess, socket, ctypes, tempfile, etc.) raise ImportError.
//   - A recursion depth limit of 500 frames.
//
// # Context cancellation
//
// Run executes Python in a goroutine and selects on ctx.Done(). If the
// context is cancelled the function returns exit code 1 immediately. Loop
// bodies check ctx.Done() at each iteration.
package pyruntime

import (
	"context"
	"fmt"
)

// Run executes Python source code in a sandboxed context.
// Returns the exit code: 0 = success, 1 = unhandled exception/error, N = sys.exit(N).
func Run(ctx context.Context, opts RunOpts) int {
	type result struct{ code int }
	ch := make(chan result, 1)
	go func() {
		ch <- result{code: runInternal(ctx, opts)}
	}()
	select {
	case r := <-ch:
		return r.code
	case <-ctx.Done():
		// Wait for the goroutine to finish before returning to avoid data races
		// on opts.Stderr: runInternal may still write traceback output after the
		// context fires. Waiting here is safe because the evaluator checks
		// ctx.Done() at each loop iteration and returns promptly.
		<-ch
		return 1
	}
}

func runInternal(ctx context.Context, opts RunOpts) (exitCode int) {
	// Parse
	mod, err := Parse(opts.Source+"\n", opts.SourceName)
	if err != nil {
		fmt.Fprintf(opts.Stderr, "  File %q\n    (at parse time)\nSyntaxError: %v\n", opts.SourceName, err)
		return 1
	}

	// Build globals: builtins + module-level names
	globals := makeBuiltins(&opts)
	globals["__name__"] = pyStr("__main__")
	globals["__file__"] = pyStr(opts.SourceName)

	// Module cache
	modules := map[string]*PyModule{}

	// Create evaluator; cleanup deregisters the goroutine's callObject entry.
	eval, cleanup := newEvaluator(ctx, &opts, globals, modules)
	defer cleanup()

	// Catch sys.exit and unhandled exceptions
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		switch sig := r.(type) {
		case controlSignal:
			if sig.kind == ctrlSysExit {
				if code, ok := sig.value.(*PyInt); ok {
					if n, ok2 := code.int64(); ok2 {
						exitCode = int(n)
						return
					}
				}
				exitCode = 1
			} else {
				exitCode = 1
			}
		case exceptionSignal:
			printTraceback(opts.Stderr, sig.exc)
			exitCode = 1
		default:
			// Real Go panic — re-panic
			panic(r)
		}
	}()

	eval.exec(mod.Body)
	return 0
}
