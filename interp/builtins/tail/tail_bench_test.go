// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tail_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/rshell/interp"
	"github.com/DataDog/rshell/interp/builtins/testutil"
)

// repeatReaderTail yields a repeating line pattern indefinitely.
type repeatReaderTail struct {
	line []byte
	pos  int
}

func newRepeatReaderTail(line string) *repeatReaderTail {
	return &repeatReaderTail{line: []byte(line)}
}

func (r *repeatReaderTail) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		if r.pos >= len(r.line) {
			r.pos = 0
		}
		copied := copy(p[n:], r.line[r.pos:])
		r.pos += copied
		n += copied
	}
	return n, nil
}

// createLargeFileTail writes totalBytes of repeating content to dir/filename.
func createLargeFileTail(tb testing.TB, dir, filename, line string, totalBytes int) string {
	tb.Helper()
	path := filepath.Join(dir, filename)
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(newRepeatReaderTail(line), int64(totalBytes))); err != nil {
		tb.Fatal(err)
	}
	return path
}

// cmdRunBTail runs a tail command with AllowedPaths set to dir (bench variant).
func cmdRunBTail(b *testing.B, script, dir string) (string, string, int) {
	b.Helper()
	return testutil.RunScript(b, script, dir, interp.AllowedPaths([]string{dir}))
}

// BenchmarkTailTenLines measures tail -n 10 on a 10MB file.
func BenchmarkTailTenLines(b *testing.B) {
	dir := b.TempDir()
	createLargeFileTail(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunBTail(b, "tail -n 10 input.txt", dir)
	}
}

// BenchmarkTailBytes measures tail -c 1024 on a 10MB file.
func BenchmarkTailBytes(b *testing.B) {
	dir := b.TempDir()
	createLargeFileTail(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunBTail(b, "tail -c 1024 input.txt", dir)
	}
}

// TestTailMemoryBounded asserts that tail -n 10 allocation is bounded.
// Note: tail reads the whole file to find the last N lines, so total
// allocations are O(n), but live heap (the ring buffer) is O(K).
// This test checks that the ceiling doesn't grow unboundedly.
func TestTailMemoryBounded(t *testing.T) {
	dir := t.TempDir()
	createLargeFileTail(t, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			cmdRunBTail(b, "tail -n 10 input.txt", dir)
		}
	})

	// tail reads line-by-line through a scanner; each line is allocated then
	// discarded from the ring buffer — total allocs are O(n) but capped here.
	const maxBytesPerOp = 32 << 20 // 32MB ceiling for a 10MB input
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("tail -n 10 allocated %d bytes/op on 10MB input; want < %d", bpo, maxBytesPerOp)
	}
}
