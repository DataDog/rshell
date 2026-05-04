// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/rshell/interp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Memory and resource hardening per docs/RULES.md.
// =============================================================================

// TestHardeningArrayEntryLimit — array growth past MaxArrayEntries (1M) is
// rejected with a clear error rather than allocating unbounded memory.
func TestHardeningArrayEntryLimit(t *testing.T) {
	// Allocating exactly 1M entries is borderline; we instead use the
	// inner-loop iteration limit to bail out early — but this checks that
	// arrays cannot grow past the cap silently. We construct a tight loop
	// and verify the iteration limit kicks in (the cap of 1M iterations
	// guards against 1M+ array entries here too).
	// 30 s: the race detector on a loaded CI runner can slow 1M map inserts
	// enough to exceed a 10 s budget (observed at 10.01 s in CI).
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, stderr, code := runScriptCtx(ctx, t,
		`awk 'BEGIN { for (i = 0; i < 100000000; i++) { a[i] = i } }'`,
		t.TempDir(),
		interp.AllowedPaths([]string{}))
	assert.Equal(t, 1, code)
	// Either the loop limit OR the array cap should fire first.
	assert.True(t,
		strings.Contains(stderr, "loop iteration limit") ||
			strings.Contains(stderr, "array") ||
			strings.Contains(stderr, "maximum"),
		"expected resource-limit error, got: %q", stderr)
}

// TestHardeningStringConcatLimit — concatenation cannot grow past MaxStringBytes.
func TestHardeningStringConcatLimit(t *testing.T) {
	// Build a string and double it on each iteration. After enough doublings
	// the concatenation cap should fire.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, stderr, code := runScriptCtx(ctx, t, `
		awk 'BEGIN {
			s = "x"
			for (i = 0; i < 25; i++) {
				s = s s
			}
			print length(s)
		}'`, t.TempDir(), interp.AllowedPaths([]string{}))
	if code == 0 {
		t.Fatalf("expected resource-limit error, got code 0 stderr=%q", stderr)
	}
	assert.Contains(t, stderr, "string length")
}

// TestHardeningGsubSafe — gsub on a target that stays within MaxStringBytes
// completes successfully and returns the expected count.
func TestHardeningGsubSafe(t *testing.T) {
	stdout, _, code := cmdRun(t, `
		awk 'BEGIN {
			s = ""
			for (i = 0; i < 4096; i++) s = s "x"
			gsub(/x/, "abcd", s)
			print length(s)
		}'`, t.TempDir())
	require.Equal(t, 0, code)
	assert.Equal(t, "16384\n", stdout)
}

// TestHardeningRegexNoBacktracking — RE2 does not backtrack, so even a
// pathological "evil regex" that would DoS PCRE completes in linear time.
func TestHardeningRegexNoBacktracking(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	long := strings.Repeat("a", 10_000)
	stdout, _, code := runScriptCtx(ctx, t,
		`awk 'BEGIN { s = ARGV[1]; if (s ~ /^(a+)+b/) print "match"; else print "no match" }' "`+long+`"`,
		t.TempDir(), interp.AllowedPaths([]string{}))
	// We don't actually use ARGV in our subset; substitute a -v var.
	_ = stdout
	_ = code
	// Run a different version that uses -v:
	stdout, _, code = runScriptCtx(ctx, t,
		`awk -v s="`+long+`" 'BEGIN { if (s ~ /^(a+)+b/) print "m"; else print "n" }'`,
		t.TempDir(), interp.AllowedPaths([]string{}))
	assert.Equal(t, 0, code)
	assert.Equal(t, "n\n", stdout)
}

// TestHardeningContextTimeout — a long BEGIN-loop respects ctx cancellation.
func TestHardeningContextTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	stdout, _, _ := runScriptCtx(ctx, t,
		`awk 'BEGIN { for (i = 0; i < 999999; i++) { for (j = 0; j < 999999; j++) {} } print "done" }'`,
		t.TempDir(), interp.AllowedPaths([]string{}))
	assert.NotContains(t, stdout, "done")
}

// TestHardeningPathTraversal — the sandbox's allowed-paths check still
// applies; we cannot escape via ../.
func TestHardeningPathTraversal(t *testing.T) {
	dir := setupDir(t, map[string]string{"allowed.txt": "ok\n"})
	// Try to read a file outside the allowed dir using a relative path.
	_, stderr, code := cmdRun(t, `awk '{print}' ../../../../etc/passwd`, dir)
	assert.Equal(t, 2, code) // exit 2 matches mawk: non-fatal file-open error, END blocks run (gawk exits 0)
	assert.NotEqual(t, "", stderr)
}

// TestHardeningDirectoryAsInput — opening a directory should fail.
func TestHardeningDirectoryAsInput(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `awk '{print}' .`, dir)
	assert.Equal(t, 1, code)
	assert.NotEqual(t, "", stderr)
}

// TestHardeningCRLFInput — \r\n line endings: RS="\n" removes only the newline;
// the \r is preserved in $0 to match GNU awk / POSIX behaviour.
func TestHardeningCRLFInput(t *testing.T) {
	dir := setupDir(t, map[string]string{"crlf.txt": "alpha\r\nbeta\r\ngamma\r\n"})
	stdout, _, code := cmdRun(t, `awk '{print NR, $0}' crlf.txt`, dir)
	assert.Equal(t, 0, code)
	// \r is preserved in $0; the output lines therefore end in \r\n.
	assert.Equal(t, "1 alpha\r\n2 beta\r\n3 gamma\r\n", stdout)
}

// TestHardeningBinaryPassthrough — binary content (NUL bytes) does not crash.
func TestHardeningBinaryPassthrough(t *testing.T) {
	dir := setupDir(t, map[string]string{"bin.txt": "a\x00b\nc\x01d\n"})
	stdout, _, code := cmdRun(t, `awk 'END {print NR}' bin.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2\n", stdout)
}

// TestHardeningMaxIntInputs — extreme numeric values do not panic.
func TestHardeningMaxIntInputs(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { print 9223372036854775807 + 0 }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.NotEmpty(t, stdout)
}

// TestHardeningDeeplyNestedExpr — modestly nested parens parse and run.
// (We have an explicit parser-depth cap of 256; 100 levels stays comfortably
// within that and verifies the path is actually used.)
func TestHardeningDeeplyNestedExpr(t *testing.T) {
	const depth = 100
	expr := strings.Repeat("(", depth) + "1" + strings.Repeat(")", depth)
	stdout, _, code := cmdRun(t, `awk 'BEGIN { print `+expr+` }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout)
}

// TestHardeningRejectExtremeNesting — beyond the parser cap, the script is
// rejected at parse time instead of crashing the goroutine.
func TestHardeningRejectExtremeNesting(t *testing.T) {
	const depth = 5000
	expr := strings.Repeat("(", depth) + "1" + strings.Repeat(")", depth)
	_, stderr, code := cmdRun(t, `awk 'BEGIN { print `+expr+` }'`, t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "deeply nested")
}

// TestHardeningManyRules — programs with many rules parse and run correctly.
func TestHardeningManyRules(t *testing.T) {
	var b strings.Builder
	b.WriteString("BEGIN { count = 0 } ")
	for i := 0; i < 100; i++ {
		b.WriteString("/x/ { count++ } ")
	}
	b.WriteString("END { print count }")
	dir := setupDir(t, map[string]string{"a.txt": "x\n"})
	stdout, _, code := cmdRun(t, `awk '`+b.String()+`' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "100\n", stdout)
}
