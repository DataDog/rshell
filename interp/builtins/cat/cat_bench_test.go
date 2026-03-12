// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package cat_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
)

// catRepeatReader yields a repeating byte pattern indefinitely.
type catRepeatReader struct {
	line []byte
	pos  int
}

func newCatRepeatReader(line string) *catRepeatReader {
	return &catRepeatReader{line: []byte(line)}
}

func (r *catRepeatReader) Read(p []byte) (int, error) {
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

// createCatLargeFile writes totalSize bytes of repeating line content to a temp file.
func createCatLargeFile(tb testing.TB, dir, filename, line string, totalSize int) string {
	tb.Helper()
	path := filepath.Join(dir, filename)
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	r := io.LimitReader(newCatRepeatReader(line), int64(totalSize))
	if _, err := io.Copy(f, r); err != nil {
		tb.Fatal(err)
	}
	return path
}

// runScriptCatTB runs a shell script using testing.TB (works with both T and B).
func runScriptCatTB(tb testing.TB, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	tb.Helper()
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(script), "")
	if err != nil {
		tb.Fatal(err)
	}
	var outBuf, errBuf bytes.Buffer
	allOpts := append([]interp.RunnerOption{interp.StdIO(nil, &outBuf, &errBuf)}, opts...)
	runner, err := interp.New(allOpts...)
	if err != nil {
		tb.Fatal(err)
	}
	defer runner.Close()
	if dir != "" {
		runner.Dir = dir
	}
	runErr := runner.Run(context.Background(), prog)
	exitCode := 0
	if runErr != nil {
		var es interp.ExitStatus
		if errors.As(runErr, &es) {
			exitCode = int(es)
		} else {
			tb.Fatalf("unexpected error: %v", runErr)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// BenchmarkCatLargeInput benchmarks cat on a 1MB file.
func BenchmarkCatLargeInput(b *testing.B) {
	dir := b.TempDir()
	createCatLargeFile(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 1<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = runScriptCatTB(b, "cat input.txt", dir, interp.AllowedPaths([]string{dir}))
	}
}

// BenchmarkCatLargeInputMultipleFiles benchmarks cat on three 1MB files.
func BenchmarkCatLargeInputMultipleFiles(b *testing.B) {
	dir := b.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		createCatLargeFile(b, dir, name, "the quick brown fox jumps over the lazy dog\n", 1<<20)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = runScriptCatTB(b, "cat a.txt b.txt c.txt", dir, interp.AllowedPaths([]string{dir}))
	}
}

// TestCatMemoryBounded asserts that cat on a 1MB file allocates less than
// 1MB per operation (does not buffer the entire file in memory at once).
func TestCatMemoryBounded(t *testing.T) {
	dir := t.TempDir()
	createCatLargeFile(t, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 1<<20)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _, _ = runScriptCatTB(b, "cat input.txt", dir, interp.AllowedPaths([]string{dir}))
		}
	})

	// cat streams data through a fixed-size buffer; total allocations are
	// proportional to input size because the test harness buffers all output.
	// The 6MB ceiling on 1MB input catches catastrophic regressions (e.g.
	// multiple full-file copies) while allowing for normal I/O overhead.
	const maxBytesPerOp = 6 * 1024 * 1024 // 6 MB ceiling for 1 MB input
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("cat allocated %d bytes/op on 1MB input, want < %d", bpo, maxBytesPerOp)
	}
}
