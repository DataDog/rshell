// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package rg_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

func cmdRunCtxFuzz(ctx context.Context, t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, interp.AllowedPaths([]string{dir}))
}

func validRgPattern(pattern string) bool {
	if !utf8.ValidString(pattern) {
		return false
	}
	if len(pattern) == 0 || len(pattern) > 100 {
		return false
	}
	for _, c := range pattern {
		if c == '\'' || c == '\x00' || c == '\n' {
			return false
		}
		// C0/DEL/C1 control chars confuse the shell script parser.
		if c < 0x20 || c == 0x7f || (c >= 0x80 && c < 0xa0) {
			return false
		}
	}
	return true
}

// FuzzRgFileContent fuzzes rg with a fixed pattern and arbitrary file content.
// Edge cases mirror grep's: binary content, null bytes, lines at the 1 MiB
// MaxLineBytes cap, invalid UTF-8, and CRLF.
func FuzzRgFileContent(f *testing.F) {
	f.Add([]byte("apple\nbanana\ncherry\n"), "banana")
	f.Add([]byte{}, "anything")
	f.Add([]byte("no newline"), "new")
	f.Add([]byte("a\x00b\nc\n"), "a")
	f.Add(bytes.Repeat([]byte("x"), 4097), "x")
	f.Add([]byte("\n\n\n"), ".")
	f.Add([]byte("hello world\nfoo bar\n"), "foo")
	f.Add([]byte{0xff, 0xfe}, "a")
	// Lines at/over the 1 MiB MaxLineBytes cap.
	f.Add(append(bytes.Repeat([]byte("a"), 1<<20-1), '\n'), "a")
	f.Add(append(bytes.Repeat([]byte("a"), 1<<20), '\n'), "a")
	// CRLF line endings.
	f.Add([]byte("hello\r\nworld\r\n"), "hello")
	// Invalid UTF-8 sequences.
	f.Add([]byte{0xfc, 0x80, 0x80, 0x80, 0x80, 0xaf, '\n'}, "a")
	f.Add([]byte{0xed, 0xa0, 0x80, '\n'}, "a")
	// Null bytes anywhere in content.
	f.Add([]byte{0x00, 0x00, '\n'}, "a")
	// Regex metacharacters in content being matched.
	f.Add([]byte("a.b\na*b\na[b\n"), "a.b")
	f.Add([]byte("(test)\n[bracket]\n"), "test")
	// Word-boundary content.
	f.Add([]byte("foo foobar barfoo\n"), "foo")
	// Multibyte content.
	f.Add([]byte("héllo\nmünchen\n"), "l")
	// Binary magic bytes.
	f.Add([]byte("\x7fELF\x02\x01\x01\n"), "ELF")
	f.Add([]byte("MZ\x90\x00\x03\n"), "MZ")
	f.Add([]byte("PK\x03\x04\n"), "PK")

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, input []byte, pattern string) {
		if t.Context().Err() != nil {
			return
		}
		if len(input) > 1<<20 {
			return
		}
		if !validRgPattern(pattern) {
			return
		}

		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()

		if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel() // safety net if t.Fatal fires before explicit cancel
		script := "rg '" + pattern + "' input.txt"
		_, _, code := cmdRunCtxFuzz(ctx, t, script, dir)
		cancel()
		if t.Context().Err() != nil {
			return
		}
		if code != 0 && code != 1 && code != 2 {
			t.Errorf("rg unexpected exit code %d", code)
		}
	})
}

// FuzzRgPatterns fuzzes rg with arbitrary regex patterns on fixed content.
// Edge cases: ReDoS-class backtracking patterns (mitigated by RE2), anchors,
// character classes, and empty patterns.
func FuzzRgPatterns(f *testing.F) {
	f.Add([]byte("hello world\nfoo bar\n"), "hel+o")
	f.Add([]byte("aaa\nbbb\n"), "a*")
	f.Add([]byte("test123\n"), "[0-9]+")
	f.Add([]byte("(parens)\n"), "[(]")
	f.Add([]byte("hello\nworld\n"), "^hello")
	f.Add([]byte("hello\nworld\n"), "world$")
	f.Add([]byte("hello\n"), "^hello$")
	// Pathological backtracking patterns (ReDoS class) - RE2 must stay linear.
	f.Add([]byte("aaaaaaaaaaaaaab\n"), "a*a*b")
	f.Add([]byte("aaaaaaaaaaaaaaaa\n"), "(a+)+")
	f.Add([]byte("ababababababababab!\n"), "([a-z]+)*")
	f.Add([]byte("abc\n"), ".")
	f.Add([]byte("\n"), ".")
	f.Add([]byte("hello123\n"), "[[:alpha:]]")
	f.Add([]byte("hello123\n"), "[[:digit:]]")
	f.Add([]byte("HELLO\n"), "[[:upper:]]")
	f.Add([]byte("hello\n"), "")
	f.Add([]byte("aaaa\n"), "a{1,4}")
	// Invalid regex syntax - must error cleanly (exit 2), never panic.
	f.Add([]byte("x\n"), "(")
	f.Add([]byte("x\n"), "[")
	f.Add([]byte("x\n"), "*")

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, input []byte, pattern string) {
		if t.Context().Err() != nil {
			return
		}
		if len(input) > 1<<20 {
			return
		}
		if !validRgPattern(pattern) {
			return
		}

		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()

		if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel() // safety net if t.Fatal fires before explicit cancel
		_, _, code := cmdRunCtxFuzz(ctx, t, "rg '"+pattern+"' input.txt", dir)
		cancel()
		if t.Context().Err() != nil {
			return
		}
		if code != 0 && code != 1 && code != 2 {
			t.Errorf("rg pattern %q unexpected exit code %d", pattern, code)
		}
	})
}

// FuzzRgStdin fuzzes rg reading from stdin with arbitrary content.
func FuzzRgStdin(f *testing.F) {
	f.Add([]byte("apple\nbanana\ncherry\n"))
	f.Add([]byte{})
	f.Add([]byte("no newline"))
	f.Add([]byte("a\x00b\nc\n"))
	f.Add(bytes.Repeat([]byte("x"), 4097))
	f.Add([]byte("\n\n\n"))
	f.Add([]byte{0xfc, 0x80, 0x80, 0x80, 0x80, 0xaf, '\n'})
	f.Add([]byte("line1\r\nline2\r\n"))
	f.Add(append(bytes.Repeat([]byte("a"), 1<<20-1), '\n'))

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, input []byte) {
		if t.Context().Err() != nil {
			return
		}
		if len(input) > 1<<20 {
			return
		}

		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()

		if err := os.WriteFile(filepath.Join(dir, "stdin.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel() // safety net if t.Fatal fires before explicit cancel
		_, _, code := cmdRunCtxFuzz(ctx, t, "rg '.' < stdin.txt", dir)
		cancel()
		if t.Context().Err() != nil {
			return
		}
		if code != 0 && code != 1 && code != 2 {
			t.Errorf("rg stdin unexpected exit code %d", code)
		}
	})
}

// FuzzRgFixedStrings fuzzes rg -F (fixed-string mode) with arbitrary content
// and patterns, mirroring the historical GNU grep -F fixed-string matcher
// CVE classes (heap read on crafted pattern/input pairs, integer overflow on
// oversized lines) even though rg's own -F path is a simple regexp.QuoteMeta.
func FuzzRgFixedStrings(f *testing.F) {
	f.Add([]byte("hello world\nfoo bar\n"), "hello")
	f.Add([]byte{}, "pattern")
	f.Add([]byte("no newline"), "no")
	f.Add([]byte("a\x00b\nc\n"), "a")
	f.Add([]byte("(parens)\n[bracket]\na.b\na*b\n"), "(parens)")
	f.Add([]byte("(parens)\n[bracket]\na.b\na*b\n"), "[bracket]")
	f.Add([]byte("a.b\naab\n"), "a.b")
	f.Add([]byte("a*b\nab\n"), "a*b")
	f.Add([]byte("a+b\nab\n"), "a+b")
	f.Add([]byte("a?b\nab\n"), "a?b")
	f.Add([]byte("^start\n"), "^start")
	f.Add([]byte("end$\n"), "end$")
	f.Add([]byte("a\\b\nab\n"), "a\\b")
	f.Add([]byte("hello\nworld\n"), "")
	f.Add([]byte{0xff, 0xfe, 'h', 'i', '\n'}, "hi")
	f.Add([]byte("hello\r\nworld\r\n"), "hello")
	f.Add([]byte{0xfc, 0x80, 0x80, 'h', 'i', '\n'}, "hi")
	f.Add(append(bytes.Repeat([]byte("a"), 1<<20-1), '\n'), "a")

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, input []byte, pattern string) {
		if t.Context().Err() != nil {
			return
		}
		if len(input) > 1<<20 {
			return
		}
		if !validRgPattern(pattern) {
			return
		}

		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()

		if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel() // safety net if t.Fatal fires before explicit cancel
		_, _, code := cmdRunCtxFuzz(ctx, t, "rg -F '"+pattern+"' input.txt", dir)
		cancel()
		if t.Context().Err() != nil {
			return
		}
		if code != 0 && code != 1 && code != 2 {
			t.Errorf("rg -F unexpected exit code %d", code)
		}
	})
}

// FuzzRgFlags fuzzes rg with various flag combinations and arbitrary file
// content, covering context-line clamping (MaxContextLines=1000), -q early
// exit, case handling, and -o/-v/-c interactions.
func FuzzRgFlags(f *testing.F) {
	f.Add([]byte("Hello\nworld\nHELLO\n"), true, false, false, false, false, int64(0), int64(0))
	f.Add([]byte("line1\nline2\n"), false, true, false, false, false, int64(0), int64(0))
	f.Add([]byte{}, true, true, false, false, false, int64(0), int64(0))
	f.Add([]byte("no newline"), false, false, false, false, false, int64(0), int64(0))
	f.Add(bytes.Repeat([]byte("abc\n"), 100), true, false, false, false, false, int64(0), int64(0))
	// Context lines.
	f.Add([]byte("a\nb\nc\nd\ne\n"), false, false, false, false, false, int64(2), int64(0))
	f.Add([]byte("a\nb\nc\nd\ne\n"), false, false, false, false, false, int64(0), int64(2))
	// Context clamping at MaxContextLines=1000.
	f.Add([]byte("a\nb\n"), false, false, false, false, false, int64(1001), int64(0))
	// -c (count) mode.
	f.Add([]byte("a\na\nb\n"), false, false, true, false, false, int64(0), int64(0))
	// -q (quiet) mode: exits on first match.
	f.Add([]byte("a\nb\nc\n"), false, false, false, true, false, int64(0), int64(0))
	// -o (only-matching) mode, including patterns that can match empty.
	f.Add([]byte("aaa\n"), false, false, false, false, true, int64(0), int64(0))
	// Binary content.
	f.Add([]byte{0xff, 0xfe, '\n'}, true, false, false, false, false, int64(0), int64(0))

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, input []byte, caseInsensitive, invertMatch, countOnly, quiet, onlyMatching bool, afterCtx, beforeCtx int64) {
		if t.Context().Err() != nil {
			return
		}
		if len(input) > 1<<20 {
			return
		}
		if afterCtx < 0 || afterCtx > 100 {
			return
		}
		if beforeCtx < 0 || beforeCtx > 100 {
			return
		}

		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()

		if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel() // safety net if t.Fatal fires before explicit cancel
		flags := ""
		if caseInsensitive {
			flags += " -i"
		}
		if invertMatch {
			flags += " -v"
		}
		if countOnly {
			flags += " -c"
		}
		if quiet {
			flags += " -q"
		}
		if onlyMatching {
			flags += " -o"
		}
		if afterCtx > 0 {
			flags += " -A " + fmt.Sprintf("%d", afterCtx)
		}
		if beforeCtx > 0 {
			flags += " -B " + fmt.Sprintf("%d", beforeCtx)
		}

		script := "rg" + flags + " 'a' input.txt"
		_, _, code := cmdRunCtxFuzz(ctx, t, script, dir)
		cancel()
		if t.Context().Err() != nil {
			return
		}
		if code != 0 && code != 1 && code != 2 {
			t.Errorf("rg%s unexpected exit code %d", flags, code)
		}
	})
}

// FuzzRgGlob fuzzes rg's -g/--glob include/exclude filtering and --hidden
// traversal against arbitrary glob patterns and file trees, exercising the
// walkDir/pathAllowed/globMatch code paths directly.
func FuzzRgGlob(f *testing.F) {
	f.Add("*.txt", false)
	f.Add("!*.txt", false)
	f.Add("*.txt", true)
	f.Add("**/*.txt", false)
	f.Add("", false)
	f.Add("!", false)
	f.Add("[", false)
	f.Add("a{b,c}*", false)
	f.Add("sub/*.txt", false)
	f.Add("!sub", false)
	f.Add(string([]byte{0xff, 0xfe}), false)
	// Many "**" segments: a DoS-shaped input for globMatchSegments'
	// dynamic-programming matcher (see TestRgGlobManyDoubleStarsBoundedTime).
	f.Add(strings.Repeat("**/", 20)+"nomatch", false)
	f.Add("a/**/**/**/b", false)

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, glob string, hidden bool) {
		if t.Context().Err() != nil {
			return
		}
		if !utf8.ValidString(glob) {
			return
		}
		if len(glob) > 100 {
			return
		}
		for _, c := range glob {
			if c == '\'' || c == '\x00' || c == '\n' {
				return
			}
			if c < 0x20 || c == 0x7f || (c >= 0x80 && c < 0xa0) {
				return
			}
		}

		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()

		if err := os.MkdirAll(filepath.Join(dir, "sub"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("needle\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("needle\n"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".hidden.txt"), []byte("needle\n"), 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel() // safety net if t.Fatal fires before explicit cancel
		hiddenFlag := ""
		if hidden {
			hiddenFlag = " --hidden"
		}
		script := "rg" + hiddenFlag + " -g '" + glob + "' needle"
		_, _, code := cmdRunCtxFuzz(ctx, t, script, dir)
		cancel()
		if t.Context().Err() != nil {
			return
		}
		if code != 0 && code != 1 && code != 2 {
			t.Errorf("rg -g %q unexpected exit code %d", glob, code)
		}
	})
}
