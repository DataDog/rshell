// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || darwin

package systemd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins"
)

const testMachineID = "0123456789abcdef0123456789abcdef"

func archivedJournalName(index int) string {
	return fmt.Sprintf("system@abcdef0123456789abcdef0123456789-%016x-%016x.journal", index, index)
}

func newVacuumTestClient(t *testing.T, policy *JournalVacuumPolicy) (*Client, string) {
	t.Helper()
	root := t.TempDir()
	machineIDPath := filepath.Join(root, "machine-id")
	journalRoot := filepath.Join(root, "journal")
	machineDir := filepath.Join(journalRoot, testMachineID)
	require.NoError(t, os.WriteFile(machineIDPath, []byte(testMachineID+"\n"), 0o600))
	require.NoError(t, os.MkdirAll(machineDir, 0o700))
	return NewClient(Target{JournalDirs: []string{journalRoot}, MachineIDPath: machineIDPath}, policy), machineDir
}

func writeVacuumFile(t *testing.T, directory, name string, modTime time.Time) string {
	t.Helper()
	path := filepath.Join(directory, name)
	require.NoError(t, os.WriteFile(path, make([]byte, 8192), 0o600))
	require.NoError(t, os.Chtimes(path, modTime, modTime))
	return path
}

func testVacuumPolicy() *JournalVacuumPolicy {
	return &JournalVacuumPolicy{
		MinRetentionAge:  24 * time.Hour,
		MinRetainedFiles: 1,
		MaxDeletedFiles:  10,
		MaxDeletedBytes:  1 << 30,
	}
}

func TestVacuumJournalDeletesOldestArchivesWithinPolicy(t *testing.T) {
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	client, directory := newVacuumTestClient(t, testVacuumPolicy())
	active := writeVacuumFile(t, directory, "system.journal", now.Add(-30*24*time.Hour))
	namespaceActive := writeVacuumFile(t, directory, "system@tenant.journal", now.Add(-30*24*time.Hour))
	malformed := writeVacuumFile(t, directory, "system@old.journal", now.Add(-30*24*time.Hour))
	oldest := writeVacuumFile(t, directory, archivedJournalName(1), now.Add(-10*24*time.Hour))
	old := writeVacuumFile(t, directory, archivedJournalName(2), now.Add(-5*24*time.Hour))
	recent := writeVacuumFile(t, directory, archivedJournalName(3), now.Add(-time.Hour))

	result, err := client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{
		Now:    now,
		Before: now.Add(-48 * time.Hour),
	})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Files)
	assert.Greater(t, result.Bytes, uint64(0))
	assert.NoFileExists(t, oldest)
	assert.NoFileExists(t, old)
	assert.FileExists(t, recent)
	assert.FileExists(t, active)
	assert.FileExists(t, namespaceActive)
	assert.FileExists(t, malformed)
}

func TestVacuumJournalDryRunDoesNotDelete(t *testing.T) {
	now := time.Now()
	client, directory := newVacuumTestClient(t, testVacuumPolicy())
	archive := writeVacuumFile(t, directory, archivedJournalName(1), now.Add(-7*24*time.Hour))
	writeVacuumFile(t, directory, archivedJournalName(2), now.Add(-6*24*time.Hour))

	result, err := client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{
		Now:    now,
		Before: now.Add(-48 * time.Hour),
		DryRun: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Files)
	assert.FileExists(t, archive)
}

func TestVacuumJournalEnforcesPerCallFileCeiling(t *testing.T) {
	now := time.Now()
	policy := testVacuumPolicy()
	policy.MinRetainedFiles = 0
	policy.MaxDeletedFiles = 1
	client, directory := newVacuumTestClient(t, policy)
	oldest := writeVacuumFile(t, directory, archivedJournalName(1), now.Add(-10*24*time.Hour))
	second := writeVacuumFile(t, directory, archivedJournalName(2), now.Add(-9*24*time.Hour))

	result, err := client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{
		Now:    now,
		Before: now.Add(-48 * time.Hour),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Files)
	assert.NoFileExists(t, oldest)
	assert.FileExists(t, second)
}

func TestVacuumJournalDoesNotUnderflowByteCeiling(t *testing.T) {
	now := time.Now()
	policy := testVacuumPolicy()
	policy.MinRetainedFiles = 0
	policy.MaxDeletedBytes = 1
	client, directory := newVacuumTestClient(t, policy)
	archive := writeVacuumFile(t, directory, archivedJournalName(1), now.Add(-10*24*time.Hour))

	result, err := client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{
		Now:    now,
		Before: now.Add(-48 * time.Hour),
	})
	require.NoError(t, err)
	assert.Zero(t, result.Files)
	assert.Zero(t, result.Bytes)
	assert.FileExists(t, archive)
}

func TestVacuumJournalHonorsAllocatedSizeTarget(t *testing.T) {
	now := time.Now()
	policy := testVacuumPolicy()
	policy.MinRetainedFiles = 0
	client, directory := newVacuumTestClient(t, policy)
	oldest := writeVacuumFile(t, directory, archivedJournalName(1), now.Add(-10*24*time.Hour))
	second := writeVacuumFile(t, directory, archivedJournalName(2), now.Add(-9*24*time.Hour))
	info, err := os.Lstat(oldest)
	require.NoError(t, err)
	stat, err := journalStat(info)
	require.NoError(t, err)

	result, err := client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{
		Now:      now,
		MaxBytes: stat.allocated,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Files)
	assert.NoFileExists(t, oldest)
	assert.FileExists(t, second)
}

func TestVacuumJournalSkipsHardlinksAndSymlinks(t *testing.T) {
	now := time.Now()
	policy := testVacuumPolicy()
	policy.MinRetainedFiles = 0
	client, directory := newVacuumTestClient(t, policy)
	first := writeVacuumFile(t, directory, archivedJournalName(1), now.Add(-10*24*time.Hour))
	second := filepath.Join(directory, archivedJournalName(2))
	require.NoError(t, os.Link(first, second))
	symlink := filepath.Join(directory, archivedJournalName(3))
	require.NoError(t, os.Symlink(first, symlink))

	result, err := client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{
		Now:    now,
		Before: now.Add(-48 * time.Hour),
	})
	require.NoError(t, err)
	assert.Zero(t, result.Files)
	assert.FileExists(t, first)
	assert.FileExists(t, second)
	_, err = os.Lstat(symlink)
	require.NoError(t, err)
}

func TestVacuumJournalRejectsSymlinkedMachineDirectory(t *testing.T) {
	now := time.Now()
	root := t.TempDir()
	journalRoot := filepath.Join(root, "journal")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.MkdirAll(journalRoot, 0o700))
	require.NoError(t, os.MkdirAll(outside, 0o700))
	archive := writeVacuumFile(t, outside, archivedJournalName(1), now.Add(-10*24*time.Hour))
	require.NoError(t, os.Symlink(outside, filepath.Join(journalRoot, testMachineID)))
	machineIDPath := filepath.Join(root, "machine-id")
	require.NoError(t, os.WriteFile(machineIDPath, []byte(testMachineID+"\n"), 0o600))
	client := NewClient(Target{JournalDirs: []string{journalRoot}, MachineIDPath: machineIDPath}, testVacuumPolicy())

	_, err := client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{
		Now:    now,
		Before: now.Add(-48 * time.Hour),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a real directory")
	assert.FileExists(t, archive)
}

func TestVacuumJournalRequiresPolicyAndBounds(t *testing.T) {
	now := time.Now()
	client, _ := newVacuumTestClient(t, nil)
	_, err := client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{Now: now, Before: now.Add(-time.Hour)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "policy is not configured")

	client, _ = newVacuumTestClient(t, testVacuumPolicy())
	_, err = client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{Now: now})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires a size or time limit")
}

func TestIsArchivedJournalName(t *testing.T) {
	for _, name := range []string{
		archivedJournalName(1),
		"system@tenant@abcdef0123456789abcdef0123456789-0000000000000001-0000000000000001.journal",
		"user-1000@abcdef0123456789abcdef0123456789-0000000000000001-0000000000000001.journal",
	} {
		assert.True(t, isArchivedJournalName(name), name)
	}
	for _, name := range []string{
		"system.journal",
		"user-1000.journal",
		"system@tenant.journal",
		"system@old.journal",
		"system@abcdef0123456789abcdef0123456789-1-1.journal",
		"system@abcdef0123456789abcdef0123456789-0000000000000001-0000000000000001.journal~",
		"../" + archivedJournalName(1),
	} {
		assert.False(t, isArchivedJournalName(name), name)
	}
}
