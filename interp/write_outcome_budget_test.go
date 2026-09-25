// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
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
