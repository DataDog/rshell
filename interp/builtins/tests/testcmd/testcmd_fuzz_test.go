// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package testcmd_test

import (
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

func cmdRunCtx(ctx context.Context, t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, interp.AllowedPaths([]string{dir}))
}

// FuzzTestStringOps fuzzes test with string comparison operators.
func FuzzTestStringOps(f *testing.F) {
	f.Add("hello", "hello", "=")
	f.Add("hello", "world", "!=")
	f.Add("", "", "=")
	f.Add("abc", "def", "=")
	f.Add("a", "b", "!=")

	f.Fuzz(func(t *testing.T, left, right, op string) {
		if len(left) > 100 || len(right) > 100 {
			return
		}
		if op != "=" && op != "!=" {
			return
		}
		if !utf8.ValidString(left) || !utf8.ValidString(right) {
			return
		}
		for _, s := range []string{left, right} {
			for _, c := range s {
				if c == '\'' || c == '\x00' || c == '\n' || c == ']' {
					return
				}
			}
		}

		dir := t.TempDir()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		script := fmt.Sprintf("test '%s' %s '%s'", left, op, right)
		_, _, code := cmdRunCtx(ctx, t, script, dir)
		if code != 0 && code != 1 {
			t.Errorf("test string op unexpected exit code %d", code)
		}
	})
}

// FuzzTestIntegerOps fuzzes test with integer comparison operators.
func FuzzTestIntegerOps(f *testing.F) {
	f.Add(int64(1), int64(2), "-lt")
	f.Add(int64(5), int64(5), "-eq")
	f.Add(int64(10), int64(3), "-gt")
	f.Add(int64(0), int64(0), "-le")
	f.Add(int64(-1), int64(1), "-ne")

	f.Fuzz(func(t *testing.T, left, right int64, op string) {
		switch op {
		case "-eq", "-ne", "-lt", "-le", "-gt", "-ge":
		default:
			return
		}
		// Clamp to reasonable range.
		if left > 1<<31 || left < -(1<<31) || right > 1<<31 || right < -(1<<31) {
			return
		}

		dir := t.TempDir()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		script := fmt.Sprintf("test %d %s %d", left, op, right)
		_, _, code := cmdRunCtx(ctx, t, script, dir)
		if code != 0 && code != 1 {
			t.Errorf("test %d %s %d unexpected exit code %d", left, op, right, code)
		}
	})
}

// FuzzTestFileOps fuzzes test with file test operators on random filenames.
func FuzzTestFileOps(f *testing.F) {
	f.Add("-e", true)
	f.Add("-f", true)
	f.Add("-d", false)
	f.Add("-s", true)
	f.Add("-r", true)
	f.Add("-z", false)

	f.Fuzz(func(t *testing.T, op string, createFile bool) {
		switch op {
		case "-e", "-f", "-d", "-s", "-r", "-w", "-x", "-h", "-L", "-p":
		default:
			return
		}

		dir := t.TempDir()
		target := "testfile.txt"
		if createFile {
			if err := os.WriteFile(filepath.Join(dir, target), []byte("content"), 0644); err != nil {
				t.Fatal(err)
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		script := fmt.Sprintf("test %s %s", op, target)
		_, _, code := cmdRunCtx(ctx, t, script, dir)
		if code != 0 && code != 1 {
			t.Errorf("test %s unexpected exit code %d", op, code)
		}
	})
}

// FuzzTestStringUnary fuzzes test with -z and -n string tests.
func FuzzTestStringUnary(f *testing.F) {
	f.Add("hello", "-z")
	f.Add("", "-z")
	f.Add("hello", "-n")
	f.Add("", "-n")

	f.Fuzz(func(t *testing.T, arg, op string) {
		if len(arg) > 200 {
			return
		}
		if op != "-z" && op != "-n" {
			return
		}
		if !utf8.ValidString(arg) {
			return
		}
		for _, c := range arg {
			if c == '\'' || c == '\x00' || c == '\n' || c == ']' {
				return
			}
		}

		dir := t.TempDir()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		script := fmt.Sprintf("test %s '%s'", op, arg)
		_, _, code := cmdRunCtx(ctx, t, script, dir)
		if code != 0 && code != 1 {
			t.Errorf("test %s unexpected exit code %d", op, code)
		}
	})
}
