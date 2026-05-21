// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: output-buffer-1mb-limit (subsystem)

package interp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeOutputBufferMiBFile(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mb.txt"), []byte(strings.Repeat("A", 1<<20)), 0o644))
}

func TestVulnHuntSubsystemOutputBuffer_ConcurrentPipelineStderrCapped(t *testing.T) {
	dir := t.TempDir()
	writeOutputBufferMiBFile(t, dir)

	var stdout, stderr bytes.Buffer
	runner, err := New(
		StdIO(nil, &stdout, &stderr),
		AllowedPaths([]string{dir}),
		allowAllCommandsOpt(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runner.Close() })
	runner.Dir = dir

	script := `for i in 1 2 3 4 5 6 7 8 9 10 11; do cat mb.txt >&2; done | for i in 1 2 3 4 5 6 7 8 9 10 11; do cat mb.txt >&2; done`
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	err = runner.Run(ctx, parseScript(t, script))
	require.ErrorIs(t, err, ErrStderrLimitExceeded)
	assert.NotErrorIs(t, err, ErrOutputLimitExceeded)
	assert.Empty(t, stdout.String())
	assert.LessOrEqual(t, stderr.Len(), maxStderrBytes)
	assert.Greater(t, stderr.Len(), 0)
}

func TestVulnHuntSubsystemOutputBuffer_WritersRestoreAfterExceededRun(t *testing.T) {
	dir := t.TempDir()
	writeOutputBufferMiBFile(t, dir)

	var stdout, stderr bytes.Buffer
	runner, err := New(
		StdIO(nil, &stdout, &stderr),
		AllowedPaths([]string{dir}),
		allowAllCommandsOpt(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runner.Close() })
	runner.Dir = dir

	err = runner.Run(context.Background(), parseScript(t, `for i in 1 2 3 4 5 6 7 8 9 10 11; do cat mb.txt; done`))
	require.ErrorIs(t, err, ErrOutputLimitExceeded)
	assert.Equal(t, int64(maxStdoutBytes), int64(stdout.Len()))

	stdout.Reset()
	stderr.Reset()
	err = runner.Run(context.Background(), parseScript(t, `echo after`))
	require.NoError(t, err)
	assert.Equal(t, "after\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntSubsystemOutputBuffer_DevNullRedirectDoesNotCountAsCallerOutput(t *testing.T) {
	dir := t.TempDir()
	writeOutputBufferMiBFile(t, dir)

	var stdout, stderr bytes.Buffer
	runner, err := New(
		StdIO(nil, &stdout, &stderr),
		AllowedPaths([]string{dir}),
		allowAllCommandsOpt(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runner.Close() })
	runner.Dir = dir

	script := `for i in 1 2 3 4 5 6 7 8 9 10 11; do cat mb.txt > ` + os.DevNull + `; done; echo after`
	err = runner.Run(context.Background(), parseScript(t, script))
	require.NoError(t, err)
	assert.Equal(t, "after\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntSubsystemOutputBuffer_CommandSubstitutionCaptureStaysAtOneMiB(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Repeat("B", maxCmdSubstOutput+100)), 0o644))

	var stdout, stderr bytes.Buffer
	runner, err := New(
		StdIO(nil, &stdout, &stderr),
		AllowedPaths([]string{dir}),
		allowAllCommandsOpt(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runner.Close() })
	runner.Dir = dir

	err = runner.Run(context.Background(), parseScript(t, `x=$(<big.txt); echo "$x" | wc -c`))
	require.NoError(t, err)
	assert.Equal(t, "1048577\n", strings.TrimLeft(stdout.String(), " "))
	assert.Empty(t, stderr.String())
}

func TestVulnHuntSubsystemOutputBuffer_LargeUnknownCommandDiagnosticsStayBounded(t *testing.T) {
	var stdout, stderr bytes.Buffer
	runner, err := New(
		StdIO(nil, &stdout, &stderr),
		allowAllCommandsOpt(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runner.Close() })

	hugeName := strings.Repeat("u", MaxVarBytes-4096)
	script := "BAD='" + hugeName + "'\nfor i in 1 2 3 4 5 6 7 8 9 10 11; do $BAD; done\n"
	err = runner.Run(context.Background(), parseScript(t, script))

	var status ExitStatus
	require.True(t, errors.As(err, &status), "expected ordinary unknown-command status, got %v", err)
	assert.Equal(t, ExitStatus(127), status)
	assert.Empty(t, stdout.String())
	assert.LessOrEqual(t, stderr.Len(), maxStderrBytes)
	assert.Contains(t, stderr.String(), "unknown command")
}

func TestVulnHuntSubsystemOutputBuffer_InfiniteProducerStopsOnConfiguredTimeout(t *testing.T) {
	var stderr bytes.Buffer
	runner, err := New(
		StdIO(nil, io.Discard, &stderr),
		allowAllCommandsOpt(),
		MaxExecutionTime(50*time.Millisecond),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runner.Close() })

	start := time.Now()
	err = runner.Run(context.Background(), parseScript(t, `while true; do echo x; done`))

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Empty(t, stderr.String())
}

func TestVulnHuntSubsystemOutputBuffer_BothStreamSentinelSurvivesExitStatus(t *testing.T) {
	dir := t.TempDir()
	writeOutputBufferMiBFile(t, dir)

	var stdout, stderr bytes.Buffer
	runner, err := New(
		StdIO(nil, &stdout, &stderr),
		AllowedPaths([]string{dir}),
		allowAllCommandsOpt(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runner.Close() })
	runner.Dir = dir

	script := `for i in 1 2 3 4 5 6 7 8 9 10 11; do cat mb.txt; cat mb.txt >&2; done; exit 7`
	err = runner.Run(context.Background(), parseScript(t, script))
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrOutputLimitExceeded))
	assert.True(t, errors.Is(err, ErrStderrLimitExceeded))
	var status ExitStatus
	assert.False(t, errors.As(err, &status))
	assert.LessOrEqual(t, stdout.Len(), maxStdoutBytes)
	assert.LessOrEqual(t, stderr.Len(), maxStderrBytes)
}
