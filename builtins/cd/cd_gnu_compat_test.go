// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Reference outputs in this file describe the expected rshell behaviour for
// GNU bash 5.2-compatible scenarios. Where rshell matches GNU bash 5.2
// (captured on Linux), the per-test comment documents the exact bash
// invocation. Where rshell intentionally diverges from bash (e.g. the
// shell-name prefix is dropped, error-message capitalisation may differ),
// the per-test comment explains the divergence so the test remains
// unambiguous.

package cd_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/rshell/interp"
)

// TestGNUCompatCdAbsolute — bash: `cd /tmp/dir; printf '%s\n' "$PWD"` prints
// the absolute target with a trailing newline.
func TestGNUCompatCdAbsolute(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("script embeds Windows path with backslashes that the shell parser strips as escapes")
	}
	dir := t.TempDir()
	sub := makeDir(t, dir, "sub")
	stdout, _, code := cmdRun(t, "cd "+sub+"\nprintf '%s\\n' \"$PWD\"", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, sub+"\n", stdout)
}

// TestGNUCompatCdDashPrints — bash: `cd a; cd b; cd -` prints the directory
// it's switching back to (`a`) on stdout, with a trailing newline.
func TestGNUCompatCdDashPrints(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("script embeds Windows path with backslashes that the shell parser strips as escapes")
	}
	dir := t.TempDir()
	a := makeDir(t, dir, "a")
	b := makeDir(t, dir, "b")
	stdout, _, code := cmdRun(t, "cd "+a+"\ncd "+b+"\ncd -", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, a+"\n", stdout)
}

// TestGNUCompatCdMissing — bash: `cd doesnotexist` prints a single error
// line beginning "bash: cd: doesnotexist:" with exit 1. We deliberately
// drop the shell-name prefix and use the canonical "cd:" form so the
// message format is stable across embeddings.
func TestGNUCompatCdMissing(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "cd doesnotexist", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "cd: doesnotexist: no such file or directory\n", stderr)
}

// TestGNUCompatCdNotADir — bash: `cd file.txt` (regular file) prints
// "bash: cd: file.txt: Not a directory" with exit 1. We use the same
// capitalisation as bash (capital N) and drop the shell prefix.
func TestGNUCompatCdNotADir(t *testing.T) {
	dir := t.TempDir()
	makeFile(t, dir, "file.txt", "x")
	_, stderr, code := cmdRun(t, "cd file.txt", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "cd: file.txt: Not a directory\n", stderr)
}

// TestGNUCompatCdNoArgsHome — bash: `cd; printf '%s\n' "$PWD"` prints the
// HOME path. With our restricted shell we have to set HOME explicitly via
// the Env option.
func TestGNUCompatCdNoArgsHome(t *testing.T) {
	dir := t.TempDir()
	home := makeDir(t, dir, "myhome")
	stdout, _, code := runScript(t,
		"cd\nprintf '%s\\n' \"$PWD\"", dir,
		interp.AllowedPaths([]string{dir}),
		interp.Env("HOME="+home))
	assert.Equal(t, 0, code)
	assert.Equal(t, home+"\n", stdout)
}

// TestGNUCompatCdLeavesPwdOnFailure — bash leaves $PWD untouched when cd
// fails. We capture the same behaviour via an ok marker so the test does
// not depend on absolute path text.
func TestGNUCompatCdLeavesPwdOnFailure(t *testing.T) {
	dir := t.TempDir()
	good := makeDir(t, dir, "good")
	script := strings.Join([]string{
		"cd " + good,
		"BEFORE=\"$PWD\"",
		"cd does-not-exist",
		"[ \"$PWD\" = \"$BEFORE\" ] && printf 'ok\\n'",
	}, "\n")
	stdout, _, _ := cmdRun(t, script, dir)
	assert.Equal(t, "ok\n", stdout)
}

// TestGNUCompatCdRelativeJoin — bash joins relative paths against $PWD
// before resolving. Capturing the exact join output keeps drift visible
// if filepath.Clean ever changes its behaviour.
func TestGNUCompatCdRelativeJoin(t *testing.T) {
	dir := t.TempDir()
	makeDir(t, dir, "a/b")
	stdout, _, code := cmdRun(t, "cd a\ncd b\nprintf '%s\\n' \"$PWD\"", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, filepath.Join(dir, "a", "b")+"\n", stdout)
}
