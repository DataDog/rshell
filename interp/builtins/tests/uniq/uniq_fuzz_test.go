// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package uniq_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/rshell/interp"
	"github.com/DataDog/rshell/interp/builtins/testutil"
)

func cmdRunCtx(ctx context.Context, t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, interp.AllowedPaths([]string{dir}))
}

// FuzzUniq fuzzes uniq with arbitrary file content.
func FuzzUniq(f *testing.F) {
	f.Add([]byte("a\na\nb\nb\nc\n"))
	f.Add([]byte{})
	f.Add([]byte("no newline"))
	f.Add([]byte("a\x00b\nc\n"))
	f.Add(bytes.Repeat([]byte("x\n"), 100))
	f.Add([]byte("\n\n\n"))
	f.Add([]byte("AAA\naaa\nAAA\n"))

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			return
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtx(ctx, t, "uniq input.txt", dir)
		if code != 0 && code != 1 {
			t.Errorf("uniq unexpected exit code %d", code)
		}
	})
}

// FuzzUniqCount fuzzes uniq -c with arbitrary file content.
func FuzzUniqCount(f *testing.F) {
	f.Add([]byte("a\na\nb\nb\nc\n"))
	f.Add([]byte{})
	f.Add([]byte("no newline"))
	f.Add([]byte("a\na\na\n"))

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			return
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtx(ctx, t, "uniq -c input.txt", dir)
		if code != 0 && code != 1 {
			t.Errorf("uniq -c unexpected exit code %d", code)
		}
	})
}

// FuzzUniqFlags fuzzes uniq with various flag combinations.
func FuzzUniqFlags(f *testing.F) {
	f.Add([]byte("a\na\nb\nb\nc\n"), true, false, false, int64(0), int64(0))
	f.Add([]byte("AAA\naaa\nAAA\n"), false, true, false, int64(0), int64(0))
	f.Add([]byte("  a x\n  a y\n  b x\n"), false, false, false, int64(1), int64(0))
	f.Add([]byte("aaa\naab\naac\n"), false, false, false, int64(0), int64(2))
	f.Add([]byte("a\na\nb\n"), false, false, true, int64(0), int64(0))

	f.Fuzz(func(t *testing.T, input []byte, repeated bool, ignoreCase bool, unique bool, skipFields int64, skipChars int64) {
		if len(input) > 1<<20 {
			return
		}
		if skipFields < 0 || skipFields > 100 {
			return
		}
		if skipChars < 0 || skipChars > 100 {
			return
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		flags := ""
		if repeated {
			flags += " -d"
		}
		if ignoreCase {
			flags += " -i"
		}
		if unique {
			flags += " -u"
		}
		if skipFields > 0 {
			flags += fmt.Sprintf(" -f %d", skipFields)
		}
		if skipChars > 0 {
			flags += fmt.Sprintf(" -s %d", skipChars)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtx(ctx, t, "uniq"+flags+" input.txt", dir)
		if code != 0 && code != 1 {
			t.Errorf("uniq%s unexpected exit code %d", flags, code)
		}
	})
}

// FuzzUniqStdin fuzzes uniq reading from stdin.
func FuzzUniqStdin(f *testing.F) {
	f.Add([]byte("a\na\nb\nb\nc\n"))
	f.Add([]byte{})
	f.Add([]byte("no newline"))

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			return
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "stdin.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtx(ctx, t, "uniq < stdin.txt", dir)
		if code != 0 && code != 1 {
			t.Errorf("uniq stdin unexpected exit code %d", code)
		}
	})
}
