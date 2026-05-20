// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: negation (shell-feature)

package interp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runNegationVulnHuntScript(t *testing.T, script, dir string, opts ...RunnerOption) (string, string, int, error) {
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

func TestVulnHuntShellFeatureExpansionChain_NegationTokenFromExpansionNotReparsed(t *testing.T) {
	stdout, stderr, code, err := runNegationVulnHuntScript(t, "bang='!'\n$bang false\necho status=$?\n", "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "status=127\n", stdout)
	assert.Contains(t, stderr, "rshell: !: unknown command")
}

func TestVulnHuntShellFeatureParserConfusion_EscapedBangIsNotNegation(t *testing.T) {
	stdout, stderr, code, err := runNegationVulnHuntScript(t, "\\! false\necho escaped=$?\n", "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "escaped=127\n", stdout)
	assert.Contains(t, stderr, "rshell: !: unknown command")
}

func TestVulnHuntShellFeatureSubshellIsolation_NegatedExitStaysInSubshell(t *testing.T) {
	stdout, stderr, code, err := runNegationVulnHuntScript(t, "x=parent\n! (x=child; exit 7)\necho status=$? x=$x\n! (true)\necho true_status=$?\n", "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "status=0 x=parent\ntrue_status=1\n", stdout)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_ExitAndTimeoutNotMasked(t *testing.T) {
	stdout, stderr, code, err := runNegationVulnHuntScript(t, "! exit 7\necho unreachable\n", "")

	require.NoError(t, err)
	assert.Equal(t, 7, code)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)

	r := newTimeoutRunner(t, MaxExecutionTime(100*time.Millisecond))
	start := time.Now()
	err = r.Run(context.Background(), parseScript(t, "! while true; do :; done"))
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 2*time.Second, "negated infinite loop did not stop promptly: %s", elapsed)
}

func TestVulnHuntShellFeatureCompositionAttack_NegatedPipelineFeedsAndOr(t *testing.T) {
	stdout, stderr, code, err := runNegationVulnHuntScript(t, "! exit 0 | exit 4\necho pipe=$?\n! false && echo continued\n! true || echo fallback\n", "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "pipe=0\ncontinued\nfallback\n", stdout)
}

func TestVulnHuntShellFeatureRedirectionChain_NegatedRedirectsStaySandboxedAndRestored(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "data.txt"), []byte("secret\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("outside\n"), 0o644))

	stdout, stderr, code, err := runNegationVulnHuntScript(t,
		"! cat < data.txt\necho allowed_status=$?\ncat < data.txt\n! cat < ../outside/secret.txt\necho blocked_status=$?\ncat <<EOF\nrestored\nEOF\n",
		allowed,
		AllowedPaths([]string{allowed}),
	)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "secret\nallowed_status=1\nsecret\nblocked_status=0\nrestored\n", stdout)
	assert.Contains(t, stderr, "outside/secret.txt")
	assert.NotContains(t, stdout, "outside")
}

func TestVulnHuntShellFeatureReadonlyBypass_NegatedInlineReadonlyAssignmentDoesNotMutate(t *testing.T) {
	stdout, stderr := runScriptWithReadonly(t, "! RO_VAR=hacked echo $RO_VAR\necho status=$? ro=$RO_VAR\n")

	assert.Contains(t, stderr, "readonly variable")
	assert.NotContains(t, stdout, "hacked")
	assert.Contains(t, stdout, "status=0 ro=original")
}
