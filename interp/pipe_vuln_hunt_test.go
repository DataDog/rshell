// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: pipe (shell-feature)

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

func runPipeVulnHuntScript(t *testing.T, script, dir string, opts ...RunnerOption) (string, string, int, error) {
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

func TestVulnHuntShellFeatureExpansionChain_PipeTokensFromExpansionNotReparsed(t *testing.T) {
	stdout, stderr, code, err := runPipeVulnHuntScript(t, "PAYLOAD='echo SAFE | cat'\n$PAYLOAD\necho status=$?\n", "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "SAFE | cat\nstatus=0\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureParserConfusion_PipeAllAndBackgroundRejectedBeforeExecution(t *testing.T) {
	tests := map[string]struct {
		script string
		want   string
	}{
		"pipe_all": {
			script: "echo before; echo secret |& cat\n",
			want:   "|& is not supported",
		},
		"background_after_pipe": {
			script: "echo before; echo left | cat & echo after\n",
			want:   "background execution",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code, err := runPipeVulnHuntScript(t, tc.script, "")
			require.NoError(t, err)
			assert.Equal(t, 2, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, tc.want)
		})
	}
}

func TestVulnHuntShellFeatureParserConfusion_LineContinuationsAfterPipe(t *testing.T) {
	tests := map[string]struct {
		script string
		want   string
	}{
		"linebreak": {
			script: "printf 'alpha\\n' |\ncat\n",
			want:   "alpha\n",
		},
		"blank_line": {
			script: "printf 'beta\\n' |\n\ncat\n",
			want:   "beta\n",
		},
		"comment": {
			script: "printf 'gamma\\n' | # ignored comment\ncat\n",
			want:   "gamma\n",
		},
		"crlf": {
			script: "printf 'delta\\n' |\r\ncat\r\n",
			want:   "delta\n",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code, err := runPipeVulnHuntScript(t, tc.script, "")
			require.NoError(t, err)
			assert.Equal(t, 0, code)
			assert.Equal(t, tc.want, stdout)
			assert.Empty(t, stderr)
		})
	}
}

func TestVulnHuntShellFeatureSubshellIsolation_PipelineStateDoesNotLeak(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	require.NoError(t, os.Mkdir(subdir, 0o755))

	script := strings.Join([]string{
		"X=parent",
		"{ X=left; echo left=$X; } | cat",
		"echo after_left=$X",
		"echo value | read X",
		"echo after_read=$X",
		"{ cd sub; pwd; } | cat",
		"pwd",
	}, "\n")
	stdout, stderr, code, err := runPipeVulnHuntScript(t, script, dir, AllowedPaths([]string{dir}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, strings.Join([]string{
		"left=left",
		"after_left=parent",
		"after_read=parent",
		subdir,
		dir,
		"",
	}, "\n"), stdout)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_RightmostStatusAndNegation(t *testing.T) {
	script := strings.Join([]string{
		"false | true",
		"echo s1=$?",
		"true | false",
		"echo s2=$?",
		"false | false | true",
		"echo s3=$?",
		"! true | false",
		"echo s4=$?",
		"! false | true",
		"echo s5=$?",
	}, "\n")
	stdout, stderr, code, err := runPipeVulnHuntScript(t, script, "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "s1=0\ns2=1\ns3=0\ns4=0\ns5=1\n", stdout)
}

func TestVulnHuntShellFeatureCompositionAttack_RedirectionPrecedenceAndRestore(t *testing.T) {
	script := strings.Join([]string{
		"echo hidden >/dev/null | cat",
		"echo after-left",
		"echo hidden-right | cat >/dev/null",
		"echo after-right",
		"echo visible | cat",
	}, "\n")
	stdout, stderr, code, err := runPipeVulnHuntScript(t, script, "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "after-left\nafter-right\nvisible\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureRedirectionChain_FailedStageRedirectDoesNotPoisonNextPipeline(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("from-file\n"), 0o644))

	script := "cat < missing.txt | cat\necho after-missing\ncat < input.txt | cat\necho after-cat\n"
	stdout, stderr, code, err := runPipeVulnHuntScript(t, script, dir, AllowedPaths([]string{dir}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "after-missing\nfrom-file\nafter-cat\n", stdout)
	assert.Contains(t, stderr, "missing.txt")
}

func TestVulnHuntShellFeatureReadonlyBypass_PipelineStagesRespectReadonly(t *testing.T) {
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

	err = r.Run(context.Background(), parseScript(t, "echo changed | read RO_VAR\necho after_read=$RO_VAR\nRO_VAR=inline echo hidden | cat\necho after_inline=$RO_VAR\n"))
	require.NoError(t, err)
	assert.Equal(t, "after_read=original\nafter_inline=original\n", stdout.String())
	assert.Contains(t, stderr.String(), "readonly")
	assert.NotContains(t, stdout.String(), "hidden")
}

func TestVulnHuntShellFeatureSignalContext_EarlyRightExitClosesPipe(t *testing.T) {
	dir := t.TempDir()
	var data strings.Builder
	for range 512 {
		data.WriteString("line\n")
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big.txt"), []byte(data.String()), 0o644))

	stdout, stderr, code, err := runPipeVulnHuntScript(t, "cat big.txt | head -n 1\necho after-head\n", dir, AllowedPaths([]string{dir}), MaxExecutionTime(2*time.Second))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line\nafter-head\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureSignalContext_LeftStageCancellationUnblocksPipeline(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt(), MaxExecutionTime(25*time.Millisecond))
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Reset()
	r.execHandler = func(ctx context.Context, _ []string) error {
		<-ctx.Done()
		return ctx.Err()
	}

	err = r.Run(context.Background(), parseScript(t, "slow_external | true\n"))
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntShellFeatureSubshellIsolation_GlobReadDirLimitSharedAcrossPipelineStages(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one.txt", "two.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644))
	}

	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt(), AllowedPaths([]string{dir}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Dir = dir

	err = r.Run(context.Background(), parseScript(t, "echo * | cat >/dev/null\necho * | cat >/dev/null\n"))
	require.NoError(t, err)
	require.NotNil(t, r.globReadDirCount)
	assert.Equal(t, int64(2), r.globReadDirCount.Load())
	assert.Empty(t, stdout.String())
	assert.Empty(t, stderr.String())
}
