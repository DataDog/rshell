// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package systemd

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveTargetDefaultsToLocalPaths(t *testing.T) {
	target, err := ResolveTarget(Target{})
	require.NoError(t, err)

	assert.Empty(t, target.Root)
	assert.Equal(t, []string{"/var/log/journal", "/run/log/journal"}, target.JournalDirs)
	assert.Equal(t, "/etc/machine-id", target.MachineIDPath)
	assert.Equal(t, "/run/systemd/journal/io.systemd.journal", target.JournalControlSocket)
	assert.Equal(t, "/run/dbus/system_bus_socket", target.SystemBusSocket)
}

func TestResolveTargetDerivesMountedRootPaths(t *testing.T) {
	target, err := ResolveTarget(Target{Root: "/host"})
	require.NoError(t, err)

	assert.Empty(t, target.Root)
	assert.Equal(t, filepath.FromSlash("/host"), target.root)
	assert.Equal(t, []string{filepath.FromSlash("/host/var/log/journal"), filepath.FromSlash("/host/run/log/journal")}, target.JournalDirs)
	assert.Equal(t, filepath.FromSlash("/host/etc/machine-id"), target.MachineIDPath)
	assert.Equal(t, filepath.FromSlash("/host/run/systemd/journal/io.systemd.journal"), target.JournalControlSocket)
	assert.Equal(t, filepath.FromSlash("/host/run/dbus/system_bus_socket"), target.SystemBusSocket)
}

func TestResolveTargetUsesOnlyExplicitPaths(t *testing.T) {
	target, err := ResolveTarget(Target{
		JournalDirs:   []string{"/mnt/logs", "/mnt/logs", "/mnt/runtime-logs/../runtime-logs"},
		MachineIDPath: "/mnt/etc/machine-id",
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"/mnt/logs", "/mnt/runtime-logs"}, target.JournalDirs)
	assert.Equal(t, "/mnt/etc/machine-id", target.MachineIDPath)
	assert.Empty(t, target.JournalControlSocket)
	assert.Empty(t, target.SystemBusSocket)
}

func TestResolveTargetRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name   string
		target Target
		needle string
	}{
		{
			name:   "root with explicit field",
			target: Target{Root: "/host", MachineIDPath: "/host/etc/machine-id"},
			needle: "cannot be combined",
		},
		{
			name:   "relative root",
			target: Target{Root: "host"},
			needle: "must be absolute",
		},
		{
			name:   "explicit without machine ID",
			target: Target{JournalDirs: []string{"/host/var/log/journal"}},
			needle: "machine ID path is required",
		},
		{
			name:   "relative journal directory",
			target: Target{JournalDirs: []string{"logs"}, MachineIDPath: "/etc/machine-id"},
			needle: "must be absolute",
		},
		{
			name:   "relative socket",
			target: Target{MachineIDPath: "/etc/machine-id", SystemBusSocket: "run/dbus/system_bus_socket"},
			needle: "must be absolute",
		},
		{
			name:   "NUL path",
			target: Target{MachineIDPath: "/etc/machine-id\x00suffix"},
			needle: "NUL byte",
		},
		{
			name: "too many journal directories",
			target: Target{
				JournalDirs:   []string{"/1", "/2", "/3", "/4", "/5", "/6", "/7", "/8", "/9"},
				MachineIDPath: "/etc/machine-id",
			},
			needle: "maximum is 8",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ResolveTarget(test.target)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.needle)
		})
	}
}
