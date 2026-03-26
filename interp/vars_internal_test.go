// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mvdan.cc/sh/v3/expand"
)

// TestOverlayEnvironUnsetFreesStorage verifies that setting a variable to the
// unset state in overlayEnviron correctly subtracts its bytes from totalBytes,
// freeing space for subsequent assignments.
func TestOverlayEnvironUnsetFreesStorage(t *testing.T) {
	o := newOverlayEnviron(expand.ListEnviron(), false)

	// Fill to near the cap with a single large variable.
	large := make([]byte, MaxTotalVarsBytes-100)
	for i := range large {
		large[i] = 'x'
	}
	err := o.Set("A", expand.Variable{Set: true, Kind: expand.String, Str: string(large)})
	require.NoError(t, err, "initial large assignment must succeed")
	assert.Equal(t, len(large), o.totalBytes, "totalBytes must equal the assigned size")

	// Now unset A — this should subtract its bytes from totalBytes.
	err = o.Set("A", expand.Variable{})
	require.NoError(t, err, "unsetting A must not return an error")
	assert.Equal(t, 0, o.totalBytes, "totalBytes must drop to 0 after unsetting the only variable")

	// A subsequent large assignment must succeed because the space was freed.
	err = o.Set("B", expand.Variable{Set: true, Kind: expand.String, Str: string(large)})
	assert.NoError(t, err, "assignment after unset must succeed when freed space is within the cap")
}

// TestSetUncappedNoTombstone verifies that setUncapped deletes the map entry
// when the variable being restored is unset, rather than leaving a zero-value
// tombstone in the overlay.  Tombstones allow unbounded map growth for inline
// command vars (FOO=bar cmd) where FOO was previously unset.
func TestSetUncappedNoTombstone(t *testing.T) {
	o := newOverlayEnviron(expand.ListEnviron(), false)

	// Simulate the restore path: setUncapped called with an unset variable for a name
	// that was never in the overlay (i.e. FOO was unset before the inline assignment).
	o.setUncapped("FOO", expand.Variable{})
	_, inOverlay := o.values["FOO"]
	assert.False(t, inOverlay, "setUncapped with unset variable must not leave a tombstone entry in values")
}

// blockingReader is an io.Reader that blocks until the provided channel is
// closed, then returns io.EOF.  Used to simulate a slow reader for testing the
// stdinFile context-cancel path without relying on real file descriptors.
type blockingReader struct {
	unblock <-chan struct{}
}

func (b *blockingReader) Read(p []byte) (int, error) {
	<-b.unblock
	return 0, io.EOF
}

// TestStdinFileContextCancelExitsGoroutine verifies that the copy goroutine
// started by stdinFile exits promptly when its context is cancelled, rather
// than blocking indefinitely on the underlying reader.
//
// The test uses a blockingReader whose Read blocks until explicitly unblocked.
// After cancelling the context, the test drains the pipe read end to verify
// the goroutine closed the write end (pipe read returns io.EOF), which can
// only happen after the goroutine exits.
func TestStdinFileContextCancelExitsGoroutine(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	unblock := make(chan struct{})
	r := &blockingReader{unblock: unblock}

	pr, err := stdinFile(ctx, r)
	require.NoError(t, err, "stdinFile must not fail to create the pipe")
	defer pr.Close()
	// Ensure the blockingReader is unblocked when the test exits so that any
	// goroutine still waiting on Read can exit cleanly.
	defer close(unblock)

	// Cancel the context.  The goroutine checks ctx.Err() at the top of its
	// loop; after cancellation the next Read call will return and the loop
	// exits, closing the pipe write end.
	cancel()

	// Read from the pipe with a short deadline.  If the goroutine exits
	// correctly after context cancellation, the write end is closed and Read
	// returns io.EOF.  If the goroutine does not exit, the read blocks until
	// the test deadline.
	done := make(chan error, 1)
	go func() {
		buf := make([]byte, 1)
		_, readErr := pr.Read(buf)
		done <- readErr
	}()

	select {
	case readErr := <-done:
		// Any error (including io.EOF) means the write end was closed — the
		// goroutine exited as expected.
		assert.Error(t, readErr, "pipe read must return an error once the goroutine closes the write end")
	case <-time.After(2 * time.Second):
		t.Fatal("stdinFile goroutine did not exit within 2s after context cancellation")
	}
}
