// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package jq_test

import (
	"bytes"
	"context"
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

// cmdRunCtxFuzz runs a script with a per-iteration AllowedPath, used by
// every fuzz function in this file. Named to avoid clashing with helpers
// that may be defined elsewhere in the package.
func cmdRunCtxFuzz(ctx context.Context, t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, interp.AllowedPaths([]string{dir}))
}

// fuzzWrite writes input as the JSON file under dir and returns its path.
func fuzzWriteJSON(t *testing.T, dir string, input []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "input.json"), input, 0644); err != nil {
		t.Fatal(err)
	}
}

// FuzzJqIdentity fuzzes the identity filter on arbitrary input. Verifies
// jq never panics and exits with one of the documented codes.
//
// Seed corpus combines:
//   - Small JSON sanity values
//   - Boundary inputs around our memory caps (4 KiB, 1 MiB)
//   - Encoding edge cases (CRLF, null bytes, invalid UTF-8, BOM)
//   - Adversarial inputs that exercise the multi-document streaming path
//   - All distinct inputs from the unit tests
func FuzzJqIdentity(f *testing.F) {
	// Source A — implementation edge cases.
	f.Add([]byte(`null`))
	f.Add([]byte(`true`))
	f.Add([]byte(`false`))
	f.Add([]byte(`0`))
	f.Add([]byte(`-1`))
	f.Add([]byte(`3.14`))
	f.Add([]byte(`""`))
	f.Add([]byte(`"hello"`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`[]`))
	f.Add([]byte(`{"a":1}`))
	f.Add([]byte(`{"a":1,"b":2}`))
	f.Add([]byte(`[1,2,3]`))
	f.Add([]byte(`{"nested":{"deep":{"value":42}}}`))
	// Boundary at 4 KiB scanner-init.
	f.Add(append(bytes.Repeat([]byte(" "), 4095), '"', '"'))
	f.Add(append(bytes.Repeat([]byte(" "), 4096), '"', '"'))
	f.Add(append(bytes.Repeat([]byte(" "), 4097), '"', '"'))
	// Multi-document streams.
	f.Add([]byte("1\n2\n3\n"))
	f.Add([]byte("{}{}{}"))
	f.Add([]byte(`{"a":1}{"b":2}`))
	// No trailing newline.
	f.Add([]byte(`{"a":1}`))
	// BOM-prefixed input — fastjq strips this; json.Decoder rejects it.
	f.Add([]byte("\xef\xbb\xbf{\"a\":1}"))
	// Single byte.
	f.Add([]byte(`1`))

	// Source B — CVE-class inputs known to break naïve JSON tooling.
	// Long strings near the per-doc boundary.
	f.Add(append(append([]byte(`"`), bytes.Repeat([]byte("a"), 4095)...), '"'))
	f.Add(append(append([]byte(`"`), bytes.Repeat([]byte("a"), 65535)...), '"'))
	// Invalid UTF-8 inside a string.
	f.Add(append(append([]byte(`"`), 0xed, 0xa0, 0x80), '"')) // surrogate half
	f.Add(append(append([]byte(`"`), 0xfc, 0x80, 0x80, 0x80, 0x80, 0xaf), '"'))
	// CRLF line endings.
	f.Add([]byte("{\"a\":1}\r\n{\"b\":2}\r\n"))
	// Embedded null bytes (invalid in JSON outside strings; jq rejects).
	f.Add([]byte("{\"a\":\x00}"))
	// Embedded ANSI escape in string contents (terminal injection class).
	f.Add([]byte("\"\\u001b[31mRED\\u001b[0m\""))
	// Binary-magic-byte payloads — jq must reject as invalid JSON.
	f.Add([]byte{0x7f, 'E', 'L', 'F'})
	f.Add([]byte{'M', 'Z'})
	f.Add([]byte{'P', 'K', 0x03, 0x04})
	// Numeric overflow / boundary.
	f.Add([]byte("9999999999999999999"))
	f.Add([]byte("-9999999999999999999"))
	f.Add([]byte("1e308"))
	f.Add([]byte("1e1000"))
	// Deeply nested array/object — tests recursion bounds.
	f.Add(append(append(bytes.Repeat([]byte("["), 100), '0'), bytes.Repeat([]byte("]"), 100)...))

	// Source C — every distinct value from the unit tests.
	f.Add([]byte(`{"a":1,"b":2}`))
	f.Add([]byte(`{"a":{"b":[1,2]}}`))
	f.Add([]byte(`{"a":[],"b":{}}`))
	f.Add([]byte(`{"name":"alice"}`))
	f.Add([]byte(`{"a":42,"b":true,"c":null}`))
	f.Add([]byte(`{"s":"a\tb\nc"}`))
	f.Add([]byte(`{"banana":2,"apple":1,"cherry":3}`))
	f.Add([]byte(`{"z":{"b":2,"a":1},"y":{"d":4,"c":3}}`))
	f.Add([]byte(`{"flag":false}`))
	f.Add([]byte(`"hello"`))
	f.Add([]byte(`"hi\nthere"`))
	f.Add([]byte(`{"s":"héllo"}`))
	f.Add([]byte(`{"s":"a😀b"}`))
	f.Add([]byte(`{"x":[]}`))
	f.Add([]byte(`{"a":9007199254740993}`))

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			return
		}
		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()
		fuzzWriteJSON(t, dir, input)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _, code := cmdRunCtxFuzz(ctx, t, "jq -c . input.json", dir)
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d", code)
		}
	})
}

// FuzzJqRawInput fuzzes -R (raw input mode) with arbitrary text bytes.
// Verifies the line scanner handles binary, CRLF, long lines, etc.
func FuzzJqRawInput(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("\n"))
	f.Add([]byte("foo\n"))
	f.Add([]byte("foo\nbar\nbaz\n"))
	// CRLF.
	f.Add([]byte("foo\r\nbar\r\n"))
	// Lone CR.
	f.Add([]byte("foo\rbar\r"))
	// No trailing newline.
	f.Add([]byte("hello"))
	// Mixed terminators.
	f.Add([]byte("a\nb\r\nc\rd"))
	// Embedded null bytes.
	f.Add([]byte("a\x00b\nc"))
	// Invalid UTF-8 (Go encoder substitutes U+FFFD).
	f.Add([]byte{0xff, 0xfe, '\n'})
	f.Add([]byte{0xed, 0xa0, 0x80, '\n'})
	// ANSI escapes.
	f.Add([]byte("\x1b[31mRED\x1b[0m\n"))
	// JSON-like content as raw text.
	f.Add([]byte(`{"a":1}` + "\n"))
	// Binary file headers.
	f.Add([]byte{0x7f, 'E', 'L', 'F', '\n'})
	// Long line near the cap.
	f.Add(append(bytes.Repeat([]byte("k"), 4095), '\n'))

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			return
		}
		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()
		if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _, code := cmdRunCtxFuzz(ctx, t, "jq -R . input.txt", dir)
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d", code)
		}
	})
}

// FuzzJqSlurp fuzzes -s with arbitrary multi-document JSON input.
func FuzzJqSlurp(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("1"))
	f.Add([]byte("1 2 3"))
	f.Add([]byte("1\n2\n3"))
	f.Add([]byte(`{"a":1}{"b":2}`))
	f.Add([]byte(`null null null`))
	f.Add([]byte(`{"x":[]}{"y":{}}`))
	f.Add([]byte("[1,2,3]\n[4,5,6]"))
	f.Add([]byte(`""`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`""""""""`)) // back-to-back empty strings

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 1<<20 {
			return
		}
		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()
		fuzzWriteJSON(t, dir, input)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _, code := cmdRunCtxFuzz(ctx, t, "jq -c -s . input.json", dir)
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d", code)
		}
	})
}

// FuzzJqFilter fuzzes a wide variety of filter expressions on a fixed
// JSON input, covering the compile path of the fastjq engine.
func FuzzJqFilter(f *testing.F) {
	// Source A — common jq filter shapes.
	f.Add(".")
	f.Add(".a")
	f.Add(".a.b.c")
	f.Add(".[]")
	f.Add(".[0]")
	f.Add(".[-1]")
	f.Add("..")
	f.Add(`select(.a == 1)`)
	f.Add(`select(.a > 0)`)
	f.Add(`del(.a)`)
	f.Add(`map(.x)`)
	f.Add(`{name: .a}`)
	f.Add(`[.a, .b]`)
	f.Add(`. as $x | $x`)
	f.Add(`if .a then 1 else 2 end`)
	f.Add(`try .a catch "fail"`)
	f.Add(`length`)
	f.Add(`type`)
	f.Add(`keys`)
	f.Add(`keys_unsorted`)
	f.Add(`to_entries`)
	f.Add(`from_entries`)
	f.Add(`add`)
	// Source B — adversarial inputs.
	f.Add("")
	f.Add(" ")
	f.Add(".[")
	f.Add("|||||")
	f.Add(`"unterminated`)
	f.Add("\x00")
	f.Add("\xed\xa0\x80")
	// ReDoS-class regex inside test().
	f.Add(`test("(a+)+b")`)
	f.Add(`test("a*a*a*b")`)
	// Very long filter expression.
	long := bytes.Repeat([]byte("."), 1000)
	f.Add(string(long))
	// Mismatched parens.
	f.Add(`(((((((`)

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, filter string) {
		if len(filter) > 1<<14 {
			return
		}
		// Skip filters that the shell parser would reject (invalid UTF-8,
		// embedded NUL, etc.) — those errors aren't on the jq path under test.
		if !utf8.ValidString(filter) || strings.ContainsRune(filter, 0) {
			return
		}
		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()
		fuzzWriteJSON(t, dir, []byte(`{"a":1,"b":2}`))

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Pass the filter as -n input to avoid shell-quoting interactions.
		_, _, code := cmdRunCtxFuzz(ctx, t, "jq -c -n -- "+shquote(filter), dir)
		// Filter compile/runtime errors return 1 or 3; success returns 0.
		// Usage errors (filter too large, etc.) return 2.
		if code != 0 && code != 1 && code != 2 && code != 3 {
			t.Errorf("unexpected exit code %d", code)
		}
	})
}

// shquote single-quotes a string for the shell, escaping embedded quotes.
func shquote(s string) string {
	var buf bytes.Buffer
	buf.WriteByte('\'')
	for _, c := range []byte(s) {
		if c == '\'' {
			buf.WriteString(`'\''`)
		} else {
			buf.WriteByte(c)
		}
	}
	buf.WriteByte('\'')
	return buf.String()
}
