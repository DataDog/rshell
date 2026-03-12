// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package wc_test

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

// wcRepeatReader yields a repeating byte pattern indefinitely.
type wcRepeatReader struct {
	line []byte
	pos  int
}

func newWcRepeatReader(line string) *wcRepeatReader {
	return &wcRepeatReader{line: []byte(line)}
}

func (r *wcRepeatReader) Read(p []byte) (int, error) {
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

// createWcLargeFile writes totalSize bytes of repeating line content to a temp file.
func createWcLargeFile(tb testing.TB, dir, filename, line string, totalSize int) string {
	tb.Helper()
	path := filepath.Join(dir, filename)
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	r := io.LimitReader(newWcRepeatReader(line), int64(totalSize))
	if _, err := io.Copy(f, r); err != nil {
		tb.Fatal(err)
	}
	return path
}

// runScriptWcTB runs a shell script using testing.TB (works with both T and B).
func runScriptWcTB(tb testing.TB, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
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

// BenchmarkWcLines benchmarks wc -l on a 10MB file.
func BenchmarkWcLines(b *testing.B) {
	dir := b.TempDir()
	createWcLargeFile(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = runScriptWcTB(b, "wc -l input.txt", dir, interp.AllowedPaths([]string{dir}))
	}
}

// BenchmarkWcDefault benchmarks wc (lines + words + bytes) on a 10MB file.
func BenchmarkWcDefault(b *testing.B) {
	dir := b.TempDir()
	createWcLargeFile(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = runScriptWcTB(b, "wc input.txt", dir, interp.AllowedPaths([]string{dir}))
	}
}

// TestWcMemoryBounded asserts that wc -l on a 10MB file allocates less than
// 512KB per operation (does not buffer the entire file in memory).
func TestWcMemoryBounded(t *testing.T) {
	dir := t.TempDir()
	createWcLargeFile(t, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _, _ = runScriptWcTB(b, "wc -l input.txt", dir, interp.AllowedPaths([]string{dir}))
		}
	})

	const maxBytesPerOp = 512 * 1024 // 512 KB ceiling
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("wc -l allocated %d bytes/op on 10MB input, want < %d", bpo, maxBytesPerOp)
	}
}
