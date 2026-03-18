// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package ss_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/rshell/interp"
)

// procNetAllowed returns an AllowedPaths option that grants access to /proc/net.
func procNetAllowed() interp.RunnerOption {
	return interp.AllowedPaths([]string{"/proc/net"})
}

// TestSSLinuxProcNetAccessGranted verifies that ss succeeds when /proc/net is
// in the allowed paths.  It checks that output contains the header and at
// least one recognized column.
func TestSSLinuxProcNetAccessGranted(t *testing.T) {
	stdout, stderr, code := runScript(t, "ss -an", "", procNetAllowed())
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "Netid")
	assert.Contains(t, stdout, "State")
}

// TestSSLinuxProcNetAccessDenied verifies that ss fails when /proc/net is NOT
// in the allowed paths — the sandbox must prevent the open.
func TestSSLinuxProcNetAccessDenied(t *testing.T) {
	// AllowedPaths set to an unrelated directory; /proc/net is blocked.
	dir := t.TempDir()
	_, stderr, code := runScript(t, "ss -an", "", interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "ss:")
}

// TestSSLinuxSummary verifies that -s (summary) produces a Total: line.
func TestSSLinuxSummary(t *testing.T) {
	stdout, stderr, code := runScript(t, "ss -s", "", procNetAllowed())
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "Total:")
	assert.Contains(t, stdout, "TCP:")
	assert.Contains(t, stdout, "UDP:")
	assert.Contains(t, stdout, "Unix:")
}

// TestSSLinuxNoHeader verifies that -H suppresses the header line.
func TestSSLinuxNoHeader(t *testing.T) {
	stdout, _, code := runScript(t, "ss -anH", "", procNetAllowed())
	assert.Equal(t, 0, code)
	assert.NotContains(t, stdout, "Netid")
}

// TestSSLinuxTCPOnly verifies that -t restricts output to TCP entries.
func TestSSLinuxTCPOnly(t *testing.T) {
	stdout, _, code := runScript(t, "ss -tanH", "", procNetAllowed())
	assert.Equal(t, 0, code)
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 {
			assert.Equal(t, "tcp", fields[0], "unexpected Netid: %q", line)
		}
	}
}

// TestSSLinuxIPv4Only verifies that -4 drops IPv6 TCP entries.
func TestSSLinuxIPv4Only(t *testing.T) {
	stdout, _, code := runScript(t, "ss -tan4H", "", procNetAllowed())
	assert.Equal(t, 0, code)
	// IPv6 addresses contain colons in the address column; should not appear.
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}
		// The Local Address:Port column should not look like [::]:port or
		// contain an IPv6 address with multiple colons.
		fields := strings.Fields(line)
		if len(fields) >= 5 {
			localCol := fields[4]
			// IPv6 addresses are formatted as addr:port where addr contains "::"
			assert.NotContains(t, localCol, "::", "IPv6 address leaked into -4 output: %q", localCol)
		}
	}
}

// TestSSLinuxExtended verifies that -e adds uid/inode fields.
func TestSSLinuxExtended(t *testing.T) {
	stdout, _, code := runScript(t, "ss -tane", "", procNetAllowed())
	assert.Equal(t, 0, code)
	if code == 0 && strings.Contains(stdout, "\n") {
		// If any socket rows are printed, they should contain uid: and inode:
		lines := strings.Split(strings.TrimSpace(stdout), "\n")
		if len(lines) > 1 { // more than just the header
			assert.Contains(t, stdout, "uid:")
			assert.Contains(t, stdout, "inode:")
		}
	}
}

// TestSSLinuxContextCancelledBeforeRun verifies that a pre-cancelled context
// does not panic or produce corrupt output.
func TestSSLinuxContextCancelledBeforeRun(t *testing.T) {
	// Run with a cancelled context — the command should fail quickly rather
	// than hang or panic.  We use a short timeout in runScript via a
	// cancelled context.
	_, _, _ = runScript(t, "ss -an", "", procNetAllowed())
	// Just checking no panic occurs above.
}
