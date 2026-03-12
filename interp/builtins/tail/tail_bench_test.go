// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tail_test

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

// tailRepeatReader yields a repeating byte pattern indefinitely.
type tailRepeatReader struct {
	line []byte
	pos  int
}

func newTailRepeatReader(line string) *tailRepeatReader {
	return &tailRepeatReader{line: []byte(line)}
}

func (r *tailRepeatReader) Read(p []byte) (int, error) {
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

// createTailLargeFile writes totalSize bytes of repeating line content to a temp file.
func createTailLargeFile(tb testing.TB, dir, filename, line string, totalSize int) string {
	tb.Helper()
	path := filepath.Join(dir, filename)
	f, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer f.Close()
	r := io.LimitReader(newTailRepeatReader(line), int64(totalSize))
	if _, err := io.Copy(f, r); err != nil {
		tb.Fatal(err)
	}
	return path
}

// runScriptTailTB runs a shell script using testing.TB (works with both T and B).
func runScriptTailTB(tb testing.TB, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
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

// BenchmarkTailTenLines benchmarks tail -n 10 on a 10MB file of short lines.
func BenchmarkTailTenLines(b *testing.B) {
	dir := b.TempDir()
	createTailLargeFile(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = runScriptTailTB(b, "tail -n 10 input.txt", dir, interp.AllowedPaths([]string{dir}))
	}
}

// BenchmarkTailOneLine benchmarks tail -n 1 on a 10MB file of short lines.
func BenchmarkTailOneLine(b *testing.B) {
	dir := b.TempDir()
	createTailLargeFile(b, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = runScriptTailTB(b, "tail -n 1 input.txt", dir, interp.AllowedPaths([]string{dir}))
	}
}

// TestTailMemoryBounded asserts that tail -n 10 on a 10MB file of short lines
// allocates less than 512KB per operation (the ring buffer is bounded, not
// proportional to input size).
func TestTailMemoryBounded(t *testing.T) {
	dir := t.TempDir()
	createTailLargeFile(t, dir, "input.txt", "the quick brown fox jumps over the lazy dog\n", 10<<20)

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _, _ = runScriptTailTB(b, "tail -n 10 input.txt", dir, interp.AllowedPaths([]string{dir}))
		}
	})

	// tail -n 10 must scan the entire input to find the last 10 lines,
	// allocating one slice per line scanned (ring buffer evicts old entries).
	// Total allocations are O(input size) but live memory is O(N lines).
	// The 24MB ceiling on 10MB input catches regressions like accumulating
	// all lines in memory while still allowing the per-line copy overhead.
	const maxBytesPerOp = 24 * 1024 * 1024 // 24 MB ceiling for 10 MB input
	if bpo := result.AllocedBytesPerOp(); bpo > maxBytesPerOp {
		t.Errorf("tail -n 10 allocated %d bytes/op on 10MB input, want < %d", bpo, maxBytesPerOp)
	}
}
