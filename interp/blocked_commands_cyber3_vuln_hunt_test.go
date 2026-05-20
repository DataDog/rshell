// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: blocked_commands (shell-feature)

package interp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runBlockedCommandsCyber3(t *testing.T, script string, opts ...RunnerOption) (string, string, int, error) {
	t.Helper()

	prog, err := ParseScript(script, "blocked_commands_cyber3_vuln_hunt.sh")
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	allOpts := append([]RunnerOption{StdIO(nil, &stdout, &stderr), allowAllCommandsOpt()}, opts...)
	r, err := New(allOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	err = r.Run(context.Background(), prog)
	code := 0
	if err != nil {
		var status ExitStatus
		if errors.As(err, &status) {
			code = int(status)
			err = nil
		}
	}
	return stdout.String(), stderr.String(), code, err
}

func TestVulnHuntShellFeatureExpansionChain_BlockedCommandsExpandedSyntaxIsData(t *testing.T) {
	stdout, stderr, code, err := runBlockedCommandsCyber3(t, `PAYLOAD='eval echo PWNED'
$PAYLOAD
T=trap
$T 'echo trapped' INT
TEXT='case x in x) echo BAD;; esac'
$TEXT
cat <<'EOF'
case x in x) echo HEREDOC;; esac
${#SECRET}
EOF
`)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "case x in x) echo HEREDOC;; esac\n${#SECRET}\n", stdout)
	assert.Contains(t, stderr, "rshell: eval: unknown command")
	assert.Contains(t, stderr, "rshell: trap: unknown command")
	assert.Contains(t, stderr, "rshell: case: unknown command")
	assert.NotContains(t, stdout, "PWNED")
	assert.NotContains(t, stdout, "trapped")
}

func TestVulnHuntShellFeatureParserConfusion_BlockedSyntaxPreExecValidation(t *testing.T) {
	tests := map[string]struct {
		script string
		want   string
	}{
		"arithmetic_expansion": {"echo $((1+2))\n", "arithmetic expansion is not supported"},
		"arithmetic_command":   {"(( 1 + 2 ))\n", "arithmetic commands are not supported"},
		"process_substitution": {"cat <(echo BAD)\n", "process substitution is not supported"},
		"case_clause":          {"case x in x) echo BAD;; esac\n", "case statements are not supported"},
		"function_decl":        {"f() { echo BAD; }\n", "function declarations are not supported"},
		"test_clause":          {"[[ -n hello ]]\n", "test expressions are not supported"},
		"decl_clause":          {"readonly X=42\n", "readonly is not supported"},
		"let_clause":           {"let \"x=1+2\"\n", "let is not supported"},
		"time_clause":          {"time echo BAD\n", "time is not supported"},
		"coproc_clause":        {"coproc echo BAD\n", "coprocesses are not supported"},
		"select_clause":        {"select x in a b; do echo \"$x\"; done\n", "select statements are not supported"},
		"c_style_for":          {"for ((i=0; i<1; i++)); do echo BAD; done\n", "c-style for loops are not supported"},
		"extglob":              {"echo @(foo|bar)\n", "extended globbing is not supported"},
		"background":           {"echo BAD &\n", "background execution (&) is not supported"},
		"pipe_all":             {"echo BAD |& cat\n", "|& is not supported"},
		"tilde":                {"echo ~\n", "tilde expansion is not supported"},
		"param_default":        {"echo ${A:=mutated}\n", "${var} operations"},
		"positional":           {"echo $1\n", "$1 is not supported"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code, err := runBlockedCommandsCyber3(t, "echo before\n"+tc.script+"echo after\n")

			require.NoError(t, err)
			assert.Equal(t, 2, code)
			assert.Empty(t, stdout, "whole-file validation must reject before any statement executes")
			assert.Contains(t, stderr, tc.want)
			assert.NotContains(t, stdout+stderr, "before")
			assert.NotContains(t, stdout+stderr, "after")
			assert.NotContains(t, stdout, "BAD")
		})
	}
}

func TestVulnHuntShellFeatureSubshellIsolation_BlockedCommandsDoNotMutateParent(t *testing.T) {
	stdout, stderr, code, err := runBlockedCommandsCyber3(t, `X=keep
OUT=$(eval echo BAD)
echo out=[$OUT]
(unset X)
echo x=$X
`)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "out=[]\nx=keep\n", stdout)
	assert.Contains(t, stderr, "rshell: eval: unknown command")
	assert.Contains(t, stderr, "rshell: unset: unknown command")
	assert.NotContains(t, stdout, "BAD")
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_BlockedCommandStatusesAndCaps(t *testing.T) {
	_, err := ParseScript(strings.Repeat(" ", MaxScriptBytes+1), "blocked_commands_oversized.sh")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
	assert.Contains(t, err.Error(), "5 MiB")

	stdout, stderr, code, runErr := runBlockedCommandsCyber3(t, "eval echo hi\n")
	require.NoError(t, runErr)
	assert.Equal(t, 127, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "rshell: eval: unknown command")

	stdout, stderr, code, runErr = runBlockedCommandsCyber3(t, "case x in x) echo BAD;; esac\n")
	require.NoError(t, runErr)
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Equal(t, "case statements are not supported\n", stderr)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_RuntimeBlockedBuiltinsDoNotExecuteHostFiles(t *testing.T) {
	dir := t.TempDir()
	payload := filepath.Join(dir, "payload.sh")
	marker := filepath.Join(dir, "marker")
	require.NoError(t, os.WriteFile(payload, []byte("#!/bin/sh\ntouch "+shellQuoteBlockedCommandsCyber3(marker)+"\n"), 0o755))

	stdout, stderr, code, err := runBlockedCommandsCyber3(t, strings.Join([]string{
		"eval " + shellQuoteBlockedCommandsCyber3(payload),
		"exec " + shellQuoteBlockedCommandsCyber3(payload),
		"command " + shellQuoteBlockedCommandsCyber3(payload),
		". " + shellQuoteBlockedCommandsCyber3(payload),
		"echo done",
		"",
	}, "\n"))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "done\n", stdout)
	assert.Contains(t, stderr, "rshell: eval: unknown command")
	assert.Contains(t, stderr, "rshell: exec: unknown command")
	assert.Contains(t, stderr, "rshell: command: unknown command")
	assert.Contains(t, stderr, "rshell: .: unknown command")
	assert.NoFileExists(t, marker)
}

func TestVulnHuntShellFeatureCompositionAttack_BlockedCommandRedirectionsRestore(t *testing.T) {
	stdout, stderr, code, err := runBlockedCommandsCyber3(t, `eval echo hidden 2>/dev/null
echo stderr_redir=$?
eval echo hidden >/dev/null
echo stdout_redir=$?
eval hidden | cat >/dev/null
echo pipe=$?
echo ok
`)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "stderr_redir=127\nstdout_redir=127\npipe=0\nok\n", stdout)
	assert.Equal(t, 2, strings.Count(stderr, "rshell: eval: unknown command"))
}

func TestVulnHuntShellFeatureReadonlyBypass_BlockedDeclsAndParamOpsDoNotMutate(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	err = r.Run(context.Background(), parseScript(t, "readonly RO=1\n"))
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	assert.Equal(t, ExitStatus(2), status)

	stdout.Reset()
	stderr.Reset()
	err = r.Run(context.Background(), parseScript(t, "RO=ok\necho $RO\n"))
	require.NoError(t, err)
	assert.Equal(t, "ok\n", stdout.String())
	assert.Empty(t, stderr.String())

	stdout.Reset()
	stderr.Reset()
	err = r.Run(context.Background(), parseScript(t, "X=original\n"))
	require.NoError(t, err)
	err = r.Run(context.Background(), parseScript(t, "echo ${X:=mutated}\n"))
	require.ErrorAs(t, err, &status)
	assert.Equal(t, ExitStatus(2), status)

	stdout.Reset()
	stderr.Reset()
	err = r.Run(context.Background(), parseScript(t, "X=tmp eval noop\necho $X\n"))
	require.NoError(t, err)
	assert.Equal(t, "original\n", stdout.String())
	assert.Contains(t, stderr.String(), "rshell: eval: unknown command")
}

func TestVulnHuntShellFeatureSignalContext_BlockedCommandStormHonorsCancellation(t *testing.T) {
	r, err := New(StdIO(nil, io.Discard, io.Discard), allowAllCommandsOpt(), MaxExecutionTime(25*time.Millisecond))
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	err = r.Run(context.Background(), parseScript(t, "while true; do eval noop; done\n"))
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestVulnHuntShellFeatureSignalContext_PreCanceledBlockedCommandIsSilent(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = r.Run(ctx, parseScript(t, "eval echo hi\n"))

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, stdout.String())
	assert.Empty(t, stderr.String())
}

func shellQuoteBlockedCommandsCyber3(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
