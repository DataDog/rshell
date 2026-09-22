// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build unix

package sed_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/DataDog/rshell/interp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInPlaceRejectsFIFOTarget verifies that sed -i refuses a FIFO target at
// the write-back stage instead of blocking indefinitely trying to open it
// for writing.
//
// The FIFO is given a writer that writes one line and then closes, so the
// *read* side reaches EOF quickly (matching a real, if unusual, use of a
// FIFO as sed's input) and processing proceeds to the write-back step.
// Before checkRegularFile was added, that write-back would reopen the same
// path O_WRONLY|O_TRUNC, which blocks indefinitely on a FIFO with no reader
// attached — and there is no longer a reader once the read side above has
// consumed the writer's output and the writer has exited. There is also no
// way for context cancellation to unblock that open, since it happens
// beneath WithContextClose. checkRegularFile now rejects the FIFO via a
// non-blocking Stat before that write-open is ever attempted, so this test
// fails fast rather than hanging.
func TestInPlaceRejectsFIFOTarget(t *testing.T) {
	dir := t.TempDir()
	fifoPath := filepath.Join(dir, "pipe")
	require.NoError(t, syscall.Mkfifo(fifoPath, 0600))

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		w, err := os.OpenFile(fifoPath, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		_, _ = w.WriteString("a\n")
		_ = w.Close()
	}()
	t.Cleanup(func() { <-writerDone })

	_, stderr, code := runScript(t, `sed -i 's/a/b/' pipe`, dir,
		interp.AllowedPaths([]string{dir + ":rw"}),
		interp.WithMode(interp.ModeRemediation),
	)
	assert.Equal(t, 1, code, "sed -i on a FIFO must fail at write-back, not hang")
	assert.Contains(t, stderr, "not a regular file")

	// The FIFO itself must still exist and be unharmed — sed -i must not
	// have attempted to remove or replace it.
	info, err := os.Lstat(fifoPath)
	require.NoError(t, err)
	assert.True(t, info.Mode()&os.ModeNamedPipe != 0, "pipe must still be a FIFO")
}
