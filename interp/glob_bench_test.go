// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !race

package interp_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

// createGlobDir creates a directory containing n files named file0000.txt …
// fileNNNN.txt and returns the directory path.
func createGlobDir(tb testing.TB, n int) string {
	tb.Helper()
	dir := tb.TempDir()
	for i := range n {
		name := filepath.Join(dir, fmt.Sprintf("file%04d.txt", i))
		f, err := os.Create(name)
		if err != nil {
			tb.Fatal(err)
		}
		f.Close()
	}
	return dir
}

// createNestedGlobDir creates a directory tree with nDirs subdirectories each
// containing nFiles files.
func createNestedGlobDir(tb testing.TB, nDirs, nFiles int) string {
	tb.Helper()
	dir := tb.TempDir()
	for d := range nDirs {
		sub := filepath.Join(dir, fmt.Sprintf("dir%03d", d))
		if err := os.Mkdir(sub, 0755); err != nil {
			tb.Fatal(err)
		}
		for f := range nFiles {
			name := filepath.Join(sub, fmt.Sprintf("f%04d.txt", f))
			fh, err := os.Create(name)
			if err != nil {
				tb.Fatal(err)
			}
			fh.Close()
		}
	}
	return dir
}

func runGlob(b *testing.B, script, dir string) {
	b.Helper()
	testutil.RunScriptDiscard(b, script, dir, interp.AllowedPaths([]string{dir}))
}

// BenchmarkGlobStar measures "echo *" in a 1000-entry directory.
func BenchmarkGlobStar(b *testing.B) {
	dir := createGlobDir(b, 1000)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		runGlob(b, "echo *", dir)
	}
}

// BenchmarkGlobStarLargeDir measures "echo *" in a 9999-entry directory
// (just under MaxGlobEntries=10k cap).
func BenchmarkGlobStarLargeDir(b *testing.B) {
	dir := createGlobDir(b, 9999)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		runGlob(b, "echo *", dir)
	}
}

// BenchmarkGlobPrefix measures a prefix glob "echo file0*" in a 1000-entry directory.
func BenchmarkGlobPrefix(b *testing.B) {
	dir := createGlobDir(b, 1000)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		runGlob(b, "echo file0*", dir)
	}
}

// BenchmarkGlobSuffix measures a suffix glob "echo *.txt" in a 1000-entry directory.
func BenchmarkGlobSuffix(b *testing.B) {
	dir := createGlobDir(b, 1000)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		runGlob(b, "echo *.txt", dir)
	}
}

// BenchmarkGlobQuestionMark measures "echo file????.txt" in a 1000-entry directory.
func BenchmarkGlobQuestionMark(b *testing.B) {
	dir := createGlobDir(b, 1000)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		runGlob(b, "echo file????.txt", dir)
	}
}

// BenchmarkGlobBracket measures "echo file[0-4]*.txt" in a 1000-entry directory.
func BenchmarkGlobBracket(b *testing.B) {
	dir := createGlobDir(b, 1000)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		runGlob(b, "echo file[0-4]*.txt", dir)
	}
}

// BenchmarkGlobNested measures "echo */*" across 20 subdirectories × 50 files.
func BenchmarkGlobNested(b *testing.B) {
	dir := createNestedGlobDir(b, 20, 50)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		runGlob(b, "echo */*", dir)
	}
}

// BenchmarkGlobNoMatch measures "echo *.xyz" where nothing matches
// (the pattern is returned as a literal).
func BenchmarkGlobNoMatch(b *testing.B) {
	dir := createGlobDir(b, 1000)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		runGlob(b, "echo *.xyz", dir)
	}
}

// BenchmarkGlobMultiplePatterns measures multiple globs in a single command.
func BenchmarkGlobMultiplePatterns(b *testing.B) {
	dir := createGlobDir(b, 1000)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		runGlob(b, "echo file00* file01* file02* file03*", dir)
	}
}

// BenchmarkGlobForLoop measures glob expansion used in a for loop iteration.
func BenchmarkGlobForLoop(b *testing.B) {
	dir := createGlobDir(b, 1000)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		runGlob(b, "for f in *.txt; do :; done", dir)
	}
}

// BenchmarkGlobSmallDir measures "echo *" in a small 10-entry directory
// to establish baseline overhead.
func BenchmarkGlobSmallDir(b *testing.B) {
	dir := createGlobDir(b, 10)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		runGlob(b, "echo *", dir)
	}
}

// BenchmarkGlobExceedsCap measures "echo *" in a directory that exceeds
// MaxGlobEntries, verifying the rejection path is fast.
func BenchmarkGlobExceedsCap(b *testing.B) {
	dir := createGlobDir(b, 10001)
	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		runGlob(b, "echo *", dir)
	}
}

// TestGlobMemoryBounded asserts that glob expansion in a large directory
// does not allocate pathological amounts of memory. With 1000 entries of
// ~12-byte names, allocation should be on the order of a few hundred KB.
func TestGlobMemoryBounded(t *testing.T) {
	dir := createGlobDir(t, 1000)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			runGlob(b, "echo *", dir)
		}
	})

	const maxBytesPerOp = 10 << 20 // 10 MB ceiling
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("glob echo * allocated %d bytes/op on 1000-entry dir; want < %d", bpo, maxBytesPerOp)
	}
}

// TestGlobLargeDirMemoryBounded asserts memory stays bounded for a 9999-entry
// directory (just under MaxGlobEntries=10k cap).
func TestGlobLargeDirMemoryBounded(t *testing.T) {
	dir := createGlobDir(t, 9999)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			runGlob(b, "echo *", dir)
		}
	})

	// With ~10k entries, expect allocation proportional to entry count.
	// Use a generous ceiling to catch regressions, not measure precisely.
	const maxBytesPerOp = 50 << 20 // 50 MB ceiling
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("glob echo * allocated %d bytes/op on 9999-entry dir; want < %d", bpo, maxBytesPerOp)
	}
}

// TestGlobNestedMemoryBounded asserts memory stays bounded for nested globs.
func TestGlobNestedMemoryBounded(t *testing.T) {
	dir := createNestedGlobDir(t, 20, 50)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			runGlob(b, "echo */*", dir)
		}
	})

	const maxBytesPerOp = 20 << 20 // 20 MB ceiling
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("glob echo */* allocated %d bytes/op on 20×50 nested dir; want < %d", bpo, maxBytesPerOp)
	}
}
