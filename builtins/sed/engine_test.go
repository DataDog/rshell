// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package sed

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/rshell/builtins"
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

// --- checkRegularFile / writeBack (backing -i's write-back safety) ---

// fakeFileInfo is a minimal fs.FileInfo stub for exercising checkRegularFile
// without touching a real filesystem.
type fakeFileInfo struct {
	mode fs.FileMode
}

func (f fakeFileInfo) Name() string       { return "stub" }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeFileInfo) Sys() any           { return nil }

// fakeWriteCloser records every byte written to it (up to failAfter, if
// non-negative) and can be made to fail the Write or the Close call, so
// tests can simulate a mid-write failure (e.g. ENOSPC) deterministically.
type fakeWriteCloser struct {
	written   bytes.Buffer
	failAfter int // -1 means never fail
	writeErr  error
	closeErr  error
}

func (f *fakeWriteCloser) Read(_ []byte) (int, error) { return 0, io.EOF }

func (f *fakeWriteCloser) Write(p []byte) (int, error) {
	if f.failAfter >= 0 && f.written.Len()+len(p) > f.failAfter {
		allowed := f.failAfter - f.written.Len()
		if allowed > 0 {
			f.written.Write(p[:allowed])
		}
		return allowed, f.writeErr
	}
	return f.written.Write(p)
}

func (f *fakeWriteCloser) Close() error { return f.closeErr }

func TestCheckRegularFileAcceptsRegularFile(t *testing.T) {
	callCtx := &builtins.CallContext{
		StatFile: func(_ context.Context, _ string) (fs.FileInfo, error) {
			return fakeFileInfo{mode: 0644}, nil
		},
	}
	require.NoError(t, checkRegularFile(context.Background(), callCtx, "file.txt"))
}

func TestCheckRegularFileRejectsFIFO(t *testing.T) {
	callCtx := &builtins.CallContext{
		StatFile: func(_ context.Context, _ string) (fs.FileInfo, error) {
			return fakeFileInfo{mode: fs.ModeNamedPipe}, nil
		},
	}
	err := checkRegularFile(context.Background(), callCtx, "pipe")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
}

func TestCheckRegularFileRejectsDirectory(t *testing.T) {
	callCtx := &builtins.CallContext{
		StatFile: func(_ context.Context, _ string) (fs.FileInfo, error) {
			return fakeFileInfo{mode: fs.ModeDir}, nil
		},
	}
	err := checkRegularFile(context.Background(), callCtx, "dir")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
}

func TestCheckRegularFilePropagatesStatError(t *testing.T) {
	statErr := errors.New("boom")
	callCtx := &builtins.CallContext{
		StatFile: func(_ context.Context, _ string) (fs.FileInfo, error) {
			return nil, statErr
		},
	}
	err := checkRegularFile(context.Background(), callCtx, "file.txt")
	require.Error(t, err)
	assert.Same(t, statErr, err)
}

func TestWriteBackSucceeds(t *testing.T) {
	dest := &fakeWriteCloser{failAfter: -1}
	callCtx := &builtins.CallContext{
		StatFile: func(_ context.Context, _ string) (fs.FileInfo, error) {
			return fakeFileInfo{mode: 0644}, nil
		},
		OpenFile: func(_ context.Context, _ string, _ int, _ os.FileMode) (io.ReadWriteCloser, error) {
			return dest, nil
		},
	}
	eng := &engine{}
	err := eng.writeBack(context.Background(), callCtx, "file.txt", []byte("new content"), []byte("old content"))
	require.NoError(t, err)
	assert.Equal(t, "new content", dest.written.String())
}

// TestWriteBackRestoresOriginalOnWriteFailure exercises the P1 fix directly:
// when the destructive write fails partway (simulating e.g. ENOSPC), the
// original content must be written back to the same path rather than the
// file being left empty or partially rewritten.
func TestWriteBackRestoresOriginalOnWriteFailure(t *testing.T) {
	writeErr := errors.New("no space left on device")
	firstWrite := &fakeWriteCloser{failAfter: 3, writeErr: writeErr} // fails partway through "new content"
	restoreWrite := &fakeWriteCloser{failAfter: -1}

	var openCalls int
	callCtx := &builtins.CallContext{
		StatFile: func(_ context.Context, _ string) (fs.FileInfo, error) {
			return fakeFileInfo{mode: 0644}, nil
		},
		OpenFile: func(_ context.Context, _ string, _ int, _ os.FileMode) (io.ReadWriteCloser, error) {
			openCalls++
			if openCalls == 1 {
				return firstWrite, nil
			}
			return restoreWrite, nil
		},
	}
	eng := &engine{}
	err := eng.writeBack(context.Background(), callCtx, "file.txt", []byte("new content"), []byte("original content"))
	require.Error(t, err)
	assert.ErrorIs(t, err, writeErr)
	assert.Contains(t, err.Error(), "restored")
	assert.Equal(t, 2, openCalls, "expected one destructive open and one restore open")
	assert.Equal(t, "original content", restoreWrite.written.String(),
		"the restore write must write back the pre-image, not the partially-written new content")
}

// TestWriteBackReportsBothErrorsWhenRestoreAlsoFails verifies that a failed
// restore attempt is not silently swallowed: both the original write error
// and the restore error must be surfaced, since at that point the caller
// cannot infer the file's on-disk state from either error alone.
func TestWriteBackReportsBothErrorsWhenRestoreAlsoFails(t *testing.T) {
	writeErr := errors.New("no space left on device")
	restoreErr := errors.New("still no space left on device")
	firstWrite := &fakeWriteCloser{failAfter: 0, writeErr: writeErr}
	restoreWrite := &fakeWriteCloser{failAfter: 0, writeErr: restoreErr}

	var openCalls int
	callCtx := &builtins.CallContext{
		StatFile: func(_ context.Context, _ string) (fs.FileInfo, error) {
			return fakeFileInfo{mode: 0644}, nil
		},
		OpenFile: func(_ context.Context, _ string, _ int, _ os.FileMode) (io.ReadWriteCloser, error) {
			openCalls++
			if openCalls == 1 {
				return firstWrite, nil
			}
			return restoreWrite, nil
		},
	}
	eng := &engine{}
	err := eng.writeBack(context.Background(), callCtx, "file.txt", []byte("new content"), []byte("original content"))
	require.Error(t, err)
	assert.ErrorIs(t, err, writeErr)
	assert.ErrorIs(t, err, restoreErr)
}

func TestWriteBackRejectsNonRegularTarget(t *testing.T) {
	var openCalled bool
	callCtx := &builtins.CallContext{
		StatFile: func(_ context.Context, _ string) (fs.FileInfo, error) {
			return fakeFileInfo{mode: fs.ModeNamedPipe}, nil
		},
		OpenFile: func(_ context.Context, _ string, _ int, _ os.FileMode) (io.ReadWriteCloser, error) {
			openCalled = true
			return &fakeWriteCloser{failAfter: -1}, nil
		},
	}
	eng := &engine{}
	err := eng.writeBack(context.Background(), callCtx, "pipe", []byte("new"), []byte("old"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
	assert.False(t, openCalled, "OpenFile must not be reached for a non-regular target")
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
