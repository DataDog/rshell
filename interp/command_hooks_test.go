// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runWithCommandHooks(t *testing.T, script string, hooks CommandHooks, allowedCommands []string) error {
	t.Helper()
	prog, err := ParseScript(script, "")
	require.NoError(t, err)
	r, err := New(
		StdIO(nil, io.Discard, io.Discard),
		AllowedCommands(allowedCommands),
		WithCommandHooks(hooks),
	)
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })
	return r.Run(context.Background(), prog)
}

func TestCommandHooksReportAllowedAndDeniedCommands(t *testing.T) {
	var events []CommandEvent
	hooks := CommandHooks{
		After: func(_ context.Context, event CommandEvent) {
			events = append(events, event)
		},
	}

	err := runWithCommandHooks(t, `echo ok; awk '{print}' missing.txt`, hooks, []string{"rshell:echo"})
	var exit ExitStatus
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, ExitStatus(127), exit)
	require.Len(t, events, 2)

	assert.Equal(t, "echo", events[0].Name)
	assert.Equal(t, []string{"ok"}, events[0].Args)
	assert.True(t, events[0].IsAllowed)
	assert.True(t, events[0].IsKnown)
	assert.Equal(t, uint8(0), events[0].ExitCode)

	assert.Equal(t, "awk", events[1].Name)
	assert.Equal(t, []string{"{print}", "missing.txt"}, events[1].Args)
	assert.False(t, events[1].IsAllowed)
	assert.False(t, events[1].IsKnown)
	assert.Equal(t, uint8(127), events[1].ExitCode)
}

func TestCommandHooksReportChildCommandDeniedByBuiltin(t *testing.T) {
	var events []CommandEvent
	hooks := CommandHooks{
		After: func(_ context.Context, event CommandEvent) {
			events = append(events, event)
		},
	}

	err := runWithCommandHooks(t, `printf 'a\n' | xargs awk`, hooks, []string{"rshell:printf", "rshell:xargs"})
	var exit ExitStatus
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, ExitStatus(125), exit)

	var denied *CommandEvent
	for i := range events {
		if events[i].Name == "awk" && !events[i].IsAllowed {
			denied = &events[i]
			break
		}
	}
	require.NotNil(t, denied, "expected denied child command event")
	assert.False(t, denied.IsKnown)
	assert.Equal(t, uint8(127), denied.ExitCode)
}

func TestCommandHooksReportFindExecDeniedWithSubstitutedArgs(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("hello\n"), 0o644))

	var events []CommandEvent
	hooks := CommandHooks{
		After: func(_ context.Context, event CommandEvent) {
			events = append(events, event)
		},
	}
	prog, err := ParseScript(`find . -name input.txt -exec {} suffix-{} \;`, "")
	require.NoError(t, err)
	r, err := New(
		StdIO(nil, io.Discard, io.Discard),
		AllowedPaths([]string{dir}),
		AllowedCommands([]string{"rshell:find"}),
		WithCommandHooks(hooks),
	)
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })
	r.Dir = dir

	err = r.Run(context.Background(), prog)
	var exit ExitStatus
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, ExitStatus(1), exit)

	var denied *CommandEvent
	for i := range events {
		if !events[i].IsAllowed {
			denied = &events[i]
			break
		}
	}
	require.NotNil(t, denied, "expected denied find -exec command event")
	assert.Contains(t, denied.Name, "input.txt")
	require.Len(t, denied.Args, 1)
	assert.NotContains(t, denied.Args[0], "{}")
	assert.Contains(t, denied.Args[0], "input.txt")
}
