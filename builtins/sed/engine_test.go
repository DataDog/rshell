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
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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
// (0-indexed), or nil once errs is exhausted. Every call reports mutated
// as true regardless of its error, matching Sandbox.WriteRegularFile's
// real behavior for the common case tests here exercise (a write that
// starts producing bytes before it fails) — use
// fakeWriteRegularFileWithMutated directly for tests that need to control
// the mutated flag itself (e.g. a write that fails before touching
// anything).
func fakeWriteRegularFile(errs ...error) (fn func(context.Context, string, []byte, fs.FileInfo) (bool, error), calls *[][]byte) {
	fn, calls, _, _ = fakeWriteRegularFileWithCtx(errs...)
	return fn, calls
}

// fakeWriteRegularFileWithCtx is fakeWriteRegularFile plus a record of the
// ctx each call actually received, so tests can assert not just that a call
// happened but that it was (or wasn't) passed a still-live context — needed
// to pin the restore-call-uses-an-uncancelled-context fix in writeBack.
// Every call reports mutated as true; see fakeWriteRegularFileWithMutated
// for control over that flag.
//
// ctxErrsAtCallTime records ctx.Err() evaluated at the moment of the call
// itself, not the ctx value; writeBack's restore call is wrapped in its own
// context.WithTimeout whose deferred cancel fires as soon as writeBack
// returns, so a caller that instead re-checked ctxs[i].Err() after
// writeBack had already returned would always observe the post-return,
// deferred-cancelled state regardless of whether the context was live
// during the call — this records the true at-call-time liveness instead.
func fakeWriteRegularFileWithCtx(errs ...error) (fn func(context.Context, string, []byte, fs.FileInfo) (bool, error), calls *[][]byte, ctxs *[]context.Context, ctxErrsAtCallTime *[]error) {
	return fakeWriteRegularFileWithMutated(true, errs...)
}

// fakeWriteRegularFileWithMutated is fakeWriteRegularFileWithCtx with
// explicit control over the mutated flag every call reports, needed to
// exercise writeBack's "only restore if the primary write actually began
// mutating the file" behavior: a real Sandbox.WriteRegularFile call that
// fails before writing anything (e.g. cancelled before its first chunk, or
// before an empty-output truncate) reports mutated=false alongside its
// error.
func fakeWriteRegularFileWithMutated(mutated bool, errs ...error) (fn func(context.Context, string, []byte, fs.FileInfo) (bool, error), calls *[][]byte, ctxs *[]context.Context, ctxErrsAtCallTime *[]error) {
	var recorded [][]byte
	var recordedCtxs []context.Context
	var recordedCtxErrs []error
	var n int
	fn = func(ctx context.Context, _ string, data []byte, _ fs.FileInfo) (bool, error) {
		// Copy data: callers may reuse/mutate the backing array after the
		// call returns (e.g. writeBack passes originalContent unmodified,
		// but a defensive copy keeps this stub correct regardless).
		cp := append([]byte(nil), data...)
		recorded = append(recorded, cp)
		recordedCtxs = append(recordedCtxs, ctx)
		recordedCtxErrs = append(recordedCtxErrs, ctx.Err())
		var err error
		if n < len(errs) {
			err = errs[n]
		}
		n++
		return mutated, err
	}
	return fn, &recorded, &recordedCtxs, &recordedCtxErrs
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

// TestWriteBackRefusesToStartWriteOnCancelledContext verifies that
// writeBack checks ctx.Err() before starting the primary (destructive)
// write: Sandbox.WriteRegularFile is fully synchronous and can write up to
// MaxInPlaceOutputBytes in one call, so a run that is already cancelled or
// past its deadline must not still begin that write.
func TestWriteBackRefusesToStartWriteOnCancelledContext(t *testing.T) {
	write, calls := fakeWriteRegularFile(nil)
	callCtx := &builtins.CallContext{WriteRegularFile: write}
	eng := &engine{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := eng.writeBack(ctx, callCtx, "file.txt", []byte("new content"), []byte("old content"), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, *calls, "no write should have been attempted once the context was already cancelled")
}

// TestWriteBackStillAttemptsRestoreOnCancelledContext verifies the other
// half of the same fix: once the primary write has actually failed (and
// may have partially mutated the file), the restore attempt must still run
// on a best-effort basis even if the context is cancelled by the time the
// failure is observed — skipping it would leave the file in exactly the
// broken state writeBack exists to prevent.
func TestWriteBackStillAttemptsRestoreOnCancelledContext(t *testing.T) {
	writeErr := errors.New("boom")
	// The context is still live for the primary write (so writeBack's
	// upfront ctx.Err() check passes and the write is attempted), but the
	// primary write itself fails; the restore attempt must still be made
	// regardless of ctx's state at that point.
	write, calls := fakeWriteRegularFile(writeErr, nil)
	callCtx := &builtins.CallContext{WriteRegularFile: write}
	eng := &engine{}

	err := eng.writeBack(context.Background(), callCtx, "file.txt", []byte("new content"), []byte("old content"), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, writeErr)
	require.Len(t, *calls, 2, "the restore attempt must still run after the primary write fails")
	assert.Equal(t, "old content", string((*calls)[1]))
}

// TestWriteBackRestoreUsesUncancelledContext is a regression test for a bug
// introduced alongside the mid-write-cancellation fix: once ctx itself is
// what caused the primary write to fail (e.g. Sandbox.WriteRegularFile's
// own chunked-write loop observed cancellation partway through and
// returned ctx.Err()), the restore call must NOT be passed that same,
// now-done ctx — doing so would make Sandbox.WriteRegularFile's own upfront
// ctx.Err() check reject the restore immediately, defeating the "still
// attempted on a best-effort basis" guarantee entirely. writeBack must pass
// a fresh, uncancelled context to the restore call instead.
func TestWriteBackRestoreUsesUncancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	write, calls, _, ctxErrsAtCallTime := fakeWriteRegularFileWithCtx(nil, nil)
	// Simulate the primary write itself observing cancellation partway
	// through (as Sandbox.WriteRegularFile's chunked write loop now does)
	// by cancelling ctx from inside the fake's first call, then reporting
	// ctx.Err() as the write's own failure — exactly what a real mid-write
	// cancellation looks like from writeBack's point of view.
	origWrite := write
	callCount := 0
	write = func(c context.Context, path string, data []byte, id fs.FileInfo) (bool, error) {
		callCount++
		if callCount == 1 {
			cancel()
			_, _ = origWrite(c, path, data, id) // still record the call/ctx
			// mutated=true: this simulates a real mid-write cancellation,
			// where at least one chunk had already landed before ctx was
			// observed cancelled — distinct from a cancellation caught
			// before the first chunk, which reports mutated=false instead.
			return true, c.Err()
		}
		return origWrite(c, path, data, id)
	}
	callCtx := &builtins.CallContext{WriteRegularFile: write}
	eng := &engine{}

	err := eng.writeBack(ctx, callCtx, "file.txt", []byte("new content"), []byte("old content"), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	require.Len(t, *calls, 2, "the restore attempt must still run even though the primary write failed via context cancellation")
	assert.Equal(t, "old content", string((*calls)[1]))

	// Checked at call time, not after writeBack returned: the restore
	// call's context.WithTimeout is deferred-cancelled as soon as writeBack
	// itself returns, so re-checking ctxs[1].Err() afterwards would always
	// see it as cancelled regardless of whether it was live during the call.
	require.Len(t, *ctxErrsAtCallTime, 2)
	assert.ErrorIs(t, (*ctxErrsAtCallTime)[0], context.Canceled, "the primary write's own ctx is expected to be the cancelled one")
	assert.NoError(t, (*ctxErrsAtCallTime)[1], "the restore call's ctx must NOT be the cancelled one, or Sandbox.WriteRegularFile's own upfront check would reject it immediately")
}

// TestWriteBackRestoreContextIsBounded is a regression test for a P2
// finding: detaching the restore call from ctx via context.Background()
// (see TestWriteBackRestoreUsesUncancelledContext above) is correct on its
// own, but a bare context.Background() has no deadline at all, so a
// restore that stalls (e.g. against a slow or stalled FUSE/network-backed
// AllowedPaths root) could hang indefinitely with nothing to bound it. The
// restore call's context must carry its own deadline (restoreTimeout)
// instead of being fully unbounded.
func TestWriteBackRestoreContextIsBounded(t *testing.T) {
	writeErr := errors.New("no space left on device")
	write, _, ctxs, ctxErrsAtCallTime := fakeWriteRegularFileWithCtx(writeErr, nil)
	callCtx := &builtins.CallContext{WriteRegularFile: write}
	eng := &engine{}

	err := eng.writeBack(context.Background(), callCtx, "file.txt", []byte("new content"), []byte("old content"), nil)
	require.Error(t, err)

	require.Len(t, *ctxs, 2)
	// Checked at call time, not after writeBack returned: the restore call's
	// context.WithTimeout is deferred-cancelled as soon as writeBack itself
	// returns, so re-checking Err() afterwards would always see it as
	// cancelled regardless of whether it was live during the call.
	assert.NoError(t, (*ctxErrsAtCallTime)[1], "the restore call's context must still be live at the moment of the call")
	restoreCtx := (*ctxs)[1]
	_, hasDeadline := restoreCtx.Deadline()
	assert.True(t, hasDeadline, "the restore call's context must carry its own deadline, not be a fully unbounded context.Background()")
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

// TestWriteBackSkipsRestoreWhenPrimaryWriteNeverMutated is a regression
// test for a P2 finding: when the primary write fails without ever
// mutating the file (e.g. Sandbox.WriteRegularFile's own mutated return
// value is false because it was cancelled before its first chunk, or
// before an empty-output truncate), writeBack must not attempt a restore
// at all — there is nothing to restore, the file is untouched, and
// rewriting it anyway would needlessly change file metadata after an
// already-failed/cancelled command and could clobber a legitimate
// concurrent write to the same inode made since the original read.
func TestWriteBackSkipsRestoreWhenPrimaryWriteNeverMutated(t *testing.T) {
	writeErr := context.Canceled
	write, calls, _, _ := fakeWriteRegularFileWithMutated(false, writeErr)
	callCtx := &builtins.CallContext{WriteRegularFile: write}
	eng := &engine{}
	err := eng.writeBack(context.Background(), callCtx, "file.txt", []byte("new content"), []byte("original content"), nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, writeErr)
	assert.NotContains(t, err.Error(), "restored", "no restore should be reported when the primary write never mutated anything")
	require.Len(t, *calls, 1, "only the primary write attempt should have run; no restore call")
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

// openRealFileCallCtx returns a CallContext whose OpenRegularFile opens the
// given real path anew on every call (via os.Open), rather than a single
// in-memory fake. Some readAllBounded tests need a real file rather than a
// pure in-memory fake for other reasons (e.g. exercising Stat against a
// real *os.File), so this helper is kept around for those.
func openRealFileCallCtx(t *testing.T, content string) (*builtins.CallContext, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	return &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return os.Open(path)
		},
	}, path
}

func TestReadAllBoundedWithinLimit(t *testing.T) {
	callCtx, _ := openRealFileCallCtx(t, "hello")
	data, info, closer, err := readAllBounded(context.Background(), callCtx, "file.txt", 10)
	require.NoError(t, err)
	defer closer.Close()
	assert.Equal(t, "hello", string(data))
	require.NotNil(t, info)
}

// TestReadAllChunkedCancellableStopsMidReadOnCancellation is a
// regression test for a P2 finding: readAllBounded's read used to be a
// single, unbounded io.ReadAll(io.LimitReader(...)) call with no ctx
// check during the read itself, so a run whose deadline expired while
// reading a large file from a slow-but-progressing FUSE/NFS mount would
// not notice cancellation until the entire (up to MaxInPlaceOutputBytes)
// read had already completed. readAllChunkedCancellable checks ctx.Err()
// before each readChunkBytes-sized chunk instead. Uses a fake reader that
// cancels ctx itself after serving exactly one chunk, so a pass here can
// only be explained by the per-chunk check actually running, not a
// coincidental early EOF.
func TestReadAllChunkedCancellableStopsMidReadOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	// Total content spans several chunks; the fake cancels ctx right after
	// its first Read call returns, simulating cancellation arriving exactly
	// between two chunk reads.
	content := bytes.Repeat([]byte("a"), readChunkBytes*4)
	reader := &cancelAfterNReads{r: bytes.NewReader(content), cancelAfter: 1, cancel: cancel}

	_, err := readAllChunkedCancellable(ctx, reader, len(content))
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, reader.reads, "the read must have stopped after exactly one chunk once cancellation was observed")
}

// TestReadAllChunkedCancellableReadsWholeContentWhenNotCancelled verifies
// the converse: an uncancelled read reads the full content across
// multiple chunks and returns it intact.
func TestReadAllChunkedCancellableReadsWholeContentWhenNotCancelled(t *testing.T) {
	content := bytes.Repeat([]byte("ab"), readChunkBytes*3)
	data, err := readAllChunkedCancellable(context.Background(), bytes.NewReader(content), len(content))
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

// TestReadAllChunkedCancellableEnforcesSizeLimit verifies the size cap is
// still enforced the same way it was under the single-io.ReadAll design:
// reading maxBytes+1 bytes without erroring, leaving the over-the-limit
// detection to the caller (readAllBounded checks len(data) > maxBytes).
func TestReadAllChunkedCancellableEnforcesSizeLimit(t *testing.T) {
	content := bytes.Repeat([]byte("x"), readChunkBytes*2)
	data, err := readAllChunkedCancellable(context.Background(), bytes.NewReader(content), readChunkBytes-1)
	require.NoError(t, err)
	assert.Len(t, data, readChunkBytes, "must read exactly maxBytes+1 bytes when the source has more, no more and no less")
}

// cancelAfterNReads wraps an io.Reader and calls cancel after its Nth
// Read call returns, simulating cancellation arriving deterministically
// between two specific chunk reads rather than relying on a wall-clock
// race.
type cancelAfterNReads struct {
	r           io.Reader
	reads       int
	cancelAfter int
	cancel      context.CancelFunc
}

func (c *cancelAfterNReads) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.reads++
	if c.reads == c.cancelAfter {
		c.cancel()
	}
	return n, err
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
	callCtx, _ := openRealFileCallCtx(t, "hello")
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
// inode-recycling P2 finding: readAllBounded must return its still-open
// handle to its caller on success, not close it itself, so the caller can
// keep the pinned inode open (preventing it from being recycled by an
// unlink+create at the same path) for as long as the identity check it
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
	assert.False(t, tracked.closed, "readAllBounded must not close the handle itself on success — it is the only handle opened, and the one returned to the caller")
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

// TestReadAllBoundedPinSurvivesReadContextCancellation is the direct
// regression test for the P2 finding: readAllBounded's identity-pin handle
// must remain open even after the ctx passed to readAllBounded itself is
// cancelled, since a real caller (allowedpaths.WithContextClose, as wired
// by callCtx.OpenRegularFile in interp/runner_exec.go) would otherwise
// force-close a handle opened against that ctx as soon as it becomes done
// — exactly the scenario writeBack's restore-on-failure path exists to
// handle, so the pin closing right when it is needed most would defeat the
// whole point of holding it open. This fake reproduces that force-close
// behavior directly (closing the returned handle when its ctx argument's
// Done channel fires) for any call keyed to the caller's ctx, while a call
// made with a different (here, always-live) context is unaffected — mirroring
// how readAllBounded's second, context.Background()-rooted open must behave
// against real WithContextClose-wrapped handles.
func TestReadAllBoundedPinSurvivesReadContextCancellation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello"), 0644))

	ctx, cancel := context.WithCancel(context.Background())

	var openedFile *os.File
	callCtx := &builtins.CallContext{
		OpenRegularFile: func(callerCtx context.Context, _ string) (io.ReadCloser, error) {
			f, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			openedFile = f
			if callerCtx == ctx {
				t.Fatal("readAllBounded must never open with the caller-supplied ctx directly — it must go through openPinBounded's context.Background()-rooted request instead, since ctx becoming done must not force-close the one and only handle this function opens")
			}
			// Simulate allowedpaths.WithContextClose's real force-close-on-
			// Done behavior for whatever context this call actually did
			// receive, so a regression that passed ctx through some other
			// path (not the exact ctx identity check above) would still be
			// caught by the handle actually closing when ctx is cancelled
			// below.
			go func() {
				<-callerCtx.Done()
				f.Close()
			}()
			return f, nil
		},
	}

	_, _, closer, err := readAllBounded(ctx, callCtx, "file.txt", 10)
	require.NoError(t, err)
	require.NotNil(t, openedFile)

	// Cancel the original read context now, after readAllBounded has
	// already returned successfully — simulating cancellation arriving
	// during the later write-back sequence, exactly when the pin is needed.
	cancel()
	// Give the fake's force-close goroutine a moment to run, so a bug that
	// passed ctx through to the open would reliably be observed as closed
	// here rather than the assertion racing a goroutine that hasn't run yet.
	time.Sleep(50 * time.Millisecond)

	_, statErr := openedFile.Stat()
	assert.NoError(t, statErr, "the one and only handle readAllBounded opens must survive cancellation of the caller-supplied ctx — it must not have been opened against ctx")

	require.NoError(t, closer.Close())
}

// TestOpenPinBoundedTimesOutRatherThanHangingForever is a regression test
// for a P2 finding: the identity pin is deliberately opened via
// context.Background() so it survives the caller's ctx being cancelled,
// but an open(2) syscall that itself blocks (e.g. a stalled FUSE/network-
// backed AllowedPaths root) has no open descriptor yet for a
// WithContextClose-style close-on-cancel mechanism to interrupt — so
// without an independent bound, that open could hang indefinitely
// regardless of any deadline. openPinBoundedWithTimeout races the open
// against an explicit timeout instead of leaving it fully unbounded.
func TestOpenPinBoundedTimesOutRatherThanHangingForever(t *testing.T) {
	unblock := make(chan struct{})
	t.Cleanup(func() { close(unblock) }) // let the abandoned goroutine finish so it doesn't leak past the test

	callCtx := &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			<-unblock // simulates an open(2) call stalled on a hung filesystem
			return nopWriteCloser{bytes.NewReader([]byte("hello"))}, nil
		},
	}

	start := time.Now()
	_, err := openPinBoundedWithTimeout(context.Background(), callCtx, "file.txt", 100*time.Millisecond)
	elapsed := time.Since(start)

	require.Error(t, err, "a stalled open must time out rather than block openPinBoundedWithTimeout forever")
	assert.Contains(t, err.Error(), "timed out")
	assert.Less(t, elapsed, 2*time.Second, "openPinBoundedWithTimeout must return promptly once its timeout elapses, not wait for the stalled open")
}

// TestOpenPinBoundedReturnsResultWhenFasterThanTimeout verifies the
// converse: a normal, fast open is unaffected by the bound and returns its
// real result rather than always timing out.
func TestOpenPinBoundedReturnsResultWhenFasterThanTimeout(t *testing.T) {
	callCtx := &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return nopWriteCloser{bytes.NewReader([]byte("hello"))}, nil
		},
	}
	f, err := openPinBoundedWithTimeout(context.Background(), callCtx, "file.txt", 2*time.Second)
	require.NoError(t, err)
	defer f.Close()
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(data))
}

// TestOpenPinBoundedHonorsRunContextDeadline is a regression test for a P2
// finding: the pin acquisition must stop waiting as soon as the run's own
// ctx becomes done, even if that happens before pinOpenTimeout would —
// otherwise sed -i would remain blocked for up to the full fixed timeout
// regardless of a shorter MaxExecutionTime/CLI timeout already having
// expired. Uses a pinOpenTimeout far longer than the test's own ctx
// deadline, so a pass here can only be explained by the run ctx itself
// being honored, not the fixed timeout.
func TestOpenPinBoundedHonorsRunContextDeadline(t *testing.T) {
	unblock := make(chan struct{})
	t.Cleanup(func() { close(unblock) })

	callCtx := &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			<-unblock // simulates an open(2) call stalled on a hung filesystem
			return nopWriteCloser{bytes.NewReader([]byte("hello"))}, nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := openPinBoundedWithTimeout(ctx, callCtx, "file.txt", 30*time.Second)
	elapsed := time.Since(start)

	require.Error(t, err, "the run context's own deadline must stop the wait, not just the (here, far longer) fixed pinOpenTimeout")
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 2*time.Second, "must return promptly once the run context's deadline elapses, not wait out pinOpenTimeout")
}

// TestOpenPinBoundedIndependentOfRunContextAfterAcquisition verifies the
// other half of the same fix: once the pin has been successfully acquired,
// it must remain unaffected by the run context's later cancellation —
// ctx only bounds how long this call itself waits for acquisition to
// complete, not the returned handle's own lifetime.
func TestOpenPinBoundedIndependentOfRunContextAfterAcquisition(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	callCtx := &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return nopWriteCloser{bytes.NewReader([]byte("hello"))}, nil
		},
	}

	f, err := openPinBoundedWithTimeout(ctx, callCtx, "file.txt", 2*time.Second)
	require.NoError(t, err)
	defer f.Close()

	cancel() // must not retroactively affect the already-returned handle

	data, err := io.ReadAll(f)
	require.NoError(t, err, "the successfully acquired handle must remain usable after ctx is cancelled")
	assert.Equal(t, "hello", string(data))
}

// TestOpenPinBoundedCapsOutstandingAbandonedAcquisitions is a regression
// test for a P2 finding: when a target's open(2) call is permanently (not
// just slowly) stuck, its opener goroutine and
// openPinBoundedWithTimeout's own abandon-path cleanup-wait goroutine both
// block forever — neither can ever be reaped. Repeated attempts against
// such a target would otherwise accumulate two goroutines per attempt
// without bound. Fills every pinOpenSlots slot with permanently-stuck
// abandoned opens (already-cancelled ctx + an OpenRegularFile that never
// returns), then verifies one further call fails fast instead of adding
// yet another unreapable goroutine pair.
func TestOpenPinBoundedCapsOutstandingAbandonedAcquisitions(t *testing.T) {
	// Save and restore the package-level slot pool so this test's forced
	// exhaustion does not leak into (or get affected by) other tests
	// sharing the same package-level state.
	orig := pinOpenSlots
	defer func() { pinOpenSlots = orig }()
	pinOpenSlots = make(chan struct{}, 2)

	neverReturns := &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			select {} // deliberately blocks forever, simulating a permanently stuck open(2)
		},
	}

	alreadyCancelled, cancel := context.WithCancel(context.Background())
	cancel()

	// Fill both slots with abandoned, permanently-stuck opens.
	for i := 0; i < 2; i++ {
		_, err := openPinBoundedWithTimeout(alreadyCancelled, neverReturns, "file.txt", 30*time.Second)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	}

	// A third call, even with plenty of time and a real, fast open, must
	// fail fast rather than proceed — there is no free slot left, and the
	// two abandoned goroutines above can never free theirs.
	fastCallCtx := &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return nopWriteCloser{bytes.NewReader([]byte("hello"))}, nil
		},
	}
	_, err := openPinBoundedWithTimeout(context.Background(), fastCallCtx, "file.txt", 2*time.Second)
	require.Error(t, err, "a call made while every slot is held by a permanently-stuck abandoned open must fail fast, not block or silently exceed the cap")
	assert.Contains(t, err.Error(), "too many in-flight identity-pin opens")
}

// TestWatchCloserCloseOnDoneClosesCloserWhenContextDone is a regression
// test for a P2 finding: readAllChunkedCancellable's own ctx.Err() check
// only ever runs *between* Read calls, so it cannot unblock a single Read
// call that is itself stuck (e.g. on a stalled FUSE/NFS mount).
// watchCloserCloseOnDone gives that case a way out, mirroring
// allowedpaths.watchContextCloseOnDone (already used for the same reason
// on the write side) but generalized to io.Closer.
//
// Exercised against a pipe's read end (a real io.ReadCloser whose Read can
// genuinely be made to block, since nothing is writing to the pipe) rather
// than simulating a stalled read, the same style already used for
// allowedpaths.TestWatchContextCloseOnDoneClosesFileWhenContextDone.
func TestWatchCloserCloseOnDoneClosesCloserWhenContextDone(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer w.Close()

	ctx, cancel := context.WithCancel(context.Background())
	stop := watchCloserCloseOnDone(ctx, r)

	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, rerr := r.Read(buf) // blocks: nothing has been written to w
		done <- rerr
	}()

	select {
	case <-done:
		t.Fatal("the pipe read returned before it should have blocked — this test's precondition (nothing written to the pipe) was not met")
	case <-time.After(200 * time.Millisecond):
		// Expected: the read is still blocked at this point.
	}

	cancel()

	select {
	case rerr := <-done:
		assert.Error(t, rerr, "closing the fd out from under a blocked Read must cause it to return an error rather than continuing to block")
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling ctx did not unblock the pending Read — watchCloserCloseOnDone failed to close the closer in time")
	}

	watcherClosed := stop()
	assert.True(t, watcherClosed, "the watcher must report that it (not the caller) closed the closer, since ctx became done before stop was called")
}

// TestWatchCloserCloseOnDoneStopBeforeContextDoneDoesNotClose verifies the
// converse: calling stop before ctx becomes done must leave the closer
// open and report that the watcher did not close it.
func TestWatchCloserCloseOnDoneStopBeforeContextDoneDoesNotClose(t *testing.T) {
	tracked := &trackedCloser{ReadCloser: io.NopCloser(bytes.NewReader([]byte("hello")))}

	ctx := context.Background() // never done
	stop := watchCloserCloseOnDone(ctx, tracked)

	watcherClosed := stop()
	assert.False(t, watcherClosed, "stop called before ctx is done must report that the watcher did not close the closer")
	assert.False(t, tracked.closed, "the closer must remain open after stop is called before ctx becomes done")
}

// TestReadAllBoundedInterruptsBlockedReadOnCancellation is the
// integration-level regression test: an in-flight, genuinely blocked Read
// (not merely a between-chunk check) inside readAllBounded's read must be
// interrupted by ctx cancellation, via the watchCloserCloseOnDone wiring.
// Uses a pipe's read end as the fake identity-pin handle, the same way
// TestWatchCloserCloseOnDoneClosesCloserWhenContextDone does, wrapped to
// also satisfy statCloser.
func TestReadAllBoundedInterruptsBlockedReadOnCancellation(t *testing.T) {
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer w.Close()

	callCtx := &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return pipeStatCloser{r}, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, _, rerr := readAllBounded(ctx, callCtx, "file.txt", 10)
		done <- rerr
	}()

	select {
	case <-done:
		t.Fatal("readAllBounded returned before it should have blocked on the pipe read — this test's precondition was not met")
	case <-time.After(200 * time.Millisecond):
		// Expected: the read is still blocked at this point.
	}

	cancel()

	select {
	case rerr := <-done:
		require.Error(t, rerr, "cancellation must interrupt the blocked read rather than leaving readAllBounded hung indefinitely")
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling ctx did not unblock readAllBounded's pending read")
	}
}

// TestReadAllBoundedInterruptsBlockedStatOnCancellation is a regression
// test for a P2 finding: the identity Stat call — a metadata lookup on
// the same descriptor that can itself block on a stalled FUSE/NFS mount,
// separately from the read that follows it — used to run *before*
// watchCloserCloseOnDone was armed, so a stall specifically during Stat
// (as opposed to during the read) would not have been interruptible.
// watchCloserCloseOnDone must now be armed before Stat, not just before
// the read.
func TestReadAllBoundedInterruptsBlockedStatOnCancellation(t *testing.T) {
	statStarted := make(chan struct{}, 1)
	h := &blockingStatCloser{statStarted: statStarted, closed: make(chan struct{})}

	callCtx := &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return h, nil
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, _, rerr := readAllBounded(ctx, callCtx, "file.txt", 10)
		done <- rerr
	}()

	select {
	case <-statStarted:
		// Expected: Stat has been called and is now blocked inside it.
	case <-time.After(2 * time.Second):
		t.Fatal("Stat was never called — this test's precondition was not met")
	}

	select {
	case <-done:
		t.Fatal("readAllBounded returned before it should have blocked in Stat")
	case <-time.After(200 * time.Millisecond):
		// Expected: still blocked in Stat at this point.
	}

	cancel()

	select {
	case rerr := <-done:
		require.Error(t, rerr, "cancellation must interrupt the blocked Stat rather than leaving readAllBounded hung indefinitely")
	case <-time.After(2 * time.Second):
		t.Fatal("cancelling ctx did not unblock readAllBounded's pending Stat")
	}
}

// blockingStatCloser is a statCloser/io.ReadCloser stub whose Stat call
// blocks until Close is called (simulating a stalled fstat(2) on a hung
// FUSE/NFS mount, unblocked only by watchCloserCloseOnDone force-closing
// it), signalling statStarted once Stat has actually been entered so the
// test can deterministically wait for that point rather than guessing
// with a fixed sleep.
type blockingStatCloser struct {
	statStarted chan struct{}
	closed      chan struct{}
	closeOnce   sync.Once
}

func (h *blockingStatCloser) Stat() (os.FileInfo, error) {
	h.statStarted <- struct{}{}
	<-h.closed
	return nil, errors.New("stat interrupted: handle closed")
}

func (h *blockingStatCloser) Read([]byte) (int, error) { return 0, io.EOF }

func (h *blockingStatCloser) Close() error {
	h.closeOnce.Do(func() { close(h.closed) })
	return nil
}

// pipeStatCloser wraps a pipe's read end (*os.File) so it also satisfies
// statCloser (Stat forwards to the real *os.File), needed since
// readAllBounded requires the handle callCtx.OpenRegularFile returns to
// implement Stat.
type pipeStatCloser struct {
	*os.File
}

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

	// Backed by a real temp file rather than an in-memory fake, matching
	// the shape of a real callCtx.OpenRegularFile implementation.
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	require.NoError(t, os.WriteFile(path, []byte(original), 0644))

	writeErr := errors.New("simulated write failure")
	write, calls := fakeWriteRegularFile(writeErr, nil)
	callCtx := &builtins.CallContext{
		OpenRegularFile: func(_ context.Context, _ string) (io.ReadCloser, error) {
			return os.Open(path)
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
