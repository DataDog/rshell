// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: logic_ops (shell-feature)

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

func runLogicOpsVulnHuntScript(t *testing.T, script string, opts ...RunnerOption) (string, string, int, error) {
	t.Helper()

	prog, err := ParseScript(script, "logic_ops_vuln_hunt.sh")
	if err != nil {
		return "", err.Error() + "\n", 2, nil
	}

	var stdout, stderr bytes.Buffer
	allOpts := []RunnerOption{StdIO(nil, &stdout, &stderr)}
	if len(opts) == 0 {
		allOpts = append(allOpts, allowAllCommandsOpt())
	} else {
		allOpts = append(allOpts, opts...)
	}
	r, err := New(allOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	err = r.Run(context.Background(), prog)
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

func TestVulnHuntShellFeatureExpansionChain_LogicOpsSkippedRightDoesNotExpand(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	forbidden := filepath.Join(base, "forbidden")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(forbidden, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(forbidden, "secret.txt"), []byte("SECRET\n"), 0o644))

	stdout, stderr, code, err := runLogicOpsVulnHuntScript(t,
		"true || echo \"$(<../forbidden/secret.txt)\"\necho after\n",
		AllowedPaths([]string{allowed}),
		AllowedCommands([]string{"rshell:true", "rshell:echo"}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "after\n", stdout)
	assert.Empty(t, stderr)
	assert.NotContains(t, stdout, "SECRET")
}

func TestVulnHuntShellFeatureExpansionChain_LogicOpsExpandedOperatorsStayData(t *testing.T) {
	stdout, stderr, code, err := runLogicOpsVulnHuntScript(t, "PAYLOAD='false || echo HACKED'\necho \"$PAYLOAD\" && echo done\n")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "false || echo HACKED\ndone\n", stdout)
}

func TestVulnHuntShellFeatureExpansionChain_LogicOpsCommandSubstCapAndStatusHold(t *testing.T) {
	payload := strings.Repeat("z", MaxVarBytes+1)
	stdout, stderr, code, err := runLogicOpsVulnHuntScript(t, "echo \"$(printf '"+payload+"')\" && echo ok\n")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, strings.Repeat("z", MaxVarBytes)+"\nok\n", stdout)
}

func TestVulnHuntShellFeatureParserConfusion_LogicOpsGroupingAndLinebreaks(t *testing.T) {
	script := `false ||
# comment between operator and operand
echo recovered && echo after; false && echo skipped; echo done
`
	stdout, stderr, code, err := runLogicOpsVulnHuntScript(t, script)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "recovered\nafter\ndone\n", stdout)
}

func TestVulnHuntShellFeatureParserConfusion_LogicOpsValidationPrecedesExecution(t *testing.T) {
	tests := map[string]string{
		"pipe_all":         "echo before\nfalse |& echo hidden || echo fallback\n",
		"skipped_function": "echo before\ntrue || f() { echo hidden; }\necho after\n",
	}

	for name, script := range tests {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code, err := runLogicOpsVulnHuntScript(t, script)

			require.NoError(t, err)
			assert.Equal(t, 2, code)
			assert.Empty(t, stdout)
			assert.NotContains(t, stdout+stderr, "before")
			assert.NotContains(t, stdout+stderr, "hidden")
			assert.NotContains(t, stdout+stderr, "after")
		})
	}
}

func TestVulnHuntShellFeatureSubshellIsolation_LogicOpsSubshellOperandDoesNotLeak(t *testing.T) {
	stdout, stderr, code, err := runLogicOpsVulnHuntScript(t, "X=parent\n(X=child; true) && echo first=$X\n(X=bad; false) || echo second=$X\n")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "first=parent\nsecond=parent\n", stdout)
}

func TestVulnHuntShellFeatureSubshellIsolation_LogicOpsPipelineStatusAndStateHold(t *testing.T) {
	stdout, stderr, code, err := runLogicOpsVulnHuntScript(t, "false | true && echo right\ntrue | false || echo fallback\nX=parent; echo child | read X && echo \"$X\"\n")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "right\nfallback\nparent\n", stdout)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_MaxScriptBytesRejectsHugeLogicOps(t *testing.T) {
	unit := "true && "
	script := strings.Repeat(unit, MaxScriptBytes/len(unit)+1) + "true\n"

	_, err := ParseScript(script, "logic_ops_oversized.sh")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_LogicOpsExitStatusLastExecuted(t *testing.T) {
	stdout, stderr, code, err := runLogicOpsVulnHuntScript(t, `false && exit 7 || echo fallback
echo after=$?
true && false || true && false
echo chain=$?
`)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "fallback\nafter=0\nchain=1\n", stdout)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_LogicOpsTimeoutPropagates(t *testing.T) {
	stdout, _, _, err := runLogicOpsVulnHuntScript(t,
		"false || while true; do true; done\necho never\n",
		allowAllCommandsOpt(),
		MaxExecutionTime(50*time.Millisecond))

	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, stdout)
}

func TestVulnHuntShellFeatureCompositionAttack_LogicOpsRedirectionSkippedOrRestored(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	forbidden := filepath.Join(base, "forbidden")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(forbidden, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "data.txt"), []byte("data\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(forbidden, "secret.txt"), []byte("SECRET\n"), 0o644))

	stdout, stderr, code, err := runLogicOpsVulnHuntScript(t,
		"true || cat < ../forbidden/secret.txt\ncat < data.txt && cat <<'EOF'\nheredoc\nEOF\n",
		AllowedPaths([]string{allowed}),
		AllowedCommands([]string{"rshell:true", "rshell:cat"}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "data\nheredoc\n", stdout)
	assert.NotContains(t, stdout, "SECRET")
}

func TestVulnHuntShellFeatureCompositionAttack_LogicOpsBlockedRedirectFailsGlobally(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code, err := runLogicOpsVulnHuntScript(t, "true || echo skipped > out.txt\necho after\n",
		AllowedPaths([]string{dir}),
		allowAllCommandsOpt())

	require.NoError(t, err)
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "> file redirection is not supported")
	require.NoFileExists(t, filepath.Join(dir, "out.txt"))
}

func TestVulnHuntShellFeatureRedirectionChain_LogicOpsExecutedInputRedirectSandboxed(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	forbidden := filepath.Join(base, "forbidden")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(forbidden, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(forbidden, "secret.txt"), []byte("SECRET\n"), 0o644))

	stdout, stderr, code, err := runLogicOpsVulnHuntScript(t,
		"P=../forbidden/secret.txt\ncat < $P || echo denied\n",
		AllowedPaths([]string{allowed}),
		AllowedCommands([]string{"rshell:cat", "rshell:echo"}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "denied\n", stdout)
	assert.Contains(t, stderr, "permission denied")
	assert.NotContains(t, stdout, "SECRET")
}

func TestVulnHuntShellFeatureReadonlyBypass_LogicOpsReadonlyFailuresDoNotRunSkippedSide(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Reset()
	require.NoError(t, r.writeEnv.Set("RO", expand.Variable{Set: true, Kind: expand.String, Str: "original", ReadOnly: true}))

	err = r.Run(context.Background(), parseScript(t, "RO=hacked echo bad && echo right\ntrue || RO=bad echo skipped\necho after=$? ro=$RO\n"))

	require.NoError(t, err)
	assert.Equal(t, "after=0 ro=original\n", stdout.String())
	assert.Contains(t, stderr.String(), "readonly variable")
	assert.NotContains(t, stdout.String(), "bad")
	assert.NotContains(t, stdout.String(), "right")
	assert.NotContains(t, stdout.String(), "skipped")
}

func TestVulnHuntShellFeatureSignalContext_LogicOpsCancellationDuringCmdSubstIsFatal(t *testing.T) {
	stdout, _, _, err := runLogicOpsVulnHuntScript(t,
		"echo \"$(while true; do echo x; done)\" && echo never\n",
		allowAllCommandsOpt(),
		MaxExecutionTime(50*time.Millisecond))

	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, stdout)
}
