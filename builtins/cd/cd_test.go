// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package cd_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

func runScript(t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScript(t, script, dir, opts...)
}

func runScriptCtx(ctx context.Context, t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, opts...)
}

// cmdRun runs a script with AllowedPaths anchored at dir.
func cmdRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return runScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

// makeDir creates an absolute subdirectory under base and returns its path.
func makeDir(t *testing.T, base, rel string) string {
	t.Helper()
	abs := filepath.Join(base, rel)
	require.NoError(t, os.MkdirAll(abs, 0o755))
	return abs
}

// makeFile creates a regular file at base/rel and returns its absolute path.
func makeFile(t *testing.T, base, rel, content string) string {
	t.Helper()
	abs := filepath.Join(base, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
	return abs
}

// --- Basic positional argument ---

func TestCdAbsoluteDir(t *testing.T) {
	dir := t.TempDir()
	sub := makeDir(t, dir, "sub")
	stdout, stderr, code := cmdRun(t, "cd "+sub+"\necho $PWD", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, sub+"\n", stdout)
}

func TestCdRelativeDir(t *testing.T) {
	dir := t.TempDir()
	sub := makeDir(t, dir, "child")
	stdout, _, code := cmdRun(t, "cd child\necho $PWD", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, sub+"\n", stdout)
}

func TestCdRelativeDotDot(t *testing.T) {
	dir := t.TempDir()
	makeDir(t, dir, "a/b")
	script := "cd a\ncd b\ncd ..\necho $PWD"
	stdout, _, code := cmdRun(t, script, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, filepath.Join(dir, "a")+"\n", stdout)
}

func TestCdUpdatesPwdAndOldpwd(t *testing.T) {
	dir := t.TempDir()
	sub := makeDir(t, dir, "sub")
	script := "cd " + sub + "\necho PWD=$PWD\necho OLDPWD=$OLDPWD"
	stdout, _, code := cmdRun(t, script, dir)
	assert.Equal(t, 0, code)
	expected := "PWD=" + sub + "\nOLDPWD=" + dir + "\n"
	assert.Equal(t, expected, stdout)
}

// --- cd - ---

func TestCdDashSwitchesAndPrints(t *testing.T) {
	dir := t.TempDir()
	sub := makeDir(t, dir, "sub")
	script := "cd " + sub + "\ncd -"
	stdout, _, code := cmdRun(t, script, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, dir+"\n", stdout)
}

func TestCdDashWithoutOldpwd(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "cd -", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "cd: OLDPWD not set\n", stderr)
}

func TestCdDashSetsOldpwdToCurrent(t *testing.T) {
	dir := t.TempDir()
	sub := makeDir(t, dir, "sub")
	script := "cd " + sub + "\ncd -\necho OLDPWD=$OLDPWD\necho PWD=$PWD"
	stdout, _, code := cmdRun(t, script, dir)
	assert.Equal(t, 0, code)
	// `cd -` prints the destination (the previous OLDPWD = dir).
	expected := dir + "\nOLDPWD=" + sub + "\nPWD=" + dir + "\n"
	assert.Equal(t, expected, stdout)
}

// --- cd with no args (HOME) ---

func TestCdNoArgsWithHome(t *testing.T) {
	dir := t.TempDir()
	home := makeDir(t, dir, "myhome")
	stdout, _, code := runScript(t, "cd\necho $PWD", dir,
		interp.AllowedPaths([]string{dir}),
		interp.Env("HOME="+home))
	assert.Equal(t, 0, code)
	assert.Equal(t, home+"\n", stdout)
}

func TestCdNoArgsWithoutHome(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "cd", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "cd: HOME not set\n", stderr)
}

func TestCdNoArgsEmptyHome(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := runScript(t, "cd", dir,
		interp.AllowedPaths([]string{dir}),
		interp.Env("HOME="))
	assert.Equal(t, 1, code)
	assert.Equal(t, "cd: HOME not set\n", stderr)
}

// --- Errors ---

func TestCdMissingDir(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "cd does-not-exist", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "cd: does-not-exist: no such file or directory\n", stderr)
}

func TestCdNotADirectory(t *testing.T) {
	dir := t.TempDir()
	makeFile(t, dir, "afile", "x")
	_, stderr, code := cmdRun(t, "cd afile", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "cd: afile: not a directory\n", stderr)
}

func TestCdOutsideAllowedPaths(t *testing.T) {
	allowed := t.TempDir()
	other := t.TempDir()
	_, stderr, code := cmdRun(t, "cd "+other, allowed)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "cd: ")
	assert.Contains(t, stderr, other)
}

func TestCdTooManyArgs(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "cd a b", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "cd: too many arguments\n", stderr)
}

func TestCdUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "cd --no-such-flag", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "cd:")
}

func TestCdShortRejectFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "cd -X", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "cd:")
}

func TestCdEmptyArg(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `cd ""`, dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "cd: : no such file or directory\n", stderr)
}

// --- Help ---

func TestCdHelp(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, "cd --help", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Contains(t, stdout, "Usage: cd")
	assert.Contains(t, stdout, "--logical")
	assert.Contains(t, stdout, "--physical")
}

func TestCdHelpShort(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, "cd -h", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage: cd")
}

// --- Failure leaves state unchanged ---

func TestCdFailureLeavesPwdAndOldpwdUntouched(t *testing.T) {
	dir := t.TempDir()
	makeDir(t, dir, "good")
	script := strings.Join([]string{
		"cd good",
		"cd does-not-exist",
		"echo PWD=$PWD",
		"echo OLDPWD=$OLDPWD",
	}, "\n")
	stdout, stderr, code := cmdRun(t, script, dir)
	// `cd does-not-exist` exits 1 but the script then reports the unchanged state.
	// The interpreter ends with the last command's exit code.
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr, "no such file or directory")
	expected := "PWD=" + filepath.Join(dir, "good") + "\nOLDPWD=" + dir + "\n"
	assert.Equal(t, expected, stdout)
}

// --- -L vs -P ---

func TestCdLogicalDefault(t *testing.T) {
	dir := t.TempDir()
	target := makeDir(t, dir, "real")
	link := filepath.Join(dir, "alias")
	require.NoError(t, os.Symlink(target, link))
	stdout, _, code := cmdRun(t, "cd alias\necho $PWD", dir)
	assert.Equal(t, 0, code)
	// Logical: PWD reflects the alias path, not the target.
	assert.Equal(t, filepath.Join(dir, "alias")+"\n", stdout)
}

func TestCdPhysicalResolvesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := makeDir(t, dir, "real")
	link := filepath.Join(dir, "alias")
	require.NoError(t, os.Symlink(target, link))
	stdout, _, code := cmdRun(t, "cd -P alias\necho $PWD", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, target+"\n", stdout)
}

func TestCdLPLastWins_PWins(t *testing.T) {
	dir := t.TempDir()
	target := makeDir(t, dir, "real")
	link := filepath.Join(dir, "alias")
	require.NoError(t, os.Symlink(target, link))
	stdout, _, code := cmdRun(t, "cd -L -P alias\necho $PWD", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, target+"\n", stdout)
}

func TestCdLPLastWins_LWins(t *testing.T) {
	dir := t.TempDir()
	target := makeDir(t, dir, "real")
	link := filepath.Join(dir, "alias")
	require.NoError(t, os.Symlink(target, link))
	stdout, _, code := cmdRun(t, "cd -P -L alias\necho $PWD", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, filepath.Join(dir, "alias")+"\n", stdout)
}

// --- Path-too-long hardening ---

func TestCdPathTooLong(t *testing.T) {
	dir := t.TempDir()
	long := "/" + strings.Repeat("a", 70000)
	_, stderr, code := cmdRun(t, "cd "+long, dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "cd: path too long\n", stderr)
}

// --- Symlink loop detection ---

func TestCdPhysicalSymlinkLoop(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	require.NoError(t, os.Symlink(b, a))
	require.NoError(t, os.Symlink(a, b))
	_, stderr, code := cmdRun(t, "cd -P a", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "cd: a:")
	assert.Contains(t, stderr, "too many levels of symbolic links")
}

// --- Sandbox preserved across cd ---

func TestCdHonorsAllowedPathsAfterChange(t *testing.T) {
	allowed := t.TempDir()
	makeDir(t, allowed, "sub")
	makeFile(t, allowed, "sub/inside.txt", "hello\n")
	other := t.TempDir()
	makeFile(t, other, "outside.txt", "blocked\n")

	script := strings.Join([]string{
		"cd sub",
		"cat inside.txt",
		"cat " + filepath.Join(other, "outside.txt"),
	}, "\n")
	stdout, stderr, code := runScript(t, script, allowed,
		interp.AllowedPaths([]string{allowed}))
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, "hello")
	assert.Contains(t, stderr, "outside.txt")
}

// --- Subshell isolation ---

func TestCdInSubshellDoesNotEscape(t *testing.T) {
	dir := t.TempDir()
	makeDir(t, dir, "sub")
	script := strings.Join([]string{
		"(cd sub; echo INNER=$PWD)",
		"echo OUTER=$PWD",
	}, "\n")
	stdout, _, code := cmdRun(t, script, dir)
	assert.Equal(t, 0, code)
	expected := "INNER=" + filepath.Join(dir, "sub") + "\nOUTER=" + dir + "\n"
	assert.Equal(t, expected, stdout)
}

// --- Cancellation safety ---

func TestCdCancelledContext(t *testing.T) {
	dir := t.TempDir()
	makeDir(t, dir, "sub")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A pre-cancelled context must terminate the script quickly and
	// without panicking. The runner surfaces context.Canceled as a
	// non-ExitStatus error, which testutil maps to exit code 0; the
	// only assertion that survives that translation is "did it return".
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = runScriptCtx(ctx, t, "cd sub", dir, interp.AllowedPaths([]string{dir}))
	}()
	select {
	case <-done:
		// Returned cleanly — no panic, no hang.
	case <-time.After(5 * time.Second):
		t.Fatal("cd hung after context cancellation")
	}
}

// --- Long (non-cyclic) symlink chain ---

func TestCdPhysicalLongSymlinkChain(t *testing.T) {
	dir := t.TempDir()
	target := makeDir(t, dir, "real")
	// Build a chain link0 -> link1 -> ... -> link50 -> real, exceeding
	// the maxSymlinkHops cap. The chain is acyclic but still must be
	// rejected so that cd cannot be made to do unbounded work.
	prev := target
	for i := 50; i >= 0; i-- {
		name := filepath.Join(dir, "link"+strings.Repeat("x", i))
		require.NoError(t, os.Symlink(prev, name))
		prev = name
	}
	_, stderr, code := cmdRun(t, "cd -P "+prev, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "too many levels of symbolic links")
}
