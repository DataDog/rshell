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

// runScriptCtx runs a shell script with a context and returns stdout, stderr,
// and the exit code.
func runScriptCtx(ctx context.Context, t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, opts...)
}

// runScript runs a shell script and returns stdout, stderr, and the exit code.
func runScript(t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScript(t, script, dir, opts...)
}

// cmdRun runs a sort command with AllowedPaths set to dir.
func cmdRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return runScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

// writeFile creates a file in dir with the given content.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
}

// --- Default behavior (lexicographic sort) ---

func TestSortDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "banana\napple\ncherry\n")
	stdout, _, code := cmdRun(t, "sort f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "apple\nbanana\ncherry\n", stdout)
}

func TestSortAlreadySorted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\nb\nc\n")
	stdout, _, code := cmdRun(t, "sort f.txt", dir)
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

func TestSortSingleLineNoNewline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "only")
	stdout, _, code := cmdRun(t, "sort f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "only\n", stdout)
}

func TestSortDuplicateLines(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "b\na\nb\na\n")
	stdout, _, code := cmdRun(t, "sort f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\na\nb\nb\n", stdout)
}

// --- Reverse sort ---

func TestSortReverse(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "banana\napple\ncherry\n")
	stdout, _, code := cmdRun(t, "sort -r f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "cherry\nbanana\napple\n", stdout)
}

func TestSortReverseLong(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "banana\napple\ncherry\n")
	stdout, _, code := cmdRun(t, "sort --reverse f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "cherry\nbanana\napple\n", stdout)
}

// --- Numeric sort ---

func TestSortNumeric(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "10\n2\n1\n20\n")
	stdout, _, code := cmdRun(t, "sort -n f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n2\n10\n20\n", stdout)
}

func TestSortNumericReverse(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "10\n2\n1\n20\n")
	stdout, _, code := cmdRun(t, "sort -n -r f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "20\n10\n2\n1\n", stdout)
}

func TestSortNumericWithNonNumeric(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "10\nabc\n2\n")
	stdout, _, code := cmdRun(t, "sort -n f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "abc\n2\n10\n", stdout)
}

func TestSortNumericNegative(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "5\n-3\n0\n-10\n")
	stdout, _, code := cmdRun(t, "sort -n f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "-10\n-3\n0\n5\n", stdout)
}

// --- Unique ---

func TestSortUnique(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "b\na\nb\na\nc\n")
	stdout, _, code := cmdRun(t, "sort -u f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\nc\n", stdout)
}

func TestSortUniqueNumeric(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "2\n1\n2\n3\n1\n")
	stdout, _, code := cmdRun(t, "sort -n -u f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n2\n3\n", stdout)
}

// --- Ignore case ---

func TestSortIgnoreCase(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "Banana\napple\nCherry\n")
	stdout, _, code := cmdRun(t, "sort -f f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "apple\nBanana\nCherry\n", stdout)
}

// --- Dictionary order ---

func TestSortDictionaryOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "b-b\na.a\nc_c\n")
	stdout, _, code := cmdRun(t, "sort -d f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a.a\nb-b\nc_c\n", stdout)
}

// --- Ignore leading blanks ---

func TestSortIgnoreLeadingBlanks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "  banana\napple\n   cherry\n")
	stdout, _, code := cmdRun(t, "sort -b f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "apple\n  banana\n   cherry\n", stdout)
}

// --- Field separator and key ---

func TestSortFieldSeparatorKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "c:3\na:1\nb:2\n")
	stdout, _, code := cmdRun(t, "sort -t : -k 2 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a:1\nb:2\nc:3\n", stdout)
}

func TestSortNumericKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "c:30\na:1\nb:20\n")
	stdout, _, code := cmdRun(t, "sort -t : -k 2n f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a:1\nb:20\nc:30\n", stdout)
}

func TestSortKeyWithFieldRange(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "c:3:z\na:1:x\nb:2:y\n")
	stdout, _, code := cmdRun(t, "sort -t : -k 2,2n f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a:1:x\nb:2:y\nc:3:z\n", stdout)
}

func TestSortMultipleKeys(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a:2\nb:1\na:1\n")
	stdout, _, code := cmdRun(t, "sort -t : -k 1,1 -k 2,2n f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a:1\na:2\nb:1\n", stdout)
}

// --- Check sorted ---

func TestSortCheckSorted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\nb\nc\n")
	_, _, code := cmdRun(t, "sort -c f.txt", dir)
	assert.Equal(t, 0, code)
}

func TestSortCheckUnsorted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "b\na\nc\n")
	_, stderr, code := cmdRun(t, "sort -c f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "disorder")
}

func TestSortCheckSilentUnsorted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "b\na\nc\n")
	_, stderr, code := cmdRun(t, "sort -C f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stderr)
}

// --- Stable sort ---

func TestSortStable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "b:2\na:1\nb:1\na:2\n")
	stdout, _, code := cmdRun(t, "sort -s -t : -k 1,1 f.txt", dir)
	assert.Equal(t, 0, code)
	// With stable sort, equal keys preserve input order.
	assert.Equal(t, "a:1\na:2\nb:2\nb:1\n", stdout)
}

// --- Stdin ---

func TestSortStdin(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src.txt", "banana\napple\ncherry\n")
	stdout, _, code := cmdRun(t, "sort < src.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "apple\nbanana\ncherry\n", stdout)
}

func TestSortStdinDash(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src.txt", "banana\napple\ncherry\n")
	stdout, _, code := cmdRun(t, "sort - < src.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "apple\nbanana\ncherry\n", stdout)
}

func TestSortPipe(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "banana\napple\ncherry\n")
	stdout, _, code := cmdRun(t, "cat f.txt | sort", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "apple\nbanana\ncherry\n", stdout)
}

// --- Multiple files ---

func TestSortMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "cherry\napple\n")
	writeFile(t, dir, "b.txt", "banana\ndate\n")
	stdout, _, code := cmdRun(t, "sort a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "apple\nbanana\ncherry\ndate\n", stdout)
}

// --- Help ---

func TestSortHelp(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, "sort --help", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage:")
	assert.Empty(t, stderr)
}

func TestSortHelpShort(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, "sort -h", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage:")
	assert.Empty(t, stderr)
}

// --- Error cases ---

func TestSortMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "sort nonexistent.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "sort:")
}

func TestSortUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "sort --no-such-flag f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "sort:")
}

func TestSortOutputFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "hello\n")
	_, stderr, code := cmdRun(t, "sort -o out.txt f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "sort:")
}

func TestSortOutsideAllowedPaths(t *testing.T) {
	allowed := t.TempDir()
	secret := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(secret, "secret.txt"), []byte("secret"), 0644))
	secretPath := filepath.ToSlash(filepath.Join(secret, "secret.txt"))
	_, stderr, code := runScript(t, "sort "+secretPath, allowed, interp.AllowedPaths([]string{allowed}))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "sort:")
}

// --- Context cancellation ---

func TestSortContextCancellation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "b\na\nc\n")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, code := runScriptCtx(ctx, t, "sort f.txt", dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 0, code)
}
