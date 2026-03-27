// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

import (
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseSnippet parses a minimal Go source file containing the given body
// inserted into a function named "f". The returned file can be passed to
// checkFileScannerBuffer or checkFileOpenFileClose.
func parseSnippet(t *testing.T, body string) (*token.FileSet, interface{ Pos() token.Pos }) {
	t.Helper()
	src := `package p

import (
	"bufio"
	"context"
	"os"
)

type fakeCtx struct{}
func (fakeCtx) OpenFile(ctx context.Context, path string, flags int, mode os.FileMode) (interface{ Read([]byte)(int,error); Close() error }, error) {
	return nil, nil
}

func run(callCtx fakeCtx) {
` + body + `
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "snippet.go", src, 0)
	require.NoError(t, err)
	return fset, f
}

// collectViolations runs fn against a parsed snippet and returns all reported
// violation messages.
func collectViolations(t *testing.T, body string, fn func(f interface{ Pos() token.Pos }, report func(token.Pos, string, ...any))) []string {
	t.Helper()
	_, f := parseSnippet(t, body)

	type astFile interface {
		Pos() token.Pos
	}

	var msgs []string
	fn(f, func(_ token.Pos, format string, args ...any) {
		msg := format
		if len(args) > 0 {
			msg = format // simplified: just collect format for test assertions
		}
		msgs = append(msgs, msg)
	})
	return msgs
}

// --- ScannerBuffer rule tests ---

func TestScannerBufferClean(t *testing.T) {
	// Scanner with Buffer() called — must not be flagged.
	fset := token.NewFileSet()
	src := `package p
import "bufio"
func run() {
	sc := bufio.NewScanner(nil)
	sc.Buffer(make([]byte, 4096), 1<<20)
	_ = sc.Scan()
}
`
	f, err := parser.ParseFile(fset, "clean.go", src, 0)
	require.NoError(t, err)

	var violations []string
	checkFileScannerBuffer(f, func(_ token.Pos, format string, _ ...any) {
		violations = append(violations, format)
	})
	assert.Empty(t, violations, "Scanner with Buffer() should not be flagged")
}

func TestScannerBufferMissing(t *testing.T) {
	// Scanner without Buffer() — must be flagged.
	fset := token.NewFileSet()
	src := `package p
import "bufio"
func run() {
	sc := bufio.NewScanner(nil)
	_ = sc.Scan()
}
`
	f, err := parser.ParseFile(fset, "missing.go", src, 0)
	require.NoError(t, err)

	var violations []string
	checkFileScannerBuffer(f, func(_ token.Pos, format string, _ ...any) {
		violations = append(violations, format)
	})
	assert.Len(t, violations, 1, "Scanner without Buffer() must be flagged")
}

func TestScannerBufferInsideFuncLit(t *testing.T) {
	// Scanner inside a func literal without Buffer() — must be flagged as its own scope.
	fset := token.NewFileSet()
	src := `package p
import "bufio"
func run() {
	fn := func() {
		sc := bufio.NewScanner(nil)
		_ = sc.Scan()
	}
	fn()
}
`
	f, err := parser.ParseFile(fset, "funlit.go", src, 0)
	require.NoError(t, err)

	var violations []string
	checkFileScannerBuffer(f, func(_ token.Pos, format string, _ ...any) {
		violations = append(violations, format)
	})
	assert.Len(t, violations, 1, "Scanner inside func literal without Buffer() must be flagged")
}

func TestScannerBufferOuterCleanInnerMissing(t *testing.T) {
	// Outer function has Buffer(), inner func literal does not — only inner flagged.
	fset := token.NewFileSet()
	src := `package p
import "bufio"
func run() {
	sc := bufio.NewScanner(nil)
	sc.Buffer(make([]byte, 4096), 1<<20)
	_ = sc.Scan()

	inner := func() {
		sc2 := bufio.NewScanner(nil)
		_ = sc2.Scan()
	}
	inner()
}
`
	f, err := parser.ParseFile(fset, "mixed.go", src, 0)
	require.NoError(t, err)

	var violations []string
	checkFileScannerBuffer(f, func(_ token.Pos, format string, _ ...any) {
		violations = append(violations, format)
	})
	assert.Len(t, violations, 1, "only the inner scanner without Buffer() must be flagged")
}

// --- OpenFileClose rule tests ---

func TestOpenFileCloseClean(t *testing.T) {
	// File opened and closed via defer — must not be flagged.
	fset := token.NewFileSet()
	src := `package p
import ("context"; "os")
type cc struct{}
func (cc) OpenFile(ctx context.Context, path string, flags int, mode os.FileMode) (interface{ Read([]byte)(int,error); Close() error }, error) { return nil, nil }
func run(callCtx cc) {
	f, err := callCtx.OpenFile(context.Background(), "x", os.O_RDONLY, 0)
	if err != nil { return }
	defer f.Close()
}
`
	f, err := parser.ParseFile(fset, "clean.go", src, 0)
	require.NoError(t, err)

	var violations []string
	checkFileOpenFileClose(f, func(_ token.Pos, format string, _ ...any) {
		violations = append(violations, format)
	})
	assert.Empty(t, violations, "OpenFile with defer f.Close() should not be flagged")
}

func TestOpenFileCloseHandOff(t *testing.T) {
	// File opened, handed off to rc, then rc closed — must not be flagged.
	fset := token.NewFileSet()
	src := `package p
import ("context"; "io"; "os")
type cc struct{}
func (cc) OpenFile(ctx context.Context, path string, flags int, mode os.FileMode) (interface{ Read([]byte)(int,error); Close() error }, error) { return nil, nil }
func run(callCtx cc) {
	var rc io.ReadCloser
	f, err := callCtx.OpenFile(context.Background(), "x", os.O_RDONLY, 0)
	if err != nil { return }
	rc = f
	defer rc.Close()
}
`
	f, err := parser.ParseFile(fset, "handoff.go", src, 0)
	require.NoError(t, err)

	var violations []string
	checkFileOpenFileClose(f, func(_ token.Pos, format string, _ ...any) {
		violations = append(violations, format)
	})
	assert.Empty(t, violations, "OpenFile handed off to rc and rc closed should not be flagged")
}

func TestOpenFileCloseMissing(t *testing.T) {
	// File opened but never closed — must be flagged.
	fset := token.NewFileSet()
	src := `package p
import ("context"; "os")
type cc struct{}
func (cc) OpenFile(ctx context.Context, path string, flags int, mode os.FileMode) (interface{ Read([]byte)(int,error); Close() error }, error) { return nil, nil }
func run(callCtx cc) {
	f, err := callCtx.OpenFile(context.Background(), "x", os.O_RDONLY, 0)
	if err != nil { return }
	_ = f
}
`
	f, err := parser.ParseFile(fset, "missing.go", src, 0)
	require.NoError(t, err)

	var violations []string
	checkFileOpenFileClose(f, func(_ token.Pos, format string, _ ...any) {
		violations = append(violations, format)
	})
	assert.Len(t, violations, 1, "OpenFile result not closed must be flagged")
}

func TestOpenFileCloseReturnTransfer(t *testing.T) {
	// File opened then returned to caller — must not be flagged.
	fset := token.NewFileSet()
	src := `package p
import ("context"; "os"; "io")
type cc struct{}
func (cc) OpenFile(ctx context.Context, path string, flags int, mode os.FileMode) (interface{ Read([]byte)(int,error); Close() error }, error) { return nil, nil }
func open(callCtx cc) (io.ReadCloser, error) {
	f, err := callCtx.OpenFile(context.Background(), "x", os.O_RDONLY, 0)
	if err != nil { return nil, err }
	return f, nil
}
`
	f, err := parser.ParseFile(fset, "return.go", src, 0)
	require.NoError(t, err)

	var violations []string
	checkFileOpenFileClose(f, func(_ token.Pos, format string, _ ...any) {
		violations = append(violations, format)
	})
	assert.Empty(t, violations, "OpenFile result returned to caller should not be flagged")
}
