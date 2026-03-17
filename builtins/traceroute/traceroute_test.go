// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package traceroute_test

import (
	"context"
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

func cmdRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return runScript(t, script, dir)
}

func cmdRunCtx(ctx context.Context, t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir)
}

// --- Help ---

func TestTracerouteHelp(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, "traceroute --help", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage: traceroute")
	assert.Contains(t, stdout, "Print the route packets take")
	assert.Empty(t, stderr)
}

func TestTracerouteHelpShort(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, "traceroute -h", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage: traceroute")
	assert.Empty(t, stderr)
}

// --- Missing / excess arguments ---

func TestTracerouteMissingHost(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, "traceroute", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "missing host operand")
	assert.Empty(t, stdout)
}

func TestTracerouteTooManyArgs(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, "traceroute host1 host2", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "too many arguments")
	assert.Empty(t, stdout)
}

// --- Unknown flags ---

func TestTracerouteUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "traceroute --follow example.com", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "traceroute:")
}

func TestTracerouteUnknownShortFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "traceroute -x example.com", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "traceroute:")
}

// --- Invalid numeric parameters ---

func TestTracerouteInvalidMaxHops(t *testing.T) {
	tests := []struct {
		name   string
		script string
		errMsg string
	}{
		{"zero", "traceroute -m 0 example.com", "--max-hops must be between 1 and 255"},
		{"negative", "traceroute -m -1 example.com", "--max-hops must be between 1 and 255"},
		{"too_large", "traceroute -m 256 example.com", "--max-hops must be between 1 and 255"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			_, stderr, code := cmdRun(t, tc.script, dir)
			assert.Equal(t, 1, code)
			assert.Contains(t, stderr, tc.errMsg)
		})
	}
}

func TestTracerouteInvalidPort(t *testing.T) {
	tests := []struct {
		name   string
		script string
	}{
		{"zero", "traceroute -p 0 example.com"},
		{"too_large", "traceroute -p 99999 example.com"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			_, stderr, code := cmdRun(t, tc.script, dir)
			assert.Equal(t, 1, code)
			assert.Contains(t, stderr, "--port must be between 1 and 65535")
		})
	}
}

func TestTracerouteInvalidQueries(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "traceroute -q 0 example.com", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "--queries must be between 1 and 100")
}

func TestTracerouteInvalidFirstTTL(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "traceroute -f 0 example.com", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "--first-ttl must be between 1 and max-hops")
}

func TestTracerouteFirstTTLExceedsMaxHops(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "traceroute -f 10 -m 5 example.com", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "--first-ttl must be between 1 and max-hops")
}

// --- Invalid string parameters ---

func TestTracerouteInvalidProtocol(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "traceroute --protocol invalid example.com", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "unknown protocol")
}

func TestTracerouteInvalidTCPMethod(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "traceroute --tcp-method bogus example.com", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "unknown TCP method")
}

// --- Valid protocol values ---

func TestTracerouteValidProtocols(t *testing.T) {
	// These pass validation but will fail/timeout on actual network call.
	// Use --e2e-queries 0 and --queries 1 to minimize network time, and
	// a short timeout so we don't hang.
	protocols := []string{"udp", "tcp", "icmp", "UDP", "TCP", "ICMP"}
	for _, proto := range protocols {
		t.Run(proto, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, stderr, code := cmdRunCtx(ctx, t,
				"traceroute --protocol "+proto+" --e2e-queries 0 -q 1 -w 1 -m 1 127.0.0.1", dir)
			// Should not fail with protocol validation error
			if code != 0 {
				assert.NotContains(t, stderr, "unknown protocol")
			}
		})
	}
}

// --- Context cancellation ---

func TestTracerouteContextCancelled(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, _, code := cmdRunCtx(ctx, t, "traceroute 127.0.0.1", dir)
	// When context is already cancelled, the shell runner may return 0
	// (skipping the command) or 1 (command sees cancelled context).
	// Either is acceptable — the key is it doesn't hang.
	assert.Contains(t, []int{0, 1}, code)
}

// --- Invalid wait/delay/e2e ---

func TestTracerouteInvalidWait(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "traceroute -w 0 example.com", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "--wait must be between 1 and 300")
}

func TestTracerouteInvalidDelay(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "traceroute --delay -1 example.com", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "--delay must be between 0 and 60000")
}

func TestTracerouteInvalidE2eQueries(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "traceroute --e2e-queries -1 example.com", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "--e2e-queries must be between 0 and 1000")
}
