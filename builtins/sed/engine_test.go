// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package sed

import (
	"bufio"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLineReaderCheckLimitNonRegularFile(t *testing.T) {
	// Create a lineReader for a non-regular-file source with totalRead
	// exceeding the limit, and verify checkLimit returns an error.
	sc := bufio.NewScanner(strings.NewReader(""))
	lr := &lineReader{sc: sc, isRegularFile: false}

	// Below the limit — no error.
	lr.totalRead = MaxTotalReadBytes - 1
	require.NoError(t, lr.checkLimit())

	// Exactly at the limit — no error (check is strictly greater-than).
	lr.totalRead = MaxTotalReadBytes
	require.NoError(t, lr.checkLimit())

	// Above the limit — error.
	lr.totalRead = MaxTotalReadBytes + 1
	err := lr.checkLimit()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "input too large")
}

func TestLineReaderCheckLimitRegularFile(t *testing.T) {
	// Regular files are not subject to the read limit.
	sc := bufio.NewScanner(strings.NewReader(""))
	lr := &lineReader{sc: sc, isRegularFile: true}
	lr.totalRead = MaxTotalReadBytes + 1
	require.NoError(t, lr.checkLimit())
}

func TestLineReaderTotalReadAccumulation(t *testing.T) {
	// Verify that totalRead accumulates across multiple readLine calls.
	input := "line1\nline2\nline3\n"
	sc := bufio.NewScanner(strings.NewReader(input))
	lr := newLineReader(sc, false)

	var totalLines int
	for {
		_, ok := lr.readLine()
		if !ok {
			break
		}
		totalLines++
	}
	assert.Equal(t, 3, totalLines)
	// totalRead should be > 0 (exact value depends on scanner behavior).
	assert.Greater(t, lr.totalRead, int64(0))
}

// --- boundedBuffer (backs -i's in-memory rewrite buffer) ---

func TestBoundedBufferWithinLimit(t *testing.T) {
	b := &boundedBuffer{maxBytes: 10}
	n, err := b.Write([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.False(t, b.overflow)
	assert.Equal(t, "hello", b.buf.String())
}

func TestBoundedBufferExceedsLimit(t *testing.T) {
	b := &boundedBuffer{maxBytes: 4}
	n, err := b.Write([]byte("hello")) // 5 bytes > 4-byte limit
	require.NoError(t, err)
	// Write must report the full length was "consumed" (io.Writer contract:
	// a short count without an error would make callers like fmt.Fprintf
	// treat it as io.ErrShortWrite) even though the bytes were discarded.
	assert.Equal(t, 5, n)
	assert.True(t, b.overflow)
	assert.Empty(t, b.buf.String(), "overflowing write must not be partially buffered")
}

func TestBoundedBufferStopsAccumulatingAfterOverflow(t *testing.T) {
	b := &boundedBuffer{maxBytes: 4}
	_, err := b.Write([]byte("hello"))
	require.NoError(t, err)
	require.True(t, b.overflow)

	// A subsequent write must also be silently discarded rather than
	// growing the buffer further, so overflow bounds memory even under
	// many small writes after the first one that tripped the limit.
	n, err := b.Write([]byte("more"))
	require.NoError(t, err)
	assert.Equal(t, 4, n)
	assert.Empty(t, b.buf.String())
}

func TestBoundedBufferExactlyAtLimit(t *testing.T) {
	b := &boundedBuffer{maxBytes: 5}
	n, err := b.Write([]byte("hello")) // exactly 5 bytes
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	assert.False(t, b.overflow)
	assert.Equal(t, "hello", b.buf.String())
}

// --- resetForNewFile / resetRangeState ---

func TestResetForNewFileClearsLineState(t *testing.T) {
	eng := &engine{
		lineNum:      42,
		lastLine:     true,
		patternSpace: "leftover",
		subMade:      true,
		appendQueue:  []string{"queued"},
	}
	eng.appendQueueBytes = len("queued")
	eng.resetForNewFile()

	assert.Equal(t, int64(0), eng.lineNum)
	assert.False(t, eng.lastLine)
	assert.Equal(t, "", eng.patternSpace)
	assert.False(t, eng.subMade)
	assert.Empty(t, eng.appendQueue)
	assert.Equal(t, 0, eng.appendQueueBytes)
}

func TestResetForNewFilePreservesHoldSpaceAndLastRe(t *testing.T) {
	// GNU sed's -s/--separate resets line numbers and $ per file but does
	// not clear the hold space or the s///-reuse regex across files.
	re := regexp.MustCompile("x")
	eng := &engine{
		holdSpace: "kept",
		lastRe:    re,
	}
	eng.resetForNewFile()

	assert.Equal(t, "kept", eng.holdSpace)
	assert.Same(t, re, eng.lastRe)
}

func TestResetRangeStateClearsNestedGroups(t *testing.T) {
	inner := &sedCmd{kind: cmdPrint, inRange: true}
	group := &sedCmd{kind: cmdGroup, inRange: true, children: []*sedCmd{inner}}
	top := &sedCmd{kind: cmdDelete, inRange: true}
	prog := []*sedCmd{top, group}

	resetRangeState(prog)

	assert.False(t, top.inRange)
	assert.False(t, group.inRange)
	assert.False(t, inner.inRange, "inRange must be cleared inside nested groups too")
}
