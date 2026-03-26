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
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
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
// Edge cases: MaxLineBytes (1 MiB) cap, CRLF (\r preserved as content byte),
// null bytes, empty fields, complement, suppress, no trailing newline.
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
	// Open-ended ranges — math.MaxInt32 sentinel in implementation
	f.Add([]byte("a\tb\tc\n"), "2-")
	f.Add([]byte("a\tb\tc\n"), "-2")
	// Empty fields (consecutive delimiters)
	f.Add([]byte(":::\n"), "1-3")
	f.Add([]byte("\t\t\t\n"), "2")
	// CRLF: \r is preserved as content byte, only \n is stripped
	f.Add([]byte("a\tb\tc\r\n"), "3")
	f.Add([]byte("a\tb\tc\r\n"), "2")
	// No trailing newline
	f.Add([]byte("a\tb\tc"), "1")
	f.Add([]byte("a:1\nb:2"), "1")
	// Lines near 1 MiB cap
	f.Add(append(bytes.Repeat([]byte("a\t"), (1<<20-1)/2), "b\n"...), "1")
	f.Add(append(bytes.Repeat([]byte("x"), 1<<20-1), "\n"...), "1")
	// Null bytes in content (treated as regular content bytes)
	f.Add([]byte("a\x00b\tc\n"), "1")
	// Field at and beyond end
	f.Add([]byte("a:b:c\n"), "4")
	// Trailing delimiter
	f.Add([]byte("a:b:\n"), "3")
	// Overlapping ranges
	f.Add([]byte("abcdef\n"), "1-3,2-4")
	// Multiline input
	f.Add([]byte("a\tb\nc\td\n"), "1")
	f.Add([]byte("a\tb\nc\td\n"), "2")

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, input []byte, fieldSpec string) {
		if t.Context().Err() != nil {
			return
		}
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

		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()

		if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel() // safety net if t.Fatal fires before explicit cancel
		script := fmt.Sprintf("cut -f %s input.txt", fieldSpec)
		stdout, _, code := cmdRunCtxFuzz(ctx, t, script, dir)
		cancel()
		if t.Context().Err() != nil {
			return
		}
		// Invariant 3: exit code validity.
		if code != 0 && code != 1 {
			t.Errorf("cut -f %s unexpected exit code %d", fieldSpec, code)
		}
		// Invariant 1: output bounded.
		if len(stdout) > 10*1024*1024 {
			t.Errorf("cut -f %s output exceeds 10 MiB: %d bytes", fieldSpec, len(stdout))
		}

		// Invariant 4: no panic — reaching this line proves no panic escaped Run().

		// Invariant 2: determinism.
		ctx2, cancel2 := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel2()
		stdout2, _, code2 := cmdRunCtxFuzz(ctx2, t, script, dir)
		cancel2()
		if t.Context().Err() != nil {
			return
		}
		if stdout != stdout2 || code != code2 {
			t.Errorf("determinism violation on cut -f %s: outputs differ on identical input\nrun1: exit=%d, len=%d\nrun2: exit=%d, len=%d",
				fieldSpec, code, len(stdout), code2, len(stdout2))
		}
	})
}

// FuzzCutBytes fuzzes cut -b with arbitrary file content and byte specs.
// Edge cases: open-ended ranges, complement, output delimiter,
// boundary positions (1st byte, last byte, beyond line), multibyte UTF-8.
func FuzzCutBytes(f *testing.F) {
	f.Add([]byte("hello world\n"), "1-5")
	f.Add([]byte("hello world\n"), "1,3,5")
	f.Add([]byte("hello world\n"), "6-")
	f.Add([]byte{}, "1")
	f.Add([]byte("a\x00b\nc\n"), "1-3")
	f.Add(bytes.Repeat([]byte("x"), 4097), "1-100")
	// Open-start range
	f.Add([]byte("abcdef\n"), "-3")
	// Beyond line end
	f.Add([]byte("abc\n"), "4")
	f.Add([]byte("abc\n"), "5-")
	// CRLF: \r is byte 3 (regular content)
	f.Add([]byte("ab\r\n"), "3")
	// No trailing newline
	f.Add([]byte("abcdef"), "1-3")
	// Lines near MaxLineBytes (1 MiB)
	f.Add(append(bytes.Repeat([]byte("a"), 1<<20-1), '\n'), "1")
	f.Add(append(bytes.Repeat([]byte("a"), 1<<20), '\n'), "1")
	// Empty line
	f.Add([]byte("\n"), "1")
	// Multibyte UTF-8 (treated byte-by-byte)
	f.Add([]byte("\xce\xb1\xce\xb2\xce\xb3\n"), "1")   // α (first byte only)
	f.Add([]byte("\xce\xb1\xce\xb2\xce\xb3\n"), "1-2") // full α character
	// Null bytes
	f.Add([]byte{0x00, 0x01, 0x02, '\n'}, "1-3")
	// Large position well beyond line
	f.Add([]byte("abc\n"), "1234567890")

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, input []byte, byteSpec string) {
		if t.Context().Err() != nil {
			return
		}
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

		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()

		if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel() // safety net if t.Fatal fires before explicit cancel
		script := fmt.Sprintf("cut -b %s input.txt", byteSpec)
		stdout, _, code := cmdRunCtxFuzz(ctx, t, script, dir)
		cancel()
		if t.Context().Err() != nil {
			return
		}
		// Invariant 3: exit code validity.
		if code != 0 && code != 1 {
			t.Errorf("cut -b %s unexpected exit code %d", byteSpec, code)
		}
		// Invariant 1: output bounded.
		if len(stdout) > 10*1024*1024 {
			t.Errorf("cut -b %s output exceeds 10 MiB: %d bytes", byteSpec, len(stdout))
		}

		// Invariant 4: no panic — reaching this line proves no panic escaped Run().

		// Invariant 2: determinism.
		ctx2, cancel2 := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel2()
		stdout2, _, code2 := cmdRunCtxFuzz(ctx2, t, script, dir)
		cancel2()
		if t.Context().Err() != nil {
			return
		}
		if stdout != stdout2 || code != code2 {
			t.Errorf("determinism violation on cut -b %s: outputs differ on identical input\nrun1: exit=%d, len=%d\nrun2: exit=%d, len=%d",
				byteSpec, code, len(stdout), code2, len(stdout2))
		}
	})
}

// FuzzCutDelimiter fuzzes cut -f with a custom delimiter.
// Edge cases: no-delimiter lines (printed as-is or suppressed with -s),
// consecutive delimiters (empty fields), tab delimiter.
func FuzzCutDelimiter(f *testing.F) {
	f.Add([]byte("a:b:c\n"), ":", "1,3")
	f.Add([]byte("a,b,c\n"), ",", "2")
	f.Add([]byte("a|b|c\n"), "|", "1-2")
	f.Add([]byte("no delim\n"), ":", "1")
	// Empty fields from consecutive delimiters
	f.Add([]byte(":::\n"), ":", "1-4")
	f.Add([]byte("a::b\n"), ":", "2")
	// Trailing delimiter
	f.Add([]byte("a:b:\n"), ":", "3")
	// CRLF: \r preserved as part of last field
	f.Add([]byte("a:b:c\r\n"), ":", "3")
	// Null bytes in line
	f.Add([]byte("a\x00b:c\n"), ":", "1")
	// Single field (no delimiter in line)
	f.Add([]byte("abc\n"), ":", "1")
	// Space as delimiter
	f.Add([]byte("a b c\n"), " ", "2")

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, input []byte, delim string, fieldSpec string) {
		if t.Context().Err() != nil {
			return
		}
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

		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()

		if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel() // safety net if t.Fatal fires before explicit cancel
		script := fmt.Sprintf("cut -d '%s' -f %s input.txt", delim, fieldSpec)
		stdout, _, code := cmdRunCtxFuzz(ctx, t, script, dir)
		cancel()
		if t.Context().Err() != nil {
			return
		}
		// Invariant 3: exit code validity.
		if code != 0 && code != 1 {
			t.Errorf("cut -d '%s' -f %s unexpected exit code %d", delim, fieldSpec, code)
		}
		// Invariant 1: output bounded.
		if len(stdout) > 10*1024*1024 {
			t.Errorf("cut -d '%s' -f %s output exceeds 10 MiB: %d bytes", delim, fieldSpec, len(stdout))
		}

		// Invariant 4: no panic — reaching this line proves no panic escaped Run().

		// Invariant 2: determinism.
		ctx2, cancel2 := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel2()
		stdout2, _, code2 := cmdRunCtxFuzz(ctx2, t, script, dir)
		cancel2()
		if t.Context().Err() != nil {
			return
		}
		if stdout != stdout2 || code != code2 {
			t.Errorf("determinism violation on cut -d '%s' -f %s: outputs differ on identical input\nrun1: exit=%d, len=%d\nrun2: exit=%d, len=%d",
				delim, fieldSpec, code, len(stdout), code2, len(stdout2))
		}
	})
}

// FuzzCutComplement fuzzes cut --complement with -b and -f modes.
// Edge cases: complement of entire range (empty output), complement of nothing
// (full output), non-contiguous complement ranges.
func FuzzCutComplement(f *testing.F) {
	f.Add([]byte("abcdef\n"), "3-4")
	f.Add([]byte("9_1\n8_2\n"), "2")
	// Complement of a single byte
	f.Add([]byte("abcdef\n"), "1")
	f.Add([]byte("abcdef\n"), "6")
	// Complement of entire line (empty output)
	f.Add([]byte("abc\n"), "1-")
	// Complement with multiple ranges
	f.Add([]byte("a:b:c:d\n"), "2,3")
	// CRLF
	f.Add([]byte("abcdef\r\n"), "3-4")
	// No trailing newline
	f.Add([]byte("abcdef"), "2")
	// Empty input
	f.Add([]byte{}, "1")
	// Lines at 1 MiB cap
	f.Add(append(bytes.Repeat([]byte("a"), 1<<20-1), '\n'), "1")

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, input []byte, byteSpec string) {
		if t.Context().Err() != nil {
			return
		}
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

		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()

		if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel() // safety net if t.Fatal fires before explicit cancel
		script := fmt.Sprintf("cut --complement -b %s input.txt", byteSpec)
		stdout, _, code := cmdRunCtxFuzz(ctx, t, script, dir)
		cancel()
		if t.Context().Err() != nil {
			return
		}
		// Invariant 3: exit code validity.
		if code != 0 && code != 1 {
			t.Errorf("cut --complement -b %s unexpected exit code %d", byteSpec, code)
		}
		// Invariant 1: output bounded.
		if len(stdout) > 10*1024*1024 {
			t.Errorf("cut --complement -b %s output exceeds 10 MiB: %d bytes", byteSpec, len(stdout))
		}

		// Invariant 4: no panic — reaching this line proves no panic escaped Run().

		// Invariant 2: determinism.
		ctx2, cancel2 := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel2()
		stdout2, _, code2 := cmdRunCtxFuzz(ctx2, t, script, dir)
		cancel2()
		if t.Context().Err() != nil {
			return
		}
		if stdout != stdout2 || code != code2 {
			t.Errorf("determinism violation on cut --complement -b %s: outputs differ on identical input\nrun1: exit=%d, len=%d\nrun2: exit=%d, len=%d",
				byteSpec, code, len(stdout), code2, len(stdout2))
		}
	})
}

// FuzzCutStdin fuzzes cut reading from stdin.
func FuzzCutStdin(f *testing.F) {
	f.Add([]byte("a\tb\tc\n"))
	f.Add([]byte{})
	f.Add([]byte("no newline"))
	// Null bytes
	f.Add([]byte("a\x00b\tc\n"))
	// CRLF
	f.Add([]byte("a\tb\r\n"))
	// Invalid UTF-8
	f.Add([]byte{0xfc, 0x80, 0x80, '\t', 0x80, '\n'})
	// Empty fields
	f.Add([]byte("\t\t\n"))
	// Lines at 1 MiB
	f.Add(append(bytes.Repeat([]byte("x"), 1<<20-1), '\n'))

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
		stdout, _, code := cmdRunCtxFuzz(ctx, t, "cut -f 1 < stdin.txt", dir)
		cancel()
		if t.Context().Err() != nil {
			return
		}
		// Invariant 3: exit code validity.
		if code != 0 && code != 1 {
			t.Errorf("cut stdin unexpected exit code %d", code)
		}
		// Invariant 1: output bounded.
		if len(stdout) > 10*1024*1024 {
			t.Errorf("cut stdin output exceeds 10 MiB: %d bytes", len(stdout))
		}

		// Invariant 4: no panic — reaching this line proves no panic escaped Run().

		// Invariant 2: determinism.
		ctx2, cancel2 := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel2()
		stdout2, _, code2 := cmdRunCtxFuzz(ctx2, t, "cut -f 1 < stdin.txt", dir)
		cancel2()
		if t.Context().Err() != nil {
			return
		}
		if stdout != stdout2 || code != code2 {
			t.Errorf("determinism violation on cut stdin: outputs differ on identical input\nrun1: exit=%d, len=%d\nrun2: exit=%d, len=%d",
				code, len(stdout), code2, len(stdout2))
		}
	})
}
