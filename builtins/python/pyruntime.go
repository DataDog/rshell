// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package python

import (
	"bufio"
	"context"
	"fmt"
	"io"
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
	// Propagate the execution context into RunOpts so that sandbox I/O calls
	// (Open, Stat, ReadDir) respect the shell's cancellation deadline.
	opts.Ctx = ctx

	// Wrap stdin in a single global LimitReader so that all input() calls and
	// sys.stdin.read*() calls share one cumulative byte budget. Without this,
	// each input() call gets a fresh 1 MiB window, allowing a script that calls
	// input() in a loop to read unbounded data from /dev/zero-like sources.
	if opts.Stdin != nil {
		opts.Stdin = io.LimitReader(opts.Stdin, int64(maxFileReadBytes))
		// A single persistent bufio.Reader shared across all input() and
		// sys.stdin.readline() calls so that read-ahead bytes are not dropped
		// between calls.
		opts.stdinReader = bufio.NewReader(opts.Stdin)
	}

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
