// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
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

// A (…) subshell resets the telemetry-only span suppression inside a pipeline
// stage, but the stage still runs concurrently with its siblings, so elevation
// must remain refused there. Otherwise the elevation callback (which lifts the
// effective UID process-wide) would run while an unelevated sibling stage is
// executing.
func TestSelectiveElevationRejectsMarkerInSubshellPipelineStage(t *testing.T) {
	for _, script := range []string{
		"marker=sudo; ($marker echo elevated) | echo ordinary",
		"echo ordinary | (sudo echo elevated)",
		"(sudo echo elevated) | echo ordinary",
		"( (sudo echo elevated) ) | echo ordinary",
		"{ (sudo echo elevated); } | echo ordinary",
		"echo a | echo b | (sudo echo elevated)",
		"x=$( (sudo echo elevated) ) | echo ordinary",
	} {
		t.Run(script, func(t *testing.T) {
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
			program, err := ParseScript(script, "")
			require.NoError(t, err)
			_ = runner.Run(context.Background(), program)
			require.False(t, called)
			require.Contains(t, stderr.String(), "not allowed in pipelines")
		})
	}
}

func TestSelectiveElevationAllowedInStandaloneSubshell(t *testing.T) {
	var stdout, stderr bytes.Buffer
	called := false
	runner, err := New(
		StdIO(nil, &stdout, &stderr),
		AllowedCommands([]string{"rshell:echo"}),
		SelectiveElevation([]string{"rshell:echo"}, func(_ context.Context, _ string, run func()) error {
			called = true
			run()
			return nil
		}),
	)
	require.NoError(t, err)
	defer runner.Close()
	program, err := ParseScript("(sudo echo elevated)", "")
	require.NoError(t, err)
	require.NoError(t, runner.Run(context.Background(), program))
	require.True(t, called)
	require.Equal(t, "elevated\n", stdout.String())
}

func TestConfiguredElevatableCommandsValidatesAndCopiesMetadata(t *testing.T) {
	for _, name := range []string{"", "echo", "otherx:echo", "rshell:", "rshell:echo:echo"} {
		_, err := New(ConfiguredElevatableCommands([]string{name}))
		require.ErrorContains(t, err, "ConfiguredElevatableCommands:")
	}
	names := []string{"rshell:truncate", "rshell:echo", "rshell:echo"}
	runner, err := New(ConfiguredElevatableCommands(names))
	require.NoError(t, err)
	defer runner.Close()
	names[1] = "rshell:cat"
	listed := runner.configuredElevatableCommandsList()
	require.Equal(t, []string{"echo", "truncate"}, listed)
	listed[0] = "cat"
	require.Equal(t, []string{"echo", "truncate"}, runner.configuredElevatableCommandsList())
	require.Empty(t, runner.elevatableCommandsList())
	require.Nil(t, runner.elevate)
}

func TestConfiguredElevatableCommandsCannotAuthorizeExecution(t *testing.T) {
	for _, test := range []struct {
		name        string
		script      string
		remediation bool
		writable    bool
		elevation   bool
	}{
		{name: "sudo read-only", script: "sudo echo changed"},
		{name: "sudo remediation", script: "sudo echo changed", remediation: true},
		{name: "command default deny", script: "cat file", remediation: true, writable: true},
		{name: "mode gate", script: "truncate -s 0 file", writable: true},
		{name: "read-only redirect", script: "echo changed > file", writable: true},
		{name: "path write gate", script: "truncate -s 0 file", remediation: true},
		{name: "path redirect gate", script: "echo changed > file", remediation: true},
		{name: "systemd grant gate", script: "systemctl restart worker.service", remediation: true},
		{name: "metadata cannot extend effective elevation", script: "sudo truncate -s 0 file", remediation: true, writable: true, elevation: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, "file")
			require.NoError(t, os.WriteFile(file, []byte("original\n"), 0600))
			path := root
			if test.writable {
				path += ":rw"
			}
			var stdout, stderr bytes.Buffer
			opts := []RunnerOption{
				StdIO(nil, &stdout, &stderr), AllowedPaths([]string{path}),
				AllowedCommands([]string{"rshell:help", "rshell:echo", "rshell:truncate", "rshell:systemctl"}),
				ConfiguredElevatableCommands([]string{"rshell:echo", "rshell:cat", "rshell:truncate", "rshell:systemctl"}),
			}
			if test.remediation {
				opts = append(opts, WithMode(ModeRemediation))
			}
			if test.elevation {
				opts = append(opts, SelectiveElevation([]string{"rshell:echo"}, func(context.Context, string, func()) error {
					t.Fatal("ungranted command must not reach elevation callback")
					return nil
				}))
			}
			runner, err := New(opts...)
			require.NoError(t, err)
			defer runner.Close()
			runner.Dir = root
			report, err := runner.Check(context.Background(), test.script)
			require.NoError(t, err)
			require.Equal(t, CheckDenied, report.Status)
			help, err := ParseScript("help", "")
			require.NoError(t, err)
			require.NoError(t, runner.Run(context.Background(), help))
			require.Contains(t, stdout.String(), "Configured elevation policy:")
			program, err := ParseScript(test.script, "")
			require.NoError(t, err)
			require.Error(t, runner.Run(context.Background(), program))
			require.NotEmpty(t, stderr.String())
			contents, err := os.ReadFile(file)
			require.NoError(t, err)
			require.Equal(t, "original\n", string(contents))
		})
	}
}

func TestConfiguredElevatableCommandsCannotDisableEffectiveElevation(t *testing.T) {
	var stdout bytes.Buffer
	called := false
	runner, err := New(StdIO(nil, &stdout, nil), AllowedCommands([]string{"rshell:echo"}),
		ConfiguredElevatableCommands(nil),
		SelectiveElevation([]string{"rshell:echo"}, func(_ context.Context, command string, run func()) error {
			require.Equal(t, "echo", command)
			called = true
			run()
			return nil
		}))
	require.NoError(t, err)
	defer runner.Close()
	report, err := runner.Check(context.Background(), "sudo echo allowed")
	require.NoError(t, err)
	require.True(t, report.Allowed)
	require.False(t, called)
	program, err := ParseScript("sudo echo allowed", "")
	require.NoError(t, err)
	require.NoError(t, runner.Run(context.Background(), program))
	require.True(t, called)
	require.Equal(t, "allowed\n", stdout.String())
}
