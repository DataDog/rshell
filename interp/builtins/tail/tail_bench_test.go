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

// createLargeFileTail writes totalBytes of repeating content to dir/filename.
func createLargeFileTail(tb testing.TB, dir, filename, line string, totalBytes int) string {
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
//
// tail must scan the entire input to find the last N lines, so total
// allocations are O(input size): one []byte copy per scanned line goes into
// the ring buffer, and old entries are evicted as new ones arrive. Live heap
// is O(K) (the ring size), but the GC has not necessarily freed evicted
// entries by the time AllocedBytesPerOp is sampled.
//
// With a 1MB input of 44-byte lines (~23 300 lines) the expected total
// allocation is roughly 1MB (one copy per line x line length). A 4MB ceiling
// allows 4x headroom for Go runtime and test-harness overhead while still
// catching regressions that accumulate all lines in memory.
func TestTailMemoryBounded(t *testing.T) {
	const line = "the quick brown fox jumps over the lazy dog\n" // 44 bytes
	const inputSize = 1 << 20                                    // 1 MB -> ~23 300 lines

	dir := t.TempDir()
	createLargeFileTail(t, dir, "input.txt", line, inputSize)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			cmdRunBTail(b, "tail -n 10 input.txt", dir)
		}
	})

	// 4MB ceiling for a 1MB input (4x multiplier for runtime/harness overhead).
	const maxBytesPerOp = 4 << 20
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("tail -n 10 allocated %d bytes/op on %d-byte input; want < %d", bpo, inputSize, maxBytesPerOp)
	}
}
