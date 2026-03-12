// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package grep_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/rshell/interp"
	"github.com/DataDog/rshell/interp/builtins/testutil"
)

func createLargeFileGrep(tb testing.TB, dir, filename, line string, totalBytes int) string {
	tb.Helper()
	path := filepath.Join(dir, filename)
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(testutil.NewRepeatReader(line), int64(totalBytes))); err != nil {
		tb.Fatal(err)
	}
	return path
}

func cmdRunBGrep(b *testing.B, script, dir string) (string, string, int) {
	b.Helper()
	return testutil.RunScript(b, script, dir, interp.AllowedPaths([]string{dir}))
}

// BenchmarkGrepMatch measures grep on a 10MB file where every line matches.
func BenchmarkGrepMatch(b *testing.B) {
	dir := b.TempDir()
	createLargeFileGrep(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunBGrep(b, "grep fox input.txt", dir)
	}
}

// BenchmarkGrepNoMatch measures grep on a 10MB file where no lines match.
func BenchmarkGrepNoMatch(b *testing.B) {
	dir := b.TempDir()
	createLargeFileGrep(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunBGrep(b, "grep NOMATCH input.txt", dir)
	}
}

// BenchmarkGrepFixedStrings measures grep -F on a 10MB file.
func BenchmarkGrepFixedStrings(b *testing.B) {
	dir := b.TempDir()
	createLargeFileGrep(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunBGrep(b, "grep -F fox input.txt", dir)
	}
}

// BenchmarkGrepCount measures grep -c on a 10MB file.
func BenchmarkGrepCount(b *testing.B) {
	dir := b.TempDir()
	createLargeFileGrep(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunBGrep(b, "grep -c fox input.txt", dir)
	}
}

// TestGrepMemoryBounded asserts that grep allocation is bounded relative to
// input size. grep is a streaming command: it reads one line at a time (up to
// MaxLineBytes = 1 MiB per line). Total allocations are O(input size) because
// bufio.Scanner.Text() allocates a new string per line, but live heap stays
// O(1). When every line matches, the output buffer also scales with input size;
// using -c avoids that second O(n) factor and isolates the scanner overhead.
//
// With 10MB of 44-byte lines (~227k lines) the scanner allocates roughly 1
// string per line ≈ 10MB of string data. A 32MB ceiling allows 3x headroom for
// runtime, output buffering, and test-harness overhead while still catching
// regressions such as accidentally storing all lines in a slice.
func TestGrepMemoryBounded(t *testing.T) {
	dir := t.TempDir()
	createLargeFileGrep(t, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			// Use -c to avoid output scaling O(n) with match count.
			cmdRunBGrep(b, "grep -c fox input.txt", dir)
		}
	})

	const maxBytesPerOp = 32 << 20 // 32MB ceiling (~3x observed ~11.5MB)
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("grep -c allocated %d bytes/op on 10MB input; want < %d", bpo, maxBytesPerOp)
	}
}
