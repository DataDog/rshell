// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: brace_group (shell-feature)

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
)

func runBraceGroupCyber3Script(t *testing.T, script, dir string, opts ...RunnerOption) (string, string, int, error) {
	t.Helper()
	prog, err := ParseScript(script, "brace_group_cyber3_vuln_hunt.sh")
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	allOpts := append([]RunnerOption{StdIO(nil, &stdout, &stderr), allowAllCommandsOpt()}, opts...)
	r, err := New(allOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	if dir != "" {
		r.Dir = dir
	}

	err = r.Run(context.Background(), prog)
	code := 0
	var status ExitStatus
	if errors.As(err, &status) {
		code = int(status)
		err = nil
	}
	return stdout.String(), stderr.String(), code, err
}

func TestVulnHuntShellFeatureExpansionChain_BraceGroupOutputNotReparsed(t *testing.T) {
	stdout, stderr, code, err := runBraceGroupCyber3Script(t,
		"PAYLOAD='echo SAFE; echo HACKED'\n{ $PAYLOAD; }\n", "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "SAFE; echo HACKED\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureExpansionChain_BraceGroupDoesNotLeakOutsideGlob(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.MkdirAll(allowed, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("leak"), 0o644))

	stdout, stderr, code, err := runBraceGroupCyber3Script(t,
		"{ echo ../outside/*; }\n",
		allowed,
		AllowedPaths([]string{allowed}),
	)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "../outside/*\n", stdout)
	assert.NotContains(t, stdout+stderr, "leak")
}

func TestVulnHuntShellFeatureParserConfusion_BlockedSyntaxInBraceGroupPreventsExecution(t *testing.T) {
	stdout, stderr, code, err := runBraceGroupCyber3Script(t,
		"echo before\n{ readonly X=1; echo bad; }\necho after\n", "")

	require.NoError(t, err)
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "readonly is not supported")
}

func TestVulnHuntShellFeatureParserConfusion_DeepValidBraceGroupsDoNotOverflow(t *testing.T) {
	var script strings.Builder
	for range 2000 {
		script.WriteString("{ ")
	}
	script.WriteString("echo ok")
	for range 2000 {
		script.WriteString("; }")
	}
	script.WriteByte('\n')

	stdout, stderr, code, err := runBraceGroupCyber3Script(t, script.String(), "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "ok\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureSubshellIsolation_BraceScopeDependsOnPipelineContext(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	require.NoError(t, os.MkdirAll(child, 0o755))

	stdout, stderr, code, err := runBraceGroupCyber3Script(t,
		"VAR=parent\n"+
			"{ VAR=brace; cd child; }\n"+
			"echo top=$VAR:$PWD\n"+
			"{ VAR=pipeline; cd ..; echo stage=$VAR:$PWD; } | cat\n"+
			"echo after=$VAR:$PWD\n",
		root,
		AllowedPaths([]string{root}),
	)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "top=brace:"+child+"\nstage=pipeline:"+root+"\nafter=brace:"+child+"\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_BraceGroupVariableStorageCapDoesNotAssign(t *testing.T) {
	large := strings.Repeat("x", MaxVarBytes+1)
	stdout, stderr, code, err := runBraceGroupCyber3Script(t,
		"{ BIG="+large+"; echo SHOULD_NOT_PRINT; }\necho after=${BIG}\n", "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "SHOULD_NOT_PRINT\nafter=\n", stdout)
	assert.Contains(t, stderr, "value too large")
}

func TestVulnHuntShellFeatureSignalContext_BraceGroupNoOutputLoopCancels(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r := newTimeoutRunner(t, StdIO(nil, &stdout, &stderr), MaxExecutionTime(50*time.Millisecond))

	err := r.Run(context.Background(), parseScript(t, "{ while true; do true; done; }\n"))

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, stdout.String())
}
