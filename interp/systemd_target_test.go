// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package interp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithSystemdTargetDefaultsToLocal(t *testing.T) {
	runner, err := New()
	require.NoError(t, err)
	defer runner.Close()

	assert.Equal(t, []string{"/var/log/journal", "/run/log/journal"}, runner.systemdTarget.JournalDirs)
	assert.Equal(t, "/etc/machine-id", runner.systemdTarget.MachineIDPath)
}

func TestWithSystemdTargetCopiesConfiguration(t *testing.T) {
	dirs := []string{"/host/var/log/journal"}
	runner, err := New(WithSystemdTarget(SystemdTargetConfig{
		JournalDirs:   dirs,
		MachineIDPath: "/host/etc/machine-id",
	}))
	require.NoError(t, err)
	defer runner.Close()

	dirs[0] = "/changed"
	assert.Equal(t, []string{"/host/var/log/journal"}, runner.systemdTarget.JournalDirs)
	assert.Empty(t, runner.systemdTarget.SystemBusSocket)
}

func TestWithSystemdTargetRejectsMixedRootAndExplicitPaths(t *testing.T) {
	runner, err := New(WithSystemdTarget(SystemdTargetConfig{
		Root:        "/host",
		JournalDirs: []string{"/host/var/log/journal"},
	}))
	if runner != nil {
		runner.Close()
	}
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be combined")
}
