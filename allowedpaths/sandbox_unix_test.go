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

	sb, _, err := New([]string{dir})
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

	sb, _, err := New([]string{dir})
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

	sb, _, err := New([]string{dir})
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

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	err = sb.Access("noexec.txt", dir, 0x01)
	assert.ErrorIs(t, err, os.ErrPermission)
}

// TestAccessReadAllowed verifies that Access succeeds for a readable file.
func TestAccessReadAllowed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readable.txt"), []byte("data"), 0644))

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	assert.NoError(t, sb.Access("readable.txt", dir, 0x04))
}

// TestAccessWriteAllowed verifies that Access succeeds for a writable file.
func TestAccessWriteAllowed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "writable.txt"), []byte("data"), 0644))

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	assert.NoError(t, sb.Access("writable.txt", dir, 0x02))
}

// TestAccessExecAllowed verifies that Access succeeds for an executable file.
func TestAccessExecAllowed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "script.sh"), []byte("#!/bin/sh"), 0755))

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	assert.NoError(t, sb.Access("script.sh", dir, 0x01))
}

// TestAccessNonexistent verifies that Access fails for a missing file.
func TestAccessNonexistent(t *testing.T) {
	dir := t.TempDir()

	sb, _, err := New([]string{dir})
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

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	err = sb.Access(filepath.Join(outside, "secret.txt"), dir, 0x04)
	assert.ErrorIs(t, err, os.ErrPermission)
}

// TestAccessDirectory verifies that Access works on directories.
func TestAccessDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "subdir"), 0755))

	sb, _, err := New([]string{dir})
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

	sb, _, err := New([]string{dir})
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

	sb, _, err := New([]string{dir})
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

	sb, _, err := New([]string{dir})
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
	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()
	assert.ErrorIs(t, sb.Access("writeonly.txt", dir, 0x04), os.ErrPermission)
}

// TestAccessReadRegularFileAllowed verifies read succeeds on a
// readable regular file via the OpenFile path.
func TestAccessReadRegularFileAllowed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readable.txt"), []byte("data"), 0644))
	sb, _, err := New([]string{dir})
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
	sb, _, err := New([]string{dir})
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
	sb, _, err := New([]string{dir})
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
	sb, _, err := New([]string{dir})
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
	sb, _, err := New([]string{dir})
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
	sb, _, err := New([]string{dir})
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
	sb, _, err := New([]string{dir})
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
	sb, _, err := New([]string{dir})
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

	sb, _, err := New([]string{dir})
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

	sb, _, err := New([]string{dir})
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

// --- Cross-root symlink tests ---

// TestCrossRootSymlinkOpen verifies that a symlink in one allowed root
// pointing to a file in another allowed root can be opened.
func TestCrossRootSymlinkOpen(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "file.txt"), []byte("hello"), 0644))
	require.NoError(t, os.Symlink(filepath.Join(dir1, "file.txt"), filepath.Join(dir2, "link.txt")))

	sb, _, err := New([]string{dir1, dir2})
	require.NoError(t, err)
	defer sb.Close()

	f, err := sb.Open("link.txt", dir2, os.O_RDONLY, 0)
	require.NoError(t, err)
	defer f.Close()

	buf := make([]byte, 64)
	n, _ := f.Read(buf)
	assert.Equal(t, "hello", string(buf[:n]))
}

// TestCrossRootSymlinkStat verifies that Stat follows a cross-root symlink.
func TestCrossRootSymlinkStat(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "file.txt"), []byte("hello"), 0644))
	require.NoError(t, os.Symlink(filepath.Join(dir1, "file.txt"), filepath.Join(dir2, "link.txt")))

	sb, _, err := New([]string{dir1, dir2})
	require.NoError(t, err)
	defer sb.Close()

	info, err := sb.Stat("link.txt", dir2)
	require.NoError(t, err)
	assert.Equal(t, int64(5), info.Size())
	assert.Zero(t, info.Mode()&os.ModeSymlink, "Stat should follow the symlink")
}

// TestCrossRootSymlinkAccess verifies that Access works through a cross-root symlink.
func TestCrossRootSymlinkAccess(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "file.txt"), []byte("hello"), 0644))
	require.NoError(t, os.Symlink(filepath.Join(dir1, "file.txt"), filepath.Join(dir2, "link.txt")))

	sb, _, err := New([]string{dir1, dir2})
	require.NoError(t, err)
	defer sb.Close()

	assert.NoError(t, sb.Access("link.txt", dir2, modeRead))
}

// TestCrossRootSymlinkRelativeTarget verifies cross-root resolution with
// a relative symlink target (e.g. ../dir1/file.txt).
func TestCrossRootSymlinkRelativeTarget(t *testing.T) {
	parent := t.TempDir()
	dir1 := filepath.Join(parent, "dir1")
	dir2 := filepath.Join(parent, "dir2")
	require.NoError(t, os.MkdirAll(dir1, 0755))
	require.NoError(t, os.MkdirAll(dir2, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "file.txt"), []byte("data"), 0644))
	require.NoError(t, os.Symlink("../dir1/file.txt", filepath.Join(dir2, "link.txt")))

	sb, _, err := New([]string{dir1, dir2})
	require.NoError(t, err)
	defer sb.Close()

	f, err := sb.Open("link.txt", dir2, os.O_RDONLY, 0)
	require.NoError(t, err)
	defer f.Close()

	buf := make([]byte, 64)
	n, _ := f.Read(buf)
	assert.Equal(t, "data", string(buf[:n]))
}

// TestCrossRootSymlinkIntermediateDir verifies that an intermediate directory
// symlink crossing roots is resolved correctly.
func TestCrossRootSymlinkIntermediateDir(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	subdir := filepath.Join(dir1, "sub")
	require.NoError(t, os.MkdirAll(subdir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(subdir, "file.txt"), []byte("deep"), 0644))
	// dir2/link -> dir1/sub (directory symlink)
	require.NoError(t, os.Symlink(subdir, filepath.Join(dir2, "link")))

	sb, _, err := New([]string{dir1, dir2})
	require.NoError(t, err)
	defer sb.Close()

	f, err := sb.Open(filepath.Join("link", "file.txt"), dir2, os.O_RDONLY, 0)
	require.NoError(t, err)
	defer f.Close()

	buf := make([]byte, 64)
	n, _ := f.Read(buf)
	assert.Equal(t, "deep", string(buf[:n]))
}

// TestCrossRootSymlinkOutsideAllRoots verifies that a symlink pointing
// outside all allowed roots is still blocked.
func TestCrossRootSymlinkOutsideAllRoots(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0644))
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir1, "escape.txt")))

	sb, _, err := New([]string{dir1, dir2})
	require.NoError(t, err)
	defer sb.Close()

	_, err = sb.Open("escape.txt", dir1, os.O_RDONLY, 0)
	assert.Error(t, err)
}

// TestCrossRootSymlinkMissingTarget verifies that a cross-root symlink
// pointing to a non-existent file returns ENOENT, not the escape error.
func TestCrossRootSymlinkMissingTarget(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	// link points into dir1, but the target file doesn't exist.
	require.NoError(t, os.Symlink(filepath.Join(dir1, "missing.txt"), filepath.Join(dir2, "link.txt")))

	sb, _, err := New([]string{dir1, dir2})
	require.NoError(t, err)
	defer sb.Close()

	_, err = sb.Open("link.txt", dir2, os.O_RDONLY, 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no such file or directory", "should report file not found, not path escape")
}

// TestCrossRootSymlinkLoopBlocked verifies that circular symlinks between
// roots are detected and rejected after maxSymlinkHops.
func TestCrossRootSymlinkLoopBlocked(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	// dir1/a.txt -> dir2/b.txt -> dir1/a.txt (circular)
	require.NoError(t, os.Symlink(filepath.Join(dir2, "b.txt"), filepath.Join(dir1, "a.txt")))
	require.NoError(t, os.Symlink(filepath.Join(dir1, "a.txt"), filepath.Join(dir2, "b.txt")))

	sb, _, err := New([]string{dir1, dir2})
	require.NoError(t, err)
	defer sb.Close()

	_, err = sb.Open("a.txt", dir1, os.O_RDONLY, 0)
	assert.Error(t, err, "circular cross-root symlinks should be rejected")

	_, err = sb.Stat("a.txt", dir1)
	assert.Error(t, err, "circular cross-root symlinks should be rejected")
}

// TestCrossRootSymlinkChainLimit verifies that a long chain of cross-root
// symlinks exceeding maxSymlinkHops is rejected.
func TestCrossRootSymlinkChainLimit(t *testing.T) {
	dirs := make([]string, maxSymlinkHops+2)
	for i := range dirs {
		dirs[i] = t.TempDir()
	}
	// Last directory has the real file.
	require.NoError(t, os.WriteFile(filepath.Join(dirs[len(dirs)-1], "file.txt"), []byte("end"), 0644))

	// Each dir[i]/link.txt -> dir[i+1]/link.txt, except the penultimate
	// which points to the real file.
	for i := 0; i < len(dirs)-1; i++ {
		target := filepath.Join(dirs[i+1], "link.txt")
		if i == len(dirs)-2 {
			target = filepath.Join(dirs[i+1], "file.txt")
		}
		require.NoError(t, os.Symlink(target, filepath.Join(dirs[i], "link.txt")))
	}

	sb, _, err := New(dirs)
	require.NoError(t, err)
	defer sb.Close()

	_, err = sb.Open("link.txt", dirs[0], os.O_RDONLY, 0)
	assert.Error(t, err, "symlink chain exceeding maxSymlinkHops should be rejected")
}

// TestCrossRootSymlinkSiblingDirs verifies that a symlink in one sibling
// directory pointing into another sibling directory can be read when both
// are in AllowedPaths.
func TestCrossRootSymlinkSiblingDirs(t *testing.T) {
	parent := t.TempDir()
	dir1 := filepath.Join(parent, "dir1")
	dir2 := filepath.Join(parent, "dir2")
	require.NoError(t, os.MkdirAll(dir1, 0755))
	require.NoError(t, os.MkdirAll(dir2, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "file.txt"), []byte("abc"), 0644))
	// dir2/sym.txt -> ../dir1/file.txt
	require.NoError(t, os.Symlink("../dir1/file.txt", filepath.Join(dir2, "sym.txt")))

	sb, _, err := New([]string{dir1, dir2})
	require.NoError(t, err)
	defer sb.Close()

	f, err := sb.Open(filepath.Join(dir2, "sym.txt"), "/", os.O_RDONLY, 0)
	require.NoError(t, err, "cross-root symlink between sibling dirs should be readable")
	defer f.Close()

	buf := make([]byte, 64)
	n, _ := f.Read(buf)
	assert.Equal(t, "abc", string(buf[:n]))

	info, err := sb.Stat(filepath.Join(dir2, "sym.txt"), "/")
	require.NoError(t, err)
	assert.Equal(t, int64(3), info.Size())

	assert.NoError(t, sb.Access(filepath.Join(dir2, "sym.txt"), "/", modeRead))
}
