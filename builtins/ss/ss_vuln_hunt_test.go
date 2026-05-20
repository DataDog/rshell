// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Vulnerability-hunt regression tests for campaign 2026-05-20-gpt-5.5-cyber-3.

package ss_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

func TestVulnHuntBuiltinFlagDrivenExploit_DangerousFlagsBeforeHelpRejected(t *testing.T) {
	tests := []string{
		"ss -F /etc/passwd --help",
		"ss --filter=/etc/passwd -h",
		"ss --processes --help",
		"ss -K -h",
	}
	for _, script := range tests {
		t.Run(script, func(t *testing.T) {
			stdout, stderr, code := cmdRun(t, script)
			assert.Equal(t, 1, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "ss:")
		})
	}
}

func TestVulnHuntBuiltinFlagDrivenExploit_RejectedAliasMatrix(t *testing.T) {
	tests := []string{
		"ss --processes",
		"ss --kill",
		"ss --events",
		"ss --net ns0",
		"ss --bpf",
		"ss --resolve",
		"ss -m",
		"ss -z",
		"ss -d",
		"ss -w",
		"ss -S",
		"ss -0",
	}
	for _, script := range tests {
		t.Run(script, func(t *testing.T) {
			_, stderr, code := cmdRun(t, script)
			assert.Equal(t, 1, code)
			assert.Contains(t, stderr, "ss:")
		})
	}
}

func TestVulnHuntBuiltinGTFObinsCoverage_FilterFormsRejectWithoutLeakingMarker(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "secret.txt")
	const marker = "VULN_HUNT_SS_FILTER_MARKER"
	require.NoError(t, os.WriteFile(secret, []byte(marker), 0o600))

	directScripts := []string{
		fmt.Sprintf("ss --filter=%q", secret),
		fmt.Sprintf("ss -F %q", secret),
	}
	for _, script := range directScripts {
		t.Run(script, func(t *testing.T) {
			stdout, stderr, code := cmdRun(t, script)
			assert.Equal(t, 1, code)
			assert.NotContains(t, stdout, marker)
			assert.NotContains(t, stderr, marker)
			assert.Contains(t, stderr, "ss:")
		})
	}

	stdout, stderr, code := cmdRun(t, fmt.Sprintf(`flag=--filter; ss $flag %q; echo status:$?`, secret))
	require.Equal(t, 0, code)
	assert.Contains(t, stdout, "status:1")
	assert.NotContains(t, stdout, marker)
	assert.NotContains(t, stderr, marker)
	assert.Contains(t, stderr, "ss:")
}

func TestVulnHuntBuiltinFileAccessBypass_PositionalsIgnoredUnderRestrictedAllowedPaths(t *testing.T) {
	outsideDir := t.TempDir()
	allowedDir := t.TempDir()
	secret := filepath.Join(outsideDir, "outside.txt")
	const marker = "VULN_HUNT_SS_POSITIONAL_MARKER"
	require.NoError(t, os.WriteFile(secret, []byte(marker), 0o600))

	stdout, stderr, code := runScript(
		t,
		fmt.Sprintf(`ss -- %q; echo status:$?`, secret),
		"",
		interp.AllowedPaths([]string{allowedDir}),
	)
	require.Equal(t, 0, code)
	assert.Contains(t, stdout, "status:")
	assert.NotContains(t, stdout, marker)
	assert.NotContains(t, stderr, marker)
}

func TestVulnHuntBuiltinSpecialFiles_PositionalsDoNotReadDevZeroOrDirectories(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stdout, stderr, code := testutil.RunScriptCtx(ctx, t, fmt.Sprintf(`ss -- /dev/zero %q; echo status:$?`, dir), "")
	require.Equal(t, 0, code)
	assert.Contains(t, stdout, "status:")
	assert.NotContains(t, stdout, strings.Repeat("\x00", 8))
	assert.NotContains(t, stderr, "context deadline exceeded")
	assert.NoError(t, ctx.Err())
}

func TestVulnHuntBuiltinResourceExhaustion_ComplexLiveInvocationReturnsUnderContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stdout, stderr, code := testutil.RunScriptCtx(ctx, t, `ss -xaeoH; echo status:$?`, "")
	require.Equal(t, 0, code)
	assert.Contains(t, stdout, "status:")
	assert.NotContains(t, stderr, "context deadline exceeded")
	assert.NoError(t, ctx.Err())
}
