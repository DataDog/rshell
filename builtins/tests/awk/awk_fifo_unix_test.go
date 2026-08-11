// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || darwin

package awk_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
)

func TestAwkGetlineFIFORespectsMaxExecutionTime(t *testing.T) {
	dir := t.TempDir()
	fifoPath := filepath.Join(dir, "input.fifo")
	require.NoError(t, unix.Mkfifo(fifoPath, 0o600))
	holder, err := os.OpenFile(fifoPath, os.O_RDWR, 0)
	require.NoError(t, err)
	defer holder.Close()

	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(`awk 'BEGIN { getline x < "input.fifo"; print "unreachable" }'`), "")
	require.NoError(t, err)
	var stdout, stderr bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(nil, &stdout, &stderr),
		interpoption.AllowAllCommands().(interp.RunnerOption),
		interp.AllowedPaths([]string{dir}),
		interp.MaxExecutionTime(50*time.Millisecond),
	)
	require.NoError(t, err)
	defer runner.Close()
	runner.Dir = dir

	done := make(chan error, 1)
	go func() { done <- runner.Run(context.Background(), prog) }()
	select {
	case runErr := <-done:
		var status interp.ExitStatus
		assert.True(t, errors.Is(runErr, context.DeadlineExceeded) || errors.As(runErr, &status), runErr)
		assert.Empty(t, stdout.String())
		assert.Contains(t, stderr.String(), "context deadline exceeded")
	case <-time.After(2 * time.Second):
		t.Fatal("awk did not interrupt its blocked FIFO read")
	}
}
