// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import (
	"context"
	"errors"
	"time"
)

// ErrSystemdUnsupported reports that the current platform cannot provide a
// requested systemd operation.
var ErrSystemdUnsupported = errors.New("systemd operation is not supported")

const (
	// MaxJournalQueryEntries is the hard per-invocation entry bound shared by
	// journal builtins and backends.
	MaxJournalQueryEntries = 1000
	// MaxJournalQueryUnits bounds exact unit scopes before any backend work.
	MaxJournalQueryUnits = 32
)

// JournalQuery is the bounded, structured query accepted by the trusted
// journal backend. Callers cannot provide raw journal matches or paths.
type JournalQuery struct {
	Units       []string
	Kernel      bool
	CurrentBoot bool
	Since       time.Time
	MaxEntries  int
}

// JournalEntry contains only the fields a journalctl builtin may expose. The
// backend deliberately does not return arbitrary journal fields.
type JournalEntry struct {
	Timestamp  time.Time
	Hostname   string
	Identifier string
	PID        string
	Message    string
}

// JournalReader reads a bounded journal query and yields entries oldest first.
type JournalReader interface {
	ReadJournal(ctx context.Context, query JournalQuery, yield func(JournalEntry) error) error
}

// JournalUsage is the allocated storage consumed by the selected target's
// active and archived journal files.
type JournalUsage struct {
	Bytes uint64
	Files int
}

// JournalStorageReader exposes read-only journal storage metadata.
type JournalStorageReader interface {
	JournalDiskUsage(ctx context.Context) (JournalUsage, error)
}

// JournalVacuumRequest contains only bounded cleanup predicates. Before is an
// absolute archive mtime cutoff; MaxBytes is an allocated archived-byte target.
type JournalVacuumRequest struct {
	Now      time.Time
	Before   time.Time
	MaxBytes uint64
	DryRun   bool
}

// JournalVacuumResult reports the cleanup selected or completed without
// exposing host paths or journal filenames.
type JournalVacuumResult struct {
	Files int
	Bytes uint64
}

// JournalCleaner performs policy-bounded cleanup of archived journal files.
type JournalCleaner interface {
	VacuumJournal(ctx context.Context, request JournalVacuumRequest) (JournalVacuumResult, error)
}

// JournalRotator synchronously archives the active journals for the selected
// target. Implementations return only after journald reports completion.
type JournalRotator interface {
	RotateJournal(ctx context.Context) error
}

// SystemdServices contains the trusted backends available to systemd-aware
// builtins. Additional manager and journal-maintenance interfaces can be added
// here without exposing transports to command implementations.
type SystemdServices struct {
	Journal        JournalReader
	JournalStorage JournalStorageReader
	JournalCleaner JournalCleaner
	JournalRotator JournalRotator
}
