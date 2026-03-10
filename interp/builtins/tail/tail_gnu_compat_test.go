// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// GNU compatibility tests for the tail builtin.
//
// Expected outputs were captured from GNU coreutils tail 9.x (macOS Homebrew
// gtail) and are embedded as string literals so the tests run without any GNU
// tooling present on CI. To reproduce a reference output, run:
//
//	gtail [flags] [file]  # then inspect with cat -A to see exact bytes

package tail_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGNUCompatDefaultOutput — default output (last 10 lines) on a 12-line file.
//
// GNU command: gtail twelve.txt
// Expected:    last 10 lines
func TestGNUCompatDefaultOutput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "twelve.txt", twelveLines)
	stdout, _, code := cmdRun(t, "tail twelve.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line03\nline04\nline05\nline06\nline07\nline08\nline09\nline10\nline11\nline12\n", stdout)
}

// TestGNUCompatLinesN — -n N smaller than file length.
//
// GNU command: gtail -n 3 five.txt
// Expected:    "gamma\ndelta\nepsilon\n"
func TestGNUCompatLinesN(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "five.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -n 3 five.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "gamma\ndelta\nepsilon\n", stdout)
}

// TestGNUCompatLinesZero — -n 0: no output.
//
// GNU command: gtail -n 0 five.txt
// Expected:    ""
func TestGNUCompatLinesZero(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "five.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -n 0 five.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

// TestGNUCompatLinesLargerThanFile — -n N larger than file: print all lines.
//
// GNU command: gtail -n 100 five.txt
// Expected:    fiveLines
func TestGNUCompatLinesLargerThanFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "five.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -n 100 five.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, fiveLines, stdout)
}

// TestGNUCompatOffsetPlus1 — +1 outputs all lines.
//
// GNU command: gtail -n +1 five.txt
// Expected:    fiveLines
func TestGNUCompatOffsetPlus1(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "five.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -n +1 five.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, fiveLines, stdout)
}

// TestGNUCompatOffsetPlus2 — +2 skips the first line.
//
// GNU command: gtail -n +2 five.txt
// Expected:    "beta\ngamma\ndelta\nepsilon\n"
func TestGNUCompatOffsetPlus2(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "five.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -n +2 five.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "beta\ngamma\ndelta\nepsilon\n", stdout)
}

// TestGNUCompatLongFormLines — --lines=N long form.
//
// GNU command: gtail --lines=3 five.txt
// Expected:    "gamma\ndelta\nepsilon\n"
func TestGNUCompatLongFormLines(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "five.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail --lines=3 five.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "gamma\ndelta\nepsilon\n", stdout)
}

// TestGNUCompatNoTrailingNewline — last line without newline is reproduced exactly.
//
// GNU command: gtail -n 1 nonewline.txt   (nonewline.txt = "no newline at end")
// Expected:    "no newline at end"   (no trailing \n added)
func TestGNUCompatNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "nonewline.txt", "no newline at end")
	stdout, _, code := cmdRun(t, "tail -n 1 nonewline.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "no newline at end", stdout)
}

// TestGNUCompatEmptyFile — empty file produces no output.
//
// GNU command: gtail empty.txt
// Expected:    ""
func TestGNUCompatEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "empty.txt", "")
	stdout, _, code := cmdRun(t, "tail empty.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

// TestGNUCompatVerboseSingleFile — -v prints header even for a single file.
//
// GNU command: gtail -v one.txt   (one.txt = "only one line\n")
// Expected:    "==> one.txt <==\nonly one line\n"
func TestGNUCompatVerboseSingleFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "one.txt", "only one line\n")
	stdout, _, code := cmdRun(t, "tail -v one.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "==> one.txt <==\nonly one line\n", stdout)
}

// TestGNUCompatTwoFilesDefault — two files: headers and blank-line separator.
//
// GNU command: gtail -n 2 five.txt nonewline.txt
// Expected:    "==> five.txt <==\ndelta\nepsilon\n\n==> nonewline.txt <==\nno newline at end"
func TestGNUCompatTwoFilesDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "five.txt", fiveLines)
	writeFile(t, dir, "nonewline.txt", "no newline at end")
	stdout, _, code := cmdRun(t, "tail -n 2 five.txt nonewline.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "==> five.txt <==\ndelta\nepsilon\n\n==> nonewline.txt <==\nno newline at end", stdout)
}

// TestGNUCompatQuietTwoFiles — -q suppresses headers for multiple files.
//
// GNU command: gtail -q -n 2 five.txt nonewline.txt
// Expected:    "delta\nepsilon\nno newline at end"
func TestGNUCompatQuietTwoFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "five.txt", fiveLines)
	writeFile(t, dir, "nonewline.txt", "no newline at end")
	stdout, _, code := cmdRun(t, "tail -q -n 2 five.txt nonewline.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "delta\nepsilon\nno newline at end", stdout)
}

// TestGNUCompatBytesMode — -c N outputs exactly the last N bytes.
//
// GNU command: gtail -c 5 five.txt
// Expected:    "ilon\n"  (last 5 bytes of "...epsilon\n")
func TestGNUCompatBytesMode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "five.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -c 5 five.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "ilon\n", stdout)
}

// TestGNUCompatBytesModeOffset — -c +3 skips first 2 bytes.
//
// GNU command: gtail -c +3 abcde.txt
// Expected:    "cde"
func TestGNUCompatBytesModeOffset(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "abcde.txt", "abcde")
	stdout, _, code := cmdRun(t, "tail -c +3 abcde.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "cde", stdout)
}

// TestGNUCompatLastFlagWinsBytes — -n then -c: last flag (-c) wins.
//
// GNU command: gtail -n 2 -c 5 five.txt
// Expected: last 5 bytes of fiveLines
func TestGNUCompatLastFlagWinsBytes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "five.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -n 2 -c 5 five.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "ilon\n", stdout)
}

// TestGNUCompatLastFlagWinsLines — -c then -n: last flag (-n) wins.
//
// GNU command: gtail -c 5 -n 2 five.txt
// Expected: last 2 lines = "delta\nepsilon\n"
func TestGNUCompatLastFlagWinsLines(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "five.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -c 5 -n 2 five.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "delta\nepsilon\n", stdout)
}

// TestGNUCompatRejectedFlag — unknown flag produces exit 1 and non-empty stderr.
//
// GNU tail --no-such-flag → exit 1, stderr non-empty
func TestGNUCompatRejectedFlag(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "five.txt", fiveLines)
	_, stderr, code := cmdRun(t, "tail --no-such-flag five.txt", dir)
	assert.Equal(t, 1, code)
	assert.NotEmpty(t, stderr)
}
