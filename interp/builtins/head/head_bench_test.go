// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package head_test

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

// repeatReader yields a repeating byte pattern indefinitely.
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

// createLargeFile writes totalSize bytes of repeating line content to a temp file.
func createLargeFile(tb testing.TB, dir, filename, line string, totalSize int) string {
	tb.Helper()
	path := filepath.Join(dir, filename)
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	r := io.LimitReader(newRepeatReader(line), int64(totalSize))
	if _, err := io.Copy(f, r); err != nil {
		tb.Fatal(err)
	}
	return path
}

// runScriptTB runs a shell script using testing.TB (works with both T and B).
func runScriptTB(tb testing.TB, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
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

// BenchmarkHeadTenLines benchmarks head -n 10 on a 10MB file of short lines.
func BenchmarkHeadTenLines(b *testing.B) {
	dir := b.TempDir()
	createLargeFile(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = runScriptTB(b, "head -n 10 input.txt", dir, interp.AllowedPaths([]string{dir}))
	}
}

// BenchmarkHeadOneLine benchmarks head -n 1 on a 10MB file of short lines.
func BenchmarkHeadOneLine(b *testing.B) {
	dir := b.TempDir()
	createLargeFile(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = runScriptTB(b, "head -n 1 input.txt", dir, interp.AllowedPaths([]string{dir}))
	}
}

// BenchmarkHeadBytes benchmarks head -c 1024 on a 10MB file.
func BenchmarkHeadBytes(b *testing.B) {
	dir := b.TempDir()
	createLargeFile(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = runScriptTB(b, "head -c 1024 input.txt", dir, interp.AllowedPaths([]string{dir}))
	}
}

// TestHeadMemoryBoundedLines asserts that head -n 10 on a 10MB file
// allocates less than 512KB per operation (does not buffer the whole file).
func TestHeadMemoryBoundedLines(t *testing.T) {
	dir := t.TempDir()
	createLargeFile(t, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _, _ = runScriptTB(b, "head -n 10 input.txt", dir, interp.AllowedPaths([]string{dir}))
		}
	})

	const maxBytesPerOp = 512 * 1024 // 512 KB ceiling
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("head -n 10 allocated %d bytes/op on 10MB input, want < %d", bpo, maxBytesPerOp)
	}
}

// TestHeadMemoryBoundedBytes asserts that head -c 1024 on a 10MB file
// allocates less than 512KB per operation.
func TestHeadMemoryBoundedBytes(t *testing.T) {
	dir := t.TempDir()
	createLargeFile(t, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _, _ = runScriptTB(b, "head -c 1024 input.txt", dir, interp.AllowedPaths([]string{dir}))
		}
	})

	const maxBytesPerOp = 512 * 1024 // 512 KB ceiling
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("head -c 1024 allocated %d bytes/op on 10MB input, want < %d", bpo, maxBytesPerOp)
	}
}
