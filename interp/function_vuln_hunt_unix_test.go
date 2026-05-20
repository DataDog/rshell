//go:build unix

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: function (shell-feature)

package interp

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVulnHuntShellFeatureRedirectionChain_FunctionInputRedirectFifoDoesNotBlock(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "in.fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))

	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt(), AllowedPaths([]string{dir}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Dir = dir

	done := make(chan error, 1)
	prog := parseScript(t, "f() { echo BAD; } < in.fifo\necho after\n")
	go func() {
		done <- r.Run(context.Background(), prog)
	}()

	select {
	case err = <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("function declaration with FIFO input redirection blocked before validation")
	}

	var status ExitStatus
	require.True(t, errors.As(err, &status), "got err %v", err)
	assert.Equal(t, ExitStatus(2), status)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "function declarations are not supported")
}
