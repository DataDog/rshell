// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: heredoc_dash (shell-feature)

package tests_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
)

func TestVulnHuntShellFeatureExpansionChain_DashHeredocTabsIntroducedByExpansionRemain(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, code := redirRun(t, "LINE=$'\\tkept\\tvalue'\ncat <<-EOF\n\t$LINE\nEOF\n", dir)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "\tkept\tvalue\n", stdout)
}

func TestVulnHuntShellFeatureExpansionChain_DashHeredocExpandedMetacharactersStayData(t *testing.T) {
	dir := t.TempDir()
	script := "PAYLOAD='echo HACKED; cat < secret.txt'\ncat <<-EOF\n\t$PAYLOAD\nEOF\n"

	stdout, stderr, code := redirRun(t, script, dir)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "echo HACKED; cat < secret.txt\n", stdout)
}

func TestVulnHuntShellFeatureExpansionChain_DashHeredocCatShortcutPolicyAndSandbox(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	forbidden := filepath.Join(base, "forbidden")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(forbidden, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "secret.txt"), []byte("INSIDE\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(forbidden, "secret.txt"), []byte("OUTSIDE\n"), 0o644))

	stdout1, stderr1, code1 := redirRunWithOpts(t, "read X <<-EOF\n\t$(<secret.txt)\nEOF\necho \"[$X]\"\n", allowed,
		interp.AllowedPaths([]string{allowed}),
		interp.AllowedCommands([]string{"rshell:read", "rshell:echo"}))
	assert.Equal(t, 0, code1)
	assert.Equal(t, "[]\n", stdout1)
	assert.Contains(t, stderr1, "cat not in allowed commands")
	assert.NotContains(t, stdout1, "INSIDE")

	stdout2, stderr2, code2 := redirRunWithOpts(t, "cat <<-EOF\n\tDENIED:$(<../forbidden/secret.txt):END\nEOF\n", allowed,
		interp.AllowedPaths([]string{allowed}),
		interp.AllowedCommands([]string{"rshell:cat"}))
	assert.Equal(t, 0, code2)
	assert.Equal(t, "DENIED::END\n", stdout2)
	assert.Contains(t, stderr2, "permission denied")
	assert.NotContains(t, stdout2, "OUTSIDE")
}

func TestVulnHuntShellFeatureParserConfusion_DashHeredocMixedWhitespaceAndDelimiter(t *testing.T) {
	dir := t.TempDir()
	script := "cat <<-EOF\n\t  tab then spaces\n    spaces stay\n\t\tdeep\n\tEOF\n"

	stdout, stderr, code := redirRun(t, script, dir)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "  tab then spaces\n    spaces stay\ndeep\n", stdout)
}

func TestVulnHuntShellFeatureParserConfusion_DashHeredocQuotedDelimitersSuppressExpansion(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, code := redirRun(t, "cat <<-'EOF'\n\t${#SECRET}\n\t$(readonly X=1)\nEOF\n", dir)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "${#SECRET}\n$(readonly X=1)\n", stdout)

	stdout, stderr, code = redirRun(t, "cat <<-EOF\n\t$(readonly X=1)\nEOF\n", dir)
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "readonly is not supported")
}

func TestVulnHuntShellFeatureParserConfusion_DashHeredocCRLFLineEndings(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, code := redirRun(t, "cat <<-EOF\r\n\tone\r\n\tEOF\r\n", dir)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "one\n", stdout)

	_, err := interp.ParseScript("cat <<-EOF\r\tone\rEOF\recho after\r", "heredoc_dash_cr_only.sh")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unclosed here-document")
}

func TestVulnHuntShellFeatureSubshellIsolation_DashHeredocDoesNotLeakState(t *testing.T) {
	dir := t.TempDir()
	script := "X=parent\n( X=child; cat <<-EOF\n\t$X\nEOF\n)\necho parent=$X\ncat <<-EOF\n\tafter\nEOF\n"

	stdout, stderr, code := redirRun(t, script, dir)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "child\nparent=parent\nafter\n", stdout)
}

func TestVulnHuntShellFeatureRedirectionChain_DashHeredocFailedRedirectRestoresStdin(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	forbidden := filepath.Join(base, "forbidden")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(forbidden, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(forbidden, "secret.txt"), []byte("S3CR3T\n"), 0o644))

	script := "cat <<-EOF < ../forbidden/secret.txt\n\tblocked\nEOF\necho after\ncat <<-EOF\n\tsafe\nEOF\n"
	stdout, stderr, code := redirRunWithOpts(t, script, allowed,
		interp.AllowedPaths([]string{allowed}),
		interp.AllowedCommands([]string{"rshell:cat", "rshell:echo"}))

	assert.Equal(t, 0, code)
	assert.Equal(t, "after\nsafe\n", stdout)
	assert.Contains(t, stderr, "permission denied")
	assert.NotContains(t, stdout, "blocked")
	assert.NotContains(t, stdout, "S3CR3T")
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_MaxScriptBytesRejectsLargeDashHeredoc(t *testing.T) {
	body := strings.Repeat("x", interp.MaxScriptBytes+1)

	_, err := interp.ParseScript("cat <<-EOF\n"+body+"\nEOF\n", "heredoc_dash_oversized.sh")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_DashHeredocExpansionCapsHold(t *testing.T) {
	dir := t.TempDir()

	overVar := strings.Repeat("v", interp.MaxVarBytes+1)
	stdout, stderr, code := redirRun(t, "BIG='"+overVar+"'\ncat <<-EOF\n\tMARKER:$BIG:END\nEOF\n", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr, "value too large")
	assert.Equal(t, "MARKER::END\n", stdout)

	overCmdSubst := strings.Repeat("z", interp.MaxVarBytes+1)
	stdout, stderr, code = redirRun(t, "cat <<-EOF\n\t$(printf '"+overCmdSubst+"')\nEOF\n", dir)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Len(t, stdout, interp.MaxVarBytes+1)
	assert.Equal(t, strings.Repeat("z", interp.MaxVarBytes)+"\n", stdout)
}

func TestVulnHuntShellFeatureCompositionAttack_DashHeredocLineContinuationAfterTabStrip(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, code := redirRun(t, "cat <<-EOF\n\thello \\\n\tworld\nEOF\n", dir)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "hello world\n", stdout)
}

func TestVulnHuntShellFeatureCompositionAttack_DashHeredocPipeReaderClosesEarly(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("\tsecond\n", 4096)
	script := "cat <<-EOF | head -n 1\n\tfirst\n" + body + "EOF\n"

	stdout, stderr, code := redirRun(t, script, dir)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "first\n", stdout)
}

func TestVulnHuntShellFeatureRedirectionChain_DashHeredocBlockedRedirectPreventsExpansion(t *testing.T) {
	dir := t.TempDir()
	script := "cat <<-EOF > out.txt\n\t$(echo BAD)\nEOF\n"

	stdout, stderr, code := redirRun(t, script, dir)

	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "> file redirection is not supported")
	require.NoFileExists(t, filepath.Join(dir, "out.txt"))
	assert.NotContains(t, stdout+stderr, "BAD")
}

func TestVulnHuntShellFeatureSignalContext_DashHeredocWriterHonorsCancel(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping ctx-cancel timing test in -short mode")
	}
	body := strings.Repeat("y", interp.MaxHeredocBytes-1)
	script := "cat <<-EOF\n" + body + "\nEOF"

	prog, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		pentestRedirRunProg(ctx, t, prog, t.TempDir())
		close(done)
	}()
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("dash heredoc writer did not honor ctx cancel within budget")
	}
}
