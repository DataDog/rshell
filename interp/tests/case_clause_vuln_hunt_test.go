// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt 2026-05-20-gpt-5.5-cyber-3 (target: case_clause)

package tests_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
)

func runCaseClauseVulnHunt(t *testing.T, script string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	prog, err := interp.ParseScript(script, "case_clause_vuln_hunt.sh")
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	allOpts := append([]interp.RunnerOption{
		interp.StdIO(nil, &stdout, &stderr),
		interpoption.AllowAllCommands().(interp.RunnerOption),
	}, opts...)
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

func TestVulnHuntShellFeatureExpansionChain_CaseKeywordExpansionNotReparsed(t *testing.T) {
	for _, script := range []string{
		"KW=case\n$KW\n",
		"PAYLOAD=$(printf 'case x in x) echo BAD;; esac')\n$PAYLOAD\n",
	} {
		stdout, stderr, code := runCaseClauseVulnHunt(t, script)

		assert.Equal(t, 127, code, "script=%q", script)
		assert.Empty(t, stdout)
		assert.Contains(t, stderr, "unknown command")
		assert.NotContains(t, stdout+stderr, "BAD")
	}
}

func TestVulnHuntShellFeatureParserConfusion_CaseFormsAllBlocked(t *testing.T) {
	for _, script := range []string{
		"case x in x) echo BAD;; esac\n",
		"case x in (x) echo BAD;; esac\n",
		"case b in [abc]) echo BAD;; esac\n",
		"case hello in hi|hello) echo BAD;; esac\n",
		"case x in *) echo BAD;; esac\n",
		"case x in x) ;; esac\n",
		"case x in x) echo BAD;& esac\n",
		"case x in x) echo BAD;;& esac\n",
		"case x in\r\n  x) echo BAD;;\r\nesac\r\n",
	} {
		stdout, stderr, code := runCaseClauseVulnHunt(t, script)

		assert.Equal(t, 2, code, "script=%q", script)
		assert.Empty(t, stdout)
		assert.Equal(t, "case statements are not supported\n", stderr)
	}
}

func TestVulnHuntShellFeatureExpansionChain_CaseSubjectPatternAndBodyNotExpanded(t *testing.T) {
	script := "case $(echo SUBJECT >&2) in $(echo PATTERN >&2)) echo BODY;; *) echo DEFAULT;; esac\n"
	stdout, stderr, code := runCaseClauseVulnHunt(t, script)

	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Equal(t, "case statements are not supported\n", stderr)
	assert.NotContains(t, stderr, "SUBJECT")
	assert.NotContains(t, stderr, "PATTERN")
}

func TestVulnHuntShellFeatureSubshellIsolation_NestedCaseDoesNotRunNeighbors(t *testing.T) {
	for _, script := range []string{
		"(case x in x) X=leak;; esac); echo after=$X\n",
		"echo left | case x in x) cat;; esac\n",
		"{ case x in x) echo BAD;; esac; echo after; }\n",
		"X=$(case x in x) echo leak;; esac)\necho after=$X\n",
	} {
		stdout, stderr, code := runCaseClauseVulnHunt(t, script)

		assert.Equal(t, 2, code, "script=%q", script)
		assert.Empty(t, stdout)
		assert.Equal(t, "case statements are not supported\n", stderr)
	}
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_WholeFileValidationPreventsSideEffects(t *testing.T) {
	stdout, stderr, code := runCaseClauseVulnHunt(t, "echo before\ncase x in x) echo BAD;; esac\necho after\n")

	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Equal(t, "case statements are not supported\n", stderr)
}

func TestVulnHuntShellFeatureCompositionAttack_CaseRedirectionsNeverApply(t *testing.T) {
	for _, script := range []string{
		"case x in x) echo BAD;; esac > out.txt\n",
		"case x in x) echo BAD;; esac < missing.txt\n",
		"case x in x) cat > out.txt;; esac\n",
	} {
		stdout, stderr, code := runCaseClauseVulnHunt(t, script)

		assert.Equal(t, 2, code, "script=%q", script)
		assert.Empty(t, stdout)
		assert.Equal(t, "case statements are not supported\n", stderr)
	}
}

func TestVulnHuntShellFeatureRedirectionChain_CaseHeredocDoesNotFeedStdin(t *testing.T) {
	stdout, stderr, code := runCaseClauseVulnHunt(t, "case x in x) cat 3<<EOF\nPAYLOAD\nEOF\n;; esac\n")

	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Equal(t, "case statements are not supported\n", stderr)
	assert.NotContains(t, stdout+stderr, "PAYLOAD")
}

func TestVulnHuntShellFeatureReadonlyBypass_CaseDoesNotMutateVariables(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := interp.New(
		interp.StdIO(nil, &stdout, &stderr),
		interpoption.AllowAllCommands().(interp.RunnerOption),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	blocked, err := interp.ParseScript("case x in x) readonly Y=1; Y=mutated;; esac\n", "blocked_case.sh")
	require.NoError(t, err)
	err = r.Run(context.Background(), blocked)
	require.Error(t, err)
	var es interp.ExitStatus
	require.True(t, errors.As(err, &es))
	assert.Equal(t, interp.ExitStatus(2), es)
	assert.Empty(t, stdout.String())
	assert.Equal(t, "case statements are not supported\n", stderr.String())

	stdout.Reset()
	stderr.Reset()
	check, err := interp.ParseScript("echo \"[$Y]\"\nY=ok\necho \"[$Y]\"\n", "check_case_env.sh")
	require.NoError(t, err)
	err = r.Run(context.Background(), check)
	require.NoError(t, err)
	assert.Equal(t, "[]\n[ok]\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_MaxScriptBytesRejectsLargeCase(t *testing.T) {
	body := strings.Repeat("x", interp.MaxScriptBytes+1)
	_, err := interp.ParseScript("case x in x) echo "+body+";; esac\n", "oversized_case.sh")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestVulnHuntShellFeatureSignalContext_CaseValidationDoesNotEvaluateBlockingSubject(t *testing.T) {
	stdout, stderr, code := runCaseClauseVulnHunt(t,
		"case $(while true; do :; done) in *) echo BAD;; esac\n",
		interp.MaxExecutionTime(100*time.Millisecond),
	)

	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Equal(t, "case statements are not supported\n", stderr)
}
