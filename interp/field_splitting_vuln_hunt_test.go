// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: field_splitting (shell-feature)

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

func runFieldSplittingVulnHuntScript(t *testing.T, script, dir string, opts ...RunnerOption) (string, string, int, error) {
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

func TestVulnHuntShellFeatureExpansionChain_IFSMetacharactersNotReparsed(t *testing.T) {
	stdout, stderr, code, err := runFieldSplittingVulnHuntScript(t, "IFS='|;'\nPAYLOAD='alpha|echo HACKED;beta'\necho $PAYLOAD\necho marker\n", "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "alpha echo HACKED beta\nmarker\n", stdout)
}

func TestVulnHuntShellFeatureExpansionChain_SplitGlobStaysSandboxed(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "visible.txt"), []byte("ok\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o644))

	stdout, stderr, code, err := runFieldSplittingVulnHuntScript(t, "PAT='*.txt'\necho $PAT\nPAT='../outside/*.txt'\necho $PAT\n", allowed, AllowedPaths([]string{allowed}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "visible.txt\n")
	assert.Contains(t, stdout, "../outside/*.txt\n")
	assert.NotContains(t, stdout, "secret.txt")
}

func TestVulnHuntShellFeatureParserConfusion_CustomIFSBytesAreLiteral(t *testing.T) {
	script := `IFS='\'
S='a\b\c'
for x in $S; do echo "[$x]"; done
IFS='
'
N='one
two'
for x in $N; do echo "<$x>"; done
`
	stdout, stderr, code, err := runFieldSplittingVulnHuntScript(t, script, "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "[a]\n[b]\n[c]\n<one>\n<two>\n", stdout)
}

func TestVulnHuntShellFeatureSubshellIsolation_CommandSubIFSDoesNotLeak(t *testing.T) {
	stdout, stderr, code, err := runFieldSplittingVulnHuntScript(t, "IFS=,\nA=$(IFS=:; echo 'x:y')\nfor w in $A; do echo \"[$w]\"; done\nB='p,q'\nfor w in $B; do echo \"<$w>\"; done\n", "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "[x:y]\n<p>\n<q>\n", stdout)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_OversizedIFSDoesNotRunCommand(t *testing.T) {
	large := strings.Repeat("x", MaxVarBytes+1)
	stdout, stderr, code, err := runFieldSplittingVulnHuntScript(t, "IFS="+large+" echo SHOULD_NOT_RUN\n", "")

	require.NoError(t, err)
	assert.NotEqual(t, 0, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "IFS: value too large")
	assert.NotContains(t, stdout, "SHOULD_NOT_RUN")
}

func TestVulnHuntShellFeatureCompositionAttack_InlineIFSRestoredAfterRead(t *testing.T) {
	script := `IFS=: read -r f1 f2 <<EOF
a:b
EOF
echo "f1=[$f1] f2=[$f2]"
DATA='c:d'
for w in $DATA; do echo "[$w]"; done
`
	stdout, stderr, code, err := runFieldSplittingVulnHuntScript(t, script, "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "f1=[a] f2=[b]\n[c:d]\n", stdout)
}

func TestVulnHuntShellFeatureRedirectionChain_RedirectOperandNotFieldSplit(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "input.txt"), []byte("allowed\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o644))

	script := "IFS=/\nP='../outside/secret.txt'\ncat < $P\necho status=$?\nP='input.txt'\ncat < $P\n"
	stdout, stderr, code, err := runFieldSplittingVulnHuntScript(t, script, allowed, AllowedPaths([]string{allowed}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "status=1\nallowed\n", stdout)
	assert.Contains(t, stderr, "secret.txt")
	assert.NotContains(t, stdout, "secret")
}

func TestVulnHuntShellFeatureReadonlyBypass_ReadonlyIFSKeepsOriginalSplitter(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	r.Reset()
	require.NoError(t, r.writeEnv.Set("IFS", expand.Variable{
		Set:      true,
		Kind:     expand.String,
		Str:      " \t\n",
		ReadOnly: true,
	}))

	err = r.Run(context.Background(), parseScript(t, "IFS=:\nA='a:b'\nfor w in $A; do echo \"[$w]\"; done\n"))
	require.NoError(t, err)
	assert.Equal(t, "[a:b]\n", stdout.String())
	assert.Contains(t, stderr.String(), "IFS: readonly variable")
}

func TestVulnHuntShellFeatureSignalContext_FieldSplitForLoopRespectsCancellation(t *testing.T) {
	r := newTimeoutRunner(t, MaxExecutionTime(100*time.Millisecond))

	start := time.Now()
	err := r.Run(context.Background(), parseScript(t, "LIST='x y'\nfor v in $LIST; do while true; do :; done; done\n"))
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 2*time.Second, "field-split for loop did not stop promptly: %s", elapsed)
}
