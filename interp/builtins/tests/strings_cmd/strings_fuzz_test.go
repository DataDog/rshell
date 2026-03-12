// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package strings_cmd_test

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

// FuzzStrings fuzzes strings with arbitrary file content.
func FuzzStrings(f *testing.F) {
	f.Add([]byte("hello world\x00\x01\x02binary\x00readable text\n"))
	f.Add([]byte{})
	f.Add([]byte{0x00, 0x01, 0x02, 0x03})
	f.Add([]byte("all printable text\n"))
	f.Add(bytes.Repeat([]byte{0xff}, 4097))
	f.Add(bytes.Repeat([]byte("abcd"), 1024))
	f.Add([]byte("short\x00ab\x00longer string here\x00"))

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			return
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "input.bin"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtx(ctx, t, "strings input.bin", dir)
		if code != 0 && code != 1 {
			t.Errorf("strings unexpected exit code %d", code)
		}
	})
}

// FuzzStringsMinLen fuzzes strings -n N with arbitrary file content and min length.
func FuzzStringsMinLen(f *testing.F) {
	f.Add([]byte("hello world\x00\x01\x02binary\n"), int64(4))
	f.Add([]byte("ab\x00cdef\x00gh\n"), int64(1))
	f.Add([]byte("ab\x00cdef\x00gh\n"), int64(10))
	f.Add([]byte{}, int64(4))
	f.Add(bytes.Repeat([]byte("x"), 100), int64(50))

	f.Fuzz(func(t *testing.T, input []byte, minLen int64) {
		if len(input) > 1<<20 {
			return
		}
		if minLen < 1 || minLen > 1000 {
			return
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "input.bin"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtx(ctx, t, fmt.Sprintf("strings -n %d input.bin", minLen), dir)
		if code != 0 && code != 1 {
			t.Errorf("strings -n %d unexpected exit code %d", minLen, code)
		}
	})
}

// FuzzStringsRadix fuzzes strings -t with offset radix formatting.
func FuzzStringsRadix(f *testing.F) {
	f.Add([]byte("hello\x00world\x00text\n"), "o")
	f.Add([]byte("hello\x00world\x00text\n"), "d")
	f.Add([]byte("hello\x00world\x00text\n"), "x")

	f.Fuzz(func(t *testing.T, input []byte, radix string) {
		if len(input) > 1<<20 {
			return
		}
		if radix != "o" && radix != "d" && radix != "x" {
			return
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "input.bin"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtx(ctx, t, fmt.Sprintf("strings -t %s input.bin", radix), dir)
		if code != 0 && code != 1 {
			t.Errorf("strings -t %s unexpected exit code %d", radix, code)
		}
	})
}

// FuzzStringsStdin fuzzes strings reading from stdin.
func FuzzStringsStdin(f *testing.F) {
	f.Add([]byte("hello\x00\x01\x02world\n"))
	f.Add([]byte{})
	f.Add([]byte{0x00, 0x01, 0x02, 0x03})

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			return
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "stdin.bin"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtx(ctx, t, "strings < stdin.bin", dir)
		if code != 0 && code != 1 {
			t.Errorf("strings stdin unexpected exit code %d", code)
		}
	})
}
