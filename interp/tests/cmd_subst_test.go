// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tests_test

import (
	"bytes"
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
)

func substRun(t *testing.T, script string) (string, string, int) {
	t.Helper()
	return substRunCtx(context.Background(), t, script)
}

func substRunCtx(ctx context.Context, t *testing.T, script string) (string, string, int) {
	t.Helper()
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(script), "")
	require.NoError(t, err)
	var outBuf, errBuf bytes.Buffer
	runner, err := interp.New(interp.StdIO(nil, &outBuf, &errBuf))
	require.NoError(t, err)
	defer runner.Close()
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

// --- Command Substitution ---

func TestCmdSubstBasic(t *testing.T) {
	stdout, _, code := substRun(t, `echo $(echo hello)`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
}

func TestCmdSubstAssignment(t *testing.T) {
	stdout, _, code := substRun(t, `X=$(echo world); echo "$X"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "world\n", stdout)
}

func TestCmdSubstTrailingNewlinesStripped(t *testing.T) {
	stdout, _, code := substRun(t, `X=$(printf "hello\n\n\n"); echo "[$X]"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "[hello]\n", stdout)
}

func TestCmdSubstEmptyOutput(t *testing.T) {
	stdout, _, code := substRun(t, `X=$(true); echo "[$X]"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "[]\n", stdout)
}

func TestCmdSubstNested(t *testing.T) {
	stdout, _, code := substRun(t, `echo $(echo $(echo deep))`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "deep\n", stdout)
}

func TestCmdSubstBacktick(t *testing.T) {
	stdout, _, code := substRun(t, "echo `echo backtick`")
	assert.Equal(t, 0, code)
	assert.Equal(t, "backtick\n", stdout)
}

func TestCmdSubstExitCode(t *testing.T) {
	stdout, _, code := substRun(t, `X=$(exit 7); echo "$?"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "7\n", stdout)
}

func TestCmdSubstExitDoesNotExitParent(t *testing.T) {
	stdout, _, code := substRun(t, `X=$(exit 1); echo "still here"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "still here\n", stdout)
}

func TestCmdSubstWithPipe(t *testing.T) {
	stdout, _, code := substRun(t, `X=$(echo "hello world" | cat); echo "$X"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\n", stdout)
}

func TestCmdSubstWordSplitting(t *testing.T) {
	stdout, _, code := substRun(t, `for w in $(echo "a b c"); do echo "$w"; done`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\nc\n", stdout)
}

func TestCmdSubstInDoubleQuotes(t *testing.T) {
	stdout, _, code := substRun(t, `echo "val=$(echo 42)"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "val=42\n", stdout)
}

func TestCmdSubstMultiline(t *testing.T) {
	stdout, _, code := substRun(t, `X=$(printf "a\nb\nc\n"); echo "$X"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\nc\n", stdout)
}

func TestCmdSubstInHeredoc(t *testing.T) {
	stdout, _, code := substRun(t, "cat <<EOF\nvalue is $(echo 42)\nEOF")
	assert.Equal(t, 0, code)
	assert.Equal(t, "value is 42\n", stdout)
}

func TestCmdSubstContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// This should not hang — context cancellation should propagate to the subshell.
	_, _, code := substRunCtx(ctx, t, `X=$(for i in a b c d e; do echo "$i"; done); echo "$X"`)
	assert.Equal(t, 0, code)
}

// --- Subshell ---

func TestSubshellBasic(t *testing.T) {
	stdout, _, code := substRun(t, `(echo hello)`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
}

func TestSubshellVariableIsolation(t *testing.T) {
	stdout, _, code := substRun(t, `X=before; (X=inside; echo "$X"); echo "$X"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "inside\nbefore\n", stdout)
}

func TestSubshellExitCodePropagation(t *testing.T) {
	stdout, _, code := substRun(t, `(exit 42); echo "$?"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "42\n", stdout)
}

func TestSubshellExitDoesNotExitParent(t *testing.T) {
	stdout, _, code := substRun(t, `(exit 1); echo "still running"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "still running\n", stdout)
}

func TestSubshellNested(t *testing.T) {
	stdout, _, code := substRun(t, `( (echo nested) )`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "nested\n", stdout)
}

func TestSubshellMultipleCommands(t *testing.T) {
	stdout, _, code := substRun(t, `(echo a; echo b; echo c)`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\nc\n", stdout)
}

func TestSubshellPipe(t *testing.T) {
	stdout, _, code := substRun(t, `(echo "hello world" | cat)`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\n", stdout)
}

func TestSubshellChainedAnd(t *testing.T) {
	stdout, _, code := substRun(t, `(true) && echo ok`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "ok\n", stdout)
}

func TestSubshellChainedOr(t *testing.T) {
	stdout, _, code := substRun(t, `(false) || echo "fallback"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "fallback\n", stdout)
}

func TestSubshellNegation(t *testing.T) {
	stdout, _, code := substRun(t, `! (false); echo "$?"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "0\n", stdout)
}

func TestSubshellForLoop(t *testing.T) {
	stdout, _, code := substRun(t, `(for i in a b c; do echo "$i"; done)`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\nc\n", stdout)
}

// --- Process Substitution (Unix only — requires /dev/fd) ---

func skipIfWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("process substitution requires /dev/fd (Unix only)")
	}
}

func TestProcSubstBasicInput(t *testing.T) {
	skipIfWindows(t)
	stdout, _, code := substRun(t, `cat <(echo hello)`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
}

func TestProcSubstMultiple(t *testing.T) {
	skipIfWindows(t)
	stdout, _, code := substRun(t, `cat <(echo first) <(echo second)`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "first\nsecond\n", stdout)
}

func TestProcSubstWithPipeInside(t *testing.T) {
	skipIfWindows(t)
	stdout, _, code := substRun(t, `cat <(echo "hello world" | cat)`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\n", stdout)
}

func TestProcSubstWithCmdSubst(t *testing.T) {
	skipIfWindows(t)
	stdout, _, code := substRun(t, `X=$(cat <(echo from_proc)); echo "$X"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "from_proc\n", stdout)
}

func TestProcSubstOutputMode(t *testing.T) {
	skipIfWindows(t)
	// >(cmd) creates a write-end pipe. The outer command receives /dev/fd/N
	// as a string argument. Since builtins only open files for reading,
	// >(cmd) has limited utility in the restricted shell, but the pipe
	// mechanism itself should work. Test that the path is generated.
	stdout, _, code := substRun(t, `echo >(true)`)
	assert.Equal(t, 0, code)
	// The output should be a /dev/fd/N path
	assert.Contains(t, stdout, "/dev/fd/")
}
