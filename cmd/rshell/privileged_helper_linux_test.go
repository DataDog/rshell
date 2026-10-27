// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package main

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadOptionalPolicyMissing(t *testing.T) {
	credential, err := loadOptionalPolicy(filepath.Join(t.TempDir(), "missing.json"))
	require.NoError(t, err)
	require.Nil(t, credential)
}

func TestLoadOptionalPolicyEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.json")
	require.NoError(t, os.WriteFile(path, nil, 0o600))

	credential, err := loadOptionalPolicy(path)
	require.NoError(t, err)
	require.Nil(t, credential)
}

// shortTempDir returns an owner-only temporary directory with a path short
// enough for a Unix socket (about 100 bytes).
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rsh")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	require.NoError(t, os.Chmod(dir, 0o755))
	return dir
}

func TestCreateListener(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "h.sock")

	listener, err := createListener(path, os.Getuid(), os.Getgid())
	require.NoError(t, err)

	var stat syscall.Stat_t
	require.NoError(t, syscall.Lstat(path, &stat))
	require.Equal(t, uint32(syscall.S_IFSOCK), stat.Mode&syscall.S_IFMT)
	require.Equal(t, uint32(0o600), stat.Mode&0o777, "only the owner may connect")
	require.Equal(t, uint32(os.Getuid()), stat.Uid)

	conn, err := net.Dial("unix", path)
	require.NoError(t, err)
	require.NoError(t, conn.Close())

	require.NoError(t, listener.Close())
	_, err = os.Lstat(path)
	require.ErrorIs(t, err, os.ErrNotExist, "the socket is removed when the helper stops")
}

func TestCreateListenerReplacesStaleSocket(t *testing.T) {
	path := filepath.Join(shortTempDir(t), "h.sock")
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	require.NoError(t, err)
	stale.SetUnlinkOnClose(false)
	require.NoError(t, stale.Close())

	listener, err := createListener(path, os.Getuid(), os.Getgid())
	require.NoError(t, err)
	require.NoError(t, listener.Close())
}

func TestCreateListenerRejectsUnsafeTargets(t *testing.T) {
	t.Run("relative path", func(t *testing.T) {
		_, err := createListener("h.sock", os.Getuid(), os.Getgid())
		require.ErrorContains(t, err, "not absolute")
	})
	t.Run("existing non-socket file", func(t *testing.T) {
		path := filepath.Join(shortTempDir(t), "h.sock")
		require.NoError(t, os.WriteFile(path, []byte("keep"), 0o600))
		_, err := createListener(path, os.Getuid(), os.Getgid())
		require.ErrorContains(t, err, "is not a socket")
		content, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, "keep", string(content), "a non-socket file must not be removed")
	})
	t.Run("directory writable by others", func(t *testing.T) {
		dir := shortTempDir(t)
		require.NoError(t, os.Chmod(dir, 0o777))
		_, err := createListener(filepath.Join(dir, "h.sock"), os.Getuid(), os.Getgid())
		require.ErrorContains(t, err, "writable by group or others")
	})
	t.Run("directory is a symlink", func(t *testing.T) {
		parent := shortTempDir(t)
		link := filepath.Join(parent, "link")
		require.NoError(t, os.Symlink(shortTempDir(t), link))
		_, err := createListener(filepath.Join(link, "h.sock"), os.Getuid(), os.Getgid())
		require.ErrorContains(t, err, "is not a directory")
	})
}
