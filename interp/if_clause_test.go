// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
)

func ifRun(t *testing.T, script string) (string, string, int) {
	t.Helper()
	return ifRunCtx(context.Background(), t, script)
}

func ifRunCtx(ctx context.Context, t *testing.T, script string) (string, string, int) {
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

// --- Basic if/elif/else ---

func TestIfBasicTrue(t *testing.T) {
	stdout, _, code := ifRun(t, `if true; then echo yes; fi`)
	assert.Equal(t, "yes\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfBasicFalse(t *testing.T) {
	stdout, _, code := ifRun(t, `if false; then echo yes; fi`)
	assert.Equal(t, "", stdout)
	assert.Equal(t, 0, code)
}

func TestIfElse(t *testing.T) {
	stdout, _, code := ifRun(t, `if false; then echo yes; else echo no; fi`)
	assert.Equal(t, "no\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfElif(t *testing.T) {
	stdout, _, code := ifRun(t, `if false; then echo a; elif true; then echo b; fi`)
	assert.Equal(t, "b\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfElifElse(t *testing.T) {
	stdout, _, code := ifRun(t, `if false; then echo a; elif false; then echo b; else echo c; fi`)
	assert.Equal(t, "c\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfElifFirstTrue(t *testing.T) {
	stdout, _, code := ifRun(t, `if true; then echo a; elif true; then echo b; else echo c; fi`)
	assert.Equal(t, "a\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfMultipleElif(t *testing.T) {
	stdout, _, code := ifRun(t, `if false; then echo a; elif false; then echo b; elif true; then echo c; else echo d; fi`)
	assert.Equal(t, "c\n", stdout)
	assert.Equal(t, 0, code)
}

// --- Nested ---

func TestIfNested(t *testing.T) {
	stdout, _, code := ifRun(t, `
if true; then
  if true; then echo inner; fi
fi`)
	assert.Equal(t, "inner\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfNestedInElse(t *testing.T) {
	stdout, _, code := ifRun(t, `
if false; then
  echo a
else
  if true; then echo b; else echo c; fi
fi`)
	assert.Equal(t, "b\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfAsCondition(t *testing.T) {
	stdout, _, code := ifRun(t, `if if true; then true; fi; then echo yes; else echo no; fi`)
	assert.Equal(t, "yes\n", stdout)
	assert.Equal(t, 0, code)
}

// --- Conditions ---

func TestIfPipelineCondition(t *testing.T) {
	stdout, _, code := ifRun(t, `if echo hello | cat; then echo yes; fi`)
	assert.Equal(t, "hello\nyes\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfAndCondition(t *testing.T) {
	stdout, _, code := ifRun(t, `if true && true; then echo yes; else echo no; fi`)
	assert.Equal(t, "yes\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfAndConditionFalse(t *testing.T) {
	stdout, _, code := ifRun(t, `if true && false; then echo yes; else echo no; fi`)
	assert.Equal(t, "no\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfOrCondition(t *testing.T) {
	stdout, _, code := ifRun(t, `if false || true; then echo yes; else echo no; fi`)
	assert.Equal(t, "yes\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfNegatedCondition(t *testing.T) {
	stdout, _, code := ifRun(t, `if ! false; then echo yes; else echo no; fi`)
	assert.Equal(t, "yes\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfNegatedTrue(t *testing.T) {
	stdout, _, code := ifRun(t, `if ! true; then echo yes; else echo no; fi`)
	assert.Equal(t, "no\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfTestBuiltin(t *testing.T) {
	stdout, _, code := ifRun(t, `if [ "a" = "a" ]; then echo match; else echo no; fi`)
	assert.Equal(t, "match\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfTestBuiltinFalse(t *testing.T) {
	stdout, _, code := ifRun(t, `if [ "a" = "b" ]; then echo match; else echo no; fi`)
	assert.Equal(t, "no\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfMultipleStmtsCondition(t *testing.T) {
	// Last exit code in condition determines branch
	stdout, _, code := ifRun(t, `if false; true; then echo yes; else echo no; fi`)
	assert.Equal(t, "yes\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfMultipleStmtsConditionLastFalse(t *testing.T) {
	stdout, _, code := ifRun(t, `if true; false; then echo yes; else echo no; fi`)
	assert.Equal(t, "no\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfVariableCondition(t *testing.T) {
	stdout, _, code := ifRun(t, `X=hello; if [ "$X" = "hello" ]; then echo match; fi`)
	assert.Equal(t, "match\n", stdout)
	assert.Equal(t, 0, code)
}

// --- Exit codes ---

func TestIfThenBranchExitCode(t *testing.T) {
	stdout, _, code := ifRun(t, `if true; then false; fi; echo $?`)
	assert.Equal(t, "1\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfElseBranchExitCode(t *testing.T) {
	stdout, _, code := ifRun(t, `if false; then true; else false; fi; echo $?`)
	assert.Equal(t, "1\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfNoBranchExitZero(t *testing.T) {
	stdout, _, code := ifRun(t, `false; if false; then echo yes; fi; echo $?`)
	assert.Equal(t, "0\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfElifBranchExitCode(t *testing.T) {
	stdout, _, code := ifRun(t, `if false; then true; elif true; then false; fi; echo $?`)
	assert.Equal(t, "1\n", stdout)
	assert.Equal(t, 0, code)
}

// --- Loop interaction ---

func TestIfBreakInLoop(t *testing.T) {
	stdout, _, code := ifRun(t, `
for i in a b c; do
  if [ "$i" = "b" ]; then break; fi
  echo $i
done`)
	assert.Equal(t, "a\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfContinueInLoop(t *testing.T) {
	stdout, _, code := ifRun(t, `
for i in a b c; do
  if [ "$i" = "b" ]; then continue; fi
  echo $i
done`)
	assert.Equal(t, "a\nc\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfBreakInElse(t *testing.T) {
	stdout, _, code := ifRun(t, `
for i in a b c; do
  if [ "$i" = "x" ]; then
    echo never
  else
    if [ "$i" = "b" ]; then break; fi
  fi
  echo $i
done`)
	assert.Equal(t, "a\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfForInBody(t *testing.T) {
	stdout, _, code := ifRun(t, `
if true; then
  for i in x y z; do echo $i; done
fi`)
	assert.Equal(t, "x\ny\nz\n", stdout)
	assert.Equal(t, 0, code)
}

// --- Edge cases ---

func TestIfExitInThen(t *testing.T) {
	_, _, code := ifRun(t, `if true; then exit 42; fi; echo nope`)
	assert.Equal(t, 42, code)
}

func TestIfCommandsBeforeAfter(t *testing.T) {
	stdout, _, code := ifRun(t, `echo before; if true; then echo inside; fi; echo after`)
	assert.Equal(t, "before\ninside\nafter\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfMultipleStmtsInBody(t *testing.T) {
	stdout, _, code := ifRun(t, `
if true; then
  echo one
  echo two
  echo three
fi`)
	assert.Equal(t, "one\ntwo\nthree\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfAssignmentInBody(t *testing.T) {
	stdout, _, code := ifRun(t, `if true; then X=hello; fi; echo $X`)
	assert.Equal(t, "hello\n", stdout)
	assert.Equal(t, 0, code)
}

func TestIfBlockedFeatureInCondition(t *testing.T) {
	_, stderr, code := ifRun(t, `if $(true); then echo yes; fi`)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "command substitution is not supported")
}
