// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	sandboxlandlock "github.com/DataDog/rshell/internal/sandbox/landlock"
	"github.com/DataDog/rshell/privilegedhelper"
	"github.com/stretchr/testify/require"
)

func TestServePrivilegedWorker(t *testing.T) {
	command := &privilegedhelper.VerifiedCommand{
		TaskID:             "task-1",
		Command:            "echo hello",
		AllowedCommands:    []string{"rshell:echo"},
		AllowedPaths:       []string{"/var/log:ro"},
		ElevatableCommands: []string{"rshell:truncate"},
	}
	var input bytes.Buffer
	require.NoError(t, writeWorkerMessage(&input, workerRequest{Version: workerProtocolVersion, Command: command}))

	var stdout, stderr bytes.Buffer
	code := servePrivilegedWorker(context.Background(), &input, &stdout, &stderr, func(_ context.Context, got *privilegedhelper.VerifiedCommand) (*privilegedhelper.ExecuteResponse, error) {
		require.Equal(t, command, got)
		return &privilegedhelper.ExecuteResponse{ExitCode: 7, Stdout: "out", Stderr: "err"}, nil
	})
	require.Zero(t, code)
	require.Empty(t, stderr.String())

	var response workerResponse
	require.NoError(t, readWorkerMessage(&stdout, &response))
	require.Equal(t, workerProtocolVersion, response.Version)
	require.Equal(t, &privilegedhelper.ExecuteResponse{ExitCode: 7, Stdout: "out", Stderr: "err"}, response.Result)
	require.Empty(t, response.Error)
}

func TestParseAccountCredentials(t *testing.T) {
	credentials, err := parseAccountCredentials("1234", "2345", []string{"4567", "2345", "3456", "4567", "0"})
	require.NoError(t, err)
	require.Equal(t, accountCredentials{
		uid:               1234,
		primaryGID:        2345,
		supplementaryGIDs: []int{0, 3456, 4567},
	}, credentials)
}

func TestParseAccountCredentialsRejectsInvalidIDs(t *testing.T) {
	tests := []struct {
		name       string
		uid        string
		primaryGID string
		groups     []string
	}{
		{name: "root uid", uid: "0", primaryGID: "1"},
		{name: "negative uid", uid: "-1", primaryGID: "1"},
		{name: "invalid uid", uid: "abc", primaryGID: "1"},
		{name: "reserved uid", uid: "4294967295", primaryGID: "1"},
		{name: "uid too large for int", uid: strconv.FormatUint(uint64(1)<<(strconv.IntSize-1), 10), primaryGID: "1"},
		{name: "negative primary gid", uid: "1", primaryGID: "-1"},
		{name: "invalid primary gid", uid: "1", primaryGID: "abc"},
		{name: "negative supplementary gid", uid: "1", primaryGID: "2", groups: []string{"-1"}},
		{name: "invalid supplementary gid", uid: "1", primaryGID: "2", groups: []string{"abc"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseAccountCredentials(test.uid, test.primaryGID, test.groups)
			require.Error(t, err)
		})
	}
}

func TestServePrivilegedWorkerReturnsExecutionError(t *testing.T) {
	var input bytes.Buffer
	require.NoError(t, writeWorkerMessage(&input, workerRequest{
		Version: workerProtocolVersion,
		Command: &privilegedhelper.VerifiedCommand{Command: "echo hello"},
	}))
	var stdout, stderr bytes.Buffer
	code := servePrivilegedWorker(context.Background(), &input, &stdout, &stderr, func(context.Context, *privilegedhelper.VerifiedCommand) (*privilegedhelper.ExecuteResponse, error) {
		return nil, errors.New("sandbox unavailable")
	})
	require.Zero(t, code)
	require.Empty(t, stderr.String())

	var response workerResponse
	require.NoError(t, readWorkerMessage(&stdout, &response))
	require.Nil(t, response.Result)
	require.Equal(t, "sandbox unavailable", response.Error)
}

func TestReadWorkerMessageRejectsTrailingData(t *testing.T) {
	var message bytes.Buffer
	require.NoError(t, writeWorkerMessage(&message, workerRequest{
		Version: workerProtocolVersion,
		Command: &privilegedhelper.VerifiedCommand{Command: "true"},
	}))
	message.WriteByte(0)

	var request workerRequest
	require.ErrorContains(t, readWorkerMessage(&message, &request), "trailing data")
}

func TestReadWorkerMessageRejectsOversizedFrame(t *testing.T) {
	var message bytes.Buffer
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], maxWorkerMessageBytes+1)
	message.Write(size[:])

	var request workerRequest
	require.ErrorContains(t, readWorkerMessage(&message, &request), "invalid message size")
}

func TestTrustedPathsForCommands(t *testing.T) {
	paths := trustedPathsForCommands([]string{
		"rshell:ss",
		"rshell:ip",
		"rshell:df",
		"rshell:uname",
		"rshell:echo",
	})
	require.Equal(t, []sandboxlandlock.TrustedPath{
		trustedReadOnlyDirectory("/proc/net"),
		trustedReadOnlyFile("/proc/self/mountinfo"),
		trustedReadOnlyFile("/proc/sys/kernel/ostype"),
		trustedReadOnlyFile("/proc/sys/kernel/hostname"),
		trustedReadOnlyFile("/proc/sys/kernel/osrelease"),
		trustedReadOnlyFile("/proc/sys/kernel/version"),
		trustedReadOnlyFile("/proc/sys/kernel/arch"),
	}, paths)
}

func TestTrustedPathsForCommandsProcSubsumesProcNet(t *testing.T) {
	paths := trustedPathsForCommands([]string{"rshell:ps", "rshell:ss", "rshell:ip"})
	require.Equal(t, []sandboxlandlock.TrustedPath{
		trustedReadOnlyDirectory("/proc"),
	}, paths)
}

func TestTrustedPathsForCommandsIgnoresUnrelatedCommands(t *testing.T) {
	require.Empty(t, trustedPathsForCommands([]string{"rshell:echo", "rshell:cat"}))
}

func TestHelperExecutorUsesFreshWorkersAndEffectivePolicy(t *testing.T) {
	executor := &helperExecutor{newWorker: newTestWorkerCommand}
	command := &privilegedhelper.VerifiedCommand{
		TaskID:             "task-1",
		Command:            "echo hello",
		AllowedCommands:    []string{"rshell:echo"},
		AllowedPaths:       []string{"/var/log:ro"},
		ElevatableCommands: []string{"rshell:truncate"},
	}
	first, err := executor.Execute(context.Background(), command)
	require.NoError(t, err)
	second, err := executor.Execute(context.Background(), command)
	require.NoError(t, err)
	require.NotEqual(t, first.Stdout, second.Stdout, "each request must run in a fresh process")

	var got privilegedhelper.VerifiedCommand
	require.NoError(t, json.Unmarshal([]byte(first.Stderr), &got))
	require.Equal(t, command, &got)
}

func TestHelperExecutorHonorsContextCancellation(t *testing.T) {
	executor := &helperExecutor{newWorker: newTestWorkerCommand}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := executor.Execute(ctx, &privilegedhelper.VerifiedCommand{Command: "block"})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func newTestWorkerCommand(ctx context.Context) (*exec.Cmd, error) {
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPrivilegedWorkerFixture$")
	command.Env = append(os.Environ(), "RSHELL_PRIVILEGED_WORKER_FIXTURE=1")
	return command, nil
}

func TestPrivilegedWorkerFixture(t *testing.T) {
	if os.Getenv("RSHELL_PRIVILEGED_WORKER_FIXTURE") != "1" {
		return
	}
	var request workerRequest
	if err := readWorkerMessage(os.Stdin, &request); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if request.Command.Command == "block" {
		time.Sleep(time.Hour)
	}
	policy, err := json.Marshal(request.Command)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	response := workerResponse{
		Version: workerProtocolVersion,
		Result: &privilegedhelper.ExecuteResponse{
			Stdout: strconv.Itoa(os.Getpid()),
			Stderr: strings.TrimSpace(string(policy)),
		},
	}
	if err := writeWorkerMessage(os.Stdout, response); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
