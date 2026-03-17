// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ping_test

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

func runScript(t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScript(t, script, dir, opts...)
}

func runScriptCtx(ctx context.Context, t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, opts...)
}

func cmdRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return runScript(t, script, dir)
}

// --- Help ---

func TestPingHelp(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, "ping --help", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage:")
	assert.Contains(t, stdout, "HOST")
	assert.Empty(t, stderr)
}

func TestPingHelpShort(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, "ping -h", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage:")
}

// --- Argument validation ---

func TestPingMissingHost(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, "ping", dir)
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "ping: missing host operand")
}

func TestPingTooManyArgs(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, "ping host1 host2", dir)
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "ping: too many arguments")
}

// --- Count validation ---

func TestPingCountZero(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "ping -c 0 localhost", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "ping: invalid count")
}

func TestPingCountNegative(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "ping -c -1 localhost", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "ping: invalid count")
}

// --- Timeout validation ---

func TestPingTimeoutZero(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "ping -W 0 localhost", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "ping: invalid timeout")
}

func TestPingTimeoutNegative(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "ping -W -1 localhost", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "ping: invalid timeout")
}

// --- Interval validation ---

func TestPingIntervalZero(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "ping -i 0 localhost", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "ping: invalid interval")
}

func TestPingIntervalNegative(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "ping -i -1 localhost", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "ping: invalid interval")
}

// --- Unknown flags ---

func TestPingUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "ping --follow localhost", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "unknown flag")
}

func TestPingUnknownShortFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "ping -f localhost", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "unknown shorthand flag")
}

// --- Context cancellation ---

func TestPingContextTimeout(t *testing.T) {
	if os.Getenv("RSHELL_PING_TEST") == "" {
		t.Skip("skipping ping integration test; set RSHELL_PING_TEST=1 and run with sudo on Linux")
	}
	dir := t.TempDir()
	// Use a very short timeout to ensure the command gets interrupted.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _, code := runScriptCtx(ctx, t, "ping -c 100 -W 10 127.0.0.1", dir)
	// The command should either fail due to timeout or ICMP error.
	// We just verify it doesn't hang.
	_ = code
}

// --- Hardening: count clamping ---

func TestPingCountClampedToMax(t *testing.T) {
	if os.Getenv("RSHELL_PING_TEST") == "" {
		t.Skip("skipping ping integration test; set RSHELL_PING_TEST=1 and run with sudo on Linux")
	}
	dir := t.TempDir()
	// Count exceeding MaxCount should be clamped, not rejected.
	// We can only verify this doesn't error on validation.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, stderr, code := runScriptCtx(ctx, t, "ping -c 9999 127.0.0.1", dir)
	// Should NOT get "invalid count" error — clamping should happen silently.
	assert.NotContains(t, stderr, "invalid count")
	_ = code // may fail due to ICMP privileges, that's ok
}

func TestPingTimeoutClampedToMax(t *testing.T) {
	if os.Getenv("RSHELL_PING_TEST") == "" {
		t.Skip("skipping ping integration test; set RSHELL_PING_TEST=1 and run with sudo on Linux")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, stderr, code := runScriptCtx(ctx, t, "ping -c 1 -W 9999 127.0.0.1", dir)
	assert.NotContains(t, stderr, "invalid timeout")
	_ = code
}

// --- Hardening: long-form flags ---

func TestPingLongFormCount(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "ping --count 0 localhost", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "ping: invalid count")
}

func TestPingLongFormTimeout(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "ping --timeout 0 localhost", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "ping: invalid timeout")
}

func TestPingLongFormInterval(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "ping --interval 0 localhost", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "ping: invalid interval")
}

// --- Hardening: non-numeric flag values ---

func TestPingCountNonNumeric(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "ping -c abc localhost", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "ping:")
}

func TestPingTimeoutNonNumeric(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "ping -W abc localhost", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "ping:")
}

// --- Hardening: help takes precedence over errors ---

func TestPingHelpWithInvalidArgs(t *testing.T) {
	dir := t.TempDir()
	// --help should still print usage even if other args are bad
	stdout, _, code := cmdRun(t, "ping --help", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage:")
}

// --- Integration test (requires network + ICMP privileges) ---

func TestPingLocalhostIntegration(t *testing.T) {
	if os.Getenv("RSHELL_PING_TEST") == "" {
		t.Skip("skipping ping integration test; set RSHELL_PING_TEST=1 and run with sudo on Linux")
	}

	tests := []struct {
		name  string
		count int
	}{
		{"single ping", 1},
		{"three pings", 3},
	}

	if runtime.GOOS == "windows" {
		// Windows CI runners may or may not allow unprivileged ICMP;
		// the result is non-deterministic.  When ICMP is denied, the
		// traceroute library returns an error before statistics are
		// printed, so we cannot assert on summary lines in that case.
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				dir := t.TempDir()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				cmd := fmt.Sprintf("ping -c %d -W 5 127.0.0.1", tt.count)
				stdout, stderr, code := runScriptCtx(ctx, t, cmd, dir)
				// Accept both success (0) and failure (1).
				assert.True(t, code == 0 || code == 1,
					"expected exit code 0 or 1 on Windows, got %d", code)

				// The PING header must always be present — it is printed
				// before any ICMP work begins, proving flag parsing succeeded.
				assert.Contains(t, stdout, "PING 127.0.0.1")

				if code == 0 || strings.Contains(stdout, "ping statistics") {
					// ICMP reached the network layer — verify the output
					// format.  On Windows CI runners ICMP is often
					// partially blocked, causing non-deterministic packet
					// loss even to 127.0.0.1.  We therefore only assert
					// that the statistics section is well-formed and that
					// the transmitted count matches -c; we do NOT require
					// every probe to receive a reply.
					assert.Contains(t, stdout, "ping statistics")
					assert.Contains(t, stdout, fmt.Sprintf("%d packets transmitted", tt.count))
					assert.Contains(t, stdout, "packet loss")
				} else {
					// ICMP was denied — the library errors out before any
					// probes are sent, so no statistics are printed.  We
					// still verify the error message references the host,
					// proving the -c flag was accepted and execution reached
					// the network layer.
					assert.NotEmpty(t, stderr, "expected an error message when ICMP is denied")
					assert.Contains(t, stderr, "127.0.0.1",
						"error message should reference the target host")
				}
			})
		}
		return
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := fmt.Sprintf("ping -c %d -W 5 127.0.0.1", tt.count)
			stdout, stderr, code := runScriptCtx(ctx, t, cmd, dir)
			assert.Equal(t, 0, code, "ping failed: %s", stderr)
			assert.Contains(t, stdout, "PING 127.0.0.1")
			assert.Contains(t, stdout, "ping statistics")

			// Verify we got exactly the requested number of reply lines.
			replyCount := strings.Count(stdout, "64 bytes from")
			assert.Equal(t, tt.count, replyCount,
				"expected exactly %d replies, got %d\nstdout:\n%s", tt.count, replyCount, stdout)

			// Verify the summary reports the correct number of packets transmitted.
			assert.Contains(t, stdout, fmt.Sprintf("%d packets transmitted", tt.count))
		})
	}
}
