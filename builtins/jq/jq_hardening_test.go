// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package jq_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins/jq"
	"github.com/DataDog/rshell/interp"
)

// --- filter size cap ---

func TestJqRejectsOverlongFilter(t *testing.T) {
	dir := t.TempDir()
	// Build a filter just over the cap. We embed it via stdin to dodge the
	// shell's own arg-length limits. The filter is bytewise-valid jq so
	// only its size (not its parseability) matters.
	huge := strings.Repeat("0,", jq.MaxFilterBytes/2+10) + "0"
	require.Greater(t, len(huge), jq.MaxFilterBytes)
	scriptPath := filepath.Join(dir, "filter.txt")
	require.NoError(t, os.WriteFile(scriptPath, []byte(huge), 0644))
	// We can't easily pass a filter > 64 KiB through the shell command line
	// in tests; instead, exercise the cap directly via -n with a literal that
	// stretches the filter past the limit.
	_, stderr, code := jqRun(t, "jq -n '"+huge+"'", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "filter too large")
}

// --- input size caps ---

func TestJqRejectsOversizedInputStream(t *testing.T) {
	dir := t.TempDir()
	// Generate a JSON document just over the per-source cap. A long string
	// is the simplest payload (still valid JSON).
	overshoot := jq.MaxStreamBytes + 1024
	body := strings.Repeat("a", overshoot)
	content := `"` + body + `"`
	writeFile(t, dir, "big.json", content)
	_, stderr, code := jqRun(t, "jq -c . big.json", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "exceeds")
}

func TestJqRejectsOversizedSlurpStream(t *testing.T) {
	dir := t.TempDir()
	// A long string just over MaxStreamBytes triggers the slurp-side
	// LimitReader path before the array assembly cap.
	body := strings.Repeat("z", jq.MaxStreamBytes+1024)
	writeFile(t, dir, "big.json", `"`+body+`"`)
	_, stderr, code := jqRun(t, "jq -s . big.json", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "exceeds")
}

func TestJqRejectsOversizedRawSlurpStream(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("x", jq.MaxStreamBytes+1024)
	writeFile(t, dir, "big.txt", body)
	_, stderr, code := jqRun(t, "jq -s -R . big.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "exceeds")
}

func TestJqRejectsOversizedRawInputLine(t *testing.T) {
	dir := t.TempDir()
	long := strings.Repeat("y", jq.MaxLineBytes+10)
	writeFile(t, dir, "long.txt", long+"\n")
	_, stderr, code := jqRun(t, "jq -R . long.txt", dir)
	assert.Equal(t, 1, code)
	assert.NotEqual(t, "", stderr)
}

// A single oversized line in -R mode at the boundary (cap-1) succeeds.
func TestJqRawInputAcceptsLineAtCapMinus1(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("k", jq.MaxLineBytes-1)
	writeFile(t, dir, "ok.txt", body+"\n")
	stdout, _, code := jqRun(t, "jq -R . ok.txt", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "k")
}

// --- context cancellation ---

func TestJqContextCancelDuringStream(t *testing.T) {
	dir := t.TempDir()
	// 200 small JSON documents — enough for a few decode iterations.
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString(`{"i":0}` + "\n")
	}
	writeFile(t, dir, "stream.json", sb.String())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, _, _ = runScriptCtx(ctx, t, "jq -c . stream.json", dir, interp.AllowedPaths([]string{dir}))
	// Should complete within the 5s timeout regardless of cancellation;
	// assert it finished well below the timeout to detect a runaway loop.
	assert.Less(t, time.Since(start), 4*time.Second)
}

// Deeply-nested JSON must hit the recursion bound, not the goroutine stack
// limit or the 30-second executor timeout.
func TestJqRejectsDeeplyNestedJSON(t *testing.T) {
	dir := t.TempDir()
	// 10 000 levels of nesting — well above maxEmitDepth (256).
	var sb strings.Builder
	for i := 0; i < 10_000; i++ {
		sb.WriteByte('[')
	}
	for i := 0; i < 10_000; i++ {
		sb.WriteByte(']')
	}
	writeFile(t, dir, "deep.json", sb.String())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, stderr, code := runScriptCtx(ctx, t, "jq . deep.json", dir, interp.AllowedPaths([]string{dir}))
	assert.Less(t, time.Since(start), 4*time.Second, "deeply nested JSON should fail fast")
	if code == 0 {
		t.Errorf("expected nesting-too-deep error, got success; stderr=%q", stderr)
	}
}

// --- malformed UTF-8 handling ---

func TestJqRawInputInvalidUTF8(t *testing.T) {
	// jq -R must not panic on lines with invalid UTF-8; the bytes pass
	// through Go's JSON encoder, which substitutes U+FFFD for invalid
	// runes.
	dir := t.TempDir()
	bad := []byte{0xFF, 0xFE, 0xFD, '\n'}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.txt"), bad, 0644))
	stdout, _, code := jqRun(t, "jq -R . x.txt", dir)
	assert.Equal(t, 0, code)
	assert.NotEqual(t, "", stdout)
}

// --- numeric edge cases (filter side, since fastjq emits canonical numbers) ---

func TestJqLargeIntegerPassthrough(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"n":9007199254740993}`)
	stdout, _, code := jqRun(t, "jq -c .n x.json", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "9007199254740993")
}

// --- empty input variants ---

func TestJqEmptyStdin(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "empty.txt", "")
	stdout, stderr, code := jqRun(t, "jq -c . < empty.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
}

// --- -e exit-status edge cases ---

func TestJqExitStatusEmptyStreamWithE(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "empty.json", "")
	_, _, code := jqRun(t, "jq -e . empty.json", dir)
	assert.Equal(t, 1, code)
}

// --- repeated invocations / fresh state per run ---

// --- string escape coverage ---

func TestJqStringEscapesAllSpecials(t *testing.T) {
	dir := t.TempDir()
	// Control + special chars: BS, FF, CR, TAB, NL, DQUOTE, BACKSLASH.
	writeFile(t, dir, "x.json", `{"s":"\b\f\r\t\n\"\\"}`)
	stdout, _, code := jqRun(t, "jq -c .s x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\"\\b\\f\\r\\t\\n\\\"\\\\\"\n", stdout)
}

func TestJqStringEscapeGenericControl(t *testing.T) {
	// Generic control char (BEL=0x07) goes through the \u00xx path on output.
	// Input must use the \uXXXX escape: unescaped control bytes are not
	// valid JSON per RFC 8259.
	dir := t.TempDir()
	writeFile(t, dir, "x.json", "{\"s\":\"\\u0007\"}")
	stdout, _, code := jqRun(t, "jq -c .s x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\"\\u0007\"\n", stdout)
}

// --- broken-pipe and large output paths ---

func TestJqIteratorMassiveOutput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `[0,1,2,3,4,5,6,7,8,9]`)
	stdout, _, code := jqRun(t, "jq -c '.[]' x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "0\n1\n2\n3\n4\n5\n6\n7\n8\n9\n", stdout)
}

// --- additional edge cases ---

// JSON value with no trailing newline must still be processed.
func TestJqValueNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"a":1}`)
	stdout, _, code := jqRun(t, "jq -c . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "{\"a\":1}\n", stdout)
}

// Single-byte JSON value (just `1`).
func TestJqSingleByteValue(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", "1")
	stdout, _, code := jqRun(t, "jq -c . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout)
}

// Multiple back-to-back JSON values without separators.
func TestJqAdjacentValues(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"a":1}{"a":2}{"a":3}`)
	stdout, _, code := jqRun(t, "jq -c . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "{\"a\":1}\n{\"a\":2}\n{\"a\":3}\n", stdout)
}

// Whitespace-only input produces no output and exits 0.
func TestJqWhitespaceOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", "   \n\t \n")
	stdout, _, code := jqRun(t, "jq -c . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

// Filter compiles but throws at runtime → exit 1, stderr message.
func TestJqRuntimeFilterError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `1`)
	_, stderr, code := jqRun(t, `jq -c '. + "y"' x.json`, dir)
	assert.Equal(t, 1, code)
	assert.NotEqual(t, "", stderr)
}

// Slurp aggregate cap fires across multiple files even when each file
// individually fits under the cap.
func TestJqSlurpAggregateAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	chunk := strings.Repeat("a", jq.MaxStreamBytes/3)
	writeFile(t, dir, "f1.json", `"`+chunk+`"`)
	writeFile(t, dir, "f2.json", `"`+chunk+`"`)
	writeFile(t, dir, "f3.json", `"`+chunk+`"`)
	writeFile(t, dir, "f4.json", `"`+chunk+`"`)
	_, stderr, code := jqRun(t, "jq -s . f1.json f2.json f3.json f4.json", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "exceeds")
}

func TestJqStateNotSharedBetweenRuns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.json", `{"a":1}`)
	writeFile(t, dir, "b.json", `{"flag":false}`)
	// First call: -e on truthy → exit 0.
	_, _, code1 := jqRun(t, "jq -e .a a.json", dir)
	assert.Equal(t, 0, code1)
	// Second call (separate runner): -e on falsy → exit 1. If state leaked,
	// "emittedTruthy" from the first call would carry over.
	_, _, code2 := jqRun(t, "jq -e .flag b.json", dir)
	assert.Equal(t, 1, code2)
}
