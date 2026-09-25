// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build unix

package interp

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestElevatedRedirectFIFOTargetDoesNotHang is a regression test for a
// finding on the elevated-redirect open path: rejectNonRegularRedirectTarget
// runs its Stat unprivileged, so against a root-only FIFO it fails closed
// with "permission denied" — a case the function deliberately treats as
// inconclusive and defers to Open. Before this fix, Open then ran elevated
// on its own, so it could reach and open(2) a root-owned FIFO with no
// reader, which blocks indefinitely (the sandbox's write-open path issues a
// plain blocking open, with no O_NONBLOCK), ignoring context cancellation —
// a real hang, not merely a wrong result.
//
// The fix moves the type-check into the same elevate() window as the open,
// so whichever privilege level actually performs the open also performs the
// check first and rejects the FIFO before ever calling open(2) on it. This
// test proves there is no hang: it bounds the run with a short timeout and
// requires it to fail fast with "not a regular file", not time out.
func TestElevatedRedirectFIFOTargetDoesNotHang(t *testing.T) {
	dir := t.TempDir()
	restrictedDir := filepath.Join(dir, "restricted")
	require.NoError(t, os.Mkdir(restrictedDir, 0000))
	t.Cleanup(func() { os.Chmod(restrictedDir, 0755) }) //nolint:errcheck
	fifoPath := filepath.Join(restrictedDir, "pipe")
	require.NoError(t, os.Chmod(restrictedDir, 0755))
	require.NoError(t, syscall.Mkfifo(fifoPath, 0600))
	require.NoError(t, os.Chmod(restrictedDir, 0000))
	// No goroutine ever opens the read end of the FIFO, so a real open(2)
	// attempt (without O_NONBLOCK) would block forever.

	var elevateCalls int
	var stderr bytes.Buffer
	runner, err := New(
		StdIO(nil, os.Stdout, &stderr),
		WithMode(ModeRemediation),
		AllowedPaths([]string{dir + ":rw"}),
		AllowedCommands([]string{"rshell:echo"}),
		SelectiveElevation([]string{"rshell:echo"}, func(_ context.Context, _ string, run func()) error {
			elevateCalls++
			require.NoError(t, os.Chmod(restrictedDir, 0755))
			defer os.Chmod(restrictedDir, 0000) //nolint:errcheck
			run()
			return nil
		}),
	)
	require.NoError(t, err)
	defer runner.Close()

	program, err := ParseScript("sudo echo hi > "+fifoPath, "")
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx, program) }()

	select {
	case runErr := <-done:
		require.Error(t, runErr, "opening a FIFO as a write-target redirect must fail, not succeed")
		require.Contains(t, stderr.String(), "not a regular file")
		require.Equal(t, 1, elevateCalls)
	case <-time.After(4 * time.Second):
		t.Fatal("HANG: elevated open on a root-only FIFO with no reader did not return within the timeout")
	}
}
