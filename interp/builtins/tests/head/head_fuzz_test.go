// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package head_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/rshell/interp"
	"github.com/DataDog/rshell/interp/builtins/testutil"
)

func cmdRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return testutil.RunScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

func cmdRunCtx(ctx context.Context, t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, interp.AllowedPaths([]string{dir}))
}

// FuzzHeadLines fuzzes head -n N with arbitrary file content.
func FuzzHeadLines(f *testing.F) {
	f.Add([]byte("line1\nline2\nline3\n"), int64(2))
	f.Add([]byte{}, int64(0))
	f.Add([]byte("no newline"), int64(1))
	f.Add([]byte("a\x00b\nc\n"), int64(2))
	f.Add(bytes.Repeat([]byte("x"), 4097), int64(1))
	f.Add([]byte("\n\n\n"), int64(5))
	f.Add(bytes.Repeat([]byte("y"), 4096), int64(1))
	f.Add([]byte("hello\nworld\n"), int64(10))

	f.Fuzz(func(t *testing.T, input []byte, n int64) {
		if len(input) > 1<<20 {
			return
		}
		if n < 0 {
			return
		}
		if n > 10000 {
			n = 10000
		}

		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		stdout, _, code := cmdRunCtx(ctx, t, fmt.Sprintf("head -n %d input.txt", n), dir)
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d", code)
		}

		// If successful, output line count must be <= n
		if code == 0 && n >= 0 {
			lineCount := strings.Count(stdout, "\n")
			if int64(lineCount) > n {
				t.Errorf("head -n %d produced %d newlines in output", n, lineCount)
			}
		}
	})
}

// FuzzHeadBytes fuzzes head -c N with arbitrary file content.
func FuzzHeadBytes(f *testing.F) {
	f.Add([]byte("line1\nline2\nline3\n"), int64(5))
	f.Add([]byte{}, int64(0))
	f.Add([]byte("no newline"), int64(3))
	f.Add([]byte("a\x00b\nc\n"), int64(4))
	f.Add(bytes.Repeat([]byte("x"), 4097), int64(4096))
	f.Add([]byte("\n\n\n"), int64(2))

	f.Fuzz(func(t *testing.T, input []byte, n int64) {
		if len(input) > 1<<20 {
			return
		}
		if n < 0 {
			return
		}
		if n > 10000 {
			n = 10000
		}

		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		stdout, _, code := cmdRunCtx(ctx, t, fmt.Sprintf("head -c %d input.txt", n), dir)
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d", code)
		}

		// If successful, output byte count must be <= n
		if code == 0 {
			outLen := int64(len(stdout))
			if outLen > n {
				t.Errorf("head -c %d produced %d bytes of output", n, outLen)
			}
		}
	})
}

// FuzzHeadStdin fuzzes head -n N reading from stdin via shell redirection.
func FuzzHeadStdin(f *testing.F) {
	f.Add([]byte("line1\nline2\nline3\n"), int64(2))
	f.Add([]byte{}, int64(1))
	f.Add([]byte("no newline"), int64(1))
	f.Add([]byte("a\x00b\nc\n"), int64(2))
	f.Add(bytes.Repeat([]byte("x"), 4097), int64(1))
	f.Add([]byte("\n\n\n"), int64(3))

	f.Fuzz(func(t *testing.T, input []byte, n int64) {
		if len(input) > 1<<20 {
			return
		}
		if n < 0 {
			return
		}
		if n > 10000 {
			n = 10000
		}

		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "stdin.txt"), input, 0644)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtx(ctx, t, fmt.Sprintf("head -n %d < stdin.txt", n), dir)
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d (stdin mode)", code)
		}
	})
}
