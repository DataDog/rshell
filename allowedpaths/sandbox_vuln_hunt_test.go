// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt 2026-05-20-gpt-5.5-cyber-3 (target: allowedpaths-sandbox)

package allowedpaths

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVulnHuntSubsystemAllowedPathsSandbox_OpenReadOnlyAndTraversalBlocked(t *testing.T) {
	parent := t.TempDir()
	allowed := filepath.Join(parent, "allowed")
	outside := filepath.Join(parent, "outside")
	siblingPrefix := filepath.Join(parent, "allowed-sibling")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.Mkdir(siblingPrefix, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "safe.txt"), []byte("safe"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(siblingPrefix, "secret.txt"), []byte("sibling"), 0o644))

	sb, _, err := New([]string{allowed})
	require.NoError(t, err)
	defer sb.Close()

	f, err := sb.Open("safe.txt", allowed, os.O_RDONLY, 0)
	require.NoError(t, err)
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	assert.Equal(t, "safe", string(data))

	for _, flag := range []int{os.O_WRONLY, os.O_RDWR, os.O_CREATE, os.O_TRUNC, os.O_APPEND, os.O_WRONLY | os.O_CREATE} {
		f, err := sb.Open("safe.txt", allowed, flag, 0o644)
		assert.Nil(t, f)
		assert.ErrorIs(t, err, os.ErrPermission, "flag %d", flag)
	}

	for _, path := range []string{
		filepath.Join(outside, "secret.txt"),
		filepath.Join("..", "outside", "secret.txt"),
		filepath.Join(siblingPrefix, "secret.txt"),
	} {
		f, err := sb.Open(path, allowed, os.O_RDONLY, 0)
		assert.Nil(t, f, "path %q", path)
		assert.ErrorIs(t, err, os.ErrPermission, "path %q", path)
	}
}

func TestVulnHuntSubsystemAllowedPathsSandbox_CrossRootSymlinkTerminalSemantics(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	outside := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir1, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "sub", "file.txt"), []byte("data"), 0o644))
	require.NoError(t, os.Symlink("file.txt", filepath.Join(dir1, "sub", "leaf.lnk")))
	require.NoError(t, os.Symlink(filepath.Join(dir1, "sub"), filepath.Join(dir2, "bridge")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(dir2, "escape.lnk")))

	sb, _, err := New([]string{dir1, dir2})
	require.NoError(t, err)
	defer sb.Close()

	f, err := sb.Open(filepath.Join("bridge", "file.txt"), dir2, os.O_RDONLY, 0)
	require.NoError(t, err)
	data, err := io.ReadAll(f)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	assert.Equal(t, "data", string(data))

	info, err := sb.Lstat(filepath.Join("bridge", "leaf.lnk"), dir2)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&fs.ModeSymlink)
	target, err := sb.Readlink(filepath.Join("bridge", "leaf.lnk"), dir2)
	require.NoError(t, err)
	assert.Equal(t, "file.txt", target)

	f, err = sb.Open("escape.lnk", dir2, os.O_RDONLY, 0)
	assert.Nil(t, f)
	assert.Error(t, err)
}

func TestVulnHuntSubsystemAllowedPathsSandbox_ReadDirCapsAndLimitedBounds(t *testing.T) {
	dir := t.TempDir()
	for i := range 5 {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d", i)), nil, 0o644))
	}
	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	entries, err := sb.readDirN(".", dir, 3)
	assert.Nil(t, entries)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too many entries")

	entries, truncated, err := sb.ReadDirLimited(".", dir, -100, 2)
	require.NoError(t, err)
	assert.True(t, truncated)
	assert.Len(t, entries, 2)

	entries, truncated, err = sb.ReadDirLimited(".", dir, 100, 2)
	require.NoError(t, err)
	assert.False(t, truncated)
	assert.Empty(t, entries)
}

func TestVulnHuntSubsystemAllowedPathsSandbox_HostPrefixAndNullDeviceBoundaries(t *testing.T) {
	hostPrefix, pods, containers := setupContainerDirsForVulnHunt(t)
	outside := filepath.Join(hostPrefix, "etc", "secret")
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "shadow"), []byte("secret"), 0o644))
	require.NoError(t, os.Symlink("/etc/secret/shadow", filepath.Join(containers, "escape.log")))

	sb, _, err := New([]string{pods, containers})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetHostPrefix(hostPrefix)

	f, err := sb.Open("app.log", containers, os.O_RDONLY, 0)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	f, err = sb.Open("escape.log", containers, os.O_RDONLY, 0)
	assert.Nil(t, f)
	assert.Error(t, err)

	info, err := sb.Stat(os.DevNull, containers)
	require.NoError(t, err)
	assert.False(t, info.IsDir())
	f, err = sb.Open(os.DevNull, containers, os.O_RDONLY, 0)
	assert.Nil(t, f)
	assert.ErrorIs(t, err, os.ErrPermission)
}

func TestVulnHuntSubsystemAllowedPathsSandbox_NilAndEmptySandboxesFailClosed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file.txt"), []byte("data"), 0o644))

	var nilSB *Sandbox
	assert.Nil(t, nilSB.Paths())
	assert.NoError(t, nilSB.Close())
	_, err := nilSB.Open("file.txt", dir, os.O_RDONLY, 0)
	assert.ErrorIs(t, err, os.ErrPermission)
	_, err = nilSB.Stat("file.txt", dir)
	assert.ErrorIs(t, err, os.ErrPermission)

	emptySB, _, err := New(nil)
	require.NoError(t, err)
	defer emptySB.Close()
	_, err = emptySB.Open("file.txt", dir, os.O_RDONLY, 0)
	assert.ErrorIs(t, err, os.ErrPermission)
	_, err = emptySB.ReadDir(".", dir)
	assert.ErrorIs(t, err, os.ErrPermission)
}

func TestVulnHuntSubsystemAllowedPathsSandbox_PortableErrorsKeepOperationAndPath(t *testing.T) {
	err := PortablePathError(&os.PathError{Op: "openat", Path: "x", Err: fs.ErrPermission})
	var pe *os.PathError
	require.True(t, errors.As(err, &pe))
	assert.Equal(t, "openat", pe.Op)
	assert.Equal(t, "x", pe.Path)
	assert.Equal(t, "permission denied", pe.Err.Error())
}

func setupContainerDirsForVulnHunt(t *testing.T) (hostPrefix, pods, containers string) {
	t.Helper()
	root := t.TempDir()
	hostPrefix = root
	pods = filepath.Join(root, "var", "log", "pods")
	containers = filepath.Join(root, "var", "log", "containers")
	require.NoError(t, os.MkdirAll(pods, 0o755))
	require.NoError(t, os.MkdirAll(containers, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pods, "app.log"), []byte("log line"), 0o644))
	require.NoError(t, os.Symlink("/var/log/pods/app.log", filepath.Join(containers, "app.log")))
	return hostPrefix, pods, containers
}
