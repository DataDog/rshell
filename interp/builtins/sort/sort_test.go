// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package sort_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/interp"
	"github.com/DataDog/rshell/interp/builtins/testutil"
)

func runScriptCtx(ctx context.Context, t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, opts...)
}

func runScript(t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScript(t, script, dir, opts...)
}

func cmdRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return runScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	return name
}

// ---------------------------------------------------------------------------
// Default / basic sorting
// ---------------------------------------------------------------------------

func TestSortDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "banana\napple\ncherry\n")
	stdout, _, code := cmdRun(t, "sort f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "apple\nbanana\ncherry\n", stdout)
}

func TestSortReverse(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "banana\napple\ncherry\n")
	stdout, _, code := cmdRun(t, "sort -r f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "cherry\nbanana\napple\n", stdout)
}

func TestSortUnique(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "b\na\nb\nc\na\n")
	stdout, _, code := cmdRun(t, "sort -u f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\nc\n", stdout)
}

func TestSortEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "")
	stdout, _, code := cmdRun(t, "sort f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

func TestSortSingleLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "only\n")
	stdout, _, code := cmdRun(t, "sort f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "only\n", stdout)
}

func TestSortNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "beta\nalpha")
	stdout, _, code := cmdRun(t, "sort f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "alpha\nbeta\n", stdout)
}

func TestSortMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "cherry\n")
	writeFile(t, dir, "b.txt", "apple\nbanana\n")
	stdout, _, code := cmdRun(t, "sort a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "apple\nbanana\ncherry\n", stdout)
}

func TestSortStable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "b 1\na 2\nb 2\na 1\n")
	stdout, _, code := cmdRun(t, "sort -s -k 1,1 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a 2\na 1\nb 1\nb 2\n", stdout)
}

// ---------------------------------------------------------------------------
// Numeric sorts
// ---------------------------------------------------------------------------

func TestSortNumeric(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "11\n2\n100\n")
	stdout, _, code := cmdRun(t, "sort -n f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2\n11\n100\n", stdout)
}

func TestSortNumericNegative(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "-1\n-9\n5\n")
	stdout, _, code := cmdRun(t, "sort -n f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "-9\n-1\n5\n", stdout)
}

func TestSortNumericDecimal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", ".02\n.01\n.001\n")
	stdout, _, code := cmdRun(t, "sort -n f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, ".001\n.01\n.02\n", stdout)
}

func TestSortGeneralNumeric(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "1e2\n2e1\n5.5\n")
	stdout, _, code := cmdRun(t, "sort -g f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "5.5\n2e1\n1e2\n", stdout)
}

func TestSortHumanNumeric(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "1G\n2K\n3M\n")
	stdout, _, code := cmdRun(t, "sort -h f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2K\n3M\n1G\n", stdout)
}

func TestSortMonthSort(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "MAR\nJAN\nFEB\nDEC\n")
	stdout, _, code := cmdRun(t, "sort -M f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "JAN\nFEB\nMAR\nDEC\n", stdout)
}

func TestSortMonthCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "mar\njan\nfeb\n")
	stdout, _, code := cmdRun(t, "sort -M f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "jan\nfeb\nmar\n", stdout)
}

func TestSortVersionSort(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "file10\nfile2\nfile1\n")
	stdout, _, code := cmdRun(t, "sort -V f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "file1\nfile2\nfile10\n", stdout)
}

// ---------------------------------------------------------------------------
// Key sorting
// ---------------------------------------------------------------------------

func TestSortKeyField(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "b 1\na 2\nc 1\n")
	stdout, _, code := cmdRun(t, "sort -k 2,2 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "b 1\nc 1\na 2\n", stdout)
}

func TestSortKeySeparator(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a:2:x\nc:1:y\nb:3:z\n")
	stdout, _, code := cmdRun(t, "sort -t : -k 2,2 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "c:1:y\na:2:x\nb:3:z\n", stdout)
}

func TestSortKeyNumeric(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a 20\nb 3\nc 100\n")
	stdout, _, code := cmdRun(t, "sort -k 2,2n f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "b 3\na 20\nc 100\n", stdout)
}

func TestSortKeyReverse(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a 1\nb 2\nc 3\n")
	stdout, _, code := cmdRun(t, "sort -k 2,2nr f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "c 3\nb 2\na 1\n", stdout)
}

func TestSortMultipleKeys(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "b 1\na 1\nb 2\na 2\n")
	stdout, _, code := cmdRun(t, "sort -k 2,2n -k 1,1 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a 1\nb 1\na 2\nb 2\n", stdout)
}

// ---------------------------------------------------------------------------
// Check mode
// ---------------------------------------------------------------------------

func TestSortCheckSorted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "A\nB\nC\n")
	_, _, code := cmdRun(t, "sort -c f.txt", dir)
	assert.Equal(t, 0, code)
}

func TestSortCheckUnsorted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "A\nC\nB\n")
	_, stderr, code := cmdRun(t, "sort -c f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "disorder")
}

func TestSortCheckQuiet(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "A\nC\nB\n")
	_, stderr, code := cmdRun(t, "sort -C f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stderr)
}

func TestSortCheckUnique(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "A\nA\nB\n")
	_, stderr, code := cmdRun(t, "sort -cu f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "disorder")
}

// ---------------------------------------------------------------------------
// Flags: -b, -d, -f, -i
// ---------------------------------------------------------------------------

func TestSortIgnoreCase(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "Banana\napple\nCherry\n")
	stdout, _, code := cmdRun(t, "sort -f f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "apple\nBanana\nCherry\n", stdout)
}

func TestSortIgnoreLeadingBlanks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "  b\na\n")
	stdout, _, code := cmdRun(t, "sort -b f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n  b\n", stdout)
}

func TestSortDictionaryOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a-b\nab\nac\n")
	stdout, _, code := cmdRun(t, "sort -d f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a-b\nab\nac\n", stdout)
}

// ---------------------------------------------------------------------------
// Error cases
// ---------------------------------------------------------------------------

func TestSortMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "sort nonexistent.txt", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "sort:")
}

func TestSortUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "sort --follow f.txt", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "sort:")
}

func TestSortIncompatibleModes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")
	_, stderr, code := cmdRun(t, "sort -n -g f.txt", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "incompatible")
}

func TestSortIncompatibleCheckFlags(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")
	_, stderr, code := cmdRun(t, "sort -c -C f.txt", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "incompatible")
}

func TestSortInvalidKeyZeroField(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")
	_, stderr, code := cmdRun(t, "sort -k 0 f.txt", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "sort:")
}

// ---------------------------------------------------------------------------
// Stdin
// ---------------------------------------------------------------------------

func TestSortStdin(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "input.txt", "cherry\napple\nbanana\n")
	stdout, _, code := cmdRun(t, "sort < input.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "apple\nbanana\ncherry\n", stdout)
}

// ---------------------------------------------------------------------------
// Help
// ---------------------------------------------------------------------------

func TestSortHelp(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, "sort --help", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage:")
}

// ---------------------------------------------------------------------------
// Context cancellation
// ---------------------------------------------------------------------------

func TestSortContextCancellation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\nb\nc\n")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	cancel()
	runScriptCtx(ctx, t, "sort f.txt", dir, interp.AllowedPaths([]string{dir}))
}

// ---------------------------------------------------------------------------
// Outside allowed paths
// ---------------------------------------------------------------------------

func TestSortOutsideAllowedPaths(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "sort /etc/hostname", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "sort:")
}

// ---------------------------------------------------------------------------
// Hardening / RULES.md compliance
// ---------------------------------------------------------------------------

func TestSortLargeLineCount(t *testing.T) {
	dir := t.TempDir()
	var sb []byte
	for i := 0; i < 1000; i++ {
		sb = append(sb, "line\n"...)
	}
	writeFile(t, dir, "f.txt", string(sb))
	stdout, _, code := cmdRun(t, "sort f.txt", dir)
	assert.Equal(t, 0, code)
	assert.NotEmpty(t, stdout)
}

func TestSortNumericOverflow(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "99999999999999999999\n1\n")
	stdout, _, code := cmdRun(t, "sort -n f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n99999999999999999999\n", stdout)
}

func TestSortNumericNonNumeric(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "abc\n5\nxyz\n")
	stdout, _, code := cmdRun(t, "sort -n f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "abc\nxyz\n5\n", stdout)
}

func TestSortDoubleDash(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "-r.txt", "b\na\n")
	stdout, _, code := cmdRun(t, "sort -- -r.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\n", stdout)
}

func TestSortNilStdin(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := runScript(t, "sort", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

func TestSortPipeInput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "cherry\napple\nbanana\n")
	stdout, _, code := cmdRun(t, "sort < f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "apple\nbanana\ncherry\n", stdout)
}

func TestSortZeroTerminated(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "b\x00a\x00c\x00")
	stdout, _, code := cmdRun(t, "sort -z f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\x00b\x00c\x00", stdout)
}

func TestSortMergeFlag(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "apple\ncherry\n")
	writeFile(t, dir, "b.txt", "banana\n")
	stdout, _, code := cmdRun(t, "sort -m a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "apple\nbanana\ncherry\n", stdout)
}

func TestSortRandomSort(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\nb\nc\nd\ne\n")
	stdout, _, code := cmdRun(t, "sort -R f.txt", dir)
	assert.Equal(t, 0, code)
	assert.NotEmpty(t, stdout)
	lines := 0
	for _, c := range stdout {
		if c == '\n' {
			lines++
		}
	}
	assert.Equal(t, 5, lines)
}

func TestSortRandomUnique(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\nb\nb\nc\n")
	stdout, _, code := cmdRun(t, "sort -Ru f.txt", dir)
	assert.Equal(t, 0, code)
	lines := 0
	for _, c := range stdout {
		if c == '\n' {
			lines++
		}
	}
	assert.Equal(t, 3, lines)
}

func TestSortGeneralNumericNaN(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "abc\n5\nNaN\n")
	stdout, _, code := cmdRun(t, "sort -g f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "NaN\nabc\n5\n", stdout)
}

func TestSortIncompatibleDictNumeric(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")
	_, stderr, code := cmdRun(t, "sort -d -n f.txt", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "incompatible")
}

func TestSortKeyInvalidCharZero(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")
	_, stderr, code := cmdRun(t, "sort -k 1.0 f.txt", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "sort:")
}

func TestSortCheckNumeric(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "1\n2\n10\n")
	_, _, code := cmdRun(t, "sort -cn f.txt", dir)
	assert.Equal(t, 0, code)
}

func TestSortCheckNumericUnsorted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "1\n10\n2\n")
	_, stderr, code := cmdRun(t, "sort -cn f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "disorder")
}
