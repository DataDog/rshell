// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.

package interp

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSelectiveElevation(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := ""
	runner, err := New(
		StdIO(nil, &stdout, &stderr),
		AllowedCommands([]string{"rshell:echo"}),
		SelectiveElevation([]string{"rshell:echo"}, func(_ context.Context, command string, run func()) error {
			called = command
			run()
			return nil
		}),
	)
	require.NoError(t, err)
	defer runner.Close()
	program, err := ParseScript("sudo echo hello", "")
	require.NoError(t, err)
	require.NoError(t, runner.Run(context.Background(), program))
	require.Equal(t, "echo", called)
	require.Equal(t, "hello\n", stdout.String())
}

func TestSelectiveElevationDefaultDeny(t *testing.T) {
	var stderr bytes.Buffer
	runner, err := New(StdIO(nil, nil, &stderr), AllowedCommands([]string{"rshell:echo"}))
	require.NoError(t, err)
	defer runner.Close()
	program, err := ParseScript("sudo echo hello", "")
	require.NoError(t, err)
	err = runner.Run(context.Background(), program)
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	require.Equal(t, ExitStatus(126), status)
	require.Contains(t, stderr.String(), "elevation not allowed")
}

func TestSelectiveElevationRejectsExpandedMarkerInPipeline(t *testing.T) {
	var stderr bytes.Buffer
	called := false
	runner, err := New(
		StdIO(nil, nil, &stderr),
		AllowedCommands([]string{"rshell:echo"}),
		SelectiveElevation([]string{"rshell:echo"}, func(_ context.Context, _ string, run func()) error {
			called = true
			run()
			return nil
		}),
	)
	require.NoError(t, err)
	defer runner.Close()
	program, err := ParseScript("marker=sudo; $marker echo elevated | echo ordinary", "")
	require.NoError(t, err)
	err = runner.Run(context.Background(), program)
	require.NoError(t, err) // pipeline status is the right-hand command's status
	require.False(t, called)
	require.Contains(t, stderr.String(), "not allowed in pipelines")
}
