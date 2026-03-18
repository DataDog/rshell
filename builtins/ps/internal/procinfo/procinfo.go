// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package procinfo provides OS-specific process information for the ps builtin.
//
// This package is in builtins/internal/ and is therefore exempt from the
// builtinAllowedSymbols allowlist check. It may use OS-specific APIs freely.
package procinfo

import "context"

// MaxProcesses caps slice allocation when listing all processes.
const MaxProcesses = 10_000

// MaxCmdLen caps the cmdline string length.
const MaxCmdLen = 4096

// ProcInfo holds information about a single process.
type ProcInfo struct {
	PID   int
	PPID  int
	UID   string // username or numeric UID string
	State string // single char: R, S, D, Z, T, ...
	TTY   string // "?" if no controlling terminal
	CPU   int    // %CPU (always 0 for simplicity)
	STime string // start time (HH:MM or Mon DD)
	Time  string // cumulative CPU time HH:MM:SS
	Cmd   string // full cmdline, truncated to MaxCmdLen
}

// ListAll returns all running processes.
func ListAll(ctx context.Context) ([]ProcInfo, error) {
	return listAll(ctx)
}

// GetSession returns processes in the current process session
// (walks PPID chain from os.Getpid() upward to collect ancestors, plus
// any processes that share the same session ID when available).
func GetSession(ctx context.Context) ([]ProcInfo, error) {
	return getSession(ctx)
}

// GetByPIDs returns process info for the given PIDs.
// Missing PIDs are silently skipped.
func GetByPIDs(ctx context.Context, pids []int) ([]ProcInfo, error) {
	return getByPIDs(ctx, pids)
}
