// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package allowedpaths

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAccessFIFODoesNotBlock verifies that Access on a FIFO (named pipe) with
// no writer returns immediately instead of blocking. Before the fix, Access
// used os.Root.Open which blocks on FIFOs until a writer appears.
func TestAccessFIFODoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	fifoPath := filepath.Join(dir, "pipe")
	require.NoError(t, syscall.Mkfifo(fifoPath, 0644))

	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	done := make(chan error, 1)
	go func() {
		done <- sb.Access("pipe", dir, 0x04) // read check
	}()

	select {
	case err := <-done:
		// Should succeed (file exists and is readable) without blocking.
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Access blocked on FIFO — expected non-blocking stat-based check")
	}
}

// TestAccessReadPermissionDenied verifies that Access returns an error for
// files that are not readable by the current user.
func TestAccessReadPermissionDenied(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "noread.txt"), []byte("secret"), 0200))

	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	err = sb.Access("noread.txt", dir, 0x04)
	assert.ErrorIs(t, err, os.ErrPermission)
}
