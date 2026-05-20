// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt 2026-05-20-gpt-5.5-cyber-3 (target: environment)

package interp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runEnvironmentVulnHunt(t *testing.T, script string, opts ...RunnerOption) (string, string, int) {
	t.Helper()

	prog := parseScript(t, script)
	var stdout, stderr bytes.Buffer
	allOpts := append([]RunnerOption{StdIO(nil, &stdout, &stderr)}, opts...)
	r, err := New(allOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	err = r.Run(context.Background(), prog)
	exitCode := 0
	if err != nil {
		var es ExitStatus
		if errors.As(err, &es) {
			exitCode = int(es)
		} else {
			t.Fatalf("unexpected Run error: %v", err)
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

func TestVulnHuntShellFeatureExpansionChain_EnvMetacharactersNotReparsed(t *testing.T) {
	stdout, stderr, code := runEnvironmentVulnHunt(t, "$PAYLOAD\n",
		Env("PAYLOAD=echo SAFE; echo HACKED"),
		allowAllCommandsOpt(),
	)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "SAFE; echo HACKED\n", stdout)
}

func TestVulnHuntShellFeatureParserConfusion_EnvQuestionDoesNotOverrideLastStatus(t *testing.T) {
	stdout, stderr, code := runEnvironmentVulnHunt(t, "false\necho \"$?\"\n",
		Env("?=99"),
		allowAllCommandsOpt(),
	)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "1\n", stdout)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_NoHostEnvInherited(t *testing.T) {
	t.Setenv("RSHELL_VULN_HUNT_SECRET", "SHOULD_NOT_LEAK")

	stdout, stderr, code := runEnvironmentVulnHunt(t, "echo \"secret=$RSHELL_VULN_HUNT_SECRET\"\n",
		allowAllCommandsOpt(),
	)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "secret=\n", stdout)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_PwdEnvDoesNotDriveSandboxPathResolution(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "file.txt"), []byte("allowed\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "file.txt"), []byte("secret\n"), 0o644))

	script := "PWD=" + shellQuoteForEnvironment(outside) + "\ncat file.txt\npwd\n"
	stdout, stderr, code := runEnvironmentVulnHunt(t, script,
		AllowedPaths([]string{allowed}),
		allowAllCommandsOpt(),
	)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "allowed\n")
	assert.NotContains(t, stdout, "secret")
	assert.Contains(t, stdout, allowed)
}

func TestVulnHuntShellFeatureCompositionAttack_EnvRedirectOperandStillSandboxed(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "input.txt"), []byte("allowed\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o644))

	script := "P=" + shellQuoteForEnvironment("../outside/secret.txt") + "\ncat < $P\necho status=$?\nP=input.txt\ncat < $P\n"
	stdout, stderr, code := runEnvironmentVulnHunt(t, script,
		AllowedPaths([]string{allowed}),
		allowAllCommandsOpt(),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "status=1\nallowed\n", stdout)
	assert.Contains(t, stderr, "secret.txt")
	assert.NotContains(t, stdout, "secret")
}

func TestVulnHuntShellFeatureRedirectionChain_TildeRedirectTargetsBlocked(t *testing.T) {
	for _, tc := range []struct {
		script string
		want   string
	}{
		{"cat < ~/secret\n", "tilde expansion is not supported"},
		{"echo hi > ~/out\n", "file redirection is not supported"},
	} {
		stdout, stderr, code := runEnvironmentVulnHunt(t, tc.script, allowAllCommandsOpt())

		assert.Equal(t, 2, code)
		assert.Empty(t, stdout)
		assert.Contains(t, stderr, tc.want)
	}
}

func TestVulnHuntShellFeatureSignalContext_EnvSplitLoopRespectsCancellation(t *testing.T) {
	r := newTimeoutRunner(t, MaxExecutionTime(100*time.Millisecond))
	start := time.Now()

	err := r.Run(context.Background(), parseScript(t, "ITEMS='a b'\nfor item in $ITEMS; do while true; do :; done; done\n"))

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestVulnHuntShellFeatureSubshellIsolation_BackgroundEnvSnapshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pipeline timing semantics in this test use POSIX-style shell snippets")
	}
	stdout, stderr, code := runEnvironmentVulnHunt(t,
		"X=parent\nprintf x | { read _; echo pipe=$X; X=pipe; }\necho parent=$X\n",
		allowAllCommandsOpt(),
	)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "pipe=parent\nparent=parent\n", stdout)
}

func shellQuoteForEnvironment(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
