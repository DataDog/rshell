// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt 2026-05-20-gpt-5.5-cyber-3 (target: redirections)

package tests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/interp"
)

func TestVulnHuntShellFeatureExpansionChain_InputRedirectOperandRemainsSingleLiteral(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a b.txt extra"), []byte("single-path\n"), 0o644))

	stdout, stderr, code := redirRun(t, "P='a b.txt extra'\ncat < $P\n", dir)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "single-path\n", stdout)
}

func TestVulnHuntShellFeatureExpansionChain_CommandSubstRedirectTargetStillSandboxed(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(secret, []byte("secret\n"), 0o644))

	script := "cat < $(printf %s " + quoteRedirectionVulnHunt(secret) + ")\necho status=$?\n"
	stdout, stderr, code := redirRun(t, script, allowed)

	assert.Equal(t, 0, code)
	assert.Equal(t, "status=1\n", stdout)
	assert.Contains(t, stderr, "permission denied")
	assert.NotContains(t, stdout, "secret")
}

func TestVulnHuntShellFeatureParserConfusion_NullDeviceVariantsRemainBlocked(t *testing.T) {
	dir := t.TempDir()
	for _, script := range []string{
		"echo hi > /dev/./null\n",
		"echo hi > /dev//null\n",
		"echo hi > /dev/null/\n",
		"echo hi > /dev/null/../null\n",
		"echo hi > '/dev/null'\n",
	} {
		stdout, stderr, code := pentestRedirRun(t, script, dir)

		assert.Equal(t, 2, code, "script=%q", script)
		assert.Empty(t, stdout)
		assert.Contains(t, stderr, "file redirection is not supported")
	}
}

func TestVulnHuntShellFeatureSubshellIsolation_BraceRedirectRestoresPipeStdin(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data.txt"), []byte("file\n"), 0o644))

	stdout, stderr, code := redirRun(t, "printf 'pipe\\n' | { cat < data.txt; cat; }\n", dir)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "file\npipe\n", stdout)
}

func TestVulnHuntShellFeatureCompositionAttack_FdDupOrderMatchesBash(t *testing.T) {
	dir := t.TempDir()

	stdout1, stderr1, code1 := redirRun(t, "cat missing 2>&1 >/dev/null\n", dir)
	assert.Equal(t, 1, code1)
	assert.NotEmpty(t, stdout1, "stderr duplicated to the original stdout before stdout moved to /dev/null")
	assert.Empty(t, stderr1)

	stdout2, stderr2, code2 := redirRun(t, "cat missing >/dev/null 2>&1\n", dir)
	assert.Equal(t, 1, code2)
	assert.Empty(t, stdout2)
	assert.Empty(t, stderr2)
}

func TestVulnHuntShellFeatureRedirectionChain_MixedAllowedBlockedRedirectFailsBeforeExecution(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRunNoAllowed(t, "echo before\necho data >/dev/null > out.txt\necho after\n", dir)

	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "> file redirection is not supported")
	require.NoFileExists(t, filepath.Join(dir, "out.txt"))
}

func TestVulnHuntShellFeatureReadonlyBypass_RedirectOperandCommandSubstCannotDeclare(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data.txt"), []byte("data\n"), 0o644))

	stdout, stderr, code := redirRun(t, "cat < $(readonly X=1; printf data.txt)\n", dir)

	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "readonly is not supported")
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_MaxScriptBytesRejectsLargeHeredoc(t *testing.T) {
	body := strings.Repeat("x", interp.MaxScriptBytes+1)
	_, err := interp.ParseScript("cat <<EOF\n"+body+"\nEOF\n", "redirections_oversized_heredoc.sh")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func quoteRedirectionVulnHunt(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
