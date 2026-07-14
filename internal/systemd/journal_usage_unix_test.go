//go:build linux || darwin

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package systemd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJournalDiskUsageCountsAllocatedJournalFiles(t *testing.T) {
	root := t.TempDir()
	machineID := "0123456789abcdef0123456789abcdef"
	machineIDPath := filepath.Join(root, "machine-id")
	journalDir := filepath.Join(root, "journal")
	machineDir := filepath.Join(journalDir, machineID)
	require.NoError(t, os.WriteFile(machineIDPath, []byte(machineID+"\n"), 0o600))
	require.NoError(t, os.MkdirAll(machineDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(machineDir, "system.journal"), make([]byte, 8192), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(machineDir, "ignored.txt"), make([]byte, 8192), 0o600))

	client := NewClient(Target{JournalDirs: []string{journalDir}, MachineIDPath: machineIDPath})
	usage, err := client.JournalDiskUsage(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, usage.Files)
	assert.Greater(t, usage.Bytes, uint64(0))
}

func TestJournalDiskUsageReturnsZeroForEmptyTarget(t *testing.T) {
	root := t.TempDir()
	machineID := "0123456789abcdef0123456789abcdef"
	machineIDPath := filepath.Join(root, "machine-id")
	require.NoError(t, os.WriteFile(machineIDPath, []byte(machineID+"\n"), 0o600))

	client := NewClient(Target{JournalDirs: []string{filepath.Join(root, "missing")}, MachineIDPath: machineIDPath})
	usage, err := client.JournalDiskUsage(context.Background())
	require.NoError(t, err)
	assert.Zero(t, usage.Files)
	assert.Zero(t, usage.Bytes)
}

func TestJournalDiskUsageHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	root := t.TempDir()
	machineID := "0123456789abcdef0123456789abcdef"
	machineIDPath := filepath.Join(root, "machine-id")
	journalDir := filepath.Join(root, "journal")
	machineDir := filepath.Join(journalDir, machineID)
	require.NoError(t, os.WriteFile(machineIDPath, []byte(machineID+"\n"), 0o600))
	require.NoError(t, os.MkdirAll(machineDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(machineDir, "system.journal"), []byte("data"), 0o600))

	client := NewClient(Target{JournalDirs: []string{journalDir}, MachineIDPath: machineIDPath})
	_, err := client.JournalDiskUsage(ctx)
	require.ErrorIs(t, err, context.Canceled)
}
