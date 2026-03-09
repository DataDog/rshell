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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
)

// runScript runs a shell script and returns stdout, stderr, and the exit code.
func runScript(t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
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

	err = runner.Run(context.Background(), prog)
	exitCode := 0
	if err != nil {
		var es interp.ExitStatus
		if errors.As(err, &es) {
			exitCode = int(es)
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// cmdRun runs a test command with AllowedPaths set to dir.
func cmdRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return runScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

// writeFile creates a file in dir with the given content.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
}

// --- String tests ---

func TestTestStringNonEmpty(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `test -n "hello" && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestStringEmpty(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `test -n "" && echo yes || echo no`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "no\n", stdout)
}

func TestTestStringZeroEmpty(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `test -z "" && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestStringZeroNonEmpty(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `test -z "hello" && echo yes || echo no`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "no\n", stdout)
}

func TestTestStringEqual(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `test "abc" = "abc" && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestStringDoubleEqual(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `test "abc" == "abc" && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestStringNotEqual(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `test "abc" != "xyz" && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

// --- Integer tests ---

func TestTestIntEq(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `test 5 -eq 5 && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestIntNe(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `test 5 -ne 3 && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestIntLt(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `test 3 -lt 5 && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestIntGt(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `test 5 -gt 3 && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestIntLe(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `test 3 -le 3 && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestIntGe(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `test 5 -ge 5 && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestIntEqFalse(t *testing.T) {
	dir := t.TempDir()
	_, _, code := cmdRun(t, `test 5 -eq 3`, dir)
	assert.Equal(t, 1, code)
}

func TestTestIntNonNumeric(t *testing.T) {
	dir := t.TempDir()
	// Non-numeric comparisons fail (return false/1 not error).
	_, _, code := cmdRun(t, `test abc -eq 3`, dir)
	assert.Equal(t, 1, code)
}

// --- File tests ---

func TestTestFileExists(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello")
	stdout, _, code := cmdRun(t, `test -e file.txt && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestFileNotExists(t *testing.T) {
	dir := t.TempDir()
	_, _, code := cmdRun(t, `test -e nonexistent`, dir)
	assert.Equal(t, 1, code)
}

func TestTestFileRegular(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello")
	stdout, _, code := cmdRun(t, `test -f file.txt && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestFileDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "subdir"), 0755))
	stdout, _, code := cmdRun(t, `test -d subdir && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestFileNonEmptySize(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello")
	stdout, _, code := cmdRun(t, `test -s file.txt && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestFileEmptySize(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "empty.txt", "")
	_, _, code := cmdRun(t, `test -s empty.txt`, dir)
	assert.Equal(t, 1, code)
}

func TestTestFileReadable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello")
	stdout, _, code := cmdRun(t, `test -r file.txt && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestFileExecutable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "script.sh", "#!/bin/sh")
	require.NoError(t, os.Chmod(filepath.Join(dir, "script.sh"), 0755))
	stdout, _, code := cmdRun(t, `test -x script.sh && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestFileSymlink(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.txt", "hello")
	require.NoError(t, os.Symlink("target.txt", filepath.Join(dir, "link.txt")))
	stdout, _, code := cmdRun(t, `test -L link.txt && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestFileSymlinkH(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.txt", "hello")
	require.NoError(t, os.Symlink("target.txt", filepath.Join(dir, "link.txt")))
	stdout, _, code := cmdRun(t, `test -h link.txt && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestFileNotSymlink(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "regular.txt", "hello")
	_, _, code := cmdRun(t, `test -L regular.txt`, dir)
	assert.Equal(t, 1, code)
}

// --- File comparisons ---

func TestTestFileNewerThan(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "old.txt", "old")
	// Ensure different mod times.
	past := time.Now().Add(-2 * time.Second)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "old.txt"), past, past))
	writeFile(t, dir, "new.txt", "new")
	stdout, _, code := cmdRun(t, `test new.txt -nt old.txt && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestFileOlderThan(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "old.txt", "old")
	past := time.Now().Add(-2 * time.Second)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "old.txt"), past, past))
	writeFile(t, dir, "new.txt", "new")
	stdout, _, code := cmdRun(t, `test old.txt -ot new.txt && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestFileSameFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello")
	stdout, _, code := cmdRun(t, `test file.txt -ef file.txt && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestFileDifferentFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "hello")
	writeFile(t, dir, "b.txt", "hello")
	_, _, code := cmdRun(t, `test a.txt -ef b.txt`, dir)
	assert.Equal(t, 1, code)
}

// --- Logic ---

func TestTestNot(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `test ! -z "hello" && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestAnd(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `test -n "a" -a -n "b" && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestOr(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `test -z "a" -o -n "b" && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

// --- Bracket syntax ---

func TestBracketBasic(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `[ -n "hello" ] && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestBracketMissingClosing(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `[ -n "hello"`, dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "[: missing `]'")
}

func TestBracketEmpty(t *testing.T) {
	dir := t.TempDir()
	_, _, code := cmdRun(t, `[ ]`, dir)
	assert.Equal(t, 1, code)
}

// --- Edge cases ---

func TestTestNoArgs(t *testing.T) {
	dir := t.TempDir()
	_, _, code := cmdRun(t, `test`, dir)
	assert.Equal(t, 1, code)
}

func TestTestSingleWord(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `test hello && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestEmptyString(t *testing.T) {
	dir := t.TempDir()
	_, _, code := cmdRun(t, `test ""`, dir)
	assert.Equal(t, 1, code)
}

func TestTestMissingOperand(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `test "a" =`, dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "test:")
}

func TestTestExtraArgument(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `test "a" = "b" extra`, dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "test:")
}

func TestTestWithVariable(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `X=hello; test -n "$X" && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestWithAndOr(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello")
	stdout, _, code := cmdRun(t, `test -f file.txt && echo exists || echo missing`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "exists\n", stdout)
}

func TestTestOutsideAllowedPaths(t *testing.T) {
	allowed := t.TempDir()
	secret := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(secret, "secret.txt"), []byte("secret"), 0644))
	secretPath := strings.ReplaceAll(filepath.Join(secret, "secret.txt"), `\`, `/`)
	// test -e should return false for files outside allowed paths.
	_, _, code := runScript(t, "test -e "+secretPath, allowed, interp.AllowedPaths([]string{allowed}))
	assert.Equal(t, 1, code)
}
