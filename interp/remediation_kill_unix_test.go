// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package interp_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startKillIgnoreTermHelperProcess(t *testing.T) *killHelperProcess {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=TestKillIgnoreTermHelperProcess")
	cmd.Env = append(os.Environ(), "RSHELL_KILL_IGNORE_TERM_HELPER=1")
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	cmd.Stderr = io.Discard
	require.NoError(t, cmd.Start())
	ready, err := bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "ready\n", ready)
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

func TestKillIgnoreTermHelperProcess(t *testing.T) {
	if os.Getenv("RSHELL_KILL_IGNORE_TERM_HELPER") != "1" {
		return
	}
	signal.Ignore(syscall.SIGTERM)
	fmt.Fprintln(os.Stdout, "ready")
	select {}
}

func TestRemediationKillTimeoutReturnsFailure(t *testing.T) {
	dir := t.TempDir()
	helper := startKillIgnoreTermHelperProcess(t)

	stdout, stderr, code := runScript(t, "kill --timeout 20ms "+strconv.Itoa(helper.cmd.Process.Pid), dir)

	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "kill: timed out waiting for pid "+strconv.Itoa(helper.cmd.Process.Pid))
}

func TestRemediationKillJSONTimeoutReturnsFailureReceipt(t *testing.T) {
	dir := t.TempDir()
	helper := startKillIgnoreTermHelperProcess(t)

	stdout, stderr, code := runScript(t, "kill --json --timeout 20ms "+strconv.Itoa(helper.cmd.Process.Pid), dir)

	assert.Equal(t, 1, code)
	assert.Equal(t, "", stderr)

	var got struct {
		PID      int    `json:"pid"`
		TimedOut bool   `json:"timed_out"`
		ExitCode uint8  `json:"exit_code"`
		Stderr   string `json:"stderr"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &got))
	assert.Equal(t, helper.cmd.Process.Pid, got.PID)
	assert.True(t, got.TimedOut)
	assert.Equal(t, uint8(1), got.ExitCode)
	assert.True(t, strings.HasPrefix(got.Stderr, "kill: timed out waiting for pid "))
}
