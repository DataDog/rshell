// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package cat_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/rshell/interp"
	"github.com/DataDog/rshell/interp/builtins/testutil"
)

// createLargeFileCat writes totalBytes of repeating content to dir/filename.
func createLargeFileCat(tb testing.TB, dir, filename, line string, totalBytes int) string {
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

// cmdRunBCat runs a cat command with AllowedPaths set to dir (bench variant).
func cmdRunBCat(b *testing.B, script, dir string) (string, string, int) {
	b.Helper()
	return testutil.RunScript(b, script, dir, interp.AllowedPaths([]string{dir}))
}

// BenchmarkCat measures cat on a 1MB file.
func BenchmarkCat(b *testing.B) {
	dir := b.TempDir()
	createLargeFileCat(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 1<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunBCat(b, "cat input.txt", dir)
	}
}

// BenchmarkCatNumbered measures cat -n on a 1MB file.
func BenchmarkCatNumbered(b *testing.B) {
	dir := b.TempDir()
	createLargeFileCat(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 1<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunBCat(b, "cat -n input.txt", dir)
	}
}

// TestCatMemoryBounded asserts that cat's allocation scales reasonably with
// file size (output buffering is expected, but should not be pathological).
func TestCatMemoryBounded(t *testing.T) {
	dir := t.TempDir()
	createLargeFileCat(t, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 1<<20)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			cmdRunBCat(b, "cat input.txt", dir)
		}
	})

	// cat buffers output through the test harness, so we allow up to 6x file size
	const maxBytesPerOp = 6 << 20 // 6MB ceiling for a 1MB input
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("cat allocated %d bytes/op on 1MB input; want < %d", bpo, maxBytesPerOp)
	}
}
