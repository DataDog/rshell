// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package systemd

import (
	"fmt"
	"time"
)

// JournalVacuumPolicy is a trusted operator ceiling for journal cleanup.
// Script-provided vacuum thresholds can only narrow this policy.
type JournalVacuumPolicy struct {
	MinRetentionAge  time.Duration
	MinRetainedFiles int
	MinRetainedBytes uint64
	MaxDeletedFiles  int
	MaxDeletedBytes  uint64
}

// Validate rejects policies that would leave repeated cleanup invocations
// unbounded. A positive age floor is mandatory even when file/byte floors are
// also configured.
func (policy JournalVacuumPolicy) Validate() error {
	if policy.MinRetentionAge <= 0 {
		return fmt.Errorf("minimum retention age must be greater than zero")
	}
	if policy.MinRetainedFiles < 0 || policy.MinRetainedFiles > maxJournalFiles {
		return fmt.Errorf("minimum retained files must be between 0 and %d", maxJournalFiles)
	}
	if policy.MaxDeletedFiles <= 0 || policy.MaxDeletedFiles > maxJournalFiles {
		return fmt.Errorf("maximum deleted files must be between 1 and %d", maxJournalFiles)
	}
	if policy.MaxDeletedBytes == 0 {
		return fmt.Errorf("maximum deleted bytes must be greater than zero")
	}
	return nil
}
