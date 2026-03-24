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

// TestAccessReadRegularFileOpenFile verifies that read access on a
// regular file uses the fd-relative OpenFile path (not syscall.Access).
// A file with 0200 (write-only) should be denied.
func TestAccessReadRegularFileOpenFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "writeonly.txt"), []byte("data"), 0200))
	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()
	assert.ErrorIs(t, sb.Access("writeonly.txt", dir, 0x04), os.ErrPermission)
}

// TestAccessReadRegularFileAllowed verifies read succeeds on a
// readable regular file via the OpenFile path.
func TestAccessReadRegularFileAllowed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readable.txt"), []byte("data"), 0644))
	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()
	assert.NoError(t, sb.Access("readable.txt", dir, 0x04))
}

// TestAccessFIFOReadFallsBackToModeBits verifies that FIFOs do NOT
// use OpenFile (which would block) and instead use effectiveHasPerm.
// A readable FIFO (0644) should pass the mode-bit check.
func TestAccessFIFOReadFallsBackToModeBits(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "pipe"), 0644))
	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	done := make(chan error, 1)
	go func() { done <- sb.Access("pipe", dir, 0x04) }()
	select {
	case err := <-done:
		assert.NoError(t, err) // readable FIFO should pass mode-bit check
	case <-time.After(2 * time.Second):
		t.Fatal("Access blocked on FIFO")
	}
}

// TestAccessFIFOReadDeniedModeBits verifies that a non-readable FIFO
// (0200) is correctly denied via mode-bit fallback.
func TestAccessFIFOReadDeniedModeBits(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	dir := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "pipe"), 0200))
	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	done := make(chan error, 1)
	go func() { done <- sb.Access("pipe", dir, 0x04) }()
	select {
	case err := <-done:
		assert.ErrorIs(t, err, os.ErrPermission)
	case <-time.After(2 * time.Second):
		t.Fatal("Access blocked on FIFO")
	}
}

// TestAccessDirectoryReadUsesModeBits verifies that directory read
// checks use mode-bit fallback (not OpenFile, which returns a handle).
func TestAccessDirectoryReadUsesModeBits(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "subdir"), 0755))
	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()
	assert.NoError(t, sb.Access("subdir", dir, 0x04))
}

// TestAccessDirectoryReadDenied verifies that a non-readable directory
// (0300) is denied via mode-bit inspection.
func TestAccessDirectoryReadDenied(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "noread"), 0300))
	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()
	assert.ErrorIs(t, sb.Access("noread", dir, 0x04), os.ErrPermission)
}

// TestAccessReadWriteCombined verifies combined read+write checks.
// Read uses OpenFile (ACL-accurate), write uses effectiveHasPerm.
func TestAccessReadWriteCombined(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	dir := t.TempDir()
	// 0444 = readable but not writable
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readonly.txt"), []byte("data"), 0444))
	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()
	// Read-only succeeds
	assert.NoError(t, sb.Access("readonly.txt", dir, 0x04))
	// Write fails
	assert.ErrorIs(t, sb.Access("readonly.txt", dir, 0x02), os.ErrPermission)
	// Read+write fails (write component fails)
	assert.ErrorIs(t, sb.Access("readonly.txt", dir, 0x04|0x02), os.ErrPermission)
}

// TestAccessFdRelativeSymlink verifies that the permission check stays
// fd-relative. Access through a symlink within the sandbox works because
// both Stat and OpenFile resolve through os.Root's fd.
func TestAccessFdRelativeSymlink(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "target.txt"), []byte("data"), 0644))
	require.NoError(t, os.Symlink("target.txt", filepath.Join(dir, "link.txt")))
	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()
	assert.NoError(t, sb.Access("link.txt", dir, 0x04))
}

// TestAccessFdRelativeEscapeBlocked verifies that symlink escapes
// are blocked at the os.Root level for both Stat and OpenFile.
func TestAccessFdRelativeEscapeBlocked(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0644))
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir, "escape.txt")))
	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()
	assert.Error(t, sb.Access("escape.txt", dir, 0x04))
}

// TestAccessSocketFallsBackToStat verifies that Unix sockets, which
// cannot be opened with open(2), fall back to Stat + effectiveHasPerm.
func TestAccessSocketFallsBackToStat(t *testing.T) {
	// Unix socket paths have a ~104-byte limit on macOS. Use a short
	// temp directory to avoid EINVAL from bind(2).
	dir, err := os.MkdirTemp("/tmp", "sock")
	require.NoError(t, err)
	defer os.RemoveAll(dir)
	sockPath := filepath.Join(dir, "s.sock")

	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	require.NoError(t, err)
	defer syscall.Close(fd)
	require.NoError(t, syscall.Bind(fd, &syscall.SockaddrUnix{Name: sockPath}))

	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	// Socket with default permissions should pass read check via the
	// Stat fallback path (OpenFile fails on sockets).
	assert.NoError(t, sb.Access("s.sock", dir, 0x04))
}

// TestAccessFIFONonBlocking verifies that O_NONBLOCK prevents blocking
// on a FIFO with no writer. Core of the TOCTOU fix: even if an attacker
// swaps a regular file for a FIFO, the open returns immediately.
func TestAccessFIFONonBlocking(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "fifo"), 0644))

	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- sb.Access("fifo", dir, 0x04) }()
	select {
	case err := <-done:
		assert.NoError(t, err)
		// With O_NONBLOCK, this should complete in well under 100ms.
		assert.Less(t, time.Since(start), 500*time.Millisecond,
			"FIFO access took too long — O_NONBLOCK may not be working")
	case <-time.After(2 * time.Second):
		t.Fatal("Access blocked on FIFO — O_NONBLOCK not effective")
	}
}

// TestFileOnlyInodeVerification verifies that file-only roots reject access
// when the file has been replaced (different inode) after sandbox construction.
func TestFileOnlyInodeVerification(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "data.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("original"), 0644))

	sb, err := New([]string{filePath})
	require.NoError(t, err)
	defer sb.Close()

	// Original file should be accessible.
	f, err := sb.Open(filePath, dir, os.O_RDONLY, 0)
	require.NoError(t, err)
	f.Close()

	// Replace the file with a guaranteed-different inode.
	// Create the replacement while the original still exists so that
	// the allocator cannot reuse the same inode (important on tmpfs).
	replacement := filepath.Join(dir, "data.txt.new")
	require.NoError(t, os.WriteFile(replacement, []byte("replaced"), 0644))
	require.NoError(t, os.Remove(filePath))
	require.NoError(t, os.Rename(replacement, filePath))

	// Same name, different inode — must be rejected.
	_, err = sb.Open(filePath, dir, os.O_RDONLY, 0)
	assert.ErrorIs(t, err, os.ErrPermission, "replaced file (different inode) should be rejected")
}

// TestFileOnlyCaseSensitiveUnix verifies that file-only matching is
// case-sensitive on Unix, where "Data.txt" and "data.txt" are distinct files.
func TestFileOnlyCaseSensitiveUnix(t *testing.T) {
	dir := t.TempDir()
	upper := filepath.Join(dir, "Data.txt")
	lower := filepath.Join(dir, "data.txt")
	require.NoError(t, os.WriteFile(upper, []byte("upper"), 0644))
	require.NoError(t, os.WriteFile(lower, []byte("lower"), 0644))

	sb, err := New([]string{upper})
	require.NoError(t, err)
	defer sb.Close()

	// Allowed file should be accessible.
	f, err := sb.Open(upper, dir, os.O_RDONLY, 0)
	require.NoError(t, err)
	f.Close()

	// Different-cased sibling must be blocked on Unix.
	_, err = sb.Open(lower, dir, os.O_RDONLY, 0)
	assert.ErrorIs(t, err, os.ErrPermission)
}

// TestFileOnlyAccessWriteExecWithoutRead verifies that write/exec access
// checks on file-only entries do not fail due to the identity verification
// opening the file with O_RDONLY (which requires read permission).
func TestFileOnlyAccessWriteExecWithoutRead(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}

	dir := t.TempDir()
	filePath := filepath.Join(dir, "noread.sh")
	require.NoError(t, os.WriteFile(filePath, []byte("#!/bin/sh"), 0333))

	sb, err := New([]string{filePath})
	require.NoError(t, err)
	defer sb.Close()

	// Write check must succeed (file is writable).
	assert.NoError(t, sb.Access(filePath, dir, 0x02),
		"write access should succeed on write-only file-only entry")

	// Exec check must succeed (file is executable).
	assert.NoError(t, sb.Access(filePath, dir, 0x01),
		"exec access should succeed on exec-only file-only entry")

	// Read check must fail (file is not readable).
	assert.ErrorIs(t, sb.Access(filePath, dir, 0x04), os.ErrPermission,
		"read access should fail on non-readable file-only entry")
}
