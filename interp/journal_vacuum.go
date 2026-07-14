// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"fmt"

	internalsystemd "github.com/DataDog/rshell/internal/systemd"
)

// JournalVacuumPolicy is the trusted retention floor and per-invocation
// deletion ceiling used by journal cleanup operations.
type JournalVacuumPolicy = internalsystemd.JournalVacuumPolicy

// WithJournalVacuumPolicy enables bounded journal cleanup. Without this
// option, cleanup remains disabled even when journal:storage/clean is granted.
func WithJournalVacuumPolicy(policy JournalVacuumPolicy) RunnerOption {
	return func(r *Runner) error {
		if err := policy.Validate(); err != nil {
			return fmt.Errorf("WithJournalVacuumPolicy: %w", err)
		}
		policyCopy := policy
		r.journalVacuumPolicy = &policyCopy
		return nil
	}
}
