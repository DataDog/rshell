// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package head_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/rshell/interp"
	"github.com/DataDog/rshell/interp/builtins/testutil"
)

// repeatReader yields a repeating line pattern indefinitely.
type repeatReader struct {
	line []byte
	pos  int
}

func newRepeatReader(line string) *repeatReader {
	return &repeatReader{line: []byte(line)}
}

func (r *repeatReader) Read(p []byte) (int, error) {
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

// createLargeFile writes totalBytes of repeating line content to dir/filename.
func createLargeFile(tb testing.TB, dir, filename, line string, totalBytes int) string {
	tb.Helper()
	path := filepath.Join(dir, filename)
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	if _, err := io.Copy(f, io.LimitReader(newRepeatReader(line), int64(totalBytes))); err != nil {
		tb.Fatal(err)
	}
	return path
}

// cmdRunB runs a head command with AllowedPaths set to dir (bench variant).
// Uses testutil.RunScript which accepts testing.TB.
func cmdRunB(b *testing.B, script, dir string) (string, string, int) {
	b.Helper()
	return testutil.RunScript(b, script, dir, interp.AllowedPaths([]string{dir}))
}

// BenchmarkHeadTenLines measures head -n 10 on a 10MB file of short lines.
func BenchmarkHeadTenLines(b *testing.B) {
	dir := b.TempDir()
	createLargeFile(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunB(b, "head -n 10 input.txt", dir)
	}
}

// BenchmarkHeadBytes measures head -c 1024 on a 10MB file.
func BenchmarkHeadBytes(b *testing.B) {
	dir := b.TempDir()
	createLargeFile(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunB(b, "head -c 1024 input.txt", dir)
	}
}

// BenchmarkHeadSingleLongLine measures head -n 1 on a 10MB file with one huge line.
func BenchmarkHeadSingleLongLine(b *testing.B) {
	dir := b.TempDir()
	// One 10MB line (no embedded newlines)
	createLargeFile(b, dir, "input.txt", "x", 10<<20)
	// Append a newline so it's a valid line
	f, err := os.OpenFile(filepath.Join(dir, "input.txt"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		b.Fatal(err)
	}
	_, _ = f.WriteString("\n")
	f.Close()
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunB(b, "head -n 1 input.txt", dir)
	}
}

// TestHeadMemoryBoundedLines asserts that head -n 10 uses O(1) memory
// regardless of input file size.
func TestHeadMemoryBoundedLines(t *testing.T) {
	dir := t.TempDir()
	createLargeFile(t, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			cmdRunB(b, "head -n 10 input.txt", dir)
		}
	})

	const maxBytesPerOp = 1 << 20 // 1MB ceiling
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("head -n 10 allocated %d bytes/op on 10MB input; want < %d", bpo, maxBytesPerOp)
	}
}

// TestHeadMemoryBoundedBytes asserts that head -c 1024 uses O(1) memory.
func TestHeadMemoryBoundedBytes(t *testing.T) {
	dir := t.TempDir()
	createLargeFile(t, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			cmdRunB(b, "head -c 1024 input.txt", dir)
		}
	})

	const maxBytesPerOp = 1 << 20 // 1MB ceiling
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("head -c 1024 allocated %d bytes/op on 10MB input; want < %d", bpo, maxBytesPerOp)
	}
}
