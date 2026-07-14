// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package systemd

import (
	"context"
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins"
)

type fakeJournalEntry struct {
	realtimeUsec uint64
	fields       map[string]string
}

type fakeJournal struct {
	calls      []string
	entries    []fakeJournalEntry
	position   int
	threshold  uint64
	dataFields []string
}

func (j *fakeJournal) AddMatch(match string) error {
	j.calls = append(j.calls, "match "+match)
	return nil
}

func (j *fakeJournal) AddDisjunction() error {
	j.calls = append(j.calls, "or")
	return nil
}

func (j *fakeJournal) AddConjunction() error {
	j.calls = append(j.calls, "and")
	return nil
}

func (j *fakeJournal) FlushMatches() {
	j.calls = append(j.calls, "flush")
}

func (j *fakeJournal) Next() (uint64, error) {
	if j.position+1 >= len(j.entries) {
		j.position = len(j.entries)
		return 0, nil
	}
	j.position++
	return 1, nil
}

func (j *fakeJournal) Previous() (uint64, error) {
	if j.position <= 0 {
		j.position = -1
		return 0, nil
	}
	j.position--
	return 1, nil
}

func (j *fakeJournal) PreviousSkip(skip uint64) (uint64, error) {
	available := j.position
	if available < 0 {
		available = 0
	}
	actual := int(skip)
	if actual > available {
		actual = available
	}
	j.position -= actual
	return uint64(actual), nil
}

func (j *fakeJournal) GetData(field string) (string, error) {
	j.dataFields = append(j.dataFields, field)
	if j.position < 0 || j.position >= len(j.entries) {
		return "", fmt.Errorf("no current entry")
	}
	value, ok := j.entries[j.position].fields[field]
	if !ok {
		return "", fmt.Errorf("missing field: %w", syscall.ENOENT)
	}
	return field + "=" + value, nil
}

func (j *fakeJournal) GetRealtimeUsec() (uint64, error) {
	if j.position < 0 || j.position >= len(j.entries) {
		return 0, fmt.Errorf("no current entry")
	}
	return j.entries[j.position].realtimeUsec, nil
}

func (j *fakeJournal) SetDataThreshold(threshold uint64) error {
	j.threshold = threshold
	return nil
}

func (j *fakeJournal) SeekHead() error {
	j.calls = append(j.calls, "head")
	j.position = -1
	return nil
}

func (j *fakeJournal) SeekTail() error {
	j.calls = append(j.calls, "tail")
	j.position = len(j.entries)
	return nil
}

func TestAddJournalMatchesUsesRestrictedUnitExpansion(t *testing.T) {
	journal := &fakeJournal{}
	err := addJournalMatches(journal, "machine", "boot", builtins.JournalQuery{
		Units: []string{"api.service", "worker.service"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"match _SYSTEMD_UNIT=api.service",
		"match _SYSTEMD_UNIT=worker.service",
		"or",
		"match _PID=1",
		"match UNIT=api.service",
		"match UNIT=worker.service",
		"and",
		"match _MACHINE_ID=machine",
		"match _BOOT_ID=boot",
	}, journal.calls)
}

func TestAddJournalMatchesConstrainsKernelToTarget(t *testing.T) {
	journal := &fakeJournal{}
	err := addJournalMatches(journal, "machine", "boot", builtins.JournalQuery{Kernel: true})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"match _TRANSPORT=kernel",
		"match _MACHINE_ID=machine",
		"match _BOOT_ID=boot",
	}, journal.calls)
}

func TestReadJournalYieldsOnlySelectedFields(t *testing.T) {
	machineID := "0123456789abcdef0123456789abcdef"
	start := time.Unix(1_700_000_000, 0)
	journal := &fakeJournal{
		position: -1,
		entries: []fakeJournalEntry{
			{
				realtimeUsec: uint64(start.Add(-time.Minute).UnixMicro()),
				fields: map[string]string{
					"_MACHINE_ID":       machineID,
					"_HOSTNAME":         "host",
					"SYSLOG_IDENTIFIER": "api",
					"_PID":              "12",
					"MESSAGE":           "old",
					"_CMDLINE":          "secret-token",
				},
			},
			{
				realtimeUsec: uint64(start.UnixMicro()),
				fields: map[string]string{
					"_MACHINE_ID":       machineID,
					"_HOSTNAME":         "host",
					"SYSLOG_IDENTIFIER": "api",
					"_PID":              "13",
					"MESSAGE":           "ready",
				},
			},
		},
	}

	var entries []builtins.JournalEntry
	err := readJournal(context.Background(), journal, machineID, builtins.JournalQuery{
		Units:      []string{"api.service"},
		Since:      start,
		MaxEntries: 2,
	}, func(entry builtins.JournalEntry) error {
		entries = append(entries, entry)
		return nil
	})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "ready", entries[0].Message)
	assert.Equal(t, uint64(maxJournalFieldSize+64), journal.threshold)
	assert.NotContains(t, journal.dataFields, "_CMDLINE")
	assert.Contains(t, journal.calls, "head")
}

func TestReadJournalFindsCurrentBootInTargetJournal(t *testing.T) {
	machineID := "0123456789abcdef0123456789abcdef"
	bootID := "abcdef0123456789abcdef0123456789"
	journal := &fakeJournal{
		position: -1,
		entries: []fakeJournalEntry{{
			realtimeUsec: 1,
			fields: map[string]string{
				"_MACHINE_ID": machineID,
				"_BOOT_ID":    bootID,
				"MESSAGE":     "booted",
			},
		}},
	}

	err := readJournal(context.Background(), journal, machineID, builtins.JournalQuery{
		Kernel:      true,
		CurrentBoot: true,
		MaxEntries:  1,
	}, func(builtins.JournalEntry) error { return nil })
	require.NoError(t, err)
	assert.Contains(t, journal.calls, "flush")
	assert.Contains(t, journal.calls, "match _BOOT_ID="+bootID)
}

func TestReadJournalRejectsEntryFromAnotherMachine(t *testing.T) {
	journal := &fakeJournal{
		position: -1,
		entries: []fakeJournalEntry{{
			realtimeUsec: 1,
			fields: map[string]string{
				"_MACHINE_ID": "ffffffffffffffffffffffffffffffff",
			},
		}},
	}
	err := readJournal(context.Background(), journal, "0123456789abcdef0123456789abcdef", builtins.JournalQuery{
		Kernel:     true,
		MaxEntries: 1,
	}, func(builtins.JournalEntry) error { return nil })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not match")
}

func TestValidateJournalQueryRejectsUnboundedOrMixedScopes(t *testing.T) {
	for _, query := range []builtins.JournalQuery{
		{MaxEntries: builtins.MaxJournalQueryEntries + 1, Kernel: true},
		{MaxEntries: 1},
		{MaxEntries: 1, Kernel: true, Units: []string{"api.service"}},
	} {
		require.Error(t, validateJournalQuery(query))
	}
}
