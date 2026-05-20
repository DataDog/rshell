// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: break (builtin)

package breakcmd_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
)

func runBreakVulnHunt(t *testing.T, script string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScript(t, script, "", opts...)
}

func TestVulnHuntBuiltinFlagDrivenExploit_BreakHelpDoesNotMutateLoopControl(t *testing.T) {
	stdout, stderr, code := runBreakVulnHunt(t,
		"for i in 1 2; do break --help; echo after-$i; done; echo done\n")

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, 2, strings.Count(stdout, "break: break [n]"))
	assert.Contains(t, stdout, "after-1\n")
	assert.Contains(t, stdout, "after-2\n")
	assert.True(t, strings.HasSuffix(stdout, "done\n"))
}

func TestVulnHuntBuiltinDeclaredVsImplemented_OutsideLoopBreakDoesNotPoisonFlow(t *testing.T) {
	stdout, stderr, code := runBreakVulnHunt(t,
		"break\n"+
			"echo status=$?\n"+
			"false || break\n"+
			"echo after-or\n"+
			"break && echo after-and\n"+
			"echo done\n")

	assert.Equal(t, 0, code)
	assert.Equal(t, "status=0\nafter-or\nafter-and\ndone\n", stdout)
	assert.Equal(t, 3, strings.Count(stderr, "break is only useful in a loop"))
}

func TestVulnHuntBuiltinIntegerOverflow_InvalidArgumentsAbortOrBreakCompatibly(t *testing.T) {
	stdout, stderr, code := runBreakVulnHunt(t, "for i in 1; do break abc; echo after; done; echo done\n")
	assert.Equal(t, 128, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "break: abc: numeric argument required")

	stdout, stderr, code = runBreakVulnHunt(t, "for i in 1; do break 1 2; echo after; done; echo done\n")
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "break: too many arguments")

	for _, arg := range []string{"0", "-1"} {
		t.Run(arg, func(t *testing.T) {
			stdout, stderr, code := runBreakVulnHunt(t,
				"for i in 1; do echo before; break "+arg+"; echo after; done; echo done\n")
			assert.Equal(t, 0, code)
			assert.Equal(t, "before\ndone\n", stdout)
			assert.Contains(t, stderr, "loop count out of range")
		})
	}
}

func TestVulnHuntBuiltinIntegerOverflow_HugeBreakLevelsClampAtOutermost(t *testing.T) {
	stdout, stderr, code := runBreakVulnHunt(t,
		"for i in 1 2; do\n"+
			"  for j in a b; do\n"+
			"    echo in-$i-$j\n"+
			"    break 999999\n"+
			"    echo inner-unreachable\n"+
			"  done\n"+
			"  echo outer-unreachable\n"+
			"done\n"+
			"echo done\n")

	assert.Equal(t, 0, code)
	assert.Equal(t, "in-1-a\ndone\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinSubshellIsolation_PipelineStagesDoNotBreakParent(t *testing.T) {
	stdout, stderr, code := runBreakVulnHunt(t,
		"for i in 1; do break | cat | cat; echo after-pipe; break; done; echo done\n")

	assert.Equal(t, 0, code)
	assert.Equal(t, "after-pipe\ndone\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinSubshellIsolation_GroupedPipelineStageDiagnosesAndContinues(t *testing.T) {
	stdout, stderr, code := runBreakVulnHunt(t,
		"for i in 1; do { break; echo grouped; } | cat; echo after-group; break; done; echo done\n")

	assert.Equal(t, 0, code)
	assert.Equal(t, "grouped\nafter-group\ndone\n", stdout)
	assert.Contains(t, stderr, "break is only useful in a loop")
}

func TestVulnHuntBuiltinStateCorruption_RunnerReuseAfterBreakHasNoStaleCounter(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(nil, &stdout, &stderr),
		interpoption.AllowAllCommands().(interp.RunnerOption),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runner.Close() })

	first, err := interp.ParseScript("for i in 1; do break; done\n", "break_reuse_1.sh")
	require.NoError(t, err)
	err = runner.Run(context.Background(), first)
	require.NoError(t, err)

	second, err := interp.ParseScript("echo second\n", "break_reuse_2.sh")
	require.NoError(t, err)
	err = runner.Run(context.Background(), second)
	var status interp.ExitStatus
	if errors.As(err, &status) {
		t.Fatalf("second run unexpectedly exited with %d", status)
	}
	require.NoError(t, err)
	assert.Equal(t, "second\n", stdout.String())
	assert.Empty(t, stderr.String())
}
