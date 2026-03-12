// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package uniq_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/rshell/interp"
	"github.com/DataDog/rshell/interp/builtins/testutil"
)

func createLargeFileUniq(tb testing.TB, dir, filename, line string, totalBytes int) string {
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

func cmdRunBUniq(b *testing.B, script, dir string) (string, string, int) {
	b.Helper()
	return testutil.RunScript(b, script, dir, interp.AllowedPaths([]string{dir}))
}

// BenchmarkUniq measures uniq on a 10MB file of identical lines (all deduplicated to one).
func BenchmarkUniq(b *testing.B) {
	dir := b.TempDir()
	createLargeFileUniq(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunBUniq(b, "uniq input.txt", dir)
	}
}

// BenchmarkUniqCount measures uniq -c on a 10MB file.
func BenchmarkUniqCount(b *testing.B) {
	dir := b.TempDir()
	createLargeFileUniq(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunBUniq(b, "uniq -c input.txt", dir)
	}
}

// TestUniqMemoryBounded asserts that uniq allocation is bounded relative to
// input size. uniq is a streaming command: only the current and previous lines
// are kept in memory at any time (live heap is O(1)), but total allocations are
// O(input size) because bufio.Scanner.Text() allocates a new string per line.
//
// With 10MB of 44-byte identical lines (~227k lines) the scanner allocates
// roughly one 44-byte string per line ≈ 10MB of string data total. Output is
// just a single deduplicated line (~44 bytes), so output buffering is trivial.
// A 32MB ceiling provides 3x headroom for runtime overhead while still catching
// regressions such as accumulating all lines in a slice before deduplicating.
func TestUniqMemoryBounded(t *testing.T) {
	dir := t.TempDir()
	createLargeFileUniq(t, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			cmdRunBUniq(b, "uniq input.txt", dir)
		}
	})

	const maxBytesPerOp = 32 << 20 // 32MB ceiling (~3x observed ~11.5MB)
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("uniq allocated %d bytes/op on 10MB input; want < %d", bpo, maxBytesPerOp)
	}
}
