// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tests_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
)

// whileRun runs a script with all commands allowed and no path restrictions.
func whileRun(t *testing.T, script string) (string, string, int) {
	t.Helper()
	return whileRunCtx(context.Background(), t, script)
}

// whileRunCtx is whileRun with an explicit context — used by tests that need
// to assert ctx-cancellation behaviour. All commands are allowed; no path
// restrictions are configured.
func whileRunCtx(ctx context.Context, t *testing.T, script string) (string, string, int) {
	t.Helper()
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(script), "")
	require.NoError(t, err)

	var outBuf, errBuf bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(nil, &outBuf, &errBuf),
		interpoption.AllowAllCommands().(interp.RunnerOption),
	)
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

// --- Basic semantics ---

func TestWhileBasicCounter(t *testing.T) {
	stdout, stderr, code := whileRun(t, `i=; while [ "$i" != aaa ]; do i="${i}a"; echo "$i"; done`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\naa\naaa\n", stdout)
	assert.Equal(t, "", stderr)
}

func TestUntilBasicCounter(t *testing.T) {
	stdout, _, code := whileRun(t, `i=; until [ "$i" = aaa ]; do i="${i}a"; echo "$i"; done`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\naa\naaa\n", stdout)
}

func TestWhileFalseZeroIterations(t *testing.T) {
	stdout, _, code := whileRun(t, `while false; do echo never; done; echo "exit=$?"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "exit=0\n", stdout)
}

func TestUntilTrueZeroIterations(t *testing.T) {
	stdout, _, code := whileRun(t, `until true; do echo never; done; echo "exit=$?"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "exit=0\n", stdout)
}

// --- Exit-status semantics ---

// Per POSIX 1003.1-2024 §2.9.4.1, the while loop's exit status is the exit
// status of the last command of the last body iteration.
func TestWhileExitStatusFromLastBody(t *testing.T) {
	stdout, _, code := whileRun(t, `i=; while [ "$i" != aa ]; do i="${i}a"; false; done; echo "exit=$?"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "exit=1\n", stdout)
}

// Cond's exit status must NOT leak as the loop's exit status — once the cond
// fails (status 1), $? after the loop should still be 0 if the body never ran.
func TestWhileCondFailDoesNotLeak(t *testing.T) {
	stdout, _, code := whileRun(t, `while false; do :; done; echo "exit=$?"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "exit=0\n", stdout)
}

func TestUntilCondTrueDoesNotLeak(t *testing.T) {
	stdout, _, code := whileRun(t, `until true; do :; done; echo "exit=$?"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "exit=0\n", stdout)
}

// Symmetric with TestWhileExitStatusFromLastBody: until's exit status is the
// last body command's exit (POSIX 2.9.4.2).
func TestUntilExitStatusFromLastBody(t *testing.T) {
	stdout, _, code := whileRun(t, `i=; until [ "$i" = aa ]; do i="${i}a"; false; done; echo "exit=$?"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "exit=1\n", stdout)
}

// --- Pipeline / list condition ---

func TestWhilePipelineCondition(t *testing.T) {
	stdout, _, code := whileRun(t, `while echo x | grep -q x; do echo body; break; done`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "body\n", stdout)
}

func TestWhileMultiStmtConditionLastWins(t *testing.T) {
	// Cond is "true; false" — last cmd is false → don't enter body.
	stdout, _, code := whileRun(t, `while true; false; do echo body; done; echo "exit=$?"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "exit=0\n", stdout)
}

func TestWhileMultiStmtConditionLastTrueRuns(t *testing.T) {
	// Cond runs an assignment then a test. Loop iterates while assignment-then-test
	// returns 0.
	stdout, _, code := whileRun(t, `i=; while i="${i}a"; [ "$i" != aaa ]; do echo "$i"; done`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\naa\n", stdout)
}

// --- break / continue from body ---

func TestWhileBreakSimple(t *testing.T) {
	stdout, _, code := whileRun(t, `while true; do echo a; break; done`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n", stdout)
}

func TestWhileBreakTwoLevels(t *testing.T) {
	stdout, _, code := whileRun(t, `while true; do while true; do echo a; break 2; done; echo unreachable; done; echo done`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\ndone\n", stdout)
}

func TestWhileExcessBreakClampedAtOutermost(t *testing.T) {
	// "break 99" at outermost loop → clamped to break-out-of-this-loop.
	stdout, _, code := whileRun(t, `while true; do echo a; break 99; done; echo done`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\ndone\n", stdout)
}

func TestWhileExcessContinueClampedAtOutermost(t *testing.T) {
	// "continue 99" at outermost loop should keep iterating (bash clamp).
	stdout, _, code := whileRun(t, `i=; while [ "$i" != aaa ]; do i="${i}a"; echo "$i"; continue 99; echo unreachable; done`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\naa\naaa\n", stdout)
}

func TestWhileContinueSkipsRestOfBody(t *testing.T) {
	stdout, _, code := whileRun(t, `i=; while [ "$i" != aa ]; do i="${i}a"; continue; echo unreachable; done; echo "i=$i"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "i=aa\n", stdout)
}

// --- Nesting ---

func TestWhileInsideUntilWithBreak2(t *testing.T) {
	stdout, _, code := whileRun(t, `until false; do while true; do echo inner; break 2; done; done; echo done`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "inner\ndone\n", stdout)
}

func TestUntilInsideWhileWithContinue2(t *testing.T) {
	stdout, _, code := whileRun(t, `i=; while [ "$i" != aa ]; do i="${i}a"; until false; do echo "iter $i"; continue 2; done; echo unreachable; done; echo done`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "iter a\niter aa\ndone\n", stdout)
}

func TestDeeplyNestedWhile(t *testing.T) {
	// 50 levels of nested while-true; innermost breaks all the way out.
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("while true; do ")
	}
	b.WriteString("echo deep; break 50; ")
	for i := 0; i < 50; i++ {
		b.WriteString("done; ")
	}
	b.WriteString("echo done")
	stdout, _, code := whileRun(t, b.String())
	assert.Equal(t, 0, code)
	assert.Equal(t, "deep\ndone\n", stdout)
}

// --- Interaction with if / brace group ---

func TestWhileBreakInsideElse(t *testing.T) {
	stdout, _, code := whileRun(t, `while true; do if false; then :; else break; fi; done; echo done`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "done\n", stdout)
}

func TestWhileContinueInsideElse(t *testing.T) {
	stdout, _, code := whileRun(t, `i=; while [ "$i" != aa ]; do i="${i}a"; if false; then :; else continue; fi; echo unreachable; done; echo "i=$i"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "i=aa\n", stdout)
}

func TestWhileBraceBody(t *testing.T) {
	stdout, _, code := whileRun(t, `while true; do { echo foo; break; }; done`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "foo\n", stdout)
}

// --- Pipes feeding the loop / loop feeding pipes ---

func TestWhilePipeOutputToHead(t *testing.T) {
	stdout, _, code := whileRun(t, `i=; while [ "$i" != aaaaa ]; do i="${i}a"; echo "$i"; done | head -3`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\naa\naaa\n", stdout)
}

// --- exit propagates out of the loop ---

func TestExitInsideWhileBody(t *testing.T) {
	_, _, code := whileRun(t, `while true; do exit 5; done`)
	assert.Equal(t, 5, code)
}

func TestExitInsideWhileCondition(t *testing.T) {
	_, _, code := whileRun(t, `while exit 7; do echo body; done`)
	assert.Equal(t, 7, code)
}

// --- break / continue from inside the condition list ---

// `break` inside the condition list of a while exits the loop (the body is
// not entered for this iteration).
func TestWhileBreakInCondition(t *testing.T) {
	stdout, _, code := whileRun(t, `while echo cond; break; do echo body; done; echo "exit=$?"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "cond\nexit=0\n", stdout)
}

// `continue` inside the condition list of a while re-evaluates the condition
// (the body is not entered).
func TestWhileContinueInCondition(t *testing.T) {
	// Use a counter-based cond so we don't hang. Because `continue` inside the
	// cond skips body and re-evaluates cond, we'll spin until [ "$i" = aa ]
	// (cond fails, loop exits).
	stdout, _, code := whileRun(t, `i=; while i="${i}a"; [ "$i" != aa ] && true || break; continue; do echo body; done; echo "i=$i"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "i=aa\n", stdout)
}

// `break N` inside the condition of an inner while propagates outward.
func TestWhileBreak2InCondition(t *testing.T) {
	stdout, _, code := whileRun(t, `while true; do while echo inner; break 2; do echo body; done; echo unreachable; done; echo done`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "inner\ndone\n", stdout)
}

// `continue 2` inside the condition of an inner while propagates outward to
// the enclosing loop.
func TestWhileContinue2InCondition(t *testing.T) {
	stdout, _, code := whileRun(t, `i=; while [ "$i" != aa ]; do i="${i}a"; while echo "iter $i"; continue 2; do echo body; done; echo unreachable; done; echo done`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "iter a\niter aa\ndone\n", stdout)
}

// `continue N` inside the condition at the OUTERMOST loop must be clamped to
// "continue 1" (re-evaluate cond) — matching bash's behaviour for excess
// continue levels. Without the clamp we would propagate out of the only loop
// and exit cleanly, which is wrong (bash spins forever on this; we simulate
// finite progress via a counter cond).
func TestWhileExcessContinueInCondClampedAtOutermost(t *testing.T) {
	stdout, _, code := whileRun(t, `i=; while i="${i}a"; [ "$i" != aaa ] && true || break; continue 99; do echo body; done; echo "i=$i"`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "i=aaa\n", stdout)
}

// --- ctx cancellation ---

// Long-running while-true terminates within the ctx deadline rather than
// hanging indefinitely. (The ctx-cancel path returns context.Canceled, which
// the helper does not surface as a non-zero exit code; the test passes if it
// returns at all within a small fraction of the test's overall timeout.)
func TestWhileTrueRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	timer := time.AfterFunc(50*time.Millisecond, cancel)
	defer timer.Stop()
	defer cancel()
	start := time.Now()
	_, _, _ = whileRunCtx(ctx, t, `while true; do :; done`)
	assert.Less(t, time.Since(start), 5*time.Second, "loop did not terminate after ctx cancel")
}

// A pre-cancelled context exits the loop on the next iteration's top-of-loop
// ctx check (loopCtx.Err()) — this exercises a path that only fires when the
// parent ctx is already done at the moment we enter a new iteration.
func TestWhileExitsOnPreCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stdout, _, _ := whileRunCtx(ctx, t, `while true; do echo x; done`)
	assert.Empty(t, stdout, "no body iterations should run when ctx is pre-cancelled")
}

// An unbounded while-loop producer feeding a finite consumer (head -N)
// must terminate when the consumer closes its read end — bash terminates
// the producer via SIGPIPE; we approximate that via the pipeBrokenWriter
// + r.stop pipeBroken signal. Without this, the producer spins until the
// ctx deadline.
//
// Regression test for a P2 finding from the codex review on PR #216.
func TestWhilePipeProducerStopsWhenConsumerCloses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	stdout, _, code := whileRunCtx(ctx, t, `while true; do echo x; done | head -3`)
	// Should return well within the ctx budget (bash does this in <50ms).
	assert.Less(t, time.Since(start), 2*time.Second, "pipeline did not terminate after consumer closed")
	assert.Equal(t, "x\nx\nx\n", stdout)
	assert.Equal(t, 0, code)
}

// pipeBroken must propagate to subshells of the producer — `while true; do
// (while true; do echo x; done); done | head` involves a nested while inside
// a subshell, and the subshell's runner only sees pipeBroken because the
// flag is shared via *bool through subshell().
func TestWhilePipeNestedSubshellTerminates(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, _, code := whileRunCtx(ctx, t, `while true; do (while true; do echo x; done); done | head -1`)
	assert.Less(t, time.Since(start), 2*time.Second, "nested subshell pipeline did not terminate")
	assert.Equal(t, 0, code)
}

// until false is symmetric to while true: the pipeline producer must also stop
// when the consumer (head) closes its read end.
func TestUntilPipeProducerStopsWhenConsumerCloses(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	stdout, _, code := whileRunCtx(ctx, t, `until false; do echo x; done | head -3`)
	assert.Less(t, time.Since(start), 2*time.Second, "until pipeline did not terminate after consumer closed")
	assert.Equal(t, "x\nx\nx\n", stdout)
	assert.Equal(t, 0, code)
}

// Infinite-output while loop must respect the runner's stdout cap rather than
// growing memory unbounded. We use a small ctx deadline as the outer bound
// and assert stdout size stays under a generous upper bound that catches an
// unbounded-growth regression.
func TestWhileTrueOutputRespectsStdoutCap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stdout, _, _ := whileRunCtx(ctx, t, `while true; do echo x; done`)
	// 32 MiB is well above any plausible cap, so any value past this points
	// at unbounded buffer growth.
	const generousUpperBound = 1 << 25
	assert.Less(t, len(stdout), generousUpperBound, "stdout grew past the cap; runaway loop?")
}
