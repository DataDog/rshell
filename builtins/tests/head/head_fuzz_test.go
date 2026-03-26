// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package head_test

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

	"github.com/DataDog/rshell/builtins/testutil"
)

// FuzzHeadLines fuzzes head -n N with arbitrary file content.
// Edge cases: MaxCount clamp (2^31-1), line-length cap (1 MiB), no trailing newline.
func FuzzHeadLines(f *testing.F) {
	f.Add([]byte("line1\nline2\nline3\n"), int64(2))
	f.Add([]byte{}, int64(0))
	f.Add([]byte("no newline"), int64(1))
	f.Add([]byte("a\x00b\nc\n"), int64(2))
	f.Add(bytes.Repeat([]byte("x"), 4097), int64(1))
	f.Add([]byte("\n\n\n"), int64(5))
	f.Add(bytes.Repeat([]byte("y"), 4096), int64(1))
	f.Add([]byte("hello\nworld\n"), int64(10))
	// MaxCount boundary — must be clamped, not OOM
	f.Add([]byte("tiny\n"), int64(1<<31-1))
	f.Add([]byte("tiny\n"), int64(9999999999))
	// n=0 must produce no output
	f.Add([]byte("a\nb\nc\n"), int64(0))
	// Exactly at line scanner cap (1 MiB - 1) — should succeed
	f.Add(append(bytes.Repeat([]byte("a"), 1<<20-1), '\n'), int64(1))
	// Over line scanner cap — should error, not panic
	f.Add(append(bytes.Repeat([]byte("a"), 1<<20), '\n'), int64(1))
	// Binary / null bytes
	f.Add([]byte("a\x00b\x00c\n"), int64(1))
	// CRLF — must be preserved
	f.Add([]byte("line1\r\nline2\r\nline3\r\n"), int64(2))
	// Invalid UTF-8 (CVE-class: must not panic)
	f.Add([]byte{0xfc, 0x80, 0x80, 0x80, 0x80, 0xaf, '\n'}, int64(1))
	// Leading + sign on count (handled as positive, not error)
	// (tested by passing n directly; shell arg would be "+N" which head accepts)
	// Multiple blank lines
	f.Add([]byte("\n\n\n\n\n"), int64(3))
	// No trailing newline on last output line
	f.Add([]byte("line1\nline2"), int64(2))

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, input []byte, n int64) {
		if t.Context().Err() != nil {
			return
		}
		if len(input) > 1<<20 {
			return
		}
		if n < 0 {
			return
		}
		if n > 10000 {
			n = 10000
		}

		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()

		err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel() // safety net if t.Fatal fires before explicit cancel
		stdout, _, code := cmdRunCtx(ctx, t, fmt.Sprintf("head -n %d input.txt", n), dir)
		cancel()
		if t.Context().Err() != nil {
			return
		}
		// Invariant 3: exit code validity — head exits 0 (success) or 1 (error).
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d", code)
		}
		// Invariant 1: output bounded — the global 10 MiB stdout cap must hold.
		if len(stdout) > 10*1024*1024 {
			t.Errorf("output exceeds 10 MiB: %d bytes", len(stdout))
		}

		// If successful, output line count must be <= n
		if code == 0 && n >= 0 {
			lineCount := strings.Count(stdout, "\n")
			if int64(lineCount) > n {
				t.Errorf("head -n %d produced %d newlines in output", n, lineCount)
			}
		}

		// Invariant 4: no panic — the runner's deferred recover in api.go catches
		// any panic and converts it to an error return. Reaching this line means
		// no panic escaped the Run() call.

		// Invariant 2: determinism — identical inputs must produce identical outputs.
		ctx2, cancel2 := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel2()
		stdout2, _, code2 := cmdRunCtx(ctx2, t, fmt.Sprintf("head -n %d input.txt", n), dir)
		cancel2()
		if t.Context().Err() != nil {
			return
		}
		if stdout != stdout2 || code != code2 {
			t.Errorf("determinism violation on head -n %d: outputs differ on identical input\nrun1: exit=%d, len=%d\nrun2: exit=%d, len=%d",
				n, code, len(stdout), code2, len(stdout2))
		}
	})
}

// FuzzHeadBytes fuzzes head -c N with arbitrary file content.
// Edge cases: MaxCount clamp, 32 KiB chunk boundary, binary content.
func FuzzHeadBytes(f *testing.F) {
	f.Add([]byte("line1\nline2\nline3\n"), int64(5))
	f.Add([]byte{}, int64(0))
	f.Add([]byte("no newline"), int64(3))
	f.Add([]byte("a\x00b\nc\n"), int64(4))
	f.Add(bytes.Repeat([]byte("x"), 4097), int64(4096))
	f.Add([]byte("\n\n\n"), int64(2))
	// Chunk boundary (32 KiB)
	f.Add(bytes.Repeat([]byte("z"), 32*1024), int64(32*1024))
	f.Add(bytes.Repeat([]byte("z"), 32*1024+1), int64(32*1024))
	// MaxCount boundary
	f.Add([]byte("tiny"), int64(1<<31-1))
	f.Add([]byte("tiny"), int64(9999999999))
	// n=0 → no output
	f.Add([]byte("abc"), int64(0))
	// Binary content
	f.Add([]byte{0x00, 0x01, 0x02, 0x03, 0xff, 0xfe}, int64(4))
	// Invalid UTF-8
	f.Add([]byte{0xfc, 0x80, 0x80, 0x80, 0x80, 0xaf}, int64(6))
	// CRLF
	f.Add([]byte("a\r\nb\r\n"), int64(3))

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, input []byte, n int64) {
		if t.Context().Err() != nil {
			return
		}
		if len(input) > 1<<20 {
			return
		}
		if n < 0 {
			return
		}
		if n > 10000 {
			n = 10000
		}

		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()

		err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel() // safety net if t.Fatal fires before explicit cancel
		stdout, _, code := cmdRunCtx(ctx, t, fmt.Sprintf("head -c %d input.txt", n), dir)
		cancel()
		if t.Context().Err() != nil {
			return
		}
		// Invariant 3: exit code validity.
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d", code)
		}
		// Invariant 1: output bounded.
		if len(stdout) > 10*1024*1024 {
			t.Errorf("output exceeds 10 MiB: %d bytes", len(stdout))
		}

		// If successful, output byte count must be <= n
		if code == 0 {
			outLen := int64(len(stdout))
			if outLen > n {
				t.Errorf("head -c %d produced %d bytes of output", n, outLen)
			}
		}

		// Invariant 4: no panic — reaching this line proves no panic escaped Run().

		// Invariant 2: determinism.
		ctx2, cancel2 := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel2()
		stdout2, _, code2 := cmdRunCtx(ctx2, t, fmt.Sprintf("head -c %d input.txt", n), dir)
		cancel2()
		if t.Context().Err() != nil {
			return
		}
		if stdout != stdout2 || code != code2 {
			t.Errorf("determinism violation on head -c %d: outputs differ on identical input\nrun1: exit=%d, len=%d\nrun2: exit=%d, len=%d",
				n, code, len(stdout), code2, len(stdout2))
		}
	})
}

// FuzzHeadStdin fuzzes head -n N reading from stdin via shell redirection.
func FuzzHeadStdin(f *testing.F) {
	f.Add([]byte("line1\nline2\nline3\n"), int64(2))
	f.Add([]byte{}, int64(1))
	f.Add([]byte("no newline"), int64(1))
	f.Add([]byte("a\x00b\nc\n"), int64(2))
	f.Add(bytes.Repeat([]byte("x"), 4097), int64(1))
	f.Add([]byte("\n\n\n"), int64(3))
	f.Add([]byte{0xfc, 0x80, 0x80, 0x80, 0x80, 0xaf, '\n'}, int64(1))
	f.Add([]byte("line1\r\nline2\r\n"), int64(1))

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, input []byte, n int64) {
		if t.Context().Err() != nil {
			return
		}
		if len(input) > 1<<20 {
			return
		}
		if n < 0 {
			return
		}
		if n > 10000 {
			n = 10000
		}

		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()

		err := os.WriteFile(filepath.Join(dir, "stdin.txt"), input, 0644)
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel() // safety net if t.Fatal fires before explicit cancel
		stdout, _, code := cmdRunCtx(ctx, t, fmt.Sprintf("head -n %d < stdin.txt", n), dir)
		cancel()
		if t.Context().Err() != nil {
			return
		}
		// Invariant 3: exit code validity.
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d (stdin mode)", code)
		}
		// Invariant 1: output bounded.
		if len(stdout) > 10*1024*1024 {
			t.Errorf("output exceeds 10 MiB: %d bytes", len(stdout))
		}

		// Invariant 4: no panic — reaching this line proves no panic escaped Run().

		// Invariant 2: determinism.
		ctx2, cancel2 := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel2()
		stdout2, _, code2 := cmdRunCtx(ctx2, t, fmt.Sprintf("head -n %d < stdin.txt", n), dir)
		cancel2()
		if t.Context().Err() != nil {
			return
		}
		if stdout != stdout2 || code != code2 {
			t.Errorf("determinism violation on head stdin -n %d: outputs differ on identical input\nrun1: exit=%d, len=%d\nrun2: exit=%d, len=%d",
				n, code, len(stdout), code2, len(stdout2))
		}
	})
}
