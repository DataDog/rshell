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

func setupTestDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
	}
	return dir
}

func testCmdRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return runScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

// TestGNUCompatTestStringEmpty — empty string is false.
// GNU test: test ''; echo $?  → exit 1
func TestGNUCompatTestStringEmpty(t *testing.T) {
	_, _, code := runScript(t, `test ""`, "")
	assert.Equal(t, 1, code)
}

// TestGNUCompatTestStringNonEmpty — non-empty string is true.
// GNU test: test 'hello'; echo $?  → exit 0
func TestGNUCompatTestStringNonEmpty(t *testing.T) {
	_, _, code := runScript(t, `test "hello"`, "")
	assert.Equal(t, 0, code)
}

// TestGNUCompatTestNoArgs — no arguments returns false.
// GNU test: test; echo $?  → exit 1
func TestGNUCompatTestNoArgs(t *testing.T) {
	_, _, code := runScript(t, `test`, "")
	assert.Equal(t, 1, code)
}

// TestGNUCompatTestStrEq — string equality with =.
// GNU test: test t = t; echo $?  → exit 0
func TestGNUCompatTestStrEq(t *testing.T) {
	_, _, code := runScript(t, `test "t" = "t"`, "")
	assert.Equal(t, 0, code)
}

// TestGNUCompatTestStrEqFail — string equality fails.
// GNU test: test t = f; echo $?  → exit 1
func TestGNUCompatTestStrEqFail(t *testing.T) {
	_, _, code := runScript(t, `test "t" = "f"`, "")
	assert.Equal(t, 1, code)
}

// TestGNUCompatTestStrNe — string inequality.
// GNU test: test t != f; echo $?  → exit 0
func TestGNUCompatTestStrNe(t *testing.T) {
	_, _, code := runScript(t, `test "t" != "f"`, "")
	assert.Equal(t, 0, code)
}

// TestGNUCompatTestStrDoubleEq — == as alias for =.
// GNU test: test t == t; echo $?  → exit 0
func TestGNUCompatTestStrDoubleEq(t *testing.T) {
	_, _, code := runScript(t, `test "t" == "t"`, "")
	assert.Equal(t, 0, code)
}

// TestGNUCompatTestIntEq — integer equality.
// GNU test: test 9 -eq 9; echo $?  → exit 0
func TestGNUCompatTestIntEq(t *testing.T) {
	_, _, code := runScript(t, `test 9 -eq 9`, "")
	assert.Equal(t, 0, code)
}

// TestGNUCompatTestIntLeadingZero — leading zeros don't trigger octal.
// GNU test: test 0 -eq 00; echo $?  → exit 0
func TestGNUCompatTestIntLeadingZero(t *testing.T) {
	_, _, code := runScript(t, `test 0 -eq 00`, "")
	assert.Equal(t, 0, code)
}

// TestGNUCompatTestIntWhitespace — whitespace around integer operands.
// GNU test: test 0 -eq ' 0 '; echo $?  → exit 0
func TestGNUCompatTestIntWhitespace(t *testing.T) {
	_, _, code := runScript(t, `test 0 -eq " 0 "`, "")
	assert.Equal(t, 0, code)
}

// TestGNUCompatTestIntNegative — negative integers.
// GNU test: test -1 -gt -2; echo $?  → exit 0
func TestGNUCompatTestIntNegative(t *testing.T) {
	_, _, code := runScript(t, `test -1 -gt -2`, "")
	assert.Equal(t, 0, code)
}

// TestGNUCompatTestHexInvalid — hex is invalid.
// GNU test: test 0x0 -eq 00; echo $?  → exit 2, stderr: "invalid integer '0x0'"
func TestGNUCompatTestHexInvalid(t *testing.T) {
	_, stderr, code := runScript(t, `test 0x0 -eq 00`, "")
	assert.Equal(t, 2, code)
	assert.Equal(t, "test: invalid integer '0x0'\n", stderr)
}

// TestGNUCompatTestNotEmpty — ! negates empty string.
// GNU test: test ! ''; echo $?  → exit 0
func TestGNUCompatTestNotEmpty(t *testing.T) {
	_, _, code := runScript(t, `test ! ""`, "")
	assert.Equal(t, 0, code)
}

// TestGNUCompatTestAndBothTrue — -a with both true.
// GNU test: test t -a t; echo $?  → exit 0
func TestGNUCompatTestAndBothTrue(t *testing.T) {
	_, _, code := runScript(t, `test "t" -a "t"`, "")
	assert.Equal(t, 0, code)
}

// TestGNUCompatTestOrBothFalse — -o with both false.
// GNU test: test '' -o ''; echo $?  → exit 1
func TestGNUCompatTestOrBothFalse(t *testing.T) {
	_, _, code := runScript(t, `test "" -o ""`, "")
	assert.Equal(t, 1, code)
}

// TestGNUCompatTestParenEmpty — parenthesized empty string is false.
// GNU test: test '(' '' ')'; echo $?  → exit 1
func TestGNUCompatTestParenEmpty(t *testing.T) {
	_, _, code := runScript(t, `test "(" "" ")"`, "")
	assert.Equal(t, 1, code)
}

// TestGNUCompatTestParenNonEmpty — parenthesized non-empty string is true.
// GNU test: test '(' '(' ')'; echo $?  → exit 0
func TestGNUCompatTestParenNonEmpty(t *testing.T) {
	_, _, code := runScript(t, `test "(" "(" ")"`, "")
	assert.Equal(t, 0, code)
}

// TestGNUCompatTestBracketMissing — [ without ] gives error.
// GNU [: [ 1 -eq; echo $?  → exit 2, stderr: "[: missing ']'"
func TestGNUCompatTestBracketMissing(t *testing.T) {
	_, stderr, code := runScript(t, `[ 1 -eq`, "")
	assert.Equal(t, 2, code)
	assert.Equal(t, "[: missing ']'\n", stderr)
}

// TestGNUCompatTestLessCollate — < for string comparison.
// GNU test: test 'a' '<' 'b'; echo $?  → exit 0
func TestGNUCompatTestLessCollate(t *testing.T) {
	_, _, code := runScript(t, `test "a" \< "b"`, "")
	assert.Equal(t, 0, code)
	_, _, code = runScript(t, `test "a" \< "a"`, "")
	assert.Equal(t, 1, code)
}

// TestGNUCompatTestGreaterCollate — > for string comparison.
// GNU test: test 'b' '>' 'a'; echo $?  → exit 0
func TestGNUCompatTestGreaterCollate(t *testing.T) {
	_, _, code := runScript(t, `test "b" \> "a"`, "")
	assert.Equal(t, 0, code)
	_, _, code = runScript(t, `test "a" \> "a"`, "")
	assert.Equal(t, 1, code)
}

// TestGNUCompatTestUnaryDiag — unary operator expected diagnostic.
// GNU test: test -o arg; echo $?  → exit 2, stderr: "test: '-o': unary operator expected"
func TestGNUCompatTestUnaryDiag(t *testing.T) {
	_, stderr, code := runScript(t, `test -o arg`, "")
	assert.Equal(t, 2, code)
	assert.Equal(t, "test: '-o': unary operator expected\n", stderr)
}

// TestGNUCompatTestFileExists — -e on an existing file.
// GNU test: test -e file.txt; echo $?  → exit 0
func TestGNUCompatTestFileExists(t *testing.T) {
	dir := setupTestDir(t, map[string]string{"file.txt": "data"})
	_, _, code := testCmdRun(t, `test -e file.txt`, dir)
	assert.Equal(t, 0, code)
}

// TestGNUCompatTestFileNotExists — -e on a nonexistent file.
// GNU test: test -e nonexistent; echo $?  → exit 1
func TestGNUCompatTestFileNotExists(t *testing.T) {
	dir := t.TempDir()
	_, _, code := testCmdRun(t, `test -e nonexistent`, dir)
	assert.Equal(t, 1, code)
}

// TestGNUCompatTestFileRegular — -f on a regular file.
// GNU test: test -f file.txt; echo $?  → exit 0
func TestGNUCompatTestFileRegular(t *testing.T) {
	dir := setupTestDir(t, map[string]string{"file.txt": "data"})
	_, _, code := testCmdRun(t, `test -f file.txt`, dir)
	assert.Equal(t, 0, code)
}

// TestGNUCompatTestFileDir — -d on a directory.
// GNU test: test -d .; echo $?  → exit 0
func TestGNUCompatTestFileDir(t *testing.T) {
	dir := t.TempDir()
	_, _, code := testCmdRun(t, `test -d .`, dir)
	assert.Equal(t, 0, code)
}

// TestGNUCompatTestFileNtMissing — -nt with one missing file.
// GNU test: test file -nt missing; echo $?  → exit 0
func TestGNUCompatTestFileNtMissing(t *testing.T) {
	dir := setupTestDir(t, map[string]string{"file.txt": "data"})
	_, _, code := testCmdRun(t, `test file.txt -nt missing`, dir)
	assert.Equal(t, 0, code)
	_, _, code = testCmdRun(t, `test missing -nt file.txt`, dir)
	assert.Equal(t, 1, code)
}

// TestGNUCompatTestFileOtMissing — -ot with one missing file.
// GNU test: test missing -ot file; echo $?  → exit 0
func TestGNUCompatTestFileOtMissing(t *testing.T) {
	dir := setupTestDir(t, map[string]string{"file.txt": "data"})
	_, _, code := testCmdRun(t, `test missing -ot file.txt`, dir)
	assert.Equal(t, 0, code)
	_, _, code = testCmdRun(t, `test file.txt -ot missing`, dir)
	assert.Equal(t, 1, code)
}

// TestGNUCompatTestFileEfSelf — -ef on same file.
// GNU test: test file -ef file; echo $?  → exit 0
func TestGNUCompatTestFileEfSelf(t *testing.T) {
	dir := setupTestDir(t, map[string]string{"file.txt": "data"})
	_, _, code := testCmdRun(t, `test file.txt -ef file.txt`, dir)
	assert.Equal(t, 0, code)
}

// TestGNUCompatTestFileEfDifferent — -ef on different files.
// GNU test: test file1 -ef file2; echo $?  → exit 1
func TestGNUCompatTestFileEfDifferent(t *testing.T) {
	dir := setupTestDir(t, map[string]string{"a.txt": "a", "b.txt": "b"})
	_, _, code := testCmdRun(t, `test a.txt -ef b.txt`, dir)
	assert.Equal(t, 1, code)
}

// TestGNUCompatTestFileEfMissing — -ef with missing files.
// GNU test: test missing1 -ef missing2; echo $?  → exit 1
func TestGNUCompatTestFileEfMissing(t *testing.T) {
	dir := t.TempDir()
	_, _, code := testCmdRun(t, `test missing1 -ef missing2`, dir)
	assert.Equal(t, 1, code)
}
