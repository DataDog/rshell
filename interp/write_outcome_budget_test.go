// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestRemainingWriteOutcomeBudgetReflectsElapsedTime is a regression test
// for a P2 finding: writeRegularFile previously gave an abandoned write's
// outcome-resolution wait a full, fresh writeOutcomeWaitTimeout every
// time, on top of however long the write attempt itself had already run
// under ctx — so a single write+restore cycle could take up to double the
// documented budget (e.g. sed -i's restoreTimeout-bounded restore call
// spending up to 30s inside Sandbox.WriteRegularFile, then a *fresh* 30s
// wait on top of that). remainingWriteOutcomeBudget must instead report
// whatever is left of a single end-to-end deadline computed once at the
// start of the whole sequence, shrinking as time passes rather than
// always reporting the same fixed duration regardless of how much of the
// budget the write attempt already consumed.
func TestRemainingWriteOutcomeBudgetReflectsElapsedTime(t *testing.T) {
	deadline := time.Now().Add(10 * time.Millisecond)
	first := remainingWriteOutcomeBudget(deadline)
	assert.Greater(t, first, time.Duration(0), "the deadline is still in the future, so some positive budget must remain")
	assert.LessOrEqual(t, first, 10*time.Millisecond, "the reported budget must not exceed what was actually left, unlike a fixed fresh timeout that ignores elapsed time entirely")

	time.Sleep(20 * time.Millisecond)
	second := remainingWriteOutcomeBudget(deadline)
	assert.LessOrEqual(t, second, time.Duration(0), "once the single end-to-end deadline has passed, no budget must remain for the outcome-resolution wait")
}

// TestWriteOutcomeDeadlineClampsToShorterCtxDeadline is a regression test
// for a P2 finding: writeOutcomeDeadline previously always granted the
// full writeOutcomeWaitTimeout (30s) regardless of ctx's own deadline, so
// a run bounded by a much shorter MaxExecutionTime (e.g. 5s) could still
// have an abandoned write's resolution wait run for up to the full,
// unrelated 30s after ctx itself already expired — stretching the run's
// actual duration far past its declared budget purely because of this
// wrapper's own internal wait. The computed deadline must never exceed
// ctx's own deadline when that deadline is sooner than
// writeOutcomeWaitTimeout would otherwise allow.
func TestWriteOutcomeDeadlineClampsToShorterCtxDeadline(t *testing.T) {
	now := time.Now()
	ctxDeadline := now.Add(5 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), ctxDeadline)
	defer cancel()

	got := writeOutcomeDeadline(ctx, now)
	assert.Equal(t, ctxDeadline, got, "ctx's own, sooner deadline must win over the full writeOutcomeWaitTimeout")
}

// TestWriteOutcomeDeadlineUsesTimeoutWhenCtxDeadlineIsLater is the control
// case: when ctx's own deadline is later than writeOutcomeWaitTimeout
// would already allow (or ctx has no deadline at all), the fixed
// writeOutcomeWaitTimeout budget must still apply, unclamped.
func TestWriteOutcomeDeadlineUsesTimeoutWhenCtxDeadlineIsLater(t *testing.T) {
	now := time.Now()

	ctxNoDeadline := context.Background()
	got := writeOutcomeDeadline(ctxNoDeadline, now)
	assert.Equal(t, now.Add(writeOutcomeWaitTimeout), got, "a ctx with no deadline at all must not clamp the fixed budget")

	ctx, cancel := context.WithDeadline(context.Background(), now.Add(time.Hour))
	defer cancel()
	got = writeOutcomeDeadline(ctx, now)
	assert.Equal(t, now.Add(writeOutcomeWaitTimeout), got, "a ctx deadline that is later than writeOutcomeWaitTimeout must not extend the fixed budget beyond it")
}

// TestShouldWaitForWriteOutcomeSkipsExplicitCancellation is a regression
// test for a P2 finding: a plain context.WithCancel-derived ctx has no
// deadline at all for writeOutcomeDeadline's clamp to shrink against, so
// an embedding caller's explicit cancellation (independent of
// Runner.MaxExecutionTime) while a write is abandoned would previously
// still grant the full writeOutcomeWaitTimeout (~30s) wait before
// surfacing the outcome as unknown — defeating the purpose of supporting
// cancellation at all. shouldWaitForWriteOutcome must report false once
// ctx has been explicitly cancelled (context.Canceled), so writeRegularFile
// skips the wait and returns immediately in that case.
func TestShouldWaitForWriteOutcomeSkipsExplicitCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.False(t, shouldWaitForWriteOutcome(ctx), "an explicitly cancelled ctx must skip the abandoned-writer wait entirely")
}

// TestShouldWaitForWriteOutcomeAllowsDeadlineExceeded is the control case:
// a ctx that expired on its own schedule (context.DeadlineExceeded, e.g.
// via Runner.MaxExecutionTime) is a case writeOutcomeDeadline's clamp
// already bounds correctly — the abandoned-writer wait must still be
// attempted in that case, unlike an explicit cancellation.
func TestShouldWaitForWriteOutcomeAllowsDeadlineExceeded(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	time.Sleep(time.Millisecond) // ensure the zero timeout has actually elapsed
	assert.True(t, shouldWaitForWriteOutcome(ctx), "a ctx that expired via its own deadline must still be allowed to attempt the abandoned-writer wait")
}
