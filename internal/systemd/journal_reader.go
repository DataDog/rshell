// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package systemd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/DataDog/rshell/builtins"
)

const (
	maxJournalFieldSize = 64 * 1024
	maxBootSearch       = 1024
)

type journalHandle interface {
	AddMatch(match string) error
	AddDisjunction() error
	AddConjunction() error
	FlushMatches()
	Next() (uint64, error)
	Previous() (uint64, error)
	PreviousSkip(skip uint64) (uint64, error)
	GetData(field string) (string, error)
	GetRealtimeUsec() (uint64, error)
	SetDataThreshold(threshold uint64) error
	SeekHead() error
	SeekTail() error
}

func validateJournalQuery(query builtins.JournalQuery) error {
	if query.MaxEntries < 0 || query.MaxEntries > builtins.MaxJournalQueryEntries {
		return fmt.Errorf("journal query entry limit must be between 0 and %d", builtins.MaxJournalQueryEntries)
	}
	if query.Kernel && len(query.Units) > 0 {
		return fmt.Errorf("journal query cannot combine kernel and unit scopes")
	}
	if !query.Kernel && len(query.Units) == 0 {
		return fmt.Errorf("journal query requires a kernel or unit scope")
	}
	if len(query.Units) > builtins.MaxJournalQueryUnits {
		return fmt.Errorf("journal query has too many units (maximum %d)", builtins.MaxJournalQueryUnits)
	}
	for _, unit := range query.Units {
		if unit == "" || len(unit) > 256 || strings.IndexByte(unit, 0) >= 0 {
			return fmt.Errorf("journal query contains an invalid unit name")
		}
	}
	return nil
}

func readJournal(ctx context.Context, journal journalHandle, machineID string, query builtins.JournalQuery, yield func(builtins.JournalEntry) error) error {
	if err := validateJournalQuery(query); err != nil {
		return err
	}
	if query.MaxEntries == 0 {
		return nil
	}
	if err := journal.SetDataThreshold(maxJournalFieldSize + 64); err != nil {
		return fmt.Errorf("set journal field limit: %w", err)
	}

	bootID := ""
	if query.CurrentBoot {
		var err error
		bootID, err = newestBootID(ctx, journal, machineID)
		if err != nil {
			return err
		}
		journal.FlushMatches()
	}
	if err := addJournalMatches(journal, machineID, bootID, query); err != nil {
		return err
	}
	if err := seekJournalTail(journal, query.MaxEntries); err != nil {
		return err
	}

	for scanned := 0; scanned < query.MaxEntries; scanned++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		advanced, err := journal.Next()
		if err != nil {
			return fmt.Errorf("iterate journal: %w", err)
		}
		if advanced == 0 {
			return nil
		}

		entry, err := selectedJournalEntry(journal, machineID)
		if err != nil {
			return err
		}
		if !query.Since.IsZero() && entry.Timestamp.Before(query.Since) {
			continue
		}
		if err := yield(entry); err != nil {
			return err
		}
	}
	return nil
}

func newestBootID(ctx context.Context, journal journalHandle, machineID string) (string, error) {
	if err := journal.AddMatch("_MACHINE_ID=" + machineID); err != nil {
		return "", fmt.Errorf("match journal machine ID: %w", err)
	}
	if err := journal.SeekTail(); err != nil {
		return "", fmt.Errorf("seek journal tail: %w", err)
	}
	for i := 0; i < maxBootSearch; i++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		advanced, err := journal.Previous()
		if err != nil {
			return "", fmt.Errorf("search current journal boot: %w", err)
		}
		if advanced == 0 {
			break
		}
		bootID, found, err := journalData(journal, "_BOOT_ID")
		if err != nil {
			return "", err
		}
		if found && validID128(bootID) {
			return strings.ToLower(bootID), nil
		}
	}
	return "", fmt.Errorf("could not determine the current boot from the selected journal")
}

func addJournalMatches(journal journalHandle, machineID, bootID string, query builtins.JournalQuery) error {
	if query.Kernel {
		if err := journal.AddMatch("_TRANSPORT=kernel"); err != nil {
			return fmt.Errorf("match kernel journal entries: %w", err)
		}
	} else {
		for _, unit := range query.Units {
			if err := journal.AddMatch("_SYSTEMD_UNIT=" + unit); err != nil {
				return fmt.Errorf("match journal unit: %w", err)
			}
		}
		if err := journal.AddDisjunction(); err != nil {
			return fmt.Errorf("combine journal unit matches: %w", err)
		}
		if err := journal.AddMatch("_PID=1"); err != nil {
			return fmt.Errorf("match system manager journal entries: %w", err)
		}
		for _, unit := range query.Units {
			if err := journal.AddMatch("UNIT=" + unit); err != nil {
				return fmt.Errorf("match manager messages about journal unit: %w", err)
			}
		}
		if err := journal.AddConjunction(); err != nil {
			return fmt.Errorf("constrain journal unit matches: %w", err)
		}
	}
	if err := journal.AddMatch("_MACHINE_ID=" + machineID); err != nil {
		return fmt.Errorf("match journal machine ID: %w", err)
	}
	if bootID != "" {
		if err := journal.AddMatch("_BOOT_ID=" + bootID); err != nil {
			return fmt.Errorf("match current journal boot: %w", err)
		}
	}
	return nil
}

func seekJournalTail(journal journalHandle, entries int) error {
	if err := journal.SeekTail(); err != nil {
		return fmt.Errorf("seek journal tail: %w", err)
	}
	skipped, err := journal.PreviousSkip(uint64(entries) + 1)
	if err != nil {
		return fmt.Errorf("seek to last %d journal entries: %w", entries, err)
	}
	if skipped != uint64(entries)+1 {
		if err := journal.SeekHead(); err != nil {
			return fmt.Errorf("seek journal head: %w", err)
		}
	}
	return nil
}

func selectedJournalEntry(journal journalHandle, machineID string) (builtins.JournalEntry, error) {
	entryMachineID, found, err := journalData(journal, "_MACHINE_ID")
	if err != nil {
		return builtins.JournalEntry{}, err
	}
	if !found || entryMachineID != machineID {
		return builtins.JournalEntry{}, fmt.Errorf("journal entry machine ID does not match the configured target")
	}

	realtimeUsec, err := journal.GetRealtimeUsec()
	if err != nil {
		return builtins.JournalEntry{}, fmt.Errorf("read journal timestamp: %w", err)
	}
	entry := builtins.JournalEntry{
		Timestamp: time.Unix(int64(realtimeUsec/1_000_000), int64(realtimeUsec%1_000_000)*1000),
	}
	if entry.Hostname, _, err = journalData(journal, "_HOSTNAME"); err != nil {
		return builtins.JournalEntry{}, err
	}
	if entry.Identifier, _, err = journalData(journal, "SYSLOG_IDENTIFIER"); err != nil {
		return builtins.JournalEntry{}, err
	}
	if entry.Identifier == "" {
		if entry.Identifier, _, err = journalData(journal, "_COMM"); err != nil {
			return builtins.JournalEntry{}, err
		}
	}
	if entry.PID, _, err = journalData(journal, "_PID"); err != nil {
		return builtins.JournalEntry{}, err
	}
	if entry.Message, _, err = journalData(journal, "MESSAGE"); err != nil {
		return builtins.JournalEntry{}, err
	}
	return entry, nil
}

func journalData(journal journalHandle, field string) (string, bool, error) {
	data, err := journal.GetData(field)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read journal field %s: %w", field, err)
	}
	prefix := field + "="
	if !strings.HasPrefix(data, prefix) {
		return "", false, fmt.Errorf("journal returned malformed field %s", field)
	}
	value := strings.TrimPrefix(data, prefix)
	if len(value) > maxJournalFieldSize {
		value = value[:maxJournalFieldSize]
	}
	return value, true, nil
}
