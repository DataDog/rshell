// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: errors (shell-feature)

package interp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runErrorsVulnHuntScript(t *testing.T, script string, opts ...RunnerOption) (string, string, int, error) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	allOpts := append([]RunnerOption{StdIO(nil, &stdout, &stderr), allowAllCommandsOpt()}, opts...)
	r, err := New(allOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	prog, err := ParseScript(script, "errors_vuln_hunt.sh")
	require.NoError(t, err)

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

func TestVulnHuntShellFeatureExpansionChain_EmptyCommandExpansionsStaySilent(t *testing.T) {
	stdout, stderr, code, err := runErrorsVulnHuntScript(t, "x=''\n$x\necho status=$?\nA=$(printf '\\n')\n$A\necho second=$?\n")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "status=0\nsecond=0\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureParserConfusion_ValidationErrorsPreventPartialExecution(t *testing.T) {
	for name, script := range map[string]string{
		"case":     "echo before\ncase x in x) echo BAD;; esac\necho after\n",
		"function": "echo before\nf() { echo BAD; }\necho after\n",
		"process":  "echo before\ncat <(echo BAD)\necho after\n",
		"arith":    "echo before\necho $((1+1))\necho after\n",
	} {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code, err := runErrorsVulnHuntScript(t, script)
			require.NoError(t, err)
			assert.Equal(t, 2, code)
			assert.Empty(t, stdout)
			assert.NotEmpty(t, stderr)
			assert.NotContains(t, stdout+stderr, "BAD")
			assert.NotContains(t, stdout, "before")
			assert.NotContains(t, stdout, "after")
		})
	}
}

func TestVulnHuntShellFeatureSubshellIsolation_ErrorStatusPropagates(t *testing.T) {
	stdout, stderr, code, err := runErrorsVulnHuntScript(t, "(no_such_cmd_xyz)\necho subshell=$?\necho result=[$(unknown_cmd_xyz)]\necho cmdsubst=$?\n")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "subshell=127\nresult=[]\ncmdsubst=0\n", stdout)
	assert.Contains(t, stderr, "no_such_cmd_xyz")
	assert.Contains(t, stderr, "unknown_cmd_xyz")
}

func TestVulnHuntShellFeatureCompositionAttack_ErrorRedirectionAndPipelineSemantics(t *testing.T) {
	stdout, stderr, code, err := runErrorsVulnHuntScript(t, "no_such_cmd 2>/dev/null\necho redir=$?\nunknown_pipe_left | cat >/dev/null\necho left=$?\necho hi | unknown_pipe_right\necho right=$?\n")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "redir=127\nleft=0\nright=127\n", stdout)
	assert.NotContains(t, stderr, "no_such_cmd")
	assert.Contains(t, stderr, "unknown_pipe_left")
	assert.Contains(t, stderr, "unknown_pipe_right")
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_ParseAndValidationErrorsStayOnStderr(t *testing.T) {
	_, err := ParseScript(strings.Repeat(" ", MaxScriptBytes+1), "errors_oversized.sh")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")

	stdout, stderr, code, runErr := runErrorsVulnHuntScript(t, "echo before\ncase x in x) echo BAD;; esac\n")
	require.NoError(t, runErr)
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Equal(t, "case statements are not supported\n", stderr)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_FailedErrorsDoNotCorruptLastStatus(t *testing.T) {
	stdout, stderr, code, err := runErrorsVulnHuntScript(t, "echo first\nno_such_cmd\necho after=$?\nfalse\necho false=$?\n")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "first\nafter=127\nfalse=1\n", stdout)
	assert.Contains(t, stderr, "no_such_cmd")
}

func TestVulnHuntShellFeatureSignalContext_ErrorLoopHonorsTimeout(t *testing.T) {
	r, err := New(StdIO(nil, io.Discard, io.Discard), allowAllCommandsOpt(), MaxExecutionTime(25*time.Millisecond))
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	prog, err := ParseScript("while true; do no_such_cmd; done\n", "errors_timeout.sh")
	require.NoError(t, err)

	err = r.Run(context.Background(), prog)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
