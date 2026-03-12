// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package wc_test

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

func cmdRunCtx(ctx context.Context, t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, interp.AllowedPaths([]string{dir}))
}

// FuzzWc fuzzes wc (default mode: lines, words, bytes) with arbitrary file content.
func FuzzWc(f *testing.F) {
	f.Add([]byte("hello world\n"))
	f.Add([]byte{})
	f.Add([]byte("no newline"))
	f.Add([]byte("a\x00b\nc\n"))
	f.Add(bytes.Repeat([]byte("x"), 4097))
	f.Add([]byte("\n\n\n"))
	f.Add(bytes.Repeat([]byte("word "), 100))

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

		_, _, code := cmdRunCtx(ctx, t, "wc input.txt", dir)
		if code != 0 && code != 1 {
			t.Errorf("wc unexpected exit code %d", code)
		}
	})
}

// FuzzWcLines fuzzes wc -l with arbitrary file content.
func FuzzWcLines(f *testing.F) {
	f.Add([]byte("line1\nline2\nline3\n"))
	f.Add([]byte{})
	f.Add([]byte("no newline"))
	f.Add([]byte("a\x00b\nc\n"))
	f.Add(bytes.Repeat([]byte("x"), 4097))
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

		_, _, code := cmdRunCtx(ctx, t, "wc -l input.txt", dir)
		if code != 0 && code != 1 {
			t.Errorf("wc -l unexpected exit code %d", code)
		}
	})
}

// FuzzWcBytes fuzzes wc -c with arbitrary file content.
func FuzzWcBytes(f *testing.F) {
	f.Add([]byte("hello\n"))
	f.Add([]byte{})
	f.Add([]byte("no newline"))
	f.Add([]byte("a\x00b\nc\n"))
	f.Add(bytes.Repeat([]byte("x"), 4097))

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

		_, _, code := cmdRunCtx(ctx, t, "wc -c input.txt", dir)
		if code != 0 && code != 1 {
			t.Errorf("wc -c unexpected exit code %d", code)
		}
	})
}

// FuzzWcStdin fuzzes wc reading from stdin via shell redirection.
func FuzzWcStdin(f *testing.F) {
	f.Add([]byte("hello world\n"))
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

		_, _, code := cmdRunCtx(ctx, t, "wc < stdin.txt", dir)
		if code != 0 && code != 1 {
			t.Errorf("wc stdin unexpected exit code %d", code)
		}
	})
}
