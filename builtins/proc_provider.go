// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/rshell/builtins/internal/procinfo"
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

// ReadKernelFile reads a single-line value from a /proc/sys/kernel/ pseudo-file.
// name is the filename relative to sys/kernel/ (e.g. "ostype", "hostname").
// The returned value is trimmed of trailing whitespace.
func (p *ProcProvider) ReadKernelFile(name string) (string, error) {
	path := filepath.Join(p.path, "sys", "kernel", name)
	// Stat before opening to reject FIFOs and other blocking file types
	// without hanging in open(2). This prevents DoS when --proc-path
	// points at a non-proc tree with mkfifo'd entries.
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() && info.Mode().Type()&os.ModeCharDevice == 0 {
		// Allow regular files and char devices (proc pseudo-files appear as
		// char devices on some configurations). Reject FIFOs, sockets, etc.
		return "", fmt.Errorf("not a regular file: %s", path)
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	// Proc kernel files are tiny single-line values. Cap at 4 KiB to
	// prevent unbounded reads if --proc-path points at a non-proc tree.
	data, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), " \t\r\n"), nil
}
