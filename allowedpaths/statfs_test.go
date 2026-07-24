// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedpaths

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSandboxStatFSAllowedPath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file"), []byte("data"), 0o600))

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	for _, path := range []string{".", "file"} {
		info, err := sb.StatFS(path, dir)
		require.NoError(t, err)
		assert.True(t, info.IDAvailable)
		assert.NotEmpty(t, info.TypeName)
		assert.NotZero(t, info.IOBlockSize)
		assert.NotZero(t, info.FundamentalBlockSize)
		assert.NotZero(t, info.Blocks)
	}
}

func TestSandboxStatFSRejectsOutsideAllowedPaths(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()

	sb, _, err := New([]string{allowed})
	require.NoError(t, err)
	defer sb.Close()

	_, err = sb.StatFS(outside, allowed)
	assert.ErrorIs(t, err, fs.ErrPermission)
}

func TestSandboxStatFSMissingAndEmptyPaths(t *testing.T) {
	dir := t.TempDir()
	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	for _, path := range []string{"missing", ""} {
		_, err := sb.StatFS(path, dir)
		assert.ErrorIs(t, err, fs.ErrNotExist)
	}
}

func TestSandboxStatFSFollowsAllowedSymlink(t *testing.T) {
	source := t.TempDir()
	target := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(target, "file"), []byte("data"), 0o600))

	link := filepath.Join(source, "link")
	if err := os.Symlink(filepath.Join(target, "file"), link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	sb, _, err := New([]string{source, target})
	require.NoError(t, err)
	defer sb.Close()

	got, err := sb.StatFS(link, source)
	require.NoError(t, err)
	want, err := sb.StatFS(filepath.Join(target, "file"), source)
	require.NoError(t, err)
	assert.Equal(t, want.ID, got.ID)
	assert.Equal(t, want.TypeName, got.TypeName)
	assert.Equal(t, want.IOBlockSize, got.IOBlockSize)
	assert.Equal(t, want.FundamentalBlockSize, got.FundamentalBlockSize)
}

func TestSandboxStatFSRejectsSymlinkOutsideAllowedPaths(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "file")
	require.NoError(t, os.WriteFile(target, []byte("data"), 0o600))

	link := filepath.Join(allowed, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}

	sb, _, err := New([]string{allowed})
	require.NoError(t, err)
	defer sb.Close()

	_, err = sb.StatFS(link, allowed)
	assert.True(t, errors.Is(err, fs.ErrPermission), "StatFS returned %v", err)
}
