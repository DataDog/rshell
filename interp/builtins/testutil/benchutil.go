// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package testutil provides shared test helpers for builtin command tests.
package testutil

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
)

// ParseScript parses a shell script string into an AST. Works with both
// *testing.T and *testing.B via the testing.TB interface.
func ParseScript(tb testing.TB, script string) *syntax.File {
	tb.Helper()
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(script), "")
	if err != nil {
		tb.Fatal(err)
	}
	return prog
}

// RunBenchScript runs a shell script in a benchmark context. It parses the
// script on every call — use RunParsedBenchScript inside b.N loops to avoid
// benchmarking the parser.
func RunBenchScript(b *testing.B, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	b.Helper()
	prog := ParseScript(b, script)
	return RunParsedBenchScript(b, prog, dir, opts...)
}

// RunParsedBenchScript runs a pre-parsed shell program in a benchmark context.
// Use this inside b.N loops to avoid benchmarking the parser.
func RunParsedBenchScript(b *testing.B, prog *syntax.File, dir string, opts ...interp.RunnerOption) (string, string, int) {
	b.Helper()
	var outBuf, errBuf bytes.Buffer
	allOpts := append([]interp.RunnerOption{interp.StdIO(nil, &outBuf, &errBuf)}, opts...)
	runner, err := interp.New(allOpts...)
	if err != nil {
		b.Fatal(err)
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
			b.Fatalf("unexpected error: %v", runErr)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}
