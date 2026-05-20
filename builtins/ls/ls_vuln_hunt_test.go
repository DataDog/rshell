// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Vulnerability-hunt regression tests for campaign 2026-05-20-gpt-5.5-cyber-3.
// These tests pin blocked attack paths only. Working exploit PoCs remain in the
// private vuln-hunt repository until a fix ships.

package ls_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVulnHuntBuiltinFlagDrivenExploit_InvalidPaginationBeforeHelpRejected(t *testing.T) {
	dir := t.TempDir()
	for _, script := range []string{
		"ls --offset nope --help",
		"ls --limit 999999999999999999999999999999 --help",
	} {
		t.Run(script, func(t *testing.T) {
			stdout, stderr, code := lsRun(t, script, dir)
			assert.Equal(t, 1, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "ls:")
			assert.NotContains(t, stdout, "Usage: ls")
		})
	}
}

func TestVulnHuntBuiltinFileAccessBypass_SymlinkToOutsideDirectoryNotListed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on some Windows setups")
	}
	allowed := t.TempDir()
	forbidden := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(forbidden, "secret.txt"), []byte("secret"), 0o644))
	require.NoError(t, os.Symlink(forbidden, filepath.Join(allowed, "escape")))

	stdout, stderr, code := lsRun(t, "ls escape", allowed)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "escape\n", stdout)

	stdout, stderr, code = lsRun(t, "ls escape/", allowed)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "escape/\n", stdout)
	assert.NotContains(t, stdout, "secret.txt")
}
