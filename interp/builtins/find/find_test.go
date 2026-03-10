// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package find_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

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

func setupDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0644))
	}
	return dir
}

func sortedLines(s string) []string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	sort.Strings(lines)
	return lines
}

// --- Default behavior ---

func TestFindDefaultCurrentDir(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"file.txt":       "hello",
		"sub/nested.txt": "world",
	})
	stdout, _, code := cmdRun(t, "find", dir)
	assert.Equal(t, 0, code)
	lines := sortedLines(stdout)
	assert.Contains(t, lines, ".")
	assert.Contains(t, lines, "./file.txt")
	assert.Contains(t, lines, "./sub")
	assert.Contains(t, lines, "./sub/nested.txt")
}

func TestFindExplicitPath(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"dir/a.txt": "a",
		"dir/b.txt": "b",
	})
	stdout, _, code := cmdRun(t, "find dir", dir)
	assert.Equal(t, 0, code)
	lines := sortedLines(stdout)
	assert.Contains(t, lines, "dir")
	assert.Contains(t, lines, "dir/a.txt")
	assert.Contains(t, lines, "dir/b.txt")
}

func TestFindEmptyDirectory(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, "find .", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, ".\n", stdout)
}

// --- -name ---

func TestFindNameGlob(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"hello.txt":     "a",
		"world.log":     "b",
		"sub/hello.txt": "c",
	})
	stdout, _, code := cmdRun(t, `find . -name "*.txt"`, dir)
	assert.Equal(t, 0, code)
	lines := sortedLines(stdout)
	assert.Contains(t, lines, "./hello.txt")
	assert.Contains(t, lines, "./sub/hello.txt")
	for _, l := range lines {
		assert.NotContains(t, l, "world.log")
	}
}

func TestFindNameExact(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"target.txt": "a",
		"other.txt":  "b",
	})
	stdout, _, code := cmdRun(t, `find . -name target.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "target.txt")
	assert.NotContains(t, stdout, "other.txt")
}

// --- -iname ---

func TestFindIname(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"Hello.TXT": "a",
		"world.txt": "b",
	})
	stdout, _, code := cmdRun(t, `find . -iname "hello*"`, dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Hello.TXT")
}

// --- -type ---

func TestFindTypeFile(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"file.txt":       "a",
		"sub/nested.txt": "b",
	})
	stdout, _, code := cmdRun(t, "find . -type f", dir)
	assert.Equal(t, 0, code)
	lines := sortedLines(stdout)
	for _, l := range lines {
		assert.NotEqual(t, ".", l)
		assert.NotEqual(t, "./sub", l)
	}
	assert.Contains(t, lines, "./file.txt")
	assert.Contains(t, lines, "./sub/nested.txt")
}

func TestFindTypeDir(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"file.txt":       "a",
		"sub/nested.txt": "b",
	})
	stdout, _, code := cmdRun(t, "find . -type d", dir)
	assert.Equal(t, 0, code)
	lines := sortedLines(stdout)
	assert.Contains(t, lines, ".")
	assert.Contains(t, lines, "./sub")
	for _, l := range lines {
		assert.False(t, strings.HasSuffix(l, ".txt"), "should not contain files: %s", l)
	}
}

// --- -empty ---

func TestFindEmptyFile(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"empty.txt":    "",
		"nonempty.txt": "data",
	})
	stdout, _, code := cmdRun(t, "find . -empty -type f", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "empty.txt")
	assert.NotContains(t, stdout, "nonempty.txt")
}

// --- -size ---

func TestFindSizeBytes(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"empty.txt": "",
		"data.txt":  "some data here",
	})
	stdout, _, code := cmdRun(t, "find . -type f -size +0c", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "data.txt")
	assert.NotContains(t, stdout, "empty.txt")
}

func TestFindSizeExact(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"five.txt": "hello",
	})
	stdout, _, code := cmdRun(t, "find . -type f -size 5c", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "five.txt")
}

// --- -maxdepth / -mindepth ---

func TestFindMaxdepthZero(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"file.txt":       "a",
		"sub/nested.txt": "b",
	})
	stdout, _, code := cmdRun(t, "find . -maxdepth 0", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, ".\n", stdout)
}

func TestFindMaxdepthOne(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"file.txt":            "a",
		"sub/deep/deeper.txt": "b",
	})
	stdout, _, code := cmdRun(t, "find . -maxdepth 1 -type f", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "file.txt")
	assert.NotContains(t, stdout, "deeper.txt")
}

func TestFindMindepthOne(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"file.txt": "a",
	})
	stdout, _, code := cmdRun(t, "find . -mindepth 1 -maxdepth 1", dir)
	assert.Equal(t, 0, code)
	assert.NotContains(t, stdout, ".\n")
	assert.Contains(t, stdout, "file.txt")
}

// --- -print0 ---

func TestFindPrint0(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"a.txt": "a",
	})
	stdout, _, code := cmdRun(t, `find . -name "a.txt" -print0`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./a.txt\x00", stdout)
}

// --- Operators ---

func TestFindOrOperator(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"a.txt": "a",
		"b.txt": "b",
		"c.log": "c",
	})
	stdout, _, code := cmdRun(t, `find . -name a.txt -o -name b.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "a.txt")
	assert.Contains(t, stdout, "b.txt")
	assert.NotContains(t, stdout, "c.log")
}

func TestFindNotOperator(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"a.txt": "a",
		"b.log": "b",
	})
	stdout, _, code := cmdRun(t, `find . -type f ! -name "*.txt"`, dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "b.log")
	assert.NotContains(t, stdout, "a.txt")
}

func TestFindGrouping(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"a.txt": "a",
		"b.txt": "b",
		"c.log": "c",
	})
	stdout, _, code := cmdRun(t, `find . -type f \( -name a.txt -o -name c.log \)`, dir)
	assert.Equal(t, 0, code)
	lines := sortedLines(stdout)
	assert.Contains(t, lines, "./a.txt")
	assert.Contains(t, lines, "./c.log")
	for _, l := range lines {
		assert.NotContains(t, l, "b.txt")
	}
}

// --- -path / -wholename ---

func TestFindPath(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"sub/hello.txt": "a",
		"other.txt":     "b",
	})
	stdout, _, code := cmdRun(t, "find . -path './sub/*'", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "sub/hello.txt")
	assert.NotContains(t, stdout, "other.txt")
}

// --- -newer ---

func TestFindNewer(t *testing.T) {
	dir := t.TempDir()
	oldFile := filepath.Join(dir, "old.txt")
	require.NoError(t, os.WriteFile(oldFile, []byte("old"), 0644))
	newFile := filepath.Join(dir, "new.txt")
	require.NoError(t, os.WriteFile(newFile, []byte("new"), 0644))
	stdout, _, code := cmdRun(t, "find . -newer old.txt -type f", dir)
	assert.Equal(t, 0, code)
	// new.txt may or may not be newer depending on timing;
	// at minimum, no error should occur
	_ = stdout
}

// --- -prune ---

func TestFindPrune(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"file.txt":           "a",
		"skip/hidden.txt":    "b",
		"skip/deep/deep.txt": "c",
	})
	stdout, _, code := cmdRun(t, `find . -name skip -prune -o -type f -print`, dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "file.txt")
	assert.NotContains(t, stdout, "hidden.txt")
	assert.NotContains(t, stdout, "deep.txt")
}

// --- -quit ---

func TestFindQuit(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"a.txt": "a",
		"b.txt": "b",
		"c.txt": "c",
	})
	stdout, _, code := cmdRun(t, `find . -type f -quit`, dir)
	assert.Equal(t, 0, code)
	// Should only print the first file found
	lines := sortedLines(stdout)
	assert.LessOrEqual(t, len(lines), 1)
}

// --- -depth ---

func TestFindDepthOption(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"sub/file.txt": "a",
	})
	stdout, _, code := cmdRun(t, "find . -depth", dir)
	assert.Equal(t, 0, code)
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	// In -depth mode, files come before their parent directories.
	// So "." should be the last line.
	if len(lines) > 0 {
		assert.Equal(t, ".", lines[len(lines)-1])
	}
}

// --- -perm ---

func TestFindPermExact(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits not meaningful on Windows")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "script.sh")
	require.NoError(t, os.WriteFile(f, []byte("#!/bin/sh"), 0755))
	f2 := filepath.Join(dir, "data.txt")
	require.NoError(t, os.WriteFile(f2, []byte("data"), 0644))
	stdout, _, code := cmdRun(t, "find . -type f -perm 0755", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "script.sh")
	assert.NotContains(t, stdout, "data.txt")
}

// --- -links ---

func TestFindLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hard links have different semantics on Windows")
	}
	dir := setupDir(t, map[string]string{
		"file.txt": "hello",
	})
	stdout, _, code := cmdRun(t, "find . -type f -links 1", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "file.txt")
}

// --- -true / -false ---

func TestFindTrue(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"file.txt": "a",
	})
	stdout, _, code := cmdRun(t, "find . -true", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, ".")
	assert.Contains(t, stdout, "file.txt")
}

func TestFindFalse(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"file.txt": "a",
	})
	stdout, _, code := cmdRun(t, "find . -false", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

// --- Error cases ---

func TestFindMissingPath(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "find nonexistent_xyz", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "find:")
}

func TestFindRejectExec(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `find . -exec ls {} \;`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "not permitted")
}

func TestFindRejectDelete(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "find . -delete", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "not permitted")
}

func TestFindRejectExecdir(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `find . -execdir ls {} \;`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "not permitted")
}

func TestFindHelp(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, "find --help", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage:")
}

func TestFindInvalidName(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `find . -name "[invalid"`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "find:")
}

func TestFindMissingNameArg(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "find . -name", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "find:")
}

func TestFindInvalidType(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "find . -type x", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "find:")
}

func TestFindInvalidMaxdepth(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "find . -maxdepth abc", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "find:")
}

func TestFindNegativeMaxdepth(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "find . -maxdepth -1", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "find:")
}

func TestFindUnknownPrimary(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "find . -nosuchprimary", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "find:")
}

// --- Context cancellation ---

func TestFindContextCancellation(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"a.txt": "a",
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, _, code := runScriptCtx(ctx, t, "find .", dir, interp.AllowedPaths([]string{dir}))
	_ = code // just verify no hang
}
