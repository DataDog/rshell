// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: cmd_separator (shell-feature)

package interp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/expand"
)

func runCmdSeparatorVulnHuntScript(t *testing.T, script, dir string, opts ...RunnerOption) (string, string, int, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	allOpts := append([]RunnerOption{
		StdIO(nil, &stdout, &stderr),
		allowAllCommandsOpt(),
	}, opts...)

	r, err := New(allOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	if dir != "" {
		r.Dir = dir
	}

	err = r.Run(context.Background(), parseScript(t, script))
	exitCode := 0
	if err != nil {
		var status ExitStatus
		if errors.As(err, &status) {
			exitCode = int(status)
			err = nil
		}
	}
	return stdout.String(), stderr.String(), exitCode, err
}

func TestVulnHuntShellFeatureExpansionChain_SeparatorsFromVariablesNotReparsed(t *testing.T) {
	stdout, stderr, code, err := runCmdSeparatorVulnHuntScript(t, "PAYLOAD='echo SAFE; echo HACKED'\n$PAYLOAD\necho status=$?\n", "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "SAFE; echo HACKED\nstatus=0\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureExpansionChain_CmdSubstSeparatorsNotReparsed(t *testing.T) {
	script := "PAYLOAD=$(printf 'echo SAFE; echo HACKED')\n$PAYLOAD\necho status=$?\n"
	stdout, stderr, code, err := runCmdSeparatorVulnHuntScript(t, script, "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "SAFE; echo HACKED\nstatus=0\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureParserConfusion_LineEndingsAndComments(t *testing.T) {
	tests := map[string]string{
		"crlf":              "echo one\r\necho two\r\n",
		"semicolon_comment": "echo one; # ignored comment\necho two\n",
		"quoted_semicolon":  "echo 'one;two'; echo three\n",
	}
	for name, script := range tests {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code, err := runCmdSeparatorVulnHuntScript(t, script, "")
			require.NoError(t, err)
			assert.Equal(t, 0, code)
			assert.Empty(t, stderr)
			switch name {
			case "quoted_semicolon":
				assert.Equal(t, "one;two\nthree\n", stdout)
			default:
				assert.Equal(t, "one\ntwo\n", stdout)
			}
		})
	}
}

func TestVulnHuntShellFeatureParserConfusion_UnsupportedBackgroundRejectedBeforeExecution(t *testing.T) {
	stdout, stderr, code, err := runCmdSeparatorVulnHuntScript(t, "echo before; echo bg & echo after\n", "")

	require.NoError(t, err)
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "background execution")
}

func TestVulnHuntShellFeatureSubshellIsolation_SeparatorScopes(t *testing.T) {
	script := strings.Join([]string{
		"X=parent; (X=child; Y=hidden; echo sub=$X/$Y); echo parent=$X/${Y}",
		"{ Z=brace; }; echo brace=$Z",
		"Q=$(A=cmdsubst; echo value); echo q=$Q a=${A}",
	}, "\n")
	stdout, stderr, code, err := runCmdSeparatorVulnHuntScript(t, script, "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "sub=child/hidden\nparent=parent/\nbrace=brace\nq=value a=\n", stdout)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_ExitStatusAndExitStop(t *testing.T) {
	stdout, stderr, code, err := runCmdSeparatorVulnHuntScript(t, "false; echo after_false=$?; definitely_missing; echo after_missing=$?; exit 7; echo unreachable\n", "")

	require.NoError(t, err)
	assert.Equal(t, 7, code)
	assert.Equal(t, "after_false=1\nafter_missing=127\n", stdout)
	assert.Contains(t, stderr, "definitely_missing")
	assert.NotContains(t, stdout, "unreachable")
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_MaxScriptBytesRejectsBeforeParse(t *testing.T) {
	_, err := ParseScript(strings.Repeat(";", MaxScriptBytes+1), "oversized-separator-chain.sh")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestVulnHuntShellFeatureCompositionAttack_RedirectionFailureDoesNotPoisonNextStatement(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("from-file\n"), 0o644))

	script := "cat < missing.txt; echo after-missing; cat < input.txt; echo after-cat\n"
	stdout, stderr, code, err := runCmdSeparatorVulnHuntScript(t, script, dir, AllowedPaths([]string{dir}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "after-missing\nfrom-file\nafter-cat\n", stdout)
	assert.Contains(t, stderr, "missing.txt")
}

func TestVulnHuntShellFeatureRedirectionChain_RedirectsRestoreAcrossSeparators(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("visible\n"), 0o644))

	script := "echo hidden > /dev/null; echo stdout-restored; cat < input.txt; echo stdin-restored\n"
	stdout, stderr, code, err := runCmdSeparatorVulnHuntScript(t, script, dir, AllowedPaths([]string{dir}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "stdout-restored\nvisible\nstdin-restored\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureReadonlyBypass_SequencedAssignmentsRespectReadonly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Reset()
	require.NoError(t, r.writeEnv.Set("RO_VAR", expand.Variable{
		Set:      true,
		Kind:     expand.String,
		Str:      "original",
		ReadOnly: true,
	}))

	err = r.Run(context.Background(), parseScript(t, "RO_VAR=changed; echo assign_status=$? value=$RO_VAR; RO_VAR=inline echo hidden; echo after=$RO_VAR\n"))
	require.NoError(t, err)
	assert.Equal(t, "assign_status=1 value=original\nafter=original\n", stdout.String())
	assert.Contains(t, stderr.String(), "readonly")
	assert.NotContains(t, stdout.String(), "hidden")
}

func TestVulnHuntShellFeatureSignalContext_LongSeparatorChainStopsBeforeNextStatement(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt(), MaxExecutionTime(25*time.Millisecond))
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Reset()
	r.execHandler = func(ctx context.Context, _ []string) error {
		<-ctx.Done()
		return ctx.Err()
	}

	err = r.Run(context.Background(), parseScript(t, "slow_external; echo should_not_run\n"))
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntShellFeatureCompositionAttack_AndOrPipelinesDoNotLeakAcrossSeparators(t *testing.T) {
	script := strings.Join([]string{
		"false && echo skipped; echo after-and",
		"true || echo skipped; echo after-or",
		"echo piped | cat; echo after-pipe",
	}, "\n")
	stdout, stderr, code, err := runCmdSeparatorVulnHuntScript(t, script, "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "after-and\nafter-or\npiped\nafter-pipe\n", stdout)
}
