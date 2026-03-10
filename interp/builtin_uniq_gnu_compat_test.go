// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/interp"
)

func setupUniqDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
	}
	return dir
}

func uniqCmdRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return runScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

// TestGNUCompatUniqEmptyInput — empty input produces empty output.
//
// GNU command: printf ” | guniq
// Expected:   ""
func TestGNUCompatUniqEmptyInput(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": ""})
	stdout, _, code := uniqCmdRun(t, "uniq f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

// TestGNUCompatUniqBasicDedupe — adjacent duplicates are merged.
//
// GNU command: printf 'a\na\n' | guniq
// Expected:   "a\n"
func TestGNUCompatUniqBasicDedupe(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "a\na\n"})
	stdout, _, code := uniqCmdRun(t, "uniq f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n", stdout)
}

// TestGNUCompatUniqNoTrailingNewline — input without trailing newline adds one.
//
// GNU command: printf 'a\na' | guniq
// Expected:   "a\n"
func TestGNUCompatUniqNoTrailingNewline(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "a\na"})
	stdout, _, code := uniqCmdRun(t, "uniq f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n", stdout)
}

// TestGNUCompatUniqTwoDifferent — two different lines both emitted.
//
// GNU command: printf 'a\nb' | guniq
// Expected:   "a\nb\n"
func TestGNUCompatUniqTwoDifferent(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "a\nb"})
	stdout, _, code := uniqCmdRun(t, "uniq f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\n", stdout)
}

// TestGNUCompatUniqThreeLinesMixed — duplicates then unique.
//
// GNU command: printf 'a\na\nb' | guniq
// Expected:   "a\nb\n"
func TestGNUCompatUniqThreeLinesMixed(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "a\na\nb"})
	stdout, _, code := uniqCmdRun(t, "uniq f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\n", stdout)
}

// TestGNUCompatUniqAllUnique — three unique lines all emitted.
//
// GNU command: printf 'a\nb\nc\n' | guniq
// Expected:   "a\nb\nc\n"
func TestGNUCompatUniqAllUnique(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "a\nb\nc\n"})
	stdout, _, code := uniqCmdRun(t, "uniq f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\nc\n", stdout)
}

// TestGNUCompatUniqCountTwoUnique — -c with all unique lines.
//
// GNU command: printf 'a\nb\n' | guniq -c
// Expected:   "      1 a\n      1 b\n"
func TestGNUCompatUniqCountTwoUnique(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "a\nb\n"})
	stdout, _, code := uniqCmdRun(t, "uniq -c f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "      1 a\n      1 b\n", stdout)
}

// TestGNUCompatUniqCountDuplicates — -c with duplicates.
//
// GNU command: printf 'a\na\n' | guniq -c
// Expected:   "      2 a\n"
func TestGNUCompatUniqCountDuplicates(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "a\na\n"})
	stdout, _, code := uniqCmdRun(t, "uniq -c f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "      2 a\n", stdout)
}

// TestGNUCompatUniqIgnoreCase — -i ignores case.
//
// GNU command: printf 'A\na\n' | guniq -i
// Expected:   "A\n"
func TestGNUCompatUniqIgnoreCase(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "A\na\n"})
	stdout, _, code := uniqCmdRun(t, "uniq -i f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "A\n", stdout)
}

// TestGNUCompatUniqCaseSensitive — default is case-sensitive.
//
// GNU command: printf 'A\na\n' | guniq
// Expected:   "A\na\n"
func TestGNUCompatUniqCaseSensitive(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "A\na\n"})
	stdout, _, code := uniqCmdRun(t, "uniq f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "A\na\n", stdout)
}

// TestGNUCompatUniqRepeated — -d only prints duplicated lines.
//
// GNU command: printf 'a\na\nb\n' | guniq -d
// Expected:   "a\n"
func TestGNUCompatUniqRepeated(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "a\na\nb\n"})
	stdout, _, code := uniqCmdRun(t, "uniq -d f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n", stdout)
}

// TestGNUCompatUniqUnique — -u only prints unique lines.
//
// GNU command: printf 'a\na\nb\n' | guniq -u
// Expected:   "b\n"
func TestGNUCompatUniqUnique(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "a\na\nb\n"})
	stdout, _, code := uniqCmdRun(t, "uniq -u f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "b\n", stdout)
}

// TestGNUCompatUniqDAndU — -d -u together produce no output.
//
// GNU command: printf 'a\na\n\b' | guniq -d -u
// Expected:   ""
func TestGNUCompatUniqDAndU(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "a\na\n\b"})
	stdout, _, code := uniqCmdRun(t, "uniq -d -u f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

// TestGNUCompatUniqSkipField — -f 1 skips the first field.
//
// GNU command: printf 'a a\nb a\n' | guniq -f 1
// Expected:   "a a\n"
func TestGNUCompatUniqSkipField(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "a a\nb a\n"})
	stdout, _, code := uniqCmdRun(t, "uniq -f 1 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a a\n", stdout)
}

// TestGNUCompatUniqSkipChars — -s 2 skips two characters.
//
// GNU command: printf 'baa\naaa\n' | guniq -s 2
// Expected:   "baa\n"
func TestGNUCompatUniqSkipChars(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "baa\naaa\n"})
	stdout, _, code := uniqCmdRun(t, "uniq -s 2 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "baa\n", stdout)
}

// TestGNUCompatUniqCheckCharsZero — -w 0 treats all lines as equal.
//
// GNU command: printf 'abc\nabcd\n' | guniq -w 0
// Expected:   "abc\n"
func TestGNUCompatUniqCheckCharsZero(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "abc\nabcd\n"})
	stdout, _, code := uniqCmdRun(t, "uniq -w 0 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "abc\n", stdout)
}

// TestGNUCompatUniqGroupSeparate — --group=separate inserts blank lines.
//
// GNU command: printf 'a\na\nb\n' | guniq --group=separate
// Expected:   "a\na\n\nb\n"
func TestGNUCompatUniqGroupSeparate(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "a\na\nb\n"})
	stdout, _, code := uniqCmdRun(t, "uniq --group=separate f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\na\n\nb\n", stdout)
}

// TestGNUCompatUniqGroupPrepend — --group=prepend prepends blank lines.
//
// GNU command: printf 'a\na\nb\n' | guniq --group=prepend
// Expected:   "\na\na\n\nb\n"
func TestGNUCompatUniqGroupPrepend(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "a\na\nb\n"})
	stdout, _, code := uniqCmdRun(t, "uniq --group=prepend f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\na\na\n\nb\n", stdout)
}

// TestGNUCompatUniqGroupAppend — --group=append appends blank lines.
//
// GNU command: printf 'a\na\nb\n' | guniq --group=append
// Expected:   "a\na\n\nb\n\n"
func TestGNUCompatUniqGroupAppend(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "a\na\nb\n"})
	stdout, _, code := uniqCmdRun(t, "uniq --group=append f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\na\n\nb\n\n", stdout)
}

// TestGNUCompatUniqGroupBoth — --group=both prepends and appends blank lines.
//
// GNU command: printf 'a\na\nb\n' | guniq --group=both
// Expected:   "\na\na\n\nb\n\n"
func TestGNUCompatUniqGroupBoth(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "a\na\nb\n"})
	stdout, _, code := uniqCmdRun(t, "uniq --group=both f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\na\na\n\nb\n\n", stdout)
}

// TestGNUCompatUniqAllRepeatedSeparate — --all-repeated=separate.
//
// GNU command: printf 'a\na\nb\nc\nc\n' | guniq --all-repeated=separate
// Expected:   "a\na\n\nc\nc\n"
func TestGNUCompatUniqAllRepeatedSeparate(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "a\na\nb\nc\nc\n"})
	stdout, _, code := uniqCmdRun(t, "uniq --all-repeated=separate f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\na\n\nc\nc\n", stdout)
}

// TestGNUCompatUniqAllRepeatedPrepend — --all-repeated=prepend.
//
// GNU command: printf 'a\na\nb\nc\nc\n' | guniq --all-repeated=prepend
// Expected:   "\na\na\n\nc\nc\n"
func TestGNUCompatUniqAllRepeatedPrepend(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"f.txt": "a\na\nb\nc\nc\n"})
	stdout, _, code := uniqCmdRun(t, "uniq --all-repeated=prepend f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\na\na\n\nc\nc\n", stdout)
}

// TestGNUCompatUniqRejectedExtraOperand — extra operand is rejected.
//
// GNU command: guniq a.txt b.txt (would use b.txt as output)
// Our behavior: reject with exit 1 (no filesystem writes)
func TestGNUCompatUniqRejectedExtraOperand(t *testing.T) {
	dir := setupUniqDir(t, map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	_, stderr, code := uniqCmdRun(t, "uniq a.txt b.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "uniq:")
}
