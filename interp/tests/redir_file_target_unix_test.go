// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package tests_test

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Opening a FIFO with O_WRONLY blocks until a reader connects. Without the
// pre-open type guard, redirecting to a FIFO inside AllowedPaths would hang
// the script during redirect setup before any command runs and before the
// runner could observe context cancellation. Sandbox.Stat is openat-based
// and never blocks, so we use it to reject non-regular targets before the
// blocking open happens.

func TestRedirToFifoRejectedFastForOutput(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0644))

	// Bound the test so a regression (a hang) fails loudly instead of
	// stalling the whole suite. The check itself is openat-only and
	// returns synchronously well within this budget.
	done := make(chan struct{})
	var stdout, stderr string
	var code int
	go func() {
		defer close(done)
		stdout, stderr, code = redirRun(t, "echo hi > fifo", dir)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("redirect to FIFO hung; pre-open type guard regressed")
	}

	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "not a regular file")
}

func TestRedirToFifoRejectedFastForBothStreams(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0644))

	done := make(chan struct{})
	var stdout, stderr string
	var code int
	go func() {
		defer close(done)
		stdout, stderr, code = redirRun(t, "echo hi &> fifo", dir)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("&> redirect to FIFO hung; pre-open type guard regressed")
	}

	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "not a regular file")
}

// A baseline: when there is no FIFO on the path, the same code path must
// still work — i.e. the pre-open Stat must not gratuitously fail on a
// non-existent target.
func TestRedirToMissingFileStillWorks(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRun(t, "echo hi > new.txt", dir)
	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}
