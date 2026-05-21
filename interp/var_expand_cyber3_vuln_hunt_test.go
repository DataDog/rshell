// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: var_expand (shell-feature)

package interp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/expand"
)

func runVarExpandCyber3(t *testing.T, script string, dir string, opts ...RunnerOption) (string, string, int, error) {
	t.Helper()

	prog, err := ParseScript(script, "var_expand_cyber3_vuln_hunt.sh")
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
	if err != nil {
		var status ExitStatus
		if errors.As(err, &status) {
			code = int(status)
			err = nil
		}
	}
	return stdout.String(), stderr.String(), code, err
}

func TestVulnHuntShellFeatureExpansionChain_VariableAndCmdSubstValuesRemainData(t *testing.T) {
	stdout, stderr, code, err := runVarExpandCyber3(t, `EVIL='; echo HACKED ;'
payload=$(printf 'echo SAFE; echo HACKED')
echo a $EVIL b
$payload
echo '$(echo literal)'
`, "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a ; echo HACKED ; b\nSAFE; echo HACKED\n$(echo literal)\n", stdout)
	assert.Empty(t, stderr)
	assert.NotContains(t, stdout, "\nHACKED\n")
}

func TestVulnHuntShellFeatureExpansionChain_GlobFromVariableStaysSandboxedAndBounded(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "safe.txt"), []byte("safe\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o644))

	stdout, stderr, code, err := runVarExpandCyber3(t, `PATTERN='../outside/*'
cat $PATTERN
echo status=$?
cat safe.txt
`, allowed, AllowedPaths([]string{allowed}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "status=1\nsafe\n", stdout)
	assert.Contains(t, stderr, "permission denied")
	assert.NotContains(t, stdout, "secret")

	args := make([]string, MaxGlobReadDirCalls+1)
	for i := range args {
		args[i] = fmt.Sprintf("nomatch_%d_*", i)
	}
	stdout, stderr, code, err = runVarExpandCyber3(t, "echo "+strings.Join(args, " ")+"\n", allowed, AllowedPaths([]string{allowed}))
	require.NoError(t, err)
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "glob expansion exceeded maximum number of directory reads")
}

func TestVulnHuntShellFeatureParserConfusion_ValidationPrecedesExpansionSideEffects(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	require.NoError(t, r.Run(context.Background(), parseScript(t, "A=original\n")))

	err = r.Run(context.Background(), parseScript(t, "A=mutated echo ${A:=fallback}\necho after\n"))
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	assert.Equal(t, ExitStatus(2), status)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "operations (defaults, pattern removal, case conversion) are not supported")

	stdout.Reset()
	stderr.Reset()
	require.NoError(t, r.Run(context.Background(), parseScript(t, "echo $A\n")))
	assert.Equal(t, "original\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntShellFeatureParserConfusion_SpecialVariablesAndOddBytesStayControlled(t *testing.T) {
	stdout, stderr, code, err := runVarExpandCyber3(t, "V=ok\necho \"[$VÀR]\"\necho \"$(printf 'before\\x00after')\"\n", "")
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "[okÀR]\nbeforeafter\n", stdout)
	assert.Empty(t, stderr)

	stdout, stderr, code, err = runVarExpandCyber3(t, "A=mutated\necho $LINENO\necho after\n", "")
	require.NoError(t, err)
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "$LINENO is not supported")
}

func TestVulnHuntShellFeatureSubshellIsolation_AssignmentsAndStatusDoNotLeak(t *testing.T) {
	stdout, stderr, code, err := runVarExpandCyber3(t, `A=parent
false
captured=$(A=child; true; echo "$A:$?")
echo "captured=$captured parent=$A status=$?"
( A=subshell; B=inside; echo "sub=$A/$B" )
echo "parent=$A/$B"
echo left | { A=pipe; cat >/dev/null; }
echo "after_pipe=$A"
`, "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "captured=child:0 parent=parent status=0\nsub=subshell/inside\nparent=parent/\nafter_pipe=parent\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_VariableAndCommandSubstitutionCaps(t *testing.T) {
	stdout, stderr, code, err := runVarExpandCyber3(t, `A=x
A=$A$A;A=$A$A;A=$A$A;A=$A$A;A=$A$A;A=$A$A;A=$A$A;A=$A$A;A=$A$A;A=$A$A
A=$A$A;A=$A$A;A=$A$A;A=$A$A;A=$A$A;A=$A$A;A=$A$A;A=$A$A;A=$A$A
B=$A
C=x
echo DONE
`, "")
	require.NoError(t, err)
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "C: variable storage limit exceeded (1048577 bytes total)")

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Repeat("A", (1<<20)+100)), 0o644))
	stdout, stderr, code, err = runVarExpandCyber3(t, `x=$(<big.txt)
echo "$x" | wc -c
`, dir, AllowedPaths([]string{dir}))
	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1048577\n", strings.TrimLeft(stdout, " "))
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_MaxScriptBytesRejectsHugeExpansionScript(t *testing.T) {
	line := "A=$A$A; echo $A\n"
	script := strings.Repeat(line, MaxScriptBytes/len(line)+1)
	require.Greater(t, len(script), MaxScriptBytes)

	_, err := ParseScript(script, "var_expand_oversized.sh")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
	assert.Contains(t, err.Error(), "5 MiB")
}

func TestVulnHuntShellFeatureCompositionAttack_MetadataVariablesCannotBroadenSandbox(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "safe.txt"), []byte("safe\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o644))

	stdout, stderr, code, err := runVarExpandCyber3(t, "ALLOWED_PATHS='"+outside+"'\nPWD='"+outside+"'\ncat ../outside/secret.txt\necho status=$?\ncat safe.txt\n", allowed, AllowedPaths([]string{allowed}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "status=1\nsafe\n", stdout)
	assert.Contains(t, stderr, "permission denied")
	assert.NotContains(t, stdout, "secret")
}

func TestVulnHuntShellFeatureRedirectionChain_ExpandedInputRedirectSandboxedAndRestored(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "safe.txt"), []byte("safe\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o644))

	stdout, stderr, code, err := runVarExpandCyber3(t, `PATH_OK=safe.txt
PATH_OUT=../outside/secret.txt
cat < "$PATH_OK"
cat < "$PATH_OUT"
echo status=$?
cat <<'EOF'
restored
EOF
`, allowed, AllowedPaths([]string{allowed}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "safe\nstatus=1\nrestored\n", stdout)
	assert.Contains(t, stderr, "permission denied")
	assert.NotContains(t, stdout, "secret")
}

func TestVulnHuntShellFeatureRedirectionChain_DynamicOutputRedirectRejectedBeforeExecution(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	require.NoError(t, r.Run(context.Background(), parseScript(t, "X=original\n")))
	err = r.Run(context.Background(), parseScript(t, "X=mutated\nDEVNULL=/dev/null\necho hi > $DEVNULL\necho after\n"))
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	assert.Equal(t, ExitStatus(2), status)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "> file redirection is not supported")

	stdout.Reset()
	stderr.Reset()
	require.NoError(t, r.Run(context.Background(), parseScript(t, "echo $X\n")))
	assert.Equal(t, "original\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntShellFeatureReadonlyBypass_AssignmentPathsRespectReadonly(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Reset()
	require.NoError(t, r.writeEnv.Set("RO", expand.Variable{
		Set:      true,
		Kind:     expand.String,
		Str:      "original",
		ReadOnly: true,
	}))

	err = r.Run(context.Background(), parseScript(t, "RO=changed\necho ro=$RO\n"))
	require.NoError(t, err)
	assert.Equal(t, "ro=original\n", stdout.String())
	assert.Contains(t, stderr.String(), "RO: readonly variable")

	stdout.Reset()
	stderr.Reset()
	err = r.Run(context.Background(), parseScript(t, "echo ${RO:=changed}\n"))
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	assert.Equal(t, ExitStatus(2), status)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "operations (defaults, pattern removal, case conversion) are not supported")

	stdout.Reset()
	stderr.Reset()
	require.NoError(t, r.Run(context.Background(), parseScript(t, "echo ro=$RO\n")))
	assert.Equal(t, "ro=original\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntShellFeatureSignalContext_ExpansionLoopsRespectCancellation(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))

	r := newTimeoutRunner(t, StdIO(nil, &bytes.Buffer{}, &bytes.Buffer{}), AllowedPaths([]string{dir}), MaxExecutionTime(60*time.Millisecond))
	r.Dir = dir
	start := time.Now()
	err := r.Run(context.Background(), parseScript(t, "while true; do PAT='*.txt'; echo $PAT >/dev/null; done\n"))
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 5*time.Second)

	r2 := newTimeoutRunner(t, StdIO(nil, &bytes.Buffer{}, &bytes.Buffer{}), MaxExecutionTime(60*time.Millisecond))
	start = time.Now()
	err = r2.Run(context.Background(), parseScript(t, "x=$(while true; do echo x; done)\necho done\n"))
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 5*time.Second)
}
