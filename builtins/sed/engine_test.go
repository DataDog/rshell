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

func TestResetForNewFileClearsHoldSpaceButPreservesLastRe(t *testing.T) {
	// Confirmed against real GNU sed 4.9 (sed/execute.c's
	// read_pattern_space clears hold.length in the same reset_at_next_file
	// branch that resets line numbers and address ranges):
	// `sed -s '/keepme/h; $G' a.txt b.txt` does NOT carry a.txt's hold-space
	// value into b.txt, but `sed -s '/foo/ s//bar/' a.txt b.txt` (each file
	// containing just "foo") does still reuse a.txt's last regex in b.txt.
	re := regexp.MustCompile("x")
	eng := &engine{
		holdSpace: "stale from a previous file",
		lastRe:    re,
	}
	eng.resetForNewFile()

	assert.Equal(t, "", eng.holdSpace, "hold space must reset per file, matching GNU sed -s/-i")
	assert.Same(t, re, eng.lastRe, "the last-used regex must persist across files")
}

// --- writeBack (backing -i's write-back safety) ---

// fakeWriteRegularFile builds a callCtx.WriteRegularFile stub that records
// every call's (path, data) pair and returns errs[call] for the Nth call
// (0-indexed), or nil once errs is exhausted.
func fakeWriteRegularFile(errs ...error) (fn func(context.Context, string, []byte, fs.FileInfo) error, calls *[][]byte) {
	var recorded [][]byte
	var n int
	fn = func(_ context.Context, _ string, data []byte, _ fs.FileInfo) error {
		// Copy data: callers may reuse/mutate the backing array after the
		// call returns (e.g. writeBack passes originalContent unmodified,
		// but a defensive copy keeps this stub correct regardless).
		cp := append([]byte(nil), data...)
		recorded = append(recorded, cp)
		var err error
		if n < len(errs) {
			err = errs[n]
		}
		n++
		return err
	}
	return fn, &recorded
}

func TestWriteBackSucceeds(t *testing.T) {
	write, calls := fakeWriteRegularFile(nil)
	callCtx := &builtins.CallContext{WriteRegularFile: write}
	eng := &engine{}
	err := eng.writeBack(context.Background(), callCtx, "file.txt", []byte("new content"), []byte("old content"), nil)
	require.NoError(t, err)
	require.Len(t, *calls, 1, "only the primary write should have been attempted")
	assert.Equal(t, "new content", string((*calls)[0]))
}

// TestWriteBackRestoresOriginalOnWriteFailure exercises the P1 fix directly:
// when the destructive write fails (simulating e.g. ENOSPC), the original
// content must be written back to the same path rather than the file being
// left in a rewritten-but-broken state.
func TestWriteBackRestoresOriginalOnWriteFailure(t *testing.T) {
	writeErr := errors.New("no space left on device")
	write, calls := fakeWriteRegularFile(writeErr, nil)
	callCtx := &builtins.CallContext{WriteRegularFile: write}
	eng := &engine{}
	err := eng.writeBack(context.Background(), callCtx, "file.txt", []byte("new content"), []byte("original content"), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, writeErr)
	assert.Contains(t, err.Error(), "restored")
	require.Len(t, *calls, 2, "expected one destructive write attempt and one restore attempt")
	assert.Equal(t, "new content", string((*calls)[0]))
	assert.Equal(t, "original content", string((*calls)[1]),
		"the restore call must write back the pre-image, not the partially-written new content")
}

// TestWriteBackReportsBothErrorsWhenRestoreAlsoFails verifies that a failed
// restore attempt is not silently swallowed: both the original write error
// and the restore error must be surfaced, since at that point the caller
// cannot infer the file's on-disk state from either error alone.
func TestWriteBackReportsBothErrorsWhenRestoreAlsoFails(t *testing.T) {
	writeErr := errors.New("no space left on device")
	restoreErr := errors.New("still no space left on device")
	write, _ := fakeWriteRegularFile(writeErr, restoreErr)
	callCtx := &builtins.CallContext{WriteRegularFile: write}
	eng := &engine{}
	err := eng.writeBack(context.Background(), callCtx, "file.txt", []byte("new content"), []byte("original content"), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, writeErr)
	assert.ErrorIs(t, err, restoreErr)
}

// TestWriteBackRejectsNonRegularTarget verifies that a non-regular-file
// rejection from the underlying WriteRegularFile call (e.g. a FIFO swapped
// in for the target) surfaces through writeBack, and that writeBack then
// attempts the restore call in response — exercising the same path a real
// FIFO-swap rejection from Sandbox.WriteRegularFile would take.
func TestWriteBackRejectsNonRegularTarget(t *testing.T) {
	notRegularErr := errors.New("not a regular file")
	write, calls := fakeWriteRegularFile(notRegularErr, nil)
	callCtx := &builtins.CallContext{WriteRegularFile: write}
	eng := &engine{}
	err := eng.writeBack(context.Background(), callCtx, "pipe", []byte("new"), []byte("old"), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a regular file")
	require.Len(t, *calls, 2)
}

// --- readAllBounded ---

func TestReadAllBoundedWithinLimit(t *testing.T) {
	callCtx := &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return nopWriteCloser{bytes.NewReader([]byte("hello"))}, nil
		},
	}
	data, info, closer, err := readAllBounded(context.Background(), callCtx, "file.txt", 10)
	require.NoError(t, err)
	defer closer.Close()
	assert.Equal(t, "hello", string(data))
	require.NotNil(t, info)
}

func TestReadAllBoundedExceedsLimit(t *testing.T) {
	callCtx := &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return nopWriteCloser{bytes.NewReader([]byte("hello world"))}, nil
		},
	}
	_, _, _, err := readAllBounded(context.Background(), callCtx, "file.txt", 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too large")
}

func TestReadAllBoundedExactlyAtLimit(t *testing.T) {
	callCtx := &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return nopWriteCloser{bytes.NewReader([]byte("hello"))}, nil
		},
	}
	data, info, closer, err := readAllBounded(context.Background(), callCtx, "file.txt", 5)
	require.NoError(t, err)
	defer closer.Close()
	assert.Equal(t, "hello", string(data))
	require.NotNil(t, info)
}

func TestReadAllBoundedPropagatesOpenError(t *testing.T) {
	openErr := errors.New("boom")
	callCtx := &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return nil, openErr
		},
	}
	_, _, _, err := readAllBounded(context.Background(), callCtx, "file.txt", 5)
	require.Error(t, err)
	assert.Same(t, openErr, err)
}

// TestReadAllBoundedKeepsHandleOpenOnSuccess is a regression test for the
// inode-recycling P2 finding: readAllBounded must return the still-open
// handle to its caller on success, not close it itself, so the caller can
// keep the original inode pinned open (preventing it from being recycled by
// an unlink+create at the same path) for as long as the identity check it
// backs needs to remain trustworthy.
func TestReadAllBoundedKeepsHandleOpenOnSuccess(t *testing.T) {
	tracked := &trackedCloser{ReadCloser: io.NopCloser(bytes.NewReader([]byte("hello")))}
	callCtx := &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return trackedStatCloser{trackedCloser: tracked}, nil
		},
	}
	_, _, closer, err := readAllBounded(context.Background(), callCtx, "file.txt", 10)
	require.NoError(t, err)
	assert.False(t, tracked.closed, "readAllBounded must not close the handle itself on success")
	require.NoError(t, closer.Close())
	assert.True(t, tracked.closed, "the caller's Close must reach the real underlying handle")
}

// TestReadAllBoundedClosesHandleOnLaterFailure verifies the converse: when
// readAllBounded itself fails partway (e.g. the size-limit check), it must
// close the handle before returning, since no closer is returned to the
// caller in that case.
func TestReadAllBoundedClosesHandleOnLaterFailure(t *testing.T) {
	tracked := &trackedCloser{ReadCloser: io.NopCloser(bytes.NewReader([]byte("hello world")))}
	callCtx := &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return trackedStatCloser{trackedCloser: tracked}, nil
		},
	}
	_, _, _, err := readAllBounded(context.Background(), callCtx, "file.txt", 5)
	require.Error(t, err)
	assert.True(t, tracked.closed, "a failed readAllBounded must close the handle since no closer is returned")
}

type trackedCloser struct {
	io.ReadCloser
	closed bool
}

func (t *trackedCloser) Close() error {
	t.closed = true
	return t.ReadCloser.Close()
}

type trackedStatCloser struct {
	*trackedCloser
}

func (trackedStatCloser) Stat() (os.FileInfo, error) { return fakeFileInfo{mode: 0644}, nil }

func TestReadAllBoundedPropagatesStatError(t *testing.T) {
	// A source whose OpenRegularFile succeeds but whose result does not
	// implement statCloser (no Stat method) must be rejected, since
	// readAllBounded cannot pin an identity for the later write-back's
	// expectedIdentity check without one.
	callCtx := &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return statlessReadWriteCloser{bytes.NewReader([]byte("hello"))}, nil
		},
	}
	_, _, _, err := readAllBounded(context.Background(), callCtx, "file.txt", 5)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot verify file identity")
}

// statlessReadWriteCloser deliberately does not implement Stat, unlike
// nopWriteCloser, to exercise readAllBounded's statCloser assertion failure
// path.
type statlessReadWriteCloser struct {
	io.Reader
}

func (statlessReadWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (statlessReadWriteCloser) Close() error                { return nil }

// TestProcessFileInPlaceCapturesCompleteOriginalOnEarlyQuit is a regression
// test for the original P1 finding: an early q/Q must not truncate the
// captured backup to only the bytes consumed before quitting. It builds an
// original file large enough (multiple MiB) that it could never fit in a
// bufio.Scanner's read-ahead buffer, quits on the very first line, forces
// the destructive write to fail so writeBack attempts a restore, and
// asserts the restore call receives the complete original content byte for
// byte — not a short prefix.
func TestProcessFileInPlaceCapturesCompleteOriginalOnEarlyQuit(t *testing.T) {
	// One short first line (so "1q" quits immediately) followed by a large
	// amount of trailing data the scanner would never have reached.
	const trailingLines = 100_000
	var sb strings.Builder
	sb.WriteString("first\n")
	for i := 0; i < trailingLines; i++ {
		sb.WriteString("the rest of the file that must still be captured for restore\n")
	}
	original := sb.String()
	require.Greater(t, len(original), 4<<20, "fixture must be large enough to exceed a scanner read-ahead buffer")

	prog, err := parseScript("1q", false)
	require.NoError(t, err)
	eng := &engine{prog: prog, labelMap: buildLabelMap(prog)}

	writeErr := errors.New("simulated write failure")
	write, calls := fakeWriteRegularFile(writeErr, nil)
	callCtx := &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return nopWriteCloser{strings.NewReader(original)}, nil
		},
		WriteRegularFile: write,
	}
	eng.callCtx = callCtx

	err = eng.processFileInPlace(context.Background(), callCtx, "big.txt")
	require.Error(t, err, "the forced write failure must surface, restored or not")

	require.Len(t, *calls, 2, "expected one destructive write attempt and one restore attempt")
	assert.Equal(t, "first\n", string((*calls)[0]), "1q's committed output is just the first line")
	assert.Equal(t, original, string((*calls)[1]),
		"the restore call must receive the complete original file, not a scanner-read-ahead-sized prefix")
}

// nopWriteCloser adapts an io.Reader to io.ReadWriteCloser for tests that
// only exercise the read side of callCtx.OpenFile. Stat returns a
// fakeFileInfo stub so readAllBounded's statCloser type-assertion succeeds
// (it needs an identity to pin for the write-back's expectedIdentity check).
type nopWriteCloser struct {
	io.Reader
}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }
func (nopWriteCloser) Stat() (os.FileInfo, error)  { return fakeFileInfo{mode: 0644}, nil }

// fakeFileInfo is a minimal os.FileInfo stub for tests that need a stand-in
// identity/mode without touching a real filesystem.
type fakeFileInfo struct {
	mode fs.FileMode
}

func (f fakeFileInfo) Name() string       { return "stub" }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeFileInfo) Sys() any           { return nil }

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
