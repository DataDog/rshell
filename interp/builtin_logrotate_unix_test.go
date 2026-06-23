// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build unix

package interp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/interp"
)

func TestLogrotateSymlinkTargetReportsActionableError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.log")
	require.NoError(t, os.WriteFile(target, []byte("target"), 0644))
	require.NoError(t, os.Symlink("target.log", filepath.Join(dir, "app.log")))

	stdout, stderr, code := runScript(t, "logrotate --dry-run --force app.log", dir,
		interp.AllowedPaths([]string{dir + ":rw"}),
		interp.WithMode(interp.ModeRemediation),
	)

	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, `logrotate: "app.log": symlinks are not supported as write targets`)
	got, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "target", string(got))
}
