// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: inline_var (shell-feature)

package tests_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
)

func TestVulnHuntShellFeatureFileAccessBypass_InlineCatShortcutSandboxed(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	forbidden := filepath.Join(base, "forbidden")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(forbidden, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(forbidden, "secret.txt"), []byte("S3CR3T\n"), 0o644))

	script := "A=before; A=$(<../forbidden/secret.txt) echo HIT; echo after=[$A]\n"
	stdout, stderr, code := redirRunWithOpts(t, script, allowed,
		interp.AllowedPaths([]string{allowed}),
		interp.AllowedCommands([]string{"rshell:cat", "rshell:echo"}))

	assert.Equal(t, 0, code)
	assert.Equal(t, "HIT\nafter=[before]\n", stdout)
	assert.Contains(t, stderr, "permission denied")
	assert.NotContains(t, stdout, "S3CR3T")
}

func TestVulnHuntShellFeatureCompositionAttack_InlineCdPwdSpoofCannotEscapeSandbox(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	sub := filepath.Join(allowed, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	script := "PWD=/spoof cd sub\npwd\necho PWD=$PWD\ncd -\npwd\necho PWD=$PWD\n"
	stdout, stderr, code := redirRunWithOpts(t, script, allowed,
		interp.AllowedPaths([]string{allowed}),
		interpoption.AllowAllCommands().(interp.RunnerOption))

	assert.Equal(t, 0, code)
	assert.Equal(t, sub+"\nPWD="+sub+"\n"+sub+"\nPWD="+sub+"\n", stdout)
	assert.Contains(t, stderr, "cd: /spoof: permission denied")
}
