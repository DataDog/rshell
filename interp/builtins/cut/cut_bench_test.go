// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package cut_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/rshell/interp"
	"github.com/DataDog/rshell/interp/builtins/testutil"
)

func createLargeFileCut(tb testing.TB, dir, filename, line string, totalBytes int) string {
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

func cmdRunBCut(b *testing.B, script, dir string) (string, string, int) {
	b.Helper()
	return testutil.RunScript(b, script, dir, interp.AllowedPaths([]string{dir}))
}

// BenchmarkCutBytes measures cut -b 1-10 on a 10MB file of short lines.
func BenchmarkCutBytes(b *testing.B) {
	dir := b.TempDir()
	createLargeFileCut(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunBCut(b, "cut -b 1-10 input.txt", dir)
	}
}

// BenchmarkCutFields measures cut -f 1 -d ' ' on a 10MB file of short lines.
func BenchmarkCutFields(b *testing.B) {
	dir := b.TempDir()
	// Tab-delimited: "field1\tfield2\tfield3"
	createLargeFileCut(b, dir, "input.txt", "alpha\tbeta\tgamma\tdelta\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunBCut(b, "cut -f 1 input.txt", dir)
	}
}

// BenchmarkCutFieldsMultiple measures cut selecting multiple fields on a 10MB file.
func BenchmarkCutFieldsMultiple(b *testing.B) {
	dir := b.TempDir()
	createLargeFileCut(b, dir, "input.txt", "alpha\tbeta\tgamma\tdelta\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		cmdRunBCut(b, "cut -f 1,3 input.txt", dir)
	}
}

// TestCutMemoryBounded asserts that cut -b allocation is bounded relative to
// input size. cut is a streaming command: it reads one line at a time (up to
// MaxLineBytes = 1 MiB per line). Total allocations are O(input size) because
// bufio.Scanner copies each line into a new buffer, but live heap stays O(1).
//
// With 10MB of 44-byte lines (~227k lines), scanning allocates ~10MB of line
// data, plus output buffering for the 10-byte selections (~2.3MB). A 48MB
// ceiling provides ~3x headroom over the observed ~16.8MB while still catching
// regressions such as accumulating all lines before emitting.
func TestCutMemoryBounded(t *testing.T) {
	dir := t.TempDir()
	createLargeFileCut(t, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			cmdRunBCut(b, "cut -b 1-10 input.txt", dir)
		}
	})

	const maxBytesPerOp = 48 << 20 // 48MB ceiling (~3x observed ~16.8MB)
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("cut -b 1-10 allocated %d bytes/op on 10MB input; want < %d", bpo, maxBytesPerOp)
	}
}

// TestCutFieldsMemoryBounded asserts that cut -f allocation is bounded.
// Field mode calls strings.Split on each line, allocating a []string per line.
// This is O(input size) in total allocations. Using 1MB input keeps the
// expected allocation manageable (~5.5MB observed) while still validating
// that no additional unbounded growth occurs.
func TestCutFieldsMemoryBounded(t *testing.T) {
	dir := t.TempDir()
	// 1MB (not 10MB) because strings.Split allocates O(fields) per line.
	createLargeFileCut(t, dir, "input.txt", "alpha\tbeta\tgamma\tdelta\n", 1<<20)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			cmdRunBCut(b, "cut -f 1 input.txt", dir)
		}
	})

	const maxBytesPerOp = 16 << 20 // 16MB ceiling (~3x observed ~5.5MB on 1MB input)
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("cut -f 1 allocated %d bytes/op on 1MB input; want < %d", bpo, maxBytesPerOp)
	}
}
