// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: command_substitution (shell-feature)

package tests_test

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/interp"
)

func TestVulnHuntShellFeatureRedirectionChain_CatShortcutGatePreventsFIFOOpen(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "in.fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))

	stdout, stderr, code := cmdSubstRunWithOpts(t,
		"x=$(<in.fifo)\necho \"[$x]\"\n",
		dir,
		interp.AllowedPaths([]string{dir}),
		interp.AllowedCommands([]string{"rshell:echo"}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "[]\n", stdout)
	assert.Contains(t, stderr, "cat not in allowed commands")
}
