// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import (
	"context"

	"github.com/DataDog/rshell/builtins/internal/procinfo"
	"github.com/DataDog/rshell/builtins/internal/procmaps"
	"github.com/DataDog/rshell/builtins/internal/procsyskernel"
)

// ProcProvider gives builtins controlled access to the proc filesystem.
// The path is fixed at construction time and cannot be overridden by callers.
type ProcProvider struct {
	path string
}

// NewProcProvider returns a ProcProvider for the given proc filesystem path.
// If path is empty, DefaultProcPath ("/proc") is used.
func NewProcProvider(path string) *ProcProvider {
	if path == "" {
		path = procinfo.DefaultProcPath
	}
	return &ProcProvider{path: path}
}

// ProcPath returns the configured proc filesystem path (e.g. "/proc" or "/host/proc").
func (p *ProcProvider) ProcPath() string {
	return p.path
}

// ListAll returns all running processes.
func (p *ProcProvider) ListAll(ctx context.Context) ([]procinfo.ProcInfo, error) {
	return procinfo.ListAll(ctx, p.path)
}

// GetSession returns processes in the current process session.
func (p *ProcProvider) GetSession(ctx context.Context) ([]procinfo.ProcInfo, error) {
	return procinfo.GetSession(ctx, p.path)
}

// GetByPIDs returns process info for the given PIDs.
func (p *ProcProvider) GetByPIDs(ctx context.Context, pids []int) ([]procinfo.ProcInfo, error) {
	return procinfo.GetByPIDs(ctx, p.path, pids)
}

// ReadMaps returns the short process name and current memory mappings for
// pid, for the pmap builtin. When extended is true, per-mapping RSS and
// Dirty are populated if the platform backend supports it.
func (p *ProcProvider) ReadMaps(ctx context.Context, pid int, extended bool) (string, []procmaps.Mapping, error) {
	return procmaps.Read(ctx, p.path, pid, extended)
}

// ReadKernelFile reads a single-line value from a /proc/sys/kernel/ pseudo-file.
// name is the filename relative to sys/kernel/ (e.g. "ostype", "hostname").
// The returned value is trimmed of trailing whitespace.
func (p *ProcProvider) ReadKernelFile(name string) (string, error) {
	return procsyskernel.ReadFile(p.path, name)
}
