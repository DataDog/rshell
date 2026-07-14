//go:build linux && cgo

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package systemd

import (
	"context"
	"fmt"

	"github.com/coreos/go-systemd/v22/sdjournal"

	"github.com/DataDog/rshell/builtins"
)

// ReadJournal opens only regular journal files belonging to the configured
// target machine and executes a bounded structured query against them.
func (c *Client) ReadJournal(ctx context.Context, query builtins.JournalQuery, yield func(builtins.JournalEntry) error) error {
	if err := validateJournalQuery(query); err != nil {
		return err
	}
	if query.MaxEntries == 0 {
		return nil
	}
	machineID, files, err := c.journalFiles()
	if err != nil {
		return err
	}
	journal, err := sdjournal.NewJournalFromFiles(files...)
	if err != nil {
		return fmt.Errorf("open systemd journal: %w", err)
	}
	defer journal.Close()
	return readJournal(ctx, journal, machineID, query, yield)
}
