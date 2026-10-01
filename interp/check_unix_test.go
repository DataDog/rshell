//go:build linux || darwin

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckDoesNotOpenFIFO(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, unix.Mkfifo(filepath.Join(root, "fifo"), 0600))
	runner := checkRunner(t, allowAllCommandsOpt(), AllowedPaths([]string{root + ":rw"}), WithMode(ModeRemediation))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan *CheckResult, 1)
	go func() {
		result, _ := runner.Check(ctx, "cat fifo; echo hi > fifo")
		done <- result
	}()
	select {
	case result := <-done:
		require.NotNil(t, result)
		assert.Equal(t, CheckDenied, result.Status)
		assert.True(t, checkHasIssue(result, CheckPathNotAllowed))
	case <-ctx.Done():
		t.Fatal("Check blocked opening a FIFO")
	}
}
