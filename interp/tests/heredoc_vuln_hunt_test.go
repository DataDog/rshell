// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: heredoc (shell-feature)

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

func TestVulnHuntShellFeatureFileAccessBypass_HeredocCatShortcutSandboxed(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	forbidden := filepath.Join(base, "forbidden")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(forbidden, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(forbidden, "secret.txt"), []byte("S3CR3T\n"), 0o644))

	script := "cat <<EOF\n$(<../forbidden/secret.txt)\nEOF\n"
	stdout, stderr, code := redirRunWithOpts(t, script, allowed,
		interp.AllowedPaths([]string{allowed}),
		interp.AllowedCommands([]string{"rshell:cat"}))

	assert.Equal(t, 0, code)
	assert.NotContains(t, stdout, "S3CR3T")
	assert.Contains(t, stderr, "permission denied")
}

func TestVulnHuntShellFeatureExpansionChain_QuotedHeredocKeepsUnsupportedSyntaxLiteral(t *testing.T) {
	dir := t.TempDir()
	script := "cat <<'EOF'\n${#SECRET}\n$(readonly X)\nEOF\n"

	stdout, stderr, code := redirRun(t, script, dir)

	assert.Equal(t, 0, code)
	assert.Equal(t, "${#SECRET}\n$(readonly X)\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureRedirectionChain_FailedInputAfterHeredocRestoresStdin(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	forbidden := filepath.Join(base, "forbidden")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(forbidden, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(forbidden, "secret.txt"), []byte("S3CR3T\n"), 0o644))

	script := "cat <<EOF < ../forbidden/secret.txt\nblocked\nEOF\necho after\ncat <<EOF\nsafe\nEOF\n"
	stdout, stderr, code := redirRunWithOpts(t, script, allowed,
		interp.AllowedPaths([]string{allowed}),
		interpoption.AllowAllCommands().(interp.RunnerOption))

	assert.Equal(t, 0, code)
	assert.Equal(t, "after\nsafe\n", stdout)
	assert.Contains(t, stderr, "permission denied")
	assert.NotContains(t, stdout, "blocked")
	assert.NotContains(t, stdout, "S3CR3T")
}

func TestVulnHuntShellFeatureDiagInjection_HeredocDelimiterEscapesControlBytes(t *testing.T) {
	_, err := interp.ParseScript("cat <<'BAD\nNAME'\nbody\nBAD\nNAME\n", "heredoc_diag.sh")
	require.Error(t, err)

	msg := err.Error()
	assert.Contains(t, msg, `BAD\nNAME`)
	assert.NotContains(t, msg, "BAD\nNAME")
}
