// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package test_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/interp"
	_ "github.com/DataDog/rshell/interp/builtins/test"
	"mvdan.cc/sh/v3/syntax"
)

func runScript(t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return runScriptCtx(context.Background(), t, script, dir, opts...)
}

func runScriptCtx(ctx context.Context, t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(script), "")
	require.NoError(t, err)
	var outBuf, errBuf bytes.Buffer
	allOpts := append([]interp.RunnerOption{interp.StdIO(nil, &outBuf, &errBuf)}, opts...)
	runner, err := interp.New(allOpts...)
	require.NoError(t, err)
	defer runner.Close()
	if dir != "" {
		runner.Dir = dir
	}
	err = runner.Run(ctx, prog)
	exitCode := 0
	if err != nil {
		var es interp.ExitStatus
		if errors.As(err, &es) {
			exitCode = int(es)
		} else if ctx.Err() == nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

func cmdRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return runScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return name
}

// --- Zero/one-argument forms ---

func TestTestNoArgs(t *testing.T) {
	_, _, code := runScript(t, "test", "")
	assert.Equal(t, 1, code)
}

func TestTestEmptyString(t *testing.T) {
	_, _, code := runScript(t, `test ""`, "")
	assert.Equal(t, 1, code)
}

func TestTestNonEmptyString(t *testing.T) {
	_, _, code := runScript(t, `test "hello"`, "")
	assert.Equal(t, 0, code)
}

func TestTestDash(t *testing.T) {
	_, _, code := runScript(t, `test "-"`, "")
	assert.Equal(t, 0, code)
}

func TestTestDoubleDash(t *testing.T) {
	_, _, code := runScript(t, `test "--"`, "")
	assert.Equal(t, 0, code)
}

// --- String operators ---

func TestTestStringN(t *testing.T) {
	_, _, code := runScript(t, `test -n "abc"`, "")
	assert.Equal(t, 0, code)
	_, _, code = runScript(t, `test -n ""`, "")
	assert.Equal(t, 1, code)
}

func TestTestStringZ(t *testing.T) {
	_, _, code := runScript(t, `test -z ""`, "")
	assert.Equal(t, 0, code)
	_, _, code = runScript(t, `test -z "abc"`, "")
	assert.Equal(t, 1, code)
}

func TestTestStringEqual(t *testing.T) {
	_, _, code := runScript(t, `test "foo" = "foo"`, "")
	assert.Equal(t, 0, code)
	_, _, code = runScript(t, `test "foo" = "bar"`, "")
	assert.Equal(t, 1, code)
}

func TestTestStringDoubleEqual(t *testing.T) {
	_, _, code := runScript(t, `test "foo" == "foo"`, "")
	assert.Equal(t, 0, code)
}

func TestTestStringNotEqual(t *testing.T) {
	_, _, code := runScript(t, `test "foo" != "bar"`, "")
	assert.Equal(t, 0, code)
	_, _, code = runScript(t, `test "foo" != "foo"`, "")
	assert.Equal(t, 1, code)
}

func TestTestStringLessThan(t *testing.T) {
	stdout, _, code := runScript(t, `test "a" \< "b" && echo yes`, "")
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestStringGreaterThan(t *testing.T) {
	stdout, _, code := runScript(t, `test "b" \> "a" && echo yes`, "")
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

// --- Integer operators ---

func TestTestIntEq(t *testing.T) {
	_, _, code := runScript(t, `test 9 -eq 9`, "")
	assert.Equal(t, 0, code)
	_, _, code = runScript(t, `test 8 -eq 9`, "")
	assert.Equal(t, 1, code)
}

func TestTestIntNe(t *testing.T) {
	_, _, code := runScript(t, `test 0 -ne 1`, "")
	assert.Equal(t, 0, code)
}

func TestTestIntGt(t *testing.T) {
	_, _, code := runScript(t, `test 5 -gt 4`, "")
	assert.Equal(t, 0, code)
	_, _, code = runScript(t, `test 5 -gt 5`, "")
	assert.Equal(t, 1, code)
}

func TestTestIntGe(t *testing.T) {
	_, _, code := runScript(t, `test 5 -ge 5`, "")
	assert.Equal(t, 0, code)
}

func TestTestIntLt(t *testing.T) {
	_, _, code := runScript(t, `test 4 -lt 5`, "")
	assert.Equal(t, 0, code)
}

func TestTestIntLe(t *testing.T) {
	_, _, code := runScript(t, `test 5 -le 5`, "")
	assert.Equal(t, 0, code)
}

func TestTestIntNegative(t *testing.T) {
	_, _, code := runScript(t, `test -1 -gt -2`, "")
	assert.Equal(t, 0, code)
}

func TestTestIntWhitespace(t *testing.T) {
	_, _, code := runScript(t, `test 42 -eq " 42 "`, "")
	assert.Equal(t, 0, code)
}

func TestTestIntInvalid(t *testing.T) {
	_, stderr, code := runScript(t, `test 123.45 -ge 6`, "")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "invalid integer")
}

func TestTestIntLeadingZeros(t *testing.T) {
	_, _, code := runScript(t, `test 0 -eq 00`, "")
	assert.Equal(t, 0, code)
}

// --- Logical operators ---

func TestTestNot(t *testing.T) {
	_, _, code := runScript(t, `test ! ""`, "")
	assert.Equal(t, 0, code)
	_, _, code = runScript(t, `test ! "hello"`, "")
	assert.Equal(t, 1, code)
}

func TestTestAnd(t *testing.T) {
	_, _, code := runScript(t, `test "a" -a "b"`, "")
	assert.Equal(t, 0, code)
	_, _, code = runScript(t, `test "" -a "b"`, "")
	assert.Equal(t, 1, code)
}

func TestTestOr(t *testing.T) {
	_, _, code := runScript(t, `test "" -o "b"`, "")
	assert.Equal(t, 0, code)
	_, _, code = runScript(t, `test "" -o ""`, "")
	assert.Equal(t, 1, code)
}

func TestTestParentheses(t *testing.T) {
	stdout, _, code := runScript(t, `test "(" "hello" ")" && echo yes`, "")
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestNotAndPrecedence(t *testing.T) {
	_, _, code := runScript(t, `test ! "" -a ""`, "")
	assert.Equal(t, 0, code)
}

func TestTestNotOrPrecedence(t *testing.T) {
	_, _, code := runScript(t, `test ! "a" -o "b"`, "")
	assert.Equal(t, 1, code)
}

// --- File operators ---

func TestTestFileExists(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "exists.txt", "content")
	_, _, code := cmdRun(t, `test -e exists.txt`, dir)
	assert.Equal(t, 0, code)
	_, _, code = cmdRun(t, `test -e nonexistent`, dir)
	assert.Equal(t, 1, code)
}

func TestTestFileRegular(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "content")
	_, _, code := cmdRun(t, `test -f file.txt`, dir)
	assert.Equal(t, 0, code)
	_, _, code = cmdRun(t, `test -f .`, dir)
	assert.Equal(t, 1, code)
}

func TestTestFileDirectory(t *testing.T) {
	dir := t.TempDir()
	_, _, code := cmdRun(t, `test -d .`, dir)
	assert.Equal(t, 0, code)
	writeFile(t, dir, "file.txt", "content")
	_, _, code = cmdRun(t, `test -d file.txt`, dir)
	assert.Equal(t, 1, code)
}

func TestTestFileSize(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "nonempty.txt", "data")
	writeFile(t, dir, "empty.txt", "")
	_, _, code := cmdRun(t, `test -s nonempty.txt`, dir)
	assert.Equal(t, 0, code)
	_, _, code = cmdRun(t, `test -s empty.txt`, dir)
	assert.Equal(t, 1, code)
}

func TestTestFileReadable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "readable.txt", "data")
	_, _, code := cmdRun(t, `test -r readable.txt`, dir)
	assert.Equal(t, 0, code)
}

func TestTestFileNonexistentNotReadable(t *testing.T) {
	dir := t.TempDir()
	_, _, code := cmdRun(t, `test -r nonexistent`, dir)
	assert.Equal(t, 1, code)
}

func TestTestFileNewerOlder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "older.txt", "old")
	writeFile(t, dir, "newer.txt", "new")
	// newer.txt may or may not be strictly newer (same-second resolution).
	// At minimum, file -nt missing should be true.
	_, _, code := cmdRun(t, `test newer.txt -nt nonexistent`, dir)
	assert.Equal(t, 0, code)
	_, _, code = cmdRun(t, `test nonexistent -nt newer.txt`, dir)
	assert.Equal(t, 1, code)
}

func TestTestFileSameFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "content")
	_, _, code := cmdRun(t, `test file.txt -ef file.txt`, dir)
	assert.Equal(t, 0, code)
}

func TestTestFileNonexistentEf(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "content")
	_, _, code := cmdRun(t, `test file.txt -ef nonexistent`, dir)
	assert.Equal(t, 1, code)
}

// --- Bracket syntax ---

func TestBracketBasic(t *testing.T) {
	_, _, code := runScript(t, `[ 1 -eq 1 ]`, "")
	assert.Equal(t, 0, code)
}

func TestBracketFailure(t *testing.T) {
	_, _, code := runScript(t, `[ 1 -eq 2 ]`, "")
	assert.Equal(t, 1, code)
}

func TestBracketMissingClose(t *testing.T) {
	_, stderr, code := runScript(t, `[ 1 -eq 2`, "")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "missing ']'")
}

func TestBracketEmpty(t *testing.T) {
	_, stderr, code := runScript(t, `[`, "")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "missing ']'")
}

// --- Help ---

func TestTestHelp(t *testing.T) {
	stdout, _, code := runScript(t, `test --help`, "")
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage: test")
}

func TestBracketHelp(t *testing.T) {
	stdout, _, code := runScript(t, `[ --help ]`, "")
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage: [")
}

// --- Error cases ---

func TestTestUnaryExpected(t *testing.T) {
	_, stderr, code := runScript(t, `test -o arg`, "")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "unary operator expected")
}

func TestTestExtraArg(t *testing.T) {
	_, stderr, code := runScript(t, `test "a" "b" "c" "d" "e"`, "")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "extra argument")
}

func TestTestHexNotOctal(t *testing.T) {
	_, stderr, code := runScript(t, `test 0x0 -eq 00`, "")
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "invalid integer")
}

// --- Combined expressions ---

func TestTestFileAndString(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "data")
	stdout, _, code := cmdRun(t, `test -f file.txt -a -n "hello" && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestIfConstruct(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "data")
	stdout, _, code := cmdRun(t, `[ -f file.txt ] && echo found`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "found\n", stdout)
}

func TestTestBinaryInThreeArgForm(t *testing.T) {
	_, _, code := runScript(t, `test "-f" "=" "a"`, "")
	assert.Equal(t, 1, code)
}

func TestTestSoloNotIsTrue(t *testing.T) {
	_, _, code := runScript(t, `test "!"`, "")
	assert.Equal(t, 0, code)
}

func TestTestDoubleNotIsFalse(t *testing.T) {
	_, _, code := runScript(t, `test "!" "!"`, "")
	assert.Equal(t, 1, code)
}

func TestTestOutsideSandbox(t *testing.T) {
	dir := t.TempDir()
	_, _, code := cmdRun(t, `test -e /etc/passwd`, dir)
	assert.Equal(t, 1, code)
}
