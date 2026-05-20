// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt 2026-05-20-gpt-5.5-cyber-3 (target: simple_command)

package interp_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
)

func runSimpleCommandVulnHunt(t *testing.T, script string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()

	prog, err := interp.ParseScript(script, "simple_command_vuln_hunt.sh")
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	allOpts := append([]interp.RunnerOption{interp.StdIO(nil, &stdout, &stderr)}, opts...)
	r, err := interp.New(allOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	err = r.Run(context.Background(), prog)
	exitCode := 0
	if err != nil {
		var es interp.ExitStatus
		if errors.As(err, &es) {
			exitCode = int(es)
		} else {
			t.Fatalf("unexpected Run error: %v", err)
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

func TestVulnHuntShellFeatureExpansionChain_UnquotedCommandExpansionNoReparse(t *testing.T) {
	stdout, stderr, code := runSimpleCommandVulnHunt(t, "PAYLOAD='echo SAFE; echo HACKED'\n$PAYLOAD\n",
		interpoption.AllowAllCommands().(interp.RunnerOption))

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "SAFE; echo HACKED\n", stdout,
		"expanded semicolon must remain an argv byte, not become a second command")
}

func TestVulnHuntShellFeatureParserConfusion_AssignmentVariantsRejectedBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		script     string
		wantStderr string
		parseError bool
	}{
		{"append", "A+=x echo BAD\n", "+= is not supported", false},
		{"indexed", "A[0]=x echo BAD\n", "inline variables cannot be arrays", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.parseError {
				_, err := interp.ParseScript(tc.script, "simple_command_vuln_hunt.sh")
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantStderr)
				return
			}

			stdout, stderr, code := runSimpleCommandVulnHunt(t, tc.script,
				interpoption.AllowAllCommands().(interp.RunnerOption))

			assert.Equal(t, 2, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, tc.wantStderr)
			assert.NotContains(t, stdout, "BAD")
		})
	}
}

func TestVulnHuntShellFeatureSubshellIsolation_AssignmentAndCmdSubstDoNotLeak(t *testing.T) {
	stdout, stderr, code := runSimpleCommandVulnHunt(t, strings.Join([]string{
		"X=outer",
		"( X=inner; Y=subshell )",
		"Z=$(LEAK=inside; echo value)",
		`echo "$X|$Y|$LEAK|$Z"`,
		"",
	}, "\n"), interpoption.AllowAllCommands().(interp.RunnerOption))

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "outer|||value\n", stdout)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_AllowedCommandsFinalNameExact(t *testing.T) {
	stdout, stderr, code := runSimpleCommandVulnHunt(t, "IFS=/\nCMD=/cat\n$CMD secret.txt\n",
		interp.AllowedCommands([]string{"rshell:echo"}))

	assert.Equal(t, 127, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "rshell: cat: command not allowed")
}

func TestVulnHuntShellFeatureCompositionAttack_InvalidRedirectPreventsEarlierSideEffects(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := interp.New(interp.StdIO(nil, &stdout, &stderr), interpoption.AllowAllCommands().(interp.RunnerOption))
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	invalid, err := interp.ParseScript("A=changed\necho hi > \"$TARGET\"\n", "invalid_redirect.sh")
	require.NoError(t, err)
	err = r.Run(context.Background(), invalid)
	require.Error(t, err)
	var es interp.ExitStatus
	require.True(t, errors.As(err, &es))
	assert.Equal(t, interp.ExitStatus(2), es)
	assert.Contains(t, stderr.String(), "> file redirection is not supported")

	stdout.Reset()
	stderr.Reset()
	check, err := interp.ParseScript(`echo "[$A]"`, "check.sh")
	require.NoError(t, err)
	err = r.Run(context.Background(), check)
	require.NoError(t, err)
	assert.Equal(t, "[]\n", stdout.String(),
		"whole-file validation must prevent assignments before a later invalid redirect from taking effect")
	assert.Empty(t, stderr.String())
}

func TestVulnHuntShellFeatureCompositionAttack_RedirectRestoredAfterFailedInlineAssignment(t *testing.T) {
	large := strings.Repeat("x", interp.MaxVarBytes+1)
	script := "BIG=" + large + " echo BAD >/dev/null\necho VISIBLE\n"

	stdout, stderr, code := runSimpleCommandVulnHunt(t, script,
		interpoption.AllowAllCommands().(interp.RunnerOption))

	assert.Equal(t, 0, code)
	assert.Equal(t, "VISIBLE\n", stdout)
	assert.Contains(t, stderr, "BIG: value too large")
	assert.NotContains(t, stdout, "BAD")
}

func TestVulnHuntShellFeatureRedirectionChain_DynamicRedirectTargetsRejected(t *testing.T) {
	for _, script := range []string{
		"TARGET=/dev/null\necho hi > \"$TARGET\"\n",
		"echo hi > /dev/nul?\n",
	} {
		stdout, stderr, code := runSimpleCommandVulnHunt(t, script,
			interpoption.AllowAllCommands().(interp.RunnerOption))

		assert.Equal(t, 2, code)
		assert.Empty(t, stdout)
		assert.Contains(t, stderr, "file redirection is not supported")
	}
}
