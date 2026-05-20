// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/interp"
)

type remediationRunResult struct {
	stdout string
	stderr string
	code   int
}

func runRemediationScriptWithoutBlocking(t *testing.T, script, dir string, opts ...interp.RunnerOption) remediationRunResult {
	t.Helper()
	done := make(chan remediationRunResult, 1)
	go func() {
		stdout, stderr, code := runScript(t, script, dir, opts...)
		done <- remediationRunResult{stdout: stdout, stderr: stderr, code: code}
	}()

	select {
	case res := <-done:
		return res
	case <-time.After(2 * time.Second):
		t.Fatalf("%q blocked", script)
		return remediationRunResult{}
	}
}

func requireHostExtraFilesSupported(t *testing.T) {
	t.Helper()
	if !builtins.HostExtraFilesSupported() {
		t.Skip("host file descriptor handoff is not supported on this platform")
	}
}

type killHelperProcess struct {
	cmd    *exec.Cmd
	waited bool
}

func startKillHelperProcess(t *testing.T) *killHelperProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestKillHelperProcess")
	cmd.Env = append(os.Environ(), "RSHELL_KILL_HELPER=1")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	require.NoError(t, cmd.Start())
	helper := &killHelperProcess{cmd: cmd}
	t.Cleanup(func() {
		if helper.waited || helper.cmd.Process == nil {
			return
		}
		_ = helper.cmd.Process.Kill()
		_ = helper.cmd.Wait()
	})
	return helper
}

func (p *killHelperProcess) waitForExit(t *testing.T) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- p.cmd.Wait()
	}()
	select {
	case <-done:
		p.waited = true
	case <-time.After(2 * time.Second):
		t.Fatalf("kill helper process %d did not exit", p.cmd.Process.Pid)
	}
}

func TestKillHelperProcess(t *testing.T) {
	if os.Getenv("RSHELL_KILL_HELPER") != "1" {
		return
	}
	select {}
}

func TestRemediationTruncateDelegatesShrinksOnly(t *testing.T) {
	requireHostExtraFilesSupported(t)
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
	requireHostExtraFilesSupported(t)
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

func TestRemediationTruncateJSONReportsSizes(t *testing.T) {
	requireHostExtraFilesSupported(t)
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "app.log"), []byte("abcdef"), 0644))

	stdout, stderr, code := runScript(t, "truncate --json -s 3 app.log", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			require.Equal(t, []string{"truncate", "-s", "3", "--", builtins.HostExtraFilePath(0)}, args)
			require.Len(t, interp.HandlerCtx(ctx).ExtraFiles, 1)
			return interp.HandlerCtx(ctx).ExtraFiles[0].Truncate(3)
		}),
	)

	assert.Equal(t, 0, code)
	assert.JSONEq(t, `{"path":"app.log","bytes_before":6,"bytes_after":3,"size_bytes":3,"exit_code":0,"stdout":"","stderr":""}`, stdout)
	assert.Equal(t, "", stderr)
	data, err := os.ReadFile(filepath.Join(dir, "app.log"))
	require.NoError(t, err)
	assert.Equal(t, "abc", string(data))
}

func TestRemediationTruncatePreservesLeadingDashOperand(t *testing.T) {
	requireHostExtraFilesSupported(t)
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
	requireHostExtraFilesSupported(t)
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

func TestRemediationTruncateRejectsSymlinkTargetBeforeHostExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior is platform-specific on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.log")
	require.NoError(t, os.WriteFile(target, []byte("abcdef"), 0644))
	require.NoError(t, os.Symlink("target.log", filepath.Join(dir, "link.log")))
	called := false

	_, stderr, code := runScript(t, "truncate -s 0 link.log", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "permission denied")
	assert.False(t, called)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "abcdef", string(data))
}

func TestRemediationTruncateRejectsFIFOWithoutBlocking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFOs are Unix-specific")
	}
	dir := t.TempDir()
	require.NoError(t, mkfifo(filepath.Join(dir, "pipe"), 0644))
	called := false

	res := runRemediationScriptWithoutBlocking(t, "truncate -s 0 pipe", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, res.code)
	assert.Equal(t, "", res.stdout)
	assert.Contains(t, res.stderr, "permission denied")
	assert.False(t, called)
}

func TestRemediationTruncateRejectsTrailingSeparatorBeforeHostExecution(t *testing.T) {
	requireHostExtraFilesSupported(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "target.log")
	require.NoError(t, os.WriteFile(target, []byte("abcdef"), 0644))
	called := false

	_, stderr, code := runScript(t, "truncate -s 0 target.log/", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "not a directory")
	assert.False(t, called)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "abcdef", string(data))
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

	for _, action := range []string{"restart", "start", "stop", "reload", "status"} {
		got = nil
		stdout, stderr, code := runScript(t, "systemctl "+action+" app.service", dir,
			interp.HostCommandHandler(func(ctx context.Context, args []string) error {
				got = append([]string(nil), args...)
				return nil
			}),
		)

		assert.Equal(t, 0, code)
		assert.Equal(t, "", stdout)
		assert.Equal(t, "", stderr)
		assert.Equal(t, []string{"systemctl", action, "--", "app.service"}, got)
	}
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

func TestRemediationSystemctlShowActiveStateDelegates(t *testing.T) {
	dir := t.TempDir()
	var got []string

	stdout, stderr, code := runScript(t, "systemctl show --property=ActiveState --value app.service", dir,
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			got = append([]string(nil), args...)
			return nil
		}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	assert.Equal(t, []string{"systemctl", "show", "--property=ActiveState", "--value", "--", "app.service"}, got)
}

func TestRemediationSystemctlShowActiveStatePreservesLeadingDashUnit(t *testing.T) {
	dir := t.TempDir()
	var got []string

	stdout, stderr, code := runScript(t, "systemctl show --property ActiveState --value -- -app.service", dir,
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			got = append([]string(nil), args...)
			return nil
		}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	assert.Equal(t, []string{"systemctl", "show", "--property=ActiveState", "--value", "--", "-app.service"}, got)
}

func TestRemediationSystemctlJSONReportsActiveState(t *testing.T) {
	dir := t.TempDir()
	var got [][]string

	stdout, stderr, code := runScript(t, "systemctl --json restart app.service", dir,
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			got = append(got, append([]string(nil), args...))
			if len(args) > 1 && args[1] == "show" {
				_, err := io.WriteString(interp.HandlerCtx(ctx).Stdout, "active\n")
				return err
			}
			return nil
		}),
	)

	assert.Equal(t, 0, code)
	assert.JSONEq(t, `{"unit":"app.service","action":"restart","active_state":"active","exit_code":0,"stdout":"","stderr":""}`, stdout)
	assert.Equal(t, "", stderr)
	assert.Equal(t, [][]string{
		{"systemctl", "restart", "--", "app.service"},
		{"systemctl", "show", "--property=ActiveState", "--value", "--", "app.service"},
	}, got)
}

func TestRemediationSystemctlJSONCapsCapturedOutput(t *testing.T) {
	dir := t.TempDir()
	large := strings.Repeat("x", 2<<20)

	stdout, stderr, code := runScript(t, "systemctl --json status app.service", dir,
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			hc := interp.HandlerCtx(ctx)
			if len(args) > 1 && args[1] == "show" {
				_, err := io.WriteString(hc.Stdout, "active\n")
				return err
			}
			if _, err := io.WriteString(hc.Stdout, large); err != nil {
				return err
			}
			_, err := io.WriteString(hc.Stderr, large)
			return err
		}),
	)

	require.Equal(t, 0, code)
	assert.Equal(t, "", stderr)

	var got struct {
		Stdout          string `json:"stdout"`
		Stderr          string `json:"stderr"`
		StdoutTruncated bool   `json:"stdout_truncated"`
		StderrTruncated bool   `json:"stderr_truncated"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.True(t, got.StdoutTruncated)
	assert.True(t, got.StderrTruncated)
	assert.NotEmpty(t, got.Stdout)
	assert.NotEmpty(t, got.Stderr)
	assert.Less(t, len(got.Stdout), len(large))
	assert.Less(t, len(got.Stderr), len(large))
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

func TestRemediationSystemctlRejectsUnsupportedShowShape(t *testing.T) {
	dir := t.TempDir()
	called := false

	for _, script := range []string{
		"systemctl show --property=SubState --value app.service",
		"systemctl show --property=ActiveState app.service",
		"systemctl restart --property=ActiveState --value app.service",
	} {
		_, stderr, code := runScript(t, script, dir,
			interp.HostCommandHandler(func(ctx context.Context, args []string) error {
				called = true
				return nil
			}),
		)

		assert.Equal(t, 1, code)
		assert.NotEmpty(t, stderr)
		assert.False(t, called)
	}
}

func TestRemediationKillTerminatesProcessDirectly(t *testing.T) {
	dir := t.TempDir()
	helper := startKillHelperProcess(t)
	called := false

	stdout, stderr, code := runScript(t, "kill -9 --timeout 0 "+strconv.Itoa(helper.cmd.Process.Pid), dir,
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	assert.False(t, called)
	helper.waitForExit(t)
}

func TestRemediationKillDefaultTimeoutConfirmsProcessExit(t *testing.T) {
	dir := t.TempDir()
	helper := startKillHelperProcess(t)

	stdout, stderr, code := runScript(t, "kill "+strconv.Itoa(helper.cmd.Process.Pid), dir)

	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	helper.waitForExit(t)
}

func TestRemediationKillJSONReportsDirectResult(t *testing.T) {
	dir := t.TempDir()
	helper := startKillHelperProcess(t)

	stdout, stderr, code := runScript(t, "kill --json --timeout 0 "+strconv.Itoa(helper.cmd.Process.Pid), dir)

	assert.Equal(t, 0, code)
	assert.JSONEq(t, `{"pid":`+strconv.Itoa(helper.cmd.Process.Pid)+`,"force":false,"signal":"SIGTERM","timed_out":false,"exit_code":0,"stdout":"","stderr":""}`, stdout)
	assert.Equal(t, "", stderr)
	helper.waitForExit(t)
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

func TestRemediationWriteFileJSONWritesAndReports(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, code := runScript(t, "write_file --json output.txt <<'EOF'\npayload\nEOF\n", dir,
		interp.AllowedPaths([]string{dir}),
	)

	assert.Equal(t, 0, code)
	assert.JSONEq(t, `{"path":"output.txt","mode":"overwrite","bytes_written":8,"bytes_after":8,"created":true,"exit_code":0,"stdout":"","stderr":""}`, stdout)
	assert.Equal(t, "", stderr)
	data, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	require.NoError(t, err)
	assert.Equal(t, "payload\n", string(data))
}

func TestRemediationWriteFileAppendReportsExistingTarget(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "output.txt"), []byte("old\n"), 0644))

	stdout, stderr, code := runScript(t, "write_file --json --mode append output.txt <<'EOF'\nnew\nEOF\n", dir,
		interp.AllowedPaths([]string{dir}),
	)

	assert.Equal(t, 0, code)
	assert.JSONEq(t, `{"path":"output.txt","mode":"append","bytes_written":4,"bytes_after":8,"created":false,"exit_code":0,"stdout":"","stderr":""}`, stdout)
	assert.Equal(t, "", stderr)
	data, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	require.NoError(t, err)
	assert.Equal(t, "old\nnew\n", string(data))
}

func TestDisableFileWritesRemovesWriteCapabilitiesFromRemediationBuiltins(t *testing.T) {
	tests := []struct {
		name        string
		script      string
		errContains string
		target      string
		initial     string
	}{
		{
			name:        "write_file",
			script:      "write_file output.txt <<'EOF'\npayload\nEOF\n",
			errContains: "write_file: file write is not available",
			target:      "output.txt",
		},
		{
			name:        "tee",
			script:      "tee output.txt <<'EOF'\npayload\nEOF\n",
			errContains: "tee: file write is not available",
			target:      "output.txt",
		},
		{
			name:        "truncate",
			script:      "truncate -s 0 app.log",
			errContains: "truncate: file write is not available",
			target:      "app.log",
			initial:     "keep\n",
		},
		{
			name:        "logrotate",
			script:      "logrotate app.log",
			errContains: "logrotate: file write is not available",
			target:      "app.log",
			initial:     "keep\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.initial != "" {
				require.NoError(t, os.WriteFile(filepath.Join(dir, tt.target), []byte(tt.initial), 0644))
			}

			stdout, stderr, code := runScript(t, tt.script, dir,
				interp.AllowedPaths([]string{dir}),
				interp.DisableFileWrites(),
				interp.HostCommandHandler(func(ctx context.Context, args []string) error {
					t.Fatalf("host command should not run with file writes disabled: %v", args)
					return nil
				}),
			)

			assert.Equal(t, 1, code)
			assert.Equal(t, "", stdout)
			assert.Contains(t, stderr, tt.errContains)
			data, err := os.ReadFile(filepath.Join(dir, tt.target))
			if tt.initial == "" {
				require.ErrorIs(t, err, os.ErrNotExist)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.initial, string(data))
		})
	}
}

func TestRemediationTeeDelegatesAppendWithStdin(t *testing.T) {
	requireHostExtraFilesSupported(t)
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
	requireHostExtraFilesSupported(t)
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
	requireHostExtraFilesSupported(t)
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

func TestRemediationTeeRejectsSymlinkTargetBeforeHostExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior is platform-specific on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("payload\n"), 0644))
	require.NoError(t, os.WriteFile(target, []byte("keep\n"), 0644))
	require.NoError(t, os.Symlink("target.txt", filepath.Join(dir, "link.txt")))
	called := false

	_, stderr, code := runScript(t, "tee link.txt < input.txt", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "permission denied")
	assert.False(t, called)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "keep\n", string(data))
}

func TestRemediationTeeRejectsFIFOWithoutBlocking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFOs are Unix-specific")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("payload\n"), 0644))
	require.NoError(t, mkfifo(filepath.Join(dir, "pipe"), 0644))
	called := false

	res := runRemediationScriptWithoutBlocking(t, "tee pipe < input.txt", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, res.code)
	assert.Equal(t, "", res.stdout)
	assert.Contains(t, res.stderr, "permission denied")
	assert.False(t, called)
}

func TestRemediationTeeRejectsTrailingSeparatorBeforeHostExecution(t *testing.T) {
	requireHostExtraFilesSupported(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("payload\n"), 0644))
	require.NoError(t, os.WriteFile(target, []byte("keep\n"), 0644))
	called := false

	_, stderr, code := runScript(t, "tee target.txt/ < input.txt", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "not a directory")
	assert.False(t, called)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "keep\n", string(data))
}

func TestRemediationTeeWithoutHostHandlerDoesNotMutateTarget(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing.txt")
	missing := filepath.Join(dir, "missing.txt")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("payload\n"), 0644))
	require.NoError(t, os.WriteFile(existing, []byte("keep\n"), 0644))

	_, stderr, code := runScript(t, "tee existing.txt < input.txt", dir,
		interp.AllowedPaths([]string{dir}),
	)
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr, "unknown command")
	data, err := os.ReadFile(existing)
	require.NoError(t, err)
	assert.Equal(t, "keep\n", string(data))

	_, stderr, code = runScript(t, "tee missing.txt < input.txt", dir,
		interp.AllowedPaths([]string{dir}),
	)
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr, "unknown command")
	assert.NoFileExists(t, missing)
}

func TestRemediationLogrotateDelegatesExistingPath(t *testing.T) {
	requireHostExtraFilesSupported(t)
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

func TestRemediationLogrotateJSONReportsRotatedPath(t *testing.T) {
	requireHostExtraFilesSupported(t)
	dir := t.TempDir()
	active := filepath.Join(dir, "app.log")
	rotated := filepath.Join(dir, "app.log.1")
	require.NoError(t, os.WriteFile(active, []byte("payload\n"), 0644))

	stdout, stderr, code := runScript(t, "logrotate --json app.log", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			require.Equal(t, []string{"logrotate", "--", builtins.HostExtraFilePath(0)}, args)
			require.NoError(t, os.Rename(active, rotated))
			return os.WriteFile(active, nil, 0644)
		}),
	)

	assert.Equal(t, 0, code)
	assert.JSONEq(t, `{"path":"app.log","rotated_path":"app.log.1","bytes_before":8,"bytes_after":0,"exit_code":0,"stdout":"","stderr":""}`, stdout)
	assert.Equal(t, "", stderr)
	assert.FileExists(t, active)
	assert.FileExists(t, rotated)
}

func TestRemediationLogrotatePreservesLeadingDashOperand(t *testing.T) {
	requireHostExtraFilesSupported(t)
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

func TestRemediationLogrotateRejectsSymlinkTargetBeforeHostExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior is platform-specific on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.log")
	require.NoError(t, os.WriteFile(target, []byte("payload\n"), 0644))
	require.NoError(t, os.Symlink("target.log", filepath.Join(dir, "link.log")))
	called := false

	_, stderr, code := runScript(t, "logrotate link.log", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "permission denied")
	assert.False(t, called)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "payload\n", string(data))
}

func TestRemediationLogrotateRejectsFIFOWithoutBlocking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFOs are Unix-specific")
	}
	dir := t.TempDir()
	require.NoError(t, mkfifo(filepath.Join(dir, "pipe"), 0644))
	called := false

	res := runRemediationScriptWithoutBlocking(t, "logrotate pipe", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, res.code)
	assert.Equal(t, "", res.stdout)
	assert.Contains(t, res.stderr, "permission denied")
	assert.False(t, called)
}

func TestRemediationLogrotateRejectsTrailingSeparatorBeforeHostExecution(t *testing.T) {
	requireHostExtraFilesSupported(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "target.log")
	require.NoError(t, os.WriteFile(target, []byte("payload\n"), 0644))
	called := false

	_, stderr, code := runScript(t, "logrotate target.log/", dir,
		interp.AllowedPaths([]string{dir}),
		interp.HostCommandHandler(func(ctx context.Context, args []string) error {
			called = true
			return nil
		}),
	)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "not a directory")
	assert.False(t, called)
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "payload\n", string(data))
}
