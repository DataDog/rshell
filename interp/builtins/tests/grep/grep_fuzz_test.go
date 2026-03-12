// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package grep_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"unicode/utf8"

	"github.com/DataDog/rshell/interp"
	"github.com/DataDog/rshell/interp/builtins/testutil"
)

func cmdRunCtx(ctx context.Context, t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, interp.AllowedPaths([]string{dir}))
}

// FuzzGrepFileContent fuzzes grep with a fixed pattern and arbitrary file content.
func FuzzGrepFileContent(f *testing.F) {
	f.Add([]byte("apple\nbanana\ncherry\n"), "banana")
	f.Add([]byte{}, "anything")
	f.Add([]byte("no newline"), "new")
	f.Add([]byte("a\x00b\nc\n"), "a")
	f.Add(bytes.Repeat([]byte("x"), 4097), "x")
	f.Add([]byte("\n\n\n"), ".")
	f.Add([]byte("hello world\nfoo bar\n"), "foo")
	f.Add([]byte{0xff, 0xfe}, "a")

	f.Fuzz(func(t *testing.T, input []byte, pattern string) {
		if len(input) > 1<<20 {
			return
		}
		// Skip patterns containing non-UTF-8 sequences: the shell parser's
		// tokenizer rejects them before grep runs, so they exercise the parser
		// error path rather than the grep builtin.
		if !utf8.ValidString(pattern) {
			return
		}
		// Skip patterns that would be problematic in shell quoting or cause the
		// shell parser to fail before grep runs.
		for _, c := range pattern {
			if c == '\'' || c == '\x00' || c == '\n' {
				return
			}
		}
		if len(pattern) == 0 {
			return
		}
		if len(pattern) > 100 {
			return
		}

		dir := t.TempDir()
		err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Use single-quoted pattern to avoid shell interpretation
		script := "grep '" + pattern + "' input.txt"
		_, _, code := cmdRunCtx(ctx, t, script, dir)
		// grep exits 0 (match found), 1 (no match), or 2 (error/invalid regex)
		if code != 0 && code != 1 && code != 2 {
			t.Errorf("grep unexpected exit code %d", code)
		}
	})
}

// FuzzGrepStdin fuzzes grep reading from stdin with arbitrary content.
func FuzzGrepStdin(f *testing.F) {
	f.Add([]byte("apple\nbanana\ncherry\n"))
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
		err := os.WriteFile(filepath.Join(dir, "stdin.txt"), input, 0644)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtx(ctx, t, "grep '.' < stdin.txt", dir)
		if code != 0 && code != 1 && code != 2 {
			t.Errorf("grep stdin unexpected exit code %d", code)
		}
	})
}

// FuzzGrepFlags fuzzes grep with various flags and arbitrary file content.
func FuzzGrepFlags(f *testing.F) {
	f.Add([]byte("Hello\nworld\nHELLO\n"), true, false)
	f.Add([]byte("line1\nline2\n"), false, true)
	f.Add([]byte{}, true, true)
	f.Add([]byte("no newline"), false, false)
	f.Add(bytes.Repeat([]byte("abc\n"), 100), true, false)

	f.Fuzz(func(t *testing.T, input []byte, caseInsensitive bool, invertMatch bool) {
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

		flags := ""
		if caseInsensitive {
			flags += " -i"
		}
		if invertMatch {
			flags += " -v"
		}

		script := "grep" + flags + " 'a' input.txt"
		_, _, code := cmdRunCtx(ctx, t, script, dir)
		if code != 0 && code != 1 && code != 2 {
			t.Errorf("grep%s unexpected exit code %d", flags, code)
		}
	})
}
