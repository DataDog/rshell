// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt 2026-05-20-gpt-5.5-cyber-3 (target: cd)

package cd_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVulnHuntBuiltinFlagDriven_CdRejectsFlagValuesAndExpansion(t *testing.T) {
	dir := canonicalTempDir(t)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "child"), 0o755))

	for _, script := range []string{
		"cd --physical=true",
		"cd --physical=true --help",
		"cd --logical=false",
		"cd child -P",
		"bad=-x; cd $bad",
		"cd -@",
	} {
		stdout, stderr, code := cdRun(t, script, dir)
		assert.Equal(t, 1, code, "script %q", script)
		assert.Empty(t, stdout, "script %q", script)
		assert.Contains(t, stderr, "cd:", "script %q", script)
	}
}

func TestVulnHuntBuiltinFileAccess_CdHomeOldpwdTargetsStaySandboxed(t *testing.T) {
	root := canonicalTempDir(t)
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))

	script := strings.Join([]string{
		"HOME=" + cdVulnQuote(outside) + "; cd",
		"echo home=$?",
		"OLDPWD=" + cdVulnQuote(outside) + "; cd -",
		"echo dash=$?",
		"pwd",
	}, "\n") + "\n"
	stdout, stderr, code := cdRun(t, script, allowed)

	assert.Equal(t, 0, code)
	assert.Equal(t, "home=1\ndash=1\n"+allowed+"\n", stdout)
	assert.Contains(t, stderr, "permission denied")
}

func TestVulnHuntBuiltinDeclaredVsImplemented_FailedCdDoesNotCorruptRelativeReads(t *testing.T) {
	dir := canonicalTempDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "safe.txt"), []byte("safe\n"), 0o644))

	stdout, stderr, code := cdRun(t, "cd missing 2>/dev/null\ncat safe.txt\npwd\n", dir)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "safe\n"+dir+"\n", stdout)
}

func TestVulnHuntBuiltinSubshellIsolation_CdInChildScopes(t *testing.T) {
	dir := canonicalTempDir(t)
	child := filepath.Join(dir, "child")
	require.NoError(t, os.Mkdir(child, 0o755))

	script := "( cd child; pwd )\nprintf 'x\\n' | { cd child; pwd; }\npwd\n"
	stdout, stderr, code := cdRun(t, script, dir)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, child+"\n"+child+"\n"+dir+"\n", stdout)
}

func TestVulnHuntBuiltinRedirectionChain_CdRedirsRestoreAndDoNotWrite(t *testing.T) {
	dir := canonicalTempDir(t)
	child := filepath.Join(dir, "child")
	require.NoError(t, os.Mkdir(child, 0o755))
	before, err := os.ReadDir(dir)
	require.NoError(t, err)

	script := "cd child < no-such-input 2>/dev/null\necho status=$?\npwd\ncd child >/dev/null\npwd\n"
	stdout, stderr, code := cdRun(t, script, dir)
	after, err := os.ReadDir(dir)
	require.NoError(t, err)

	assert.Equal(t, 0, code)
	assert.Contains(t, stderr, "no-such-input")
	assert.Equal(t, "status=1\n"+dir+"\n"+child+"\n", stdout)
	assert.Len(t, after, len(before), "cd with redirections must not create filesystem entries")
}

func TestVulnHuntBuiltinSignalContext_CdLoopRespectsCancellation(t *testing.T) {
	dir := canonicalTempDir(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()

	_, _, _ = cdRunCtx(ctx, t, "while true; do cd .; done\n", dir)

	assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func cdVulnQuote(s string) string {
	return "'" + strings.ReplaceAll(filepath.ToSlash(s), "'", `'\''`) + "'"
}
