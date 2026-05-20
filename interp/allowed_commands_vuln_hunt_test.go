// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Tripwire tests added by vuln-hunt campaign 2026-05-20-gpt-5.5-cyber-3 /
// allowed_commands.

package interp_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/interp"
)

func runAllowedCommandsVulnHunt(t *testing.T, ctx context.Context, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	prog, err := interp.ParseScript(script, "allowed_commands_vuln_hunt.sh")
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	allOpts := append([]interp.RunnerOption{interp.StdIO(nil, &stdout, &stderr)}, opts...)
	r, err := interp.New(allOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	if dir != "" {
		r.Dir = dir
	}

	err = r.Run(ctx, prog)
	code := 0
	if err != nil {
		var status interp.ExitStatus
		if errors.As(err, &status) {
			code = int(status)
		} else if ctx.Err() != nil {
			code = 124
		} else {
			t.Fatalf("unexpected Run error: %v", err)
		}
	}
	return stdout.String(), stderr.String(), code
}

func TestVulnHuntShellFeatureAllowedCommands_ExpandedMetacharactersAreNotReparsed(t *testing.T) {
	stdout, stderr, code := runAllowedCommandsVulnHunt(t,
		context.Background(),
		"CMD='cat; echo PWNED'\n$CMD\necho after\n",
		"",
		interp.AllowedCommands([]string{"rshell:echo"}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "after\n", stdout)
	assert.Contains(t, stderr, "command not allowed")
	assert.NotContains(t, stdout, "PWNED")
}

func TestVulnHuntShellFeatureAllowedCommands_InlinePolicyAssignmentCannotAllowCommand(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("SECRET"), 0o644))

	stdout, stderr, code := runAllowedCommandsVulnHunt(t,
		context.Background(),
		"ALLOWED_COMMANDS=rshell:cat cat secret.txt\necho after\n",
		dir,
		interp.AllowedPaths([]string{dir}),
		interp.AllowedCommands([]string{"rshell:echo"}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "after\n", stdout)
	assert.Contains(t, stderr, "rshell: cat: command not allowed")
	assert.NotContains(t, stdout, "SECRET")
}

func TestVulnHuntShellFeatureAllowedCommands_SubshellPolicyAssignmentCannotAllowCommand(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("SECRET"), 0o644))

	stdout, stderr, code := runAllowedCommandsVulnHunt(t,
		context.Background(),
		"( ALLOWED_COMMANDS=rshell:cat; cat secret.txt; echo inside )\necho after\n",
		dir,
		interp.AllowedPaths([]string{dir}),
		interp.AllowedCommands([]string{"rshell:echo"}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "inside\nafter\n", stdout)
	assert.Contains(t, stderr, "rshell: cat: command not allowed")
	assert.NotContains(t, stdout, "SECRET")
}

func TestVulnHuntShellFeatureAllowedCommands_HelpHintRequiresHelpAllowed(t *testing.T) {
	_, stderr, code := runAllowedCommandsVulnHunt(t,
		context.Background(),
		"cat /dev/null\n",
		"",
		interp.AllowedCommands([]string{"rshell:echo"}),
	)
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr, "rshell: cat: command not allowed")
	assert.NotContains(t, stderr, "Run 'help'")

	_, stderr, code = runAllowedCommandsVulnHunt(t,
		context.Background(),
		"cat /dev/null\n",
		"",
		interp.AllowedCommands([]string{"rshell:echo", "rshell:help"}),
	)
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr, "Run 'help' to see allowed commands.")
}

func TestVulnHuntShellFeatureAllowedCommands_FindExecReplacementRechecked(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "cat"), []byte("placeholder"), 0o644))

	stdout, stderr, code := runAllowedCommandsVulnHunt(t,
		context.Background(),
		"find . -maxdepth 1 -name cat -exec {} \\;\necho after\n",
		dir,
		interp.AllowedPaths([]string{dir}),
		interp.AllowedCommands([]string{"rshell:find", "rshell:echo"}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "after\n", stdout)
	assert.Contains(t, stderr, "command not allowed")
}

func TestVulnHuntShellFeatureAllowedCommands_CatShortcutDeniedBeforeFileOpen(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("SECRET"), 0o644))

	stdout, stderr, code := runAllowedCommandsVulnHunt(t,
		context.Background(),
		"x=$(<secret.txt); echo \"[$x]\"\n",
		dir,
		interp.AllowedPaths([]string{dir}),
		interp.AllowedCommands([]string{"rshell:echo"}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "[]\n", stdout)
	assert.Contains(t, stderr, "cat not in allowed commands")
	assert.NotContains(t, stdout, "SECRET")
}

func TestVulnHuntShellFeatureAllowedCommands_CatShortcutStillUsesAllowedPaths(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("SECRET"), 0o644))

	stdout, stderr, code := runAllowedCommandsVulnHunt(t,
		context.Background(),
		"x=$(<"+outsideFile+"); echo \"[$x]\"\n",
		dir,
		interp.AllowedPaths([]string{dir}),
		interp.AllowedCommands([]string{"rshell:cat", "rshell:echo"}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "[]\n", stdout)
	assert.NotContains(t, stdout, "SECRET")
	assert.NotContains(t, stderr, "cat not in allowed commands")
	assert.NotEmpty(t, stderr)
}

func TestVulnHuntShellFeatureAllowedCommands_PreCanceledContextStopsBeforeDiagnostic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stdout, stderr, code := runAllowedCommandsVulnHunt(t,
		ctx,
		"cat /dev/null\n",
		"",
		interp.AllowedCommands([]string{"rshell:echo"}),
	)

	assert.Equal(t, 124, code)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureAllowedCommands_BlockedPipelineDoesNotHang(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	stdout, stderr, code := runAllowedCommandsVulnHunt(t,
		ctx,
		"echo hello | cat\n",
		"",
		interp.AllowedCommands([]string{"rshell:echo"}),
	)

	assert.Equal(t, 127, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "rshell: cat: command not allowed")
}
