// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: continue (builtin)

package continuecmd_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
)

func runContinueVulnHunt(t *testing.T, script string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScript(t, script, "", opts...)
}

func TestVulnHuntBuiltinFlagDrivenExploit_ContinueHelpDoesNotMutateLoopControl(t *testing.T) {
	stdout, stderr, code := runContinueVulnHunt(t,
		"for i in 1 2; do continue --help; echo after-$i; done; echo done\n")

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, 2, strings.Count(stdout, "continue: continue [n]"))
	assert.Contains(t, stdout, "after-1\n")
	assert.Contains(t, stdout, "after-2\n")
	assert.True(t, strings.HasSuffix(stdout, "done\n"))
}

func TestVulnHuntBuiltinFlagDrivenExploit_FlagShapedOperandsValidateAsNumbers(t *testing.T) {
	stdout, stderr, code := runContinueVulnHunt(t, "for i in 1; do continue --; echo after; done; echo done\n")
	assert.Equal(t, 128, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "continue: --: numeric argument required")

	stdout, stderr, code = runContinueVulnHunt(t,
		"for i in 1 2; do\n"+
			"  for j in a b; do\n"+
			"    echo in-$i-$j\n"+
			"    continue +2\n"+
			"    echo inner-unreachable\n"+
			"  done\n"+
			"  echo outer-unreachable\n"+
			"done\n"+
			"echo done\n")
	assert.Equal(t, 0, code)
	assert.Equal(t, "in-1-a\nin-2-a\ndone\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinDeclaredVsImplemented_OutsideLoopContinueDoesNotPoisonFlow(t *testing.T) {
	stdout, stderr, code := runContinueVulnHunt(t,
		"continue\n"+
			"echo status=$?\n"+
			"false || continue\n"+
			"echo after-or\n"+
			"continue && echo after-and\n"+
			"echo done\n")

	assert.Equal(t, 0, code)
	assert.Equal(t, "status=0\nafter-or\nafter-and\ndone\n", stdout)
	assert.Equal(t, 3, strings.Count(stderr, "continue is only useful in a loop"))
}

func TestVulnHuntBuiltinResourceExhaustion_HugeContinueLevelsClampAtOutermost(t *testing.T) {
	stdout, stderr, code := runContinueVulnHunt(t,
		"for i in 1 2 3; do\n"+
			"  echo before-$i\n"+
			"  continue 999999\n"+
			"  echo unreachable\n"+
			"done\n"+
			"echo done\n")

	assert.Equal(t, 0, code)
	assert.Equal(t, "before-1\nbefore-2\nbefore-3\ndone\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinResourceExhaustion_InfiniteContinueRespectsContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	stdout, stderr, _ := testutil.RunScriptCtx(ctx, t, "while true; do continue; done\n", "")

	assert.Less(t, time.Since(start), 2*time.Second)
	assert.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinIntegerOverflow_InvalidArgumentsAbort(t *testing.T) {
	stdout, stderr, code := runContinueVulnHunt(t, "for i in 1; do continue abc; echo after; done; echo done\n")
	assert.Equal(t, 128, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "continue: abc: numeric argument required")

	stdout, stderr, code = runContinueVulnHunt(t, "for i in 1; do continue 1 2; echo after; done; echo done\n")
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "continue: too many arguments")

	stdout, stderr, code = runContinueVulnHunt(t, "for i in 1; do continue 99999999999999999999; echo after; done; echo done\n")
	assert.Equal(t, 128, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "numeric argument required")
}

func TestVulnHuntBuiltinIntegerOverflow_ZeroAndNegativeContinueBreakCompatibly(t *testing.T) {
	for _, arg := range []string{"0", "-1"} {
		t.Run(arg, func(t *testing.T) {
			stdout, stderr, code := runContinueVulnHunt(t,
				"for i in 1 2; do echo before-$i; continue "+arg+"; echo after-$i; done; echo done\n")

			assert.Equal(t, 0, code)
			assert.Equal(t, "before-1\ndone\n", stdout)
			assert.Contains(t, stderr, "loop count out of range")
		})
	}
}

func TestVulnHuntBuiltinControlFlow_ContinueAcrossLoopTypesAndLists(t *testing.T) {
	stdout, stderr, code := runContinueVulnHunt(t,
		"for i in 1 2; do echo for-$i; continue; echo for-bad; done\n"+
			"while read line; do echo while-$line; continue; echo while-bad; done <<EOF\n"+
			"a\n"+
			"b\n"+
			"EOF\n"+
			"for a in A B; do for b in x y; do echo nested-$a-$b; continue 2; echo nested-bad; done; echo outer-bad; done\n"+
			"echo done\n")

	assert.Equal(t, 0, code)
	assert.Equal(t, "for-1\nfor-2\nwhile-a\nwhile-b\nnested-A-x\nnested-B-x\ndone\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinControlFlow_LogicListsStopAfterContinue(t *testing.T) {
	stdout, stderr, code := runContinueVulnHunt(t,
		"for i in 1 2; do echo before-$i; true && continue && echo after-and; echo body-end; done; echo done\n")

	assert.Equal(t, 0, code)
	assert.Equal(t, "before-1\nbefore-2\ndone\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinSubshellIsolation_PipelineStagesDoNotContinueParent(t *testing.T) {
	stdout, stderr, code := runContinueVulnHunt(t,
		"for i in 1; do continue | cat | cat; echo after-pipe; continue; echo after-real; done; echo done\n")

	assert.Equal(t, 0, code)
	assert.Equal(t, "after-pipe\ndone\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinSubshellIsolation_GroupedPipelineStageDiagnosesAndContinues(t *testing.T) {
	stdout, stderr, code := runContinueVulnHunt(t,
		"for i in 1; do { continue; echo grouped; } | cat; echo after-group; continue; echo after-real; done; echo done\n")

	assert.Equal(t, 0, code)
	assert.Equal(t, "grouped\nafter-group\ndone\n", stdout)
	assert.Contains(t, stderr, "continue is only useful in a loop")
}

func TestVulnHuntBuiltinSubshellIsolation_CommandSubstitutionDoesNotContinueParent(t *testing.T) {
	stdout, stderr, code := runContinueVulnHunt(t,
		"for i in 1; do value=$(continue); echo after-cmdsubst; continue; echo after-real; done; echo done\n")

	assert.Equal(t, 0, code)
	assert.Equal(t, "after-cmdsubst\ndone\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinCompositionAttack_RedirectionFailureDoesNotLeaveContinueState(t *testing.T) {
	stdout, stderr, code := runContinueVulnHunt(t,
		"for i in 1; do continue < missing.txt; echo after-redirect; done; echo done\n")

	assert.Equal(t, 0, code)
	assert.Equal(t, "after-redirect\ndone\n", stdout)
	assert.Contains(t, stderr, "missing.txt")
}

func TestVulnHuntBuiltinStateCorruption_RunnerReuseAfterContinueHasNoStaleCounter(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(nil, &stdout, &stderr),
		interpoption.AllowAllCommands().(interp.RunnerOption),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runner.Close() })

	first, err := interp.ParseScript("for i in 1; do continue; echo bad; done\n", "continue_reuse_1.sh")
	require.NoError(t, err)
	err = runner.Run(context.Background(), first)
	require.NoError(t, err)

	second, err := interp.ParseScript("echo second\n", "continue_reuse_2.sh")
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
