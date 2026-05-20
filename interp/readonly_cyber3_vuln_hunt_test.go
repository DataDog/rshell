// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: readonly (shell-feature)

package interp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/expand"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newReadonlyCyber3Runner(t *testing.T, stdin io.Reader, opts ...RunnerOption) (*Runner, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	allOpts := append([]RunnerOption{StdIO(stdin, &stdout, &stderr), allowAllCommandsOpt()}, opts...)
	r, err := New(allOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Reset()
	require.NoError(t, r.writeEnv.Set("RO_VAR", expand.Variable{
		Set:      true,
		Kind:     expand.String,
		Str:      "original",
		ReadOnly: true,
	}))
	return r, &stdout, &stderr
}

func runReadonlyCyber3Script(t *testing.T, script string, stdin io.Reader, opts ...RunnerOption) (string, string, int, error) {
	t.Helper()

	r, stdout, stderr := newReadonlyCyber3Runner(t, stdin, opts...)
	err := r.Run(context.Background(), parseScript(t, script))
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

func TestVulnHuntShellFeatureParserConfusion_ReadonlyValidationPrecedesExecution(t *testing.T) {
	tests := map[string]string{
		"plain":     "readonly X=1\n",
		"print":     "readonly -p\n",
		"separator": "readonly -- X=1\n",
		"group":     "{ readonly X=1; }\n",
		"cmdsubst":  "echo $(readonly X=1; echo bad)\n",
		"redirect":  "readonly X=1 > out.txt\n",
	}

	for name, tail := range tests {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
			require.NoError(t, err)
			t.Cleanup(func() { _ = r.Close() })
			r.openHandler = func(context.Context, string, int, os.FileMode) (io.ReadWriteCloser, error) {
				t.Fatalf("readonly validation reached openHandler for %s", name)
				return nil, nil
			}

			err = r.Run(context.Background(), parseScript(t, "X=changed\n"+tail+"echo after\n"))
			var status ExitStatus
			require.ErrorAs(t, err, &status)
			assert.Equal(t, ExitStatus(2), status)
			assert.Empty(t, stdout.String())
			assert.Contains(t, stderr.String(), "readonly is not supported")
		})
	}
}

func TestVulnHuntShellFeatureExpansionChain_ReadonlyAssignmentVectorsPreserveValue(t *testing.T) {
	tests := map[string]struct {
		script       string
		stdin        io.Reader
		wantStdout   string
		wantStderr   string
		notInStdout  string
		expectedCode int
	}{
		"direct_assignment": {
			script:      "RO_VAR=hacked\necho after=$RO_VAR\n",
			wantStdout:  "after=original\n",
			wantStderr:  "readonly variable",
			notInStdout: "hacked",
		},
		"inline_assignment": {
			script:      "RO_VAR=hacked echo HIT\necho after=$RO_VAR\n",
			wantStdout:  "after=original\n",
			wantStderr:  "readonly variable",
			notInStdout: "HIT",
		},
		"read_builtin": {
			script:      "read RO_VAR\necho after=$RO_VAR\n",
			stdin:       strings.NewReader("hacked\n"),
			wantStdout:  "after=original\n",
			wantStderr:  "readonly variable",
			notInStdout: "hacked",
		},
		"assigning_parameter_expansion": {
			script:       "echo ${RO_VAR:=hacked}\necho after=$RO_VAR\n",
			wantStdout:   "",
			wantStderr:   "not supported",
			notInStdout:  "hacked",
			expectedCode: 2,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code, err := runReadonlyCyber3Script(t, tc.script, tc.stdin)

			require.NoError(t, err)
			if tc.expectedCode != 0 {
				assert.Equal(t, tc.expectedCode, code)
			}
			assert.Contains(t, stdout, tc.wantStdout)
			assert.Contains(t, stderr, tc.wantStderr)
			assert.NotContains(t, stdout, tc.notInStdout)
		})
	}
}

func TestVulnHuntShellFeatureSubshellIsolation_ReadonlyPropagatesToChildren(t *testing.T) {
	tests := map[string]string{
		"subshell":   "( RO_VAR=hacked; echo inside=$RO_VAR )\necho after=$RO_VAR\n",
		"pipeline":   "echo seed | { RO_VAR=hacked; echo inside=$RO_VAR; }\necho after=$RO_VAR\n",
		"pipe_paren": "echo seed | ( RO_VAR=hacked; echo inside=$RO_VAR; )\necho after=$RO_VAR\n",
		"cmdsubst":   "echo got=$(RO_VAR=hacked; echo $RO_VAR)\necho after=$RO_VAR\n",
	}

	for name, script := range tests {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code, err := runReadonlyCyber3Script(t, script, nil)

			require.NoError(t, err)
			assert.Equal(t, 0, code)
			assert.Contains(t, stderr, "readonly variable")
			assert.NotContains(t, stdout, "hacked")
			assert.Contains(t, stdout, "after=original")
		})
	}
}

func TestVulnHuntShellFeatureCompositionAttack_MixedInlineRestorePreservesReadonly(t *testing.T) {
	stdout, stderr, code, err := runReadonlyCyber3Script(t, `FOO=ok RO_VAR=evil echo HIT
echo "after foo=$FOO ro=$RO_VAR"
RO_VAR=again
echo "check=$RO_VAR"
`, nil)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr, "readonly variable")
	assert.NotContains(t, stdout, "HIT")
	assert.Contains(t, stdout, "after foo= ro=original")
	assert.Contains(t, stdout, "check=original")
}

func TestVulnHuntShellFeatureRedirectionChain_FailedRedirectDoesNotDowngradeReadonly(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code, err := runReadonlyCyber3Script(t, `RO_VAR=hacked < missing.txt
echo "after=$RO_VAR"
RO_VAR=again
echo "check=$RO_VAR"
`, nil, AllowedPaths([]string{dir}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "after=original\n")
	assert.Contains(t, stdout, "check=original\n")
	assert.Contains(t, stderr, "no such file")
	assert.Contains(t, stderr, "readonly variable")
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_WriteEnvRejectsReadonlyChanges(t *testing.T) {
	r, _, _ := newReadonlyCyber3Runner(t, nil)

	assert.ErrorContains(t, r.writeEnv.Set("RO_VAR", expand.Variable{Set: true, Kind: expand.String, Str: "changed"}), "readonly variable")
	assert.ErrorContains(t, r.writeEnv.Set("RO_VAR", expand.Variable{}), "readonly variable")
	assert.ErrorContains(t, r.writeEnv.Set("RO_VAR", expand.Variable{Kind: expand.KeepValue, Exported: true}), "readonly variable")

	vr := r.writeEnv.Get("RO_VAR")
	assert.True(t, vr.ReadOnly)
	assert.Equal(t, "original", vr.Str)
}

func TestVulnHuntShellFeatureSignalContext_ReadonlyReadHonorsCancellation(t *testing.T) {
	pr, pw, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = pr.Close()
		_ = pw.Close()
	})

	var stdout, stderr bytes.Buffer
	r, err := New(
		StdIO(pr, &stdout, &stderr),
		allowAllCommandsOpt(),
		MaxExecutionTime(40*time.Millisecond),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Reset()
	require.NoError(t, r.writeEnv.Set("RO_VAR", expand.Variable{
		Set:      true,
		Kind:     expand.String,
		Str:      "original",
		ReadOnly: true,
	}))

	err = r.Run(context.Background(), parseScript(t, "read RO_VAR\n"))

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Empty(t, stdout.String())
	assert.NotContains(t, stderr.String(), "hacked")
	assert.Equal(t, "original", r.writeEnv.Get("RO_VAR").Str)
}
