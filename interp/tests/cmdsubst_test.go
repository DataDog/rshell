// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tests_test

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

	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
)

// cmdSubstRun runs a script with the given dir as working directory and allowed path.
func cmdSubstRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return cmdSubstRunWithOpts(t, script, dir, interp.AllowedPaths([]string{dir}), interpoption.AllowAllCommands().(interp.RunnerOption))
}

func cmdSubstRunCtx(ctx context.Context, t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return cmdSubstRunCtxWithOpts(ctx, t, script, dir, interp.AllowedPaths([]string{dir}), interpoption.AllowAllCommands().(interp.RunnerOption))
}

func cmdSubstRunWithOpts(t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return cmdSubstRunCtxWithOpts(context.Background(), t, script, dir, opts...)
}

func cmdSubstRunCtxWithOpts(ctx context.Context, t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
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

// --- Basic command substitution ---

func TestCmdSubstBasicEcho(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdSubstRun(t, `echo $(echo hello)`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
	assert.Equal(t, "", stderr)
}

func TestCmdSubstBacktick(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, "echo `echo hello`", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
}

func TestCmdSubstAssignment(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, `x=$(echo world); echo "hello $x"`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\n", stdout)
}

// --- Trailing newline stripping ---

func TestCmdSubstTrailingNewlinesStripped(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, `x=$(printf "hello\n\n\n"); echo "[$x]"`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "[hello]\n", stdout)
}

// --- Empty output ---

func TestCmdSubstEmptyOutput(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, `x=$(true); echo "[$x]"`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "[]\n", stdout)
}

// --- Exit status propagation ---

func TestCmdSubstExitStatus(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, `x=$(exit 3); echo "$?"`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "3\n", stdout)
}

func TestCmdSubstExitStatusFalse(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, `x=$(false); echo "$?"`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout)
}

// --- Nested substitution ---

func TestCmdSubstNested(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, `echo $(echo $(echo nested))`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "nested\n", stdout)
}

// --- Pipes inside command substitution ---

func TestCmdSubstWithPipe(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, `x=$(echo "hello world" | grep hello); echo "$x"`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\n", stdout)
}

// --- Double quotes preserve spaces ---

func TestCmdSubstInDoubleQuotes(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, `echo "$(echo "hello   world")"`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello   world\n", stdout)
}

// --- Word splitting without quotes ---

func TestCmdSubstWordSplitting(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, `for w in $(echo "a  b  c"); do echo "[$w]"; done`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "[a]\n[b]\n[c]\n", stdout)
}

// --- $(<file) shortcut rejection ---
//
// The POSIX $(<file) shortcut is intentionally NOT supported because it
// reads file contents without invoking any command, bypassing the
// AllowedCommands allowlist. Scripts must use $(cat file) or an
// equivalent allowed builtin instead. Both of the tests below verify
// that the shortcut is rejected at validation (exit code 2) rather
// than silently performing the read.

func TestCmdSubstCatShortcutRejected(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data.txt"), []byte("file content"), 0644))
	stdout, stderr, code := cmdSubstRun(t, `x=$(<data.txt); echo "$x"`, dir)
	assert.Equal(t, 2, code, "$(<file) must be rejected at validation")
	assert.Equal(t, "", stdout, "must not emit any file content")
	assert.Contains(t, stderr, "< input redirection requires a command")
}

func TestCmdSubstCatShortcutMissingFileRejected(t *testing.T) {
	dir := t.TempDir()
	// Even with a nonexistent file, the shortcut must be rejected at
	// validation before any file access is attempted.
	stdout, stderr, code := cmdSubstRun(t, `x=$(<nonexistent.txt); echo "$?"`, dir)
	assert.Equal(t, 2, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "< input redirection requires a command")
}

func TestCmdSubstCatShortcutCommandAllowlistBypass(t *testing.T) {
	// Regression test for the original vulnerability: with the file in
	// AllowedPaths but no file-reading commands in the allowlist, the
	// shortcut must not leak file contents.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("top secret"), 0644))
	stdout, stderr, code := cmdSubstRunWithOpts(t,
		`x=$(<secret.txt); echo "$x"`,
		dir,
		interp.AllowedPaths([]string{dir}),
		interp.AllowedCommands([]string{"rshell:echo"}),
	)
	assert.Equal(t, 2, code, "must reject at validation before any read occurs")
	assert.Equal(t, "", stdout, "must not leak the file contents")
	assert.Contains(t, stderr, "< input redirection requires a command")
}

// --- For loop integration ---

func TestCmdSubstInForLoop(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, `for x in $(echo "a b c"); do echo "$x"; done`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\nc\n", stdout)
}

// --- If condition ---

func TestCmdSubstInIfCondition(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, `if [ "$(echo yes)" = "yes" ]; then echo matched; fi`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "matched\n", stdout)
}

// --- Context cancellation ---

func TestCmdSubstContextCancellation(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// This should complete quickly, not hang
	stdout, _, code := cmdSubstRunCtx(ctx, t, `echo $(echo fast)`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "fast\n", stdout)
}

// --- Multiline output ---

func TestCmdSubstMultilineOutput(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, `x=$(printf "line1\nline2\nline3"); echo "$x"`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line1\nline2\nline3\n", stdout)
}

// --- Heredoc with command substitution ---

func TestCmdSubstInHeredoc(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, "cat <<EOF\nhello $(echo world)\nEOF", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\n", stdout)
}

// --- Process substitution remains blocked ---

func TestProcessSubstitutionBlocked(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdSubstRun(t, `cat <(echo hello)`, dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "process substitution is not supported")
}
