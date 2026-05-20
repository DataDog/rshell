// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: function (shell-feature)

package interp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runFunctionVulnHuntScript(t *testing.T, script, dir string, opts ...RunnerOption) (string, string, int, error) {
	t.Helper()

	prog, err := ParseScript(script, "function_vuln_hunt.sh")
	if err != nil {
		return "", err.Error() + "\n", 2, nil
	}

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

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = r.Run(ctx, prog)
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

func TestVulnHuntShellFeatureExpansionChain_FunctionTokensFromExpansionNotReparsed(t *testing.T) {
	stdout, stderr, code, err := runFunctionVulnHuntScript(t, "PAYLOAD='f() { echo BAD; }'\n$PAYLOAD\necho status=$?\n", "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "status=127\n", stdout)
	assert.Contains(t, stderr, "unknown command")
	assert.NotContains(t, stdout+stderr, "BAD")
}

func TestVulnHuntShellFeatureExpansionChain_FunctionTextInHeredocIsData(t *testing.T) {
	script := "cat <<'EOF'\nf() { echo BAD; }\nEOF\n"

	stdout, stderr, code, err := runFunctionVulnHuntScript(t, script, "")

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "f() { echo BAD; }\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureParserConfusion_FunctionFormsRejectedBeforeExecution(t *testing.T) {
	tests := map[string]string{
		"paren":            "echo before\nf() { echo BAD; }\necho after\n",
		"function_keyword": "echo before\nfunction f { echo BAD; }\necho after\n",
		"function_paren":   "echo before\nfunction f() { echo BAD; }\necho after\n",
		"newline_body":     "echo before\nf()\n{\necho BAD\n}\necho after\n",
		"if_body":          "echo before\nf() { if true; then echo BAD; fi; }\necho after\n",
		"loop_body":        "echo before\nf() for i in 1; do echo BAD; done\necho after\n",
		"nested":           "echo before\nouter() { inner() { echo BAD; }; inner; }\necho after\n",
		"portable_name":    "echo before\nbad_name() { echo BAD; }\necho after\n",
	}

	for name, script := range tests {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code, err := runFunctionVulnHuntScript(t, script, "")
			require.NoError(t, err)
			assert.Equal(t, 2, code)
			assert.Empty(t, stdout, "whole-file validation must reject before any echo runs")
			assert.Contains(t, stderr, "function declarations are not supported")
		})
	}
}

func TestVulnHuntShellFeatureParserConfusion_DeepFunctionNestingRejectedCleanly(t *testing.T) {
	var nested strings.Builder
	for i := 0; i < 3000; i++ {
		nested.WriteString("f")
		nested.WriteString(strconv.Itoa(i))
		nested.WriteString("() {\n")
	}
	nested.WriteString("echo BAD\n")
	for i := 0; i < 3000; i++ {
		nested.WriteString("}\n")
	}

	stdout, stderr, code, err := runFunctionVulnHuntScript(t, nested.String(), "")

	require.NoError(t, err)
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "function declarations are not supported")
}

func TestVulnHuntShellFeatureParserConfusion_DeepFunctionBodyCommandSubstRejectedCleanly(t *testing.T) {
	var script strings.Builder
	script.WriteString("f() { echo ")
	for i := 0; i < 1000; i++ {
		script.WriteString("$(echo ")
	}
	script.WriteString("BAD")
	for i := 0; i < 1000; i++ {
		script.WriteByte(')')
	}
	script.WriteString("\n}\n")

	stdout, stderr, code, err := runFunctionVulnHuntScript(t, script.String(), "")

	require.NoError(t, err)
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "function declarations are not supported")
}

func TestVulnHuntShellFeatureSubshellIsolation_FunctionFailureDoesNotPoisonReusableRunner(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	err = r.Run(context.Background(), parseScript(t, "f() { echo BAD; }\n"))
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	assert.Equal(t, ExitStatus(2), status)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "function declarations are not supported")

	stdout.Reset()
	stderr.Reset()
	err = r.Run(context.Background(), parseScript(t, "echo clean\n"))
	require.NoError(t, err)
	assert.Equal(t, "clean\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_OversizedFunctionScriptRejectedBeforeParse(t *testing.T) {
	_, err := ParseScript("f() {\n"+strings.Repeat("x", MaxScriptBytes+1)+"\n}\n", "oversized_function.sh")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestVulnHuntShellFeatureCompositionAttack_FunctionRedirectionsDoNotOpenOrExpand(t *testing.T) {
	dir := t.TempDir()
	scripts := []string{
		"f() { echo BAD; } > out.txt\necho after\n",
		"f() { echo BAD; } > /dev/null\necho after\n",
		`f() { echo BAD; } > "$(echo out.txt)"` + "\necho after\n",
	}

	for _, script := range scripts {
		t.Run(script, func(t *testing.T) {
			stdout, stderr, code, err := runFunctionVulnHuntScript(t, script, dir, AllowedPaths([]string{dir}))
			require.NoError(t, err)
			assert.Equal(t, 2, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "function declarations are not supported")
			assert.NoFileExists(t, filepath.Join(dir, "out.txt"))
		})
	}
}

func TestVulnHuntShellFeatureCompositionAttack_FunctionInCommandSubstitutionRejectedBeforeParentRuns(t *testing.T) {
	stdout, stderr, code, err := runFunctionVulnHuntScript(t, `echo before
x=$(f() { echo BAD; }; f)
echo after
`, "")

	require.NoError(t, err)
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "function declarations are not supported")
}

func TestVulnHuntShellFeatureReadonlyBypass_FunctionBodyDoesNotMutateParent(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	err = r.Run(context.Background(), parseScript(t, "f() { readonly RO_VAR=1; RO_VAR=bad; }\n"))
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	assert.Equal(t, ExitStatus(2), status)

	stdout.Reset()
	stderr.Reset()
	err = r.Run(context.Background(), parseScript(t, "RO_VAR=ok\necho $RO_VAR\n"))
	require.NoError(t, err)
	assert.Equal(t, "ok\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntShellFeatureRedirectionChain_FunctionInputRedirectDoesNotReadOutsideAllowedPaths(t *testing.T) {
	allowed := t.TempDir()
	secretDir := t.TempDir()
	secretPath := filepath.Join(secretDir, "secret.txt")
	require.NoError(t, os.WriteFile(secretPath, []byte("secret\n"), 0o644))

	script := "f() { echo BAD; } < " + shellQuoteFunctionVulnHunt(secretPath) + "\necho after\n"
	stdout, stderr, code, err := runFunctionVulnHuntScript(t, script, allowed, AllowedPaths([]string{allowed}))

	require.NoError(t, err)
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "function declarations are not supported")
}

func shellQuoteFunctionVulnHunt(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
