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

// TestAccessWriteDenied verifies that Access returns an error for files
// that are not writable by the current user (mode 0444).
func TestAccessWriteDenied(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readonly.txt"), []byte("data"), 0444))

	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	err = sb.Access("readonly.txt", dir, 0x02)
	assert.ErrorIs(t, err, os.ErrPermission)
}

// TestAccessExecDenied verifies that Access returns an error for files
// that are not executable by the current user (mode 0644).
func TestAccessExecDenied(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "noexec.txt"), []byte("data"), 0644))

	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	err = sb.Access("noexec.txt", dir, 0x01)
	assert.ErrorIs(t, err, os.ErrPermission)
}

// TestAccessReadAllowed verifies that Access succeeds for a readable file.
func TestAccessReadAllowed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readable.txt"), []byte("data"), 0644))

	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	assert.NoError(t, sb.Access("readable.txt", dir, 0x04))
}

// TestAccessWriteAllowed verifies that Access succeeds for a writable file.
func TestAccessWriteAllowed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "writable.txt"), []byte("data"), 0644))

	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	assert.NoError(t, sb.Access("writable.txt", dir, 0x02))
}

// TestAccessExecAllowed verifies that Access succeeds for an executable file.
func TestAccessExecAllowed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "script.sh"), []byte("#!/bin/sh"), 0755))

	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	assert.NoError(t, sb.Access("script.sh", dir, 0x01))
}

// TestAccessNonexistent verifies that Access fails for a missing file.
func TestAccessNonexistent(t *testing.T) {
	dir := t.TempDir()

	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	err = sb.Access("missing.txt", dir, 0x04)
	assert.Error(t, err)
}

// TestAccessOutsideSandbox verifies that Access fails for a path
// outside the sandbox.
func TestAccessOutsideSandbox(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0644))

	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	err = sb.Access(filepath.Join(outside, "secret.txt"), dir, 0x04)
	assert.ErrorIs(t, err, os.ErrPermission)
}

// TestAccessDirectory verifies that Access works on directories.
func TestAccessDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "subdir"), 0755))

	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	assert.NoError(t, sb.Access("subdir", dir, 0x04))
}

// TestAccessSymlinkWithinSandbox verifies that Access succeeds for a
// symlink that resolves to a target within the sandbox.
func TestAccessSymlinkWithinSandbox(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "target.txt"), []byte("data"), 0644))
	require.NoError(t, os.Symlink("target.txt", filepath.Join(dir, "link.txt")))

	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	assert.NoError(t, sb.Access("link.txt", dir, 0x04))
}

// TestAccessSymlinkEscapeBlocked verifies that Access blocks symlinks
// that resolve outside the sandbox.
func TestAccessSymlinkEscapeBlocked(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0644))
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "escape.txt")))

	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	err = sb.Access("escape.txt", dir, 0x04)
	assert.Error(t, err)
}

// TestAccessCombinedModes verifies that Access correctly checks
// combined permission modes (read+write, read+exec, etc.).
func TestAccessCombinedModes(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "rx.sh"), []byte("#!/bin/sh"), 0555))

	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	// Read+exec should succeed on 0555 file.
	assert.NoError(t, sb.Access("rx.sh", dir, 0x04|0x01))

	// Write should fail on 0555 file.
	assert.ErrorIs(t, sb.Access("rx.sh", dir, 0x02), os.ErrPermission)

	// Read+write should fail on 0555 file.
	assert.ErrorIs(t, sb.Access("rx.sh", dir, 0x04|0x02), os.ErrPermission)
}
