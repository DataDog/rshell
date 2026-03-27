// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tests_test

import (
	"bytes"
	"context"
	"fmt"
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

// --- Memory limits: output capping ---

// TestGlobalStdoutCapReturnsError verifies that Run returns ErrOutputLimitExceeded
// when a script exceeds the 10 MiB stdout cap. The script runs to completion
// but partial output (up to the limit) is still delivered, and the caller
// receives a well-defined error rather than a silent truncation.
func TestGlobalStdoutCapReturnsError(t *testing.T) {
	dir := t.TempDir()

	// Create a file of exactly 1 MiB.
	content := strings.Repeat("A", 1<<20)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mb.txt"), []byte(content), 0644))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// cat the file 11 times — produces 11 MiB, exceeding the 10 MiB cap.
	script := `for i in 1 2 3 4 5 6 7 8 9 10 11; do cat mb.txt; done`
	var outBuf bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(nil, &outBuf, nil),
		interp.AllowedPaths([]string{dir}),
		interpoption.AllowAllCommands().(interp.RunnerOption),
	)
	require.NoError(t, err)
	defer runner.Close()
	runner.Dir = dir

	prog, err := syntax.NewParser().Parse(strings.NewReader(script), "test")
	require.NoError(t, err)

	runErr := runner.Run(ctx, prog)
	assert.ErrorIs(t, runErr, interp.ErrOutputLimitExceeded,
		"Run must return ErrOutputLimitExceeded when stdout cap is exceeded")
	// Output up to the cap must still be delivered.
	assert.LessOrEqual(t, outBuf.Len(), 10*1024*1024,
		"stdout must not exceed 10 MiB; got %d bytes", outBuf.Len())
	assert.Greater(t, outBuf.Len(), 0, "expected non-empty stdout before cap")
}

func TestCmdSubstOutputCapped(t *testing.T) {
	// Generate output exceeding 1 MiB inside command substitution.
	// The output should be truncated, not cause OOM.
	dir := t.TempDir()

	// Create a file slightly over 1 MiB
	content := strings.Repeat("A", 1<<20+100)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big.txt"), []byte(content), 0644))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Using $(<file) shortcut with a large file — verify truncation via wc -c.
	// The substitution captures at most 1 MiB (1048576 bytes). Trailing newline
	// stripping does not apply here because the content has no trailing newlines.
	// echo adds a newline, so wc -c sees 1048576 + 1 = 1048577.
	stdout, _, code := cmdSubstRunCtx(ctx, t, `x=$(<big.txt); echo "$x" | wc -c`, dir)
	assert.Equal(t, 0, code)
	// wc -c output may have leading whitespace on some platforms
	assert.Equal(t, "1048577", strings.TrimSpace(stdout))
}

func TestCmdSubstOutputCappedEcho(t *testing.T) {
	// Verify that command substitution with large output doesn't crash
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create a script that generates many lines of output in a subshell
	script := `x=$(for i in a b c d e f g h i j; do for j in a b c d e f g h i j; do echo "line"; done; done); echo done`
	stdout, _, code := cmdSubstRunCtx(ctx, t, script, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "done\n", stdout)
}

// --- Edge cases ---

func TestCmdSubstEmptyStmts(t *testing.T) {
	// $() with no commands should produce empty string
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, `echo "[$(true)]"`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "[]\n", stdout)
}

func TestCmdSubstOnlyWhitespaceOutput(t *testing.T) {
	dir := t.TempDir()
	// printf with spaces + trailing newline
	stdout, _, code := cmdSubstRun(t, `x=$(printf "  spaces  \n"); echo "[$x]"`, dir)
	assert.Equal(t, 0, code)
	// Trailing newlines are stripped, but internal spaces preserved
	assert.Equal(t, "[  spaces  ]\n", stdout)
}

func TestCmdSubstMultipleInOneLine(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, `echo "$(echo hello) $(echo world)"`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\n", stdout)
}

func TestCmdSubstInlineVar(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, `x=hello; echo "$(echo $x)"`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
}

// --- Subshell hardening ---

func TestSubshellDeeplyNested(t *testing.T) {
	dir := t.TempDir()
	// 5 levels of nesting — each level wraps a subshell
	script := `( ( ( ( ( echo deep ) ) ) ) )`
	stdout, _, code := subshellRun(t, script, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "deep\n", stdout)
}

func TestSubshellWithRedirection(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("from file\n"), 0644))
	stdout, _, code := subshellRun(t, `(cat < input.txt)`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "from file\n", stdout)
}

func TestSubshellStderrToDevNull(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := subshellRun(t, fmt.Sprintf(`(echo out; echo err >&2) 2>%s`, os.DevNull), dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "out\n", stdout)
	assert.Equal(t, "", stderr)
}

func TestCmdSubstStderr(t *testing.T) {
	dir := t.TempDir()
	// stderr from command substitution goes to parent's stderr, not captured
	stdout, stderr, code := cmdSubstRun(t, `x=$(echo err >&2; echo out); echo "$x"`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "out\n", stdout)
	assert.Equal(t, "err\n", stderr)
}

// --- Combined features ---

func TestCmdSubstInSubshell(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := subshellRun(t, `(echo "$(echo nested)")`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "nested\n", stdout)
}

func TestSubshellInCmdSubst(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, `x=$( (echo inside) ); echo "$x"`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "inside\n", stdout)
}

func TestCmdSubstPreservesInternalNewlines(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdSubstRun(t, `x=$(printf "a\n\nb\n\n"); echo "$x"`, dir)
	assert.Equal(t, 0, code)
	// Trailing newlines stripped, internal ones preserved
	assert.Equal(t, "a\n\nb\n", stdout)
}
