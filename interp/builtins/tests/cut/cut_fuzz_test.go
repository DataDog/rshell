// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package cut_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/DataDog/rshell/interp"
	"github.com/DataDog/rshell/interp/builtins/testutil"
)

// cmdRunCtxFuzz provides the test helper for fuzz tests.
// The cut package already has cmdRunCtx in the existing test file,
// but that uses a different (inline) implementation. We use a
// differently-named function to avoid redeclaration.
func cmdRunCtxFuzz(ctx context.Context, t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, interp.AllowedPaths([]string{dir}))
}

// FuzzCutFields fuzzes cut -f with arbitrary file content and field specs.
func FuzzCutFields(f *testing.F) {
	f.Add([]byte("a\tb\tc\n"), "1")
	f.Add([]byte("a\tb\tc\n"), "1,3")
	f.Add([]byte("a\tb\tc\n"), "2-")
	f.Add([]byte("a\tb\tc\n"), "-2")
	f.Add([]byte("a\tb\tc\n"), "1-3")
	f.Add([]byte{}, "1")
	f.Add([]byte("no tab\n"), "1")
	f.Add([]byte("a\x00b\tc\n"), "2")
	f.Add(bytes.Repeat([]byte("x\t"), 100), "1,50,100")
	f.Add([]byte("\n\n\n"), "1")

	f.Fuzz(func(t *testing.T, input []byte, fieldSpec string) {
		if len(input) > 1<<20 {
			return
		}
		if len(fieldSpec) == 0 || len(fieldSpec) > 50 {
			return
		}
		if !utf8.ValidString(fieldSpec) {
			return
		}
		// Only allow characters valid in field specs.
		for _, c := range fieldSpec {
			if !((c >= '0' && c <= '9') || c == ',' || c == '-') {
				return
			}
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtxFuzz(ctx, t, fmt.Sprintf("cut -f %s input.txt", fieldSpec), dir)
		if code != 0 && code != 1 {
			t.Errorf("cut -f %s unexpected exit code %d", fieldSpec, code)
		}
	})
}

// FuzzCutBytes fuzzes cut -b with arbitrary file content and byte specs.
func FuzzCutBytes(f *testing.F) {
	f.Add([]byte("hello world\n"), "1-5")
	f.Add([]byte("hello world\n"), "1,3,5")
	f.Add([]byte("hello world\n"), "6-")
	f.Add([]byte{}, "1")
	f.Add([]byte("a\x00b\nc\n"), "1-3")
	f.Add(bytes.Repeat([]byte("x"), 4097), "1-100")

	f.Fuzz(func(t *testing.T, input []byte, byteSpec string) {
		if len(input) > 1<<20 {
			return
		}
		if len(byteSpec) == 0 || len(byteSpec) > 50 {
			return
		}
		if !utf8.ValidString(byteSpec) {
			return
		}
		for _, c := range byteSpec {
			if !((c >= '0' && c <= '9') || c == ',' || c == '-') {
				return
			}
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtxFuzz(ctx, t, fmt.Sprintf("cut -b %s input.txt", byteSpec), dir)
		if code != 0 && code != 1 {
			t.Errorf("cut -b %s unexpected exit code %d", byteSpec, code)
		}
	})
}

// FuzzCutDelimiter fuzzes cut -f with a custom delimiter.
func FuzzCutDelimiter(f *testing.F) {
	f.Add([]byte("a:b:c\n"), ":", "1,3")
	f.Add([]byte("a,b,c\n"), ",", "2")
	f.Add([]byte("a|b|c\n"), "|", "1-2")
	f.Add([]byte("no delim\n"), ":", "1")

	f.Fuzz(func(t *testing.T, input []byte, delim string, fieldSpec string) {
		if len(input) > 1<<20 {
			return
		}
		if len(delim) != 1 {
			return
		}
		if len(fieldSpec) == 0 || len(fieldSpec) > 50 {
			return
		}
		if !utf8.ValidString(fieldSpec) || !utf8.ValidString(delim) {
			return
		}
		// Delimiter must be shell-safe.
		d := delim[0]
		if d == '\'' || d == '\x00' || d == '\n' || d == '\\' || d == '"' || d == '`' || d == '$' {
			return
		}
		for _, c := range fieldSpec {
			if !((c >= '0' && c <= '9') || c == ',' || c == '-') {
				return
			}
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		script := fmt.Sprintf("cut -d '%s' -f %s input.txt", delim, fieldSpec)
		_, _, code := cmdRunCtxFuzz(ctx, t, script, dir)
		if code != 0 && code != 1 {
			t.Errorf("cut -d '%s' -f %s unexpected exit code %d", delim, fieldSpec, code)
		}
	})
}

// FuzzCutStdin fuzzes cut reading from stdin.
func FuzzCutStdin(f *testing.F) {
	f.Add([]byte("a\tb\tc\n"))
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

		_, _, code := cmdRunCtxFuzz(ctx, t, "cut -f 1 < stdin.txt", dir)
		if code != 0 && code != 1 {
			t.Errorf("cut stdin unexpected exit code %d", code)
		}
	})
}
