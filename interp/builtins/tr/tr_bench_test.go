// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tr_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/rshell/interp"
	"github.com/DataDog/rshell/interp/builtins/testutil"
)

func createLargeFileTr(tb testing.TB, dir, filename, line string, totalBytes int) string {
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

func cmdRunBTr(b *testing.B, script, dir string) (string, string, int) {
	b.Helper()
	return testutil.RunScript(b, script, dir, interp.AllowedPaths([]string{dir}))
}

// BenchmarkTrTranslate measures tr 'a-z' 'A-Z' on a 1MB file piped through tr.
// tr reads input from stdin in fixed 32 KiB chunks and translates byte-by-byte
// using a pre-built 256-entry lookup table.
func BenchmarkTrTranslate(b *testing.B) {
	dir := b.TempDir()
	createLargeFileTr(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 1<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunBTr(b, "cat input.txt | tr 'a-z' 'A-Z'", dir)
	}
}

// BenchmarkTrDelete measures tr -d on a 1MB file.
func BenchmarkTrDelete(b *testing.B) {
	dir := b.TempDir()
	createLargeFileTr(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 1<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunBTr(b, "cat input.txt | tr -d ' '", dir)
	}
}

// BenchmarkTrSqueeze measures tr -s on a 1MB file.
func BenchmarkTrSqueeze(b *testing.B) {
	dir := b.TempDir()
	createLargeFileTr(b, dir, "input.txt", "the  quick  brown  fox  jumps  over  the  lazy  dog\n", 1<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunBTr(b, "cat input.txt | tr -s ' '", dir)
	}
}

// TestTrMemoryBounded asserts that tr uses O(1) memory regardless of input
// size. tr operates on a 256-entry lookup table built once at startup. Input
// is read in fixed 32 KiB chunks and translated in-place; no allocation is
// proportional to input length. Output is buffered through the test harness,
// so total allocations are O(input size) due to output buffering — using 1MB
// input keeps the ceiling manageable.
//
// With 1MB of input, output is also ~1MB; a 6MB ceiling provides ~3x headroom
// over the expected ~2–3MB (output buffer doublings + runtime overhead) while
// still catching regressions such as accumulating the entire translated output
// in a pre-allocated slice.
func TestTrMemoryBounded(t *testing.T) {
	dir := t.TempDir()
	createLargeFileTr(t, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 1<<20)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			cmdRunBTr(b, "cat input.txt | tr 'a-z' 'A-Z'", dir)
		}
	})

	const maxBytesPerOp = 6 << 20 // 6MB ceiling for a 1MB input with output buffering
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("tr allocated %d bytes/op on 1MB input; want < %d", bpo, maxBytesPerOp)
	}
}
