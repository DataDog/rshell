// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build unix

package awk_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/interp"
)

func TestAwkDevNull(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := runScript(t, `awk '{print}' /dev/null`, dir, interp.AllowedPaths([]string{dir, "/dev"}))
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

func TestAwkSymlinkFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "real.txt", "data\n")
	require.NoError(t, os.Symlink("real.txt", filepath.Join(dir, "link.txt")))
	stdout, _, code := cmdRun(t, `awk '{print}' link.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "data\n", stdout)
}
