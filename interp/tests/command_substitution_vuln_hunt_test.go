// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: command_substitution (shell-feature)

package tests_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/interp"
)

func cmdSubstVHMkdir(t *testing.T, base, name string) string {
	t.Helper()
	dir := filepath.Join(base, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	return dir
}

func cmdSubstVHWriteFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

func TestVulnHuntShellFeatureExpansionChain_CommandSubstOutputNotReparsed(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdSubstRun(t, "payload=$(printf 'echo SAFE; echo HACKED')\n$payload\n", dir)

	assert.Equal(t, 0, code)
	assert.Equal(t, "SAFE; echo HACKED\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureExpansionChain_CatShortcutExpandedPathStillSandboxed(t *testing.T) {
	root := t.TempDir()
	allowed := cmdSubstVHMkdir(t, root, "allowed")
	outside := cmdSubstVHMkdir(t, root, "outside")
	cmdSubstVHWriteFile(t, outside, "secret.txt", "leak")

	stdout, stderr, code := cmdSubstRun(t,
		"name=$(printf '../outside/secret.txt')\n"+
			"x=$(<$name)\n"+
			"echo \"[$x]\"\n",
		allowed,
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "[]\n", stdout)
	assert.Contains(t, stderr, "permission denied")
	assert.NotContains(t, stdout+stderr, "leak")
}

func TestVulnHuntShellFeatureParserConfusion_BlockedSyntaxInsideCommandSubstPreventsExecution(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdSubstRun(t, "echo before\nx=$(readonly X=1; echo bad)\necho after\n", dir)

	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "readonly is not supported")
}

func TestVulnHuntShellFeatureParserConfusion_DeepNestedCommandSubstCompletesCleanly(t *testing.T) {
	dir := t.TempDir()
	var script strings.Builder
	script.WriteString("echo ")
	for range 2000 {
		script.WriteString("$(echo ")
	}
	script.WriteString("ok")
	for range 2000 {
		script.WriteByte(')')
	}
	script.WriteByte('\n')

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stdout, stderr, code := cmdSubstRunCtx(ctx, t, script.String(), dir)

	assert.Equal(t, 0, code)
	assert.Equal(t, "ok\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureParserConfusion_NULInCommandSubstSourceIsDataNotSyntax(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdSubstRun(t, "echo \"$(printf before\x00after)\"\n", dir)

	assert.Equal(t, 0, code)
	assert.Equal(t, "beforeafter\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureSubshellIsolation_CommandSubstStateDoesNotLeak(t *testing.T) {
	root := t.TempDir()
	allowed := cmdSubstVHMkdir(t, root, "allowed")
	cmdSubstVHMkdir(t, allowed, "child")

	stdout, stderr, code := cmdSubstRun(t,
		"VAR=parent\n"+
			"captured=$(VAR=child; cd child; printf \"$VAR\")\n"+
			"printf 'captured=%s parent=%s pwd=%s\\n' \"$captured\" \"$VAR\" \"$PWD\"\n",
		allowed,
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "captured=child parent=parent pwd="+allowed+"\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureSubshellIsolation_BreakInCommandSubstDoesNotBreakParentLoop(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdSubstRun(t,
		"while true; do\n"+
			"  out=$(break; echo child)\n"+
			"  echo \"$out\"\n"+
			"  break\n"+
			"done\n"+
			"echo parent\n",
		dir,
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "\nparent\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_CatShortcutDevZeroStopsAtCaptureCap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("/dev/zero is Unix-specific")
	}

	var stdout, stderr bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(nil, &stdout, &stderr),
		interp.AllowedPaths([]string{"/dev"}),
		interp.AllowedCommands([]string{"rshell:cat", "rshell:echo"}),
		interp.MaxExecutionTime(2*time.Second),
	)
	require.NoError(t, err)
	defer runner.Close()

	prog, err := interp.ParseScript("x=$(</dev/zero)\necho after\n", "cmdsubst_devzero.sh")
	require.NoError(t, err)

	runErr := runner.Run(context.Background(), prog)
	require.NoError(t, runErr)
	assert.Equal(t, "after\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntShellFeatureSignalContext_InfiniteProducerPropagatesTimeout(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(nil, &stdout, &stderr),
		interp.AllowedCommands([]string{"rshell:echo", "rshell:true"}),
		interp.MaxExecutionTime(50*time.Millisecond),
	)
	require.NoError(t, err)
	defer runner.Close()

	prog, err := interp.ParseScript("x=$(while true; do echo x; done)\necho done\n", "cmdsubst_timeout.sh")
	require.NoError(t, err)

	runErr := runner.Run(context.Background(), prog)
	assert.ErrorIs(t, runErr, context.DeadlineExceeded)
	assert.Empty(t, stdout.String())
	_ = stderr
}

func TestVulnHuntShellFeatureCompositionAttack_HeredocCommandSubstQuotingRulesHold(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, code := cmdSubstRun(t, "cat <<EOF\n$(printf x)\nEOF\ncat <<'EOF'\n$(printf y)\nEOF\n", dir)

	assert.Equal(t, 0, code)
	assert.Equal(t, "x\n$(printf y)\n", stdout)
	assert.Empty(t, stderr)
}
