// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package echo_test

import (
	"context"
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

// FuzzEcho fuzzes echo with arbitrary arguments.
func FuzzEcho(f *testing.F) {
	f.Add("hello world")
	f.Add("")
	f.Add("a\tb\tc")
	f.Add("line1\\nline2")
	f.Add("\\x41\\x42\\x43")
	f.Add("\\u0041")
	f.Add("no newline\\c")
	f.Add("back\\\\slash")

	f.Fuzz(func(t *testing.T, arg string) {
		if len(arg) > 1000 {
			return
		}
		if !utf8.ValidString(arg) {
			return
		}
		// Skip characters problematic for shell parsing.
		for _, c := range arg {
			if c == '\'' || c == '\x00' || c == '\n' {
				return
			}
		}

		dir := t.TempDir()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtx(ctx, t, "echo '"+arg+"'", dir)
		if code != 0 {
			t.Errorf("echo unexpected exit code %d", code)
		}
	})
}

// FuzzEchoEscapes fuzzes echo -e with arbitrary escape sequences.
func FuzzEchoEscapes(f *testing.F) {
	f.Add("hello\\nworld")
	f.Add("\\t\\t\\t")
	f.Add("\\x00\\x01\\xff")
	f.Add("\\0101")
	f.Add("\\u0048\\u0065\\u006c")
	f.Add("abc\\cdef")
	f.Add("\\a\\b\\f\\r\\v")
	f.Add("\\\\")

	f.Fuzz(func(t *testing.T, arg string) {
		if len(arg) > 1000 {
			return
		}
		if !utf8.ValidString(arg) {
			return
		}
		for _, c := range arg {
			if c == '\'' || c == '\x00' || c == '\n' {
				return
			}
		}

		dir := t.TempDir()

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtx(ctx, t, "echo -e '"+arg+"'", dir)
		if code != 0 {
			t.Errorf("echo -e unexpected exit code %d", code)
		}
	})
}
