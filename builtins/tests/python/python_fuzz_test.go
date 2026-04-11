// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package python_test

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

// FuzzPythonSource fuzzes arbitrary Python source code via python -c.
// The goal is to ensure gpython never panics regardless of input.
func FuzzPythonSource(f *testing.F) {
	f.Add("print('hello')")
	f.Add("import sys; sys.exit(0)")
	f.Add("raise ValueError('oops')")
	f.Add("def foo(: pass")                      // syntax error
	f.Add("x = 1/0")                             // runtime error
	f.Add("import os; os.system('id')")          // sandbox violation
	f.Add("open('/tmp/x', 'w')")                 // write blocked
	f.Add("import tempfile; tempfile.mkstemp()") // blocked module
	f.Add("while True: pass")                    // infinite loop (short ctx)
	f.Add("print('a' * 10000)")                  // large output
	f.Add("")                                    // empty source

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, src string) {
		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()

		// Use a tight timeout to prevent infinite loops from hanging the corpus.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		script := fmt.Sprintf("python -c %q", src)
		// We only care that it doesn't panic or hang.
		testutil.RunScriptCtx(ctx, t, script, dir, interp.AllowedPaths([]string{dir}))
	})
}

// FuzzPythonFileContent fuzzes arbitrary content in a script file.
func FuzzPythonFileContent(f *testing.F) {
	f.Add([]byte("print('hello')\n"))
	f.Add([]byte("import sys\nsys.exit(0)\n"))
	f.Add([]byte("raise RuntimeError('oops')\n"))
	f.Add([]byte("def foo(:\n    pass\n")) // syntax error
	f.Add([]byte(""))

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, content []byte) {
		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()

		scriptPath := dir + "/script.py"
		if err := writeFile(scriptPath, content); err != nil {
			t.Skip("write error:", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		testutil.RunScriptCtx(ctx, t, "python script.py", dir, interp.AllowedPaths([]string{dir}))
	})
}

func writeFile(path string, content []byte) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(content)
	return err
}
