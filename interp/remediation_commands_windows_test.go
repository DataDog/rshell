// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package interp_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/interp"
)

func TestRemediationTeeRejectsWindowsFDHandoffBeforeOpeningTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("payload\n"), 0644))
	require.NoError(t, os.WriteFile(target, []byte("keep\n"), 0644))
	called := false

	_, stderr, code := runScript(t, "tee target.txt < input.txt", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "host file descriptor handoff is not supported")
	assert.False(t, called)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "keep\n", string(data))
}

func TestRemediationTruncateRejectsWindowsFDHandoffBeforeOpeningTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.log")
	require.NoError(t, os.WriteFile(target, []byte("payload\n"), 0644))
	called := false

	_, stderr, code := runScript(t, "truncate -s 0 target.log", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "host file descriptor handoff is not supported")
	assert.False(t, called)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "payload\n", string(data))
}

func TestRemediationLogrotateRejectsWindowsFDHandoffBeforeOpeningTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.log")
	require.NoError(t, os.WriteFile(target, []byte("payload\n"), 0644))
	called := false

	_, stderr, code := runScript(t, "logrotate target.log", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "host file descriptor handoff is not supported")
	assert.False(t, called)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "payload\n", string(data))
}
