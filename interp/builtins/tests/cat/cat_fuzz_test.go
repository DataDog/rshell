// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package cat_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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

// FuzzCat fuzzes cat with arbitrary file content and verifies output equals input.
func FuzzCat(f *testing.F) {
	f.Add([]byte("hello\nworld\n"))
	f.Add([]byte{})
	f.Add([]byte("no newline"))
	f.Add([]byte("a\x00b\n"))
	f.Add(bytes.Repeat([]byte("x"), 4097))
	f.Add([]byte("\n\n\n"))
	f.Add(bytes.Repeat([]byte("y"), 4096))
	f.Add([]byte{0xff, 0xfe, 0x00, 0x01})

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			return
		}

		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		stdout, _, code := cmdRunCtx(ctx, t, "cat input.txt", dir)
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d", code)
		}

		// cat must output exactly the file contents
		if code == 0 && stdout != string(input) {
			t.Errorf("cat output differs from input: got %d bytes, want %d bytes", len(stdout), len(input))
		}
	})
}

// FuzzCatNumberLines fuzzes cat -n with arbitrary file content.
func FuzzCatNumberLines(f *testing.F) {
	f.Add([]byte("line1\nline2\n"))
	f.Add([]byte{})
	f.Add([]byte("no newline"))
	f.Add([]byte("a\x00b\nc\n"))
	f.Add([]byte("\n\n\n"))

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			return
		}

		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtx(ctx, t, "cat -n input.txt", dir)
		if code != 0 && code != 1 {
			t.Errorf("cat -n unexpected exit code %d", code)
		}
	})
}

// FuzzCatStdin fuzzes cat reading from stdin via shell redirection.
func FuzzCatStdin(f *testing.F) {
	f.Add([]byte("hello\nworld\n"))
	f.Add([]byte{})
	f.Add([]byte("no newline"))
	f.Add([]byte("a\x00b\n"))
	f.Add(bytes.Repeat([]byte("x"), 4097))
	f.Add([]byte("\n\n\n"))

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			return
		}

		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "stdin.txt"), input, 0644)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		stdout, _, code := cmdRunCtx(ctx, t, "cat < stdin.txt", dir)
		if code != 0 && code != 1 {
			t.Errorf("cat stdin unexpected exit code %d", code)
		}

		if code == 0 && stdout != string(input) {
			t.Errorf("cat stdin output differs from input: got %d bytes, want %d bytes", len(stdout), len(input))
		}
	})
}
