// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: subshell (shell-feature)

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

func runSubshellVulnHuntScript(t *testing.T, script, dir string, opts ...RunnerOption) (string, string, int, error) {
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

func TestVulnHuntShellFeatureExpansionChain_SubshellTokensFromExpansionNotReparsed(t *testing.T) {
	stdout, stderr, code, err := runSubshellVulnHuntScript(t, "PAYLOAD='echo SAFE ( echo HACKED )'\n$PAYLOAD\necho status=$?\n", "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "SAFE ( echo HACKED )\nstatus=0\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureParserConfusion_UnsupportedSubshellContentRejectedBeforeExecution(t *testing.T) {
	stdout, stderr, code, err := runSubshellVulnHuntScript(t, "echo before\n(echo bg & echo after)\n", "")

	require.NoError(t, err)
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "background execution")
}

func TestVulnHuntShellFeatureSubshellIsolation_StateDoesNotLeakToParent(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	require.NoError(t, os.Mkdir(subdir, 0o755))

	script := strings.Join([]string{
		"X=parent",
		"(X=child; Y=new; cd sub; echo child=$X/$Y; pwd)",
		"echo parent=$X/${Y}",
		"pwd",
	}, "\n")
	stdout, stderr, code, err := runSubshellVulnHuntScript(t, script, dir, AllowedPaths([]string{dir}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, strings.Join([]string{
		"child=child/new",
		subdir,
		"parent=parent/",
		dir,
		"",
	}, "\n"), stdout)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_StatusAndNegation(t *testing.T) {
	script := strings.Join([]string{
		"(false)",
		"echo false_status=$?",
		"(exit 7)",
		"echo exit_status=$?",
		"! (false)",
		"echo neg_false=$?",
		"! (true)",
		"echo neg_true=$?",
	}, "\n")
	stdout, stderr, code, err := runSubshellVulnHuntScript(t, script, "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "false_status=1\nexit_status=7\nneg_false=0\nneg_true=1\n", stdout)
}

func TestVulnHuntShellFeatureRedirectionChain_SubshellRedirectsDoNotPoisonParent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("from-file\n"), 0o644))

	script := "(cat < missing.txt)\necho after-missing\n(cat < input.txt)\necho after-good\ncat < input.txt\n"
	stdout, stderr, code, err := runSubshellVulnHuntScript(t, script, dir, AllowedPaths([]string{dir}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "after-missing\nfrom-file\nafter-good\nfrom-file\n", stdout)
	assert.Contains(t, stderr, "missing.txt")
}

func TestVulnHuntShellFeatureRedirectionChain_SubshellCannotBypassAllowedPaths(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	secret := filepath.Join(root, "secret")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(secret, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(secret, "hidden.txt"), []byte("secret\n"), 0o644))

	stdout, stderr, code, err := runSubshellVulnHuntScript(t, "(cat ../secret/hidden.txt)\necho status=$?\n", allowed, AllowedPaths([]string{allowed}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "status=1\n", stdout)
	assert.Contains(t, stderr, "permission denied")
	assert.NotContains(t, stdout, "secret")
}

func TestVulnHuntShellFeatureReadonlyBypass_SubshellRespectsReadonly(t *testing.T) {
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

	err = r.Run(context.Background(), parseScript(t, "(RO_VAR=hacked; echo in=$RO_VAR)\necho out=$RO_VAR\n"))
	require.NoError(t, err)
	assert.Equal(t, "in=original\nout=original\n", stdout.String())
	assert.Contains(t, stderr.String(), "readonly")
	assert.NotContains(t, stdout.String(), "hacked")
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_VarStorageCapSharedWithSubshell(t *testing.T) {
	r, err := New(allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Reset()
	require.NoError(t, r.writeEnv.Set("PAD", expand.Variable{
		Set:  true,
		Kind: expand.String,
		Str:  strings.Repeat("x", MaxTotalVarsBytes-2),
	}))

	sub := r.subshell(false)
	err = sub.writeEnv.Set("EXTRA", expand.Variable{Set: true, Kind: expand.String, Str: "abcd"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "variable storage limit")
}

func TestVulnHuntShellFeatureSubshellIsolation_GlobReadDirLimitSharedWithSubshell(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one.txt", "two.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644))
	}

	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt(), AllowedPaths([]string{dir}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Dir = dir

	err = r.Run(context.Background(), parseScript(t, "(echo * >/dev/null)\necho * >/dev/null\n"))
	require.NoError(t, err)
	require.NotNil(t, r.globReadDirCount)
	assert.Equal(t, int64(2), r.globReadDirCount.Load())
	assert.Empty(t, stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntShellFeatureSignalContext_SubshellCancellationPropagates(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt(), MaxExecutionTime(25*time.Millisecond))
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Reset()
	r.execHandler = func(ctx context.Context, _ []string) error {
		<-ctx.Done()
		return ctx.Err()
	}

	err = r.Run(context.Background(), parseScript(t, "(slow_external)\necho after\n"))
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, stdout.String())
	assert.Empty(t, stderr.String())
}
