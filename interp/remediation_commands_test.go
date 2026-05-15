// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp_test

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/interp"
)

func TestRemediationTruncateDelegatesShrinksOnly(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.log"), []byte("abcdef"), 0644))
	var got []string

	stdout, stderr, code := runScript(t, "truncate -s 3 app.log", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			got = append([]string(nil), args...)
			hc := interp.HandlerCtx(ctx)
			assert.Equal(t, dir, hc.Dir)
			require.Len(t, hc.ExtraFiles, 1)
			info, err := hc.ExtraFiles[0].Stat()
			require.NoError(t, err)
			assert.Equal(t, int64(6), info.Size())
			return nil
		}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	assert.Equal(t, []string{"truncate", "-s", "3", "--", builtins.HostExtraFilePath(0)}, got)
}

func TestRemediationTruncateDelegatesThroughExecHandlerByDefault(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.log"), []byte("abcdef"), 0644))
	var got []string

	stdout, stderr, code := runScript(t, "truncate -s 0 app.log", dir,
		interp.AllowedPaths([]string{dir}),
		interp.ExecHandler(func(ctx context.Context, args []string) error {
			got = append([]string(nil), args...)
			hc := interp.HandlerCtx(ctx)
			assert.Equal(t, dir, hc.Dir)
			require.Len(t, hc.ExtraFiles, 1)
			return nil
		}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	assert.Equal(t, []string{"truncate", "-s", "0", "--", builtins.HostExtraFilePath(0)}, got)
}

func TestRemediationTruncatePreservesLeadingDashOperand(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "--help"), []byte("abcdef"), 0644))
	var got []string

	stdout, stderr, code := runScript(t, "truncate -s 0 -- --help", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			got = append([]string(nil), args...)
			require.Len(t, interp.HandlerCtx(ctx).ExtraFiles, 1)
			return nil
		}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	assert.Equal(t, []string{"truncate", "-s", "0", "--", builtins.HostExtraFilePath(0)}, got)
}

func TestRemediationTruncateRejectsGrowth(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.log"), []byte("abc"), 0644))
	called := false

	_, stderr, code := runScript(t, "truncate -s 4 app.log", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "cannot grow file")
	assert.False(t, called)
}

func TestRemediationTruncateRejectsRelativeSizeSyntax(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.log"), []byte("abc"), 0644))
	called := false

	_, stderr, code := runScript(t, "truncate -s +1 app.log", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "invalid size")
	assert.False(t, called)
}

func TestRemediationTruncateRejectsSymlinkEscapeBeforeHostExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink escape behavior is platform-specific on Windows")
	}
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.log")
	require.NoError(t, os.WriteFile(outside, []byte("abcdef"), 0644))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "escape.log")))
	called := false

	_, stderr, code := runScript(t, "truncate -s 0 escape.log", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "permission denied")
	assert.False(t, called)
}

func TestExecHandlerOptionRunsAllowedExternalCommand(t *testing.T) {
	dir := t.TempDir()
	var got []string

	stdout, stderr, code := runScript(t, "external one two", dir,
		interp.ExecHandler(func(ctx context.Context, args []string) error {
			got = append([]string(nil), args...)
			return nil
		}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	assert.Equal(t, []string{"external", "one", "two"}, got)
}

func TestRemediationSystemctlDelegatesLifecycleAction(t *testing.T) {
	dir := t.TempDir()
	var got []string

	stdout, stderr, code := runScript(t, "systemctl restart app.service", dir,
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			got = append([]string(nil), args...)
			return nil
		}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	assert.Equal(t, []string{"systemctl", "restart", "--", "app.service"}, got)
}

func TestRemediationSystemctlPreservesLeadingDashUnit(t *testing.T) {
	dir := t.TempDir()
	var got []string

	stdout, stderr, code := runScript(t, "systemctl restart -- -app.service", dir,
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			got = append([]string(nil), args...)
			return nil
		}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	assert.Equal(t, []string{"systemctl", "restart", "--", "-app.service"}, got)
}

func TestRemediationSystemctlRejectsUnsupportedAction(t *testing.T) {
	dir := t.TempDir()
	called := false

	_, stderr, code := runScript(t, "systemctl enable app.service", dir,
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "unsupported action")
	assert.False(t, called)
}

func TestRemediationKillDelegatesForcePid(t *testing.T) {
	dir := t.TempDir()
	var got []string

	stdout, stderr, code := runScript(t, "kill -9 123", dir,
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			got = append([]string(nil), args...)
			return nil
		}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	assert.Equal(t, []string{"kill", "-9", "123"}, got)
}

func TestRemediationKillRejectsInvalidPid(t *testing.T) {
	dir := t.TempDir()
	called := false

	_, stderr, code := runScript(t, "kill 0", dir,
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "invalid pid")
	assert.False(t, called)
}

func TestRemediationTeeDelegatesAppendWithStdin(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("payload\n"), 0644))
	var got []string
	var stdin string

	stdout, stderr, code := runScript(t, "tee -a output.txt < input.txt", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			got = append([]string(nil), args...)
			require.Len(t, interp.HandlerCtx(ctx).ExtraFiles, 1)
			data, err := io.ReadAll(interp.HandlerCtx(ctx).Stdin)
			require.NoError(t, err)
			stdin = string(data)
			return nil
		}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	assert.Equal(t, []string{"tee", "-a", "--", builtins.HostExtraFilePath(0)}, got)
	assert.Equal(t, "payload\n", stdin)
}

func TestRemediationTeePreservesLeadingDashOperand(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("payload\n"), 0644))
	var got []string

	stdout, stderr, code := runScript(t, "tee -- --help < input.txt", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			got = append([]string(nil), args...)
			require.Len(t, interp.HandlerCtx(ctx).ExtraFiles, 1)
			return nil
		}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	assert.Equal(t, []string{"tee", "--", builtins.HostExtraFilePath(0)}, got)
}

func TestRemediationTeeRejectsOutsideAllowedPathsBeforeHostExecution(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("payload\n"), 0644))
	called := false

	_, stderr, code := runScript(t, "tee "+outside+" < input.txt", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "permission denied")
	assert.False(t, called)
	assert.NoFileExists(t, outside)
}

func TestRemediationTeeRejectsSymlinkEscapeBeforeHostExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink escape behavior is platform-specific on Windows")
	}
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("payload\n"), 0644))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "escape.txt")))
	called := false

	_, stderr, code := runScript(t, "tee escape.txt < input.txt", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "permission denied")
	assert.False(t, called)
	assert.NoFileExists(t, outside)
}

func TestRemediationLogrotateDelegatesExistingPath(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.log"), []byte("payload\n"), 0644))
	var got []string

	stdout, stderr, code := runScript(t, "logrotate app.log", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			got = append([]string(nil), args...)
			require.Len(t, interp.HandlerCtx(ctx).ExtraFiles, 1)
			return nil
		}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	assert.Equal(t, []string{"logrotate", "--", builtins.HostExtraFilePath(0)}, got)
}

func TestRemediationLogrotatePreservesLeadingDashOperand(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "--help"), []byte("payload\n"), 0644))
	var got []string

	stdout, stderr, code := runScript(t, "logrotate -- --help", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			got = append([]string(nil), args...)
			require.Len(t, interp.HandlerCtx(ctx).ExtraFiles, 1)
			return nil
		}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	assert.Equal(t, []string{"logrotate", "--", builtins.HostExtraFilePath(0)}, got)
}

func TestRemediationLogrotateRejectsSymlinkEscapeBeforeHostExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink escape behavior is platform-specific on Windows")
	}
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.log")
	require.NoError(t, os.WriteFile(outside, []byte("payload\n"), 0644))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "escape.log")))
	called := false

	_, stderr, code := runScript(t, "logrotate escape.log", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "permission denied")
	assert.False(t, called)
}
