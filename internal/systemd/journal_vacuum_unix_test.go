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

func corruptedJournalName(index int) string {
	return fmt.Sprintf("system@%016x-%016x.journal~", index, index)
}

func newVacuumTestClient(t *testing.T) (*Client, string) {
	t.Helper()
	root := t.TempDir()
	machineIDPath := filepath.Join(root, "machine-id")
	journalRoot := filepath.Join(root, "journal")
	machineDir := filepath.Join(journalRoot, testMachineID)
	require.NoError(t, os.WriteFile(machineIDPath, []byte(testMachineID+"\n"), 0o600))
	require.NoError(t, os.MkdirAll(machineDir, 0o700))
	return NewClient(Target{JournalDirs: []string{journalRoot}, MachineIDPath: machineIDPath}), machineDir
}

func writeVacuumFile(t *testing.T, directory, name string, modTime time.Time) string {
	t.Helper()
	return writeVacuumFileOfSize(t, directory, name, modTime, 8192)
}

func writeVacuumFileOfSize(t *testing.T, directory, name string, modTime time.Time, size int) string {
	t.Helper()
	path := filepath.Join(directory, name)
	require.NoError(t, os.WriteFile(path, make([]byte, size), 0o600))
	require.NoError(t, os.Chtimes(path, modTime, modTime))
	return path
}

func allocatedBytesOf(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Lstat(path)
	require.NoError(t, err)
	stat, err := journalStat(info)
	require.NoError(t, err)
	require.Greater(t, stat.allocated, uint64(0))
	return stat.allocated
}

func TestVacuumJournalDeletesOldestArchivesWithinRequest(t *testing.T) {
	now := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	client, directory := newVacuumTestClient(t)
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
	client, directory := newVacuumTestClient(t)
	archive := writeVacuumFile(t, directory, archivedJournalName(1), now.Add(-7*24*time.Hour))
	second := writeVacuumFile(t, directory, archivedJournalName(2), now.Add(-6*24*time.Hour))

	result, err := client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{
		Now:    now,
		Before: now.Add(-48 * time.Hour),
		DryRun: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Files)
	assert.FileExists(t, archive)
	assert.FileExists(t, second)
}

func TestVacuumJournalDeletesCorruptionArchivesWithinRequest(t *testing.T) {
	now := time.Now()
	client, directory := newVacuumTestClient(t)
	old := writeVacuumFile(t, directory, corruptedJournalName(1), now.Add(-7*24*time.Hour))
	recent := writeVacuumFile(t, directory, corruptedJournalName(2), now.Add(-time.Hour))
	malformed := writeVacuumFile(t, directory, archivedJournalName(3)+"~", now.Add(-7*24*time.Hour))

	result, err := client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{
		Now:    now,
		Before: now.Add(-48 * time.Hour),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Files)
	assert.NoFileExists(t, old)
	assert.FileExists(t, recent)
	assert.FileExists(t, malformed)
}

func TestVacuumJournalHonorsAllocatedSizeTargetAndTimeFloor(t *testing.T) {
	now := time.Now()
	client, directory := newVacuumTestClient(t)
	oldest := writeVacuumFile(t, directory, archivedJournalName(1), now.Add(-10*24*time.Hour))
	second := writeVacuumFile(t, directory, archivedJournalName(2), now.Add(-9*24*time.Hour))
	recent := writeVacuumFile(t, directory, archivedJournalName(3), now.Add(-time.Hour))
	info, err := os.Lstat(oldest)
	require.NoError(t, err)
	stat, err := journalStat(info)
	require.NoError(t, err)
	require.Greater(t, stat.allocated, uint64(1))

	result, err := client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{
		Now:      now,
		MaxBytes: stat.allocated - 1,
		Before:   now.Add(-48 * time.Hour),
	})
	require.NoError(t, err)
	assert.Equal(t, 2, result.Files)
	assert.NoFileExists(t, oldest)
	assert.NoFileExists(t, second)
	assert.FileExists(t, recent)
}

func TestVacuumJournalCombinedCleanupStopsAtSizeTarget(t *testing.T) {
	now := time.Now()
	client, directory := newVacuumTestClient(t)
	archive := writeVacuumFile(t, directory, archivedJournalName(1), now.Add(-10*24*time.Hour))
	info, err := os.Lstat(archive)
	require.NoError(t, err)
	stat, err := journalStat(info)
	require.NoError(t, err)

	result, err := client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{
		Now:      now,
		MaxBytes: stat.allocated,
		Before:   now.Add(-48 * time.Hour),
	})
	require.NoError(t, err)
	assert.Zero(t, result.Files)
	assert.FileExists(t, archive)
}

// The size target measures total allocated journal storage (active plus
// archived), matching --disk-usage and host journalctl --vacuum-size, rather
// than archived bytes alone.
func TestVacuumJournalSizeTargetCountsActiveAndArchivedAllocation(t *testing.T) {
	now := time.Now()
	client, directory := newVacuumTestClient(t)
	active := writeVacuumFileOfSize(t, directory, "system.journal", now.Add(-30*24*time.Hour), 5*8192)
	oldest := writeVacuumFile(t, directory, archivedJournalName(1), now.Add(-10*24*time.Hour))
	second := writeVacuumFile(t, directory, archivedJournalName(2), now.Add(-9*24*time.Hour))
	activeAllocated := allocatedBytesOf(t, active)
	oldestAllocated := allocatedBytesOf(t, oldest)
	secondAllocated := allocatedBytesOf(t, second)
	// Archived bytes alone (oldest+second) are already below this target, so the
	// pre-fix archived-only accounting would have deleted nothing at all.
	maxBytes := activeAllocated + secondAllocated
	require.Less(t, oldestAllocated+secondAllocated, maxBytes)

	result, err := client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{
		Now:      now,
		MaxBytes: maxBytes,
		Before:   now.Add(-48 * time.Hour),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Files)
	assert.Equal(t, oldestAllocated, result.Bytes)
	assert.Equal(t, maxBytes, result.RemainingBytes)
	assert.NoFileExists(t, oldest)
	assert.FileExists(t, second)
	assert.FileExists(t, active)
}

func TestVacuumJournalNeverDeletesActiveJournalsForSizeTarget(t *testing.T) {
	now := time.Now()
	client, directory := newVacuumTestClient(t)
	active := writeVacuumFileOfSize(t, directory, "system.journal", now.Add(-30*24*time.Hour), 5*8192)
	namespaceActive := writeVacuumFile(t, directory, "system@tenant.journal", now.Add(-30*24*time.Hour))
	archive := writeVacuumFile(t, directory, archivedJournalName(1), now.Add(-10*24*time.Hour))
	remaining := allocatedBytesOf(t, active) + allocatedBytesOf(t, namespaceActive)

	result, err := client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{
		Now:      now,
		MaxBytes: 1,
		Before:   now.Add(-48 * time.Hour),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Files)
	assert.FileExists(t, active)
	assert.FileExists(t, namespaceActive)
	assert.NoFileExists(t, archive)
	// The target is unreachable without deleting active journals; the result
	// reports the remaining allocation instead of implying success.
	assert.Equal(t, remaining, result.RemainingBytes)
	assert.Greater(t, result.RemainingBytes, uint64(1))
}

func TestVacuumJournalTimeCutoffOutranksSizeTarget(t *testing.T) {
	now := time.Now()
	client, directory := newVacuumTestClient(t)
	active := writeVacuumFileOfSize(t, directory, "system.journal", now.Add(-30*24*time.Hour), 5*8192)
	recent := writeVacuumFile(t, directory, archivedJournalName(1), now.Add(-time.Hour))

	result, err := client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{
		Now:      now,
		MaxBytes: 1,
		Before:   now.Add(-48 * time.Hour),
	})
	require.NoError(t, err)
	assert.Zero(t, result.Files)
	assert.Zero(t, result.Bytes)
	assert.Equal(t, allocatedBytesOf(t, active)+allocatedBytesOf(t, recent), result.RemainingBytes)
	assert.FileExists(t, active)
	assert.FileExists(t, recent)
}

func TestVacuumJournalSkipsHardlinksAndSymlinks(t *testing.T) {
	now := time.Now()
	client, directory := newVacuumTestClient(t)
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
	client := NewClient(Target{JournalDirs: []string{journalRoot}, MachineIDPath: machineIDPath})

	_, err := client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{
		Now:    now,
		Before: now.Add(-48 * time.Hour),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a real directory")
	assert.FileExists(t, archive)
}

func TestVacuumJournalRequiresRequestBounds(t *testing.T) {
	now := time.Now()
	client, _ := newVacuumTestClient(t)
	_, err := client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{Before: now.Add(-time.Hour)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reference time")

	_, err = client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{Now: now})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires a size or time limit")

	_, err = client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{Now: now, MaxBytes: 1})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "size vacuum requires a time cutoff")

	_, err = client.VacuumJournal(context.Background(), builtins.JournalVacuumRequest{Now: now, Before: now.Add(time.Hour)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be in the future")
}

func TestIsArchivedJournalName(t *testing.T) {
	for _, name := range []string{
		archivedJournalName(1),
		"system@tenant@abcdef0123456789abcdef0123456789-0000000000000001-0000000000000001.journal",
		"user-1000@abcdef0123456789abcdef0123456789-0000000000000001-0000000000000001.journal",
		corruptedJournalName(1),
		"system@tenant@0000000000000001-0000000000000001.journal~",
		"user-1000@0000000000000001-0000000000000001.journal~",
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
		"system@0000000000000001-1.journal~",
		"system@0000000000000001-0000000000000001.journal~~",
		"../" + archivedJournalName(1),
	} {
		assert.False(t, isArchivedJournalName(name), name)
	}
}
