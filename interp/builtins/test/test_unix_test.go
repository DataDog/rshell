// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build unix

package test_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/interp"
)

func TestTestSymlinkL(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.txt", "data")
	require.NoError(t, os.Symlink("target.txt", filepath.Join(dir, "link")))
	_, _, code := cmdRun(t, `test -L link`, dir)
	assert.Equal(t, 0, code)
	_, _, code = cmdRun(t, `test -L target.txt`, dir)
	assert.Equal(t, 1, code)
}

func TestTestSymlinkH(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "target.txt", "data")
	require.NoError(t, os.Symlink("target.txt", filepath.Join(dir, "link")))
	_, _, code := cmdRun(t, `test -h link`, dir)
	assert.Equal(t, 0, code)
}

func TestTestDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Symlink("nonexistent", filepath.Join(dir, "broken")))
	_, _, code := cmdRun(t, `test -L broken`, dir)
	assert.Equal(t, 0, code)
	_, _, code = cmdRun(t, `test -e broken`, dir)
	assert.Equal(t, 1, code)
}

func TestTestEfSymlink(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "data")
	require.NoError(t, os.Symlink("file.txt", filepath.Join(dir, "link")))
	_, _, code := cmdRun(t, `test file.txt -ef link`, dir)
	assert.Equal(t, 0, code)
}

func TestTestDevNull(t *testing.T) {
	dir := t.TempDir()
	_, _, code := runScript(t, `test -e /dev/null`, dir, interp.AllowedPaths([]string{dir, "/dev"}))
	assert.Equal(t, 0, code)
	_, _, code = runScript(t, `test -c /dev/null`, dir, interp.AllowedPaths([]string{dir, "/dev"}))
	assert.Equal(t, 0, code)
}

func TestTestPermissionBits(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "noexec.txt")
	require.NoError(t, os.WriteFile(f, []byte("data"), 0644))
	_, _, code := cmdRun(t, `test -x noexec.txt`, dir)
	assert.Equal(t, 1, code)

	exec := filepath.Join(dir, "exec.sh")
	require.NoError(t, os.WriteFile(exec, []byte("#!/bin/sh"), 0755))
	_, _, code = cmdRun(t, `test -x exec.sh`, dir)
	assert.Equal(t, 0, code)
}
