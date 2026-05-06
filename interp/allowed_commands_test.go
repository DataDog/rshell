// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
)

func TestAllowedCommandsNamespaceRequired(t *testing.T) {
	_, err := interp.New(interp.AllowedCommands([]string{"echo"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing namespace prefix")
}

func TestAllowedCommandsUnknownNamespace(t *testing.T) {
	_, err := interp.New(interp.AllowedCommands([]string{"bogus:echo"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown namespace")
}

func TestAllowedCommandsEmptyCommandName(t *testing.T) {
	_, err := interp.New(interp.AllowedCommands([]string{"rshell:"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty command name")
}

func TestAllowedCommandsValidPrefix(t *testing.T) {
	_, err := interp.New(interp.AllowedCommands([]string{"rshell:echo"}))
	require.NoError(t, err)
}

func TestAllowedCommandsRejectsWhitespace(t *testing.T) {
	// Whitespace in entries is not trimmed (by design per specs).
	_, err := interp.New(
		interp.AllowedCommands([]string{" rshell:echo "}),
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown namespace")
}

func TestAllowedCommandsMultipleColons(t *testing.T) {
	_, err := interp.New(interp.AllowedCommands([]string{"rshell:foo:bar"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multiple colons")
}

func TestAllowedCommandsDuplicateEntries(t *testing.T) {
	_, err := interp.New(interp.AllowedCommands([]string{"rshell:echo", "rshell:echo"}))
	require.NoError(t, err, "duplicate entries should be accepted (idempotent)")
}

func TestAllowedCommandsEmpty(t *testing.T) {
	_, err := interp.New(interp.AllowedCommands([]string{""}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty command name")
}

// TestHostEntryDoesNotAuthorizeBuiltin is a regression test: a host: entry
// whose name collides with a builtin must NOT silently authorize the
// builtin. Without the !isKnown gate in call(), an entry like
// "host:cat=<path>" would flip isAllowed=true and the builtin cat would
// run with stdin/AllowedPaths access, never executing the host path.
// Cross-platform: this exercises the dispatch gate, not the actual host
// exec, so it runs on darwin/windows too. The host path uses
// t.TempDir() rather than a hardcoded /bin/... so AllowedCommands'
// filepath.IsAbs check passes on every OS (Windows requires drive
// letter or UNC).
func TestHostEntryDoesNotAuthorizeBuiltin(t *testing.T) {
	hostPath := filepath.Join(t.TempDir(), "fake-binary")
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	r, err := interp.New(
		interp.AllowedCommands([]string{"host:cat=" + hostPath}),
		interp.StdIO(strings.NewReader(""), stdout, stderr),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	prog, err := syntax.NewParser().Parse(strings.NewReader("cat"), "")
	require.NoError(t, err)

	runErr := r.Run(context.Background(), prog)
	var status interp.ExitStatus
	require.True(t, errors.As(runErr, &status), "expected ExitStatus error, got %v", runErr)
	assert.Equal(t, interp.ExitStatus(127), status)
	assert.Contains(t, stderr.String(), "command not allowed")
	assert.Empty(t, stdout.String(), "cat builtin must not have run")
}

// TestHostEntryAuthorizesNonBuiltin verifies the positive case for the
// dispatch gate — a host: entry whose name does NOT collide with a
// builtin still passes the allowlist check (it would fail with
// "command not allowed" if the gate were too strict). The actual exec
// path is platform-specific and tested in host_exec_test.go (linux);
// here we only assert that dispatch reaches it (on darwin/windows the
// host-exec stub returns 127 with a different message). t.TempDir()
// produces an absolute path on every OS so AllowedCommands accepts it
// on Windows too.
func TestHostEntryAuthorizesNonBuiltin(t *testing.T) {
	hostPath := filepath.Join(t.TempDir(), "fake-binary")
	stderr := &bytes.Buffer{}
	r, err := interp.New(
		interp.AllowedCommands([]string{"host:somenonsensename=" + hostPath}),
		interp.StdIO(strings.NewReader(""), &bytes.Buffer{}, stderr),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	prog, err := syntax.NewParser().Parse(strings.NewReader("somenonsensename"), "")
	require.NoError(t, err)

	_ = r.Run(context.Background(), prog)
	// Whatever exit code we got, the rejection path ("command not
	// allowed") must NOT have been taken — that's the only thing the
	// dispatch gate is responsible for here.
	assert.NotContains(t, stderr.String(), "command not allowed")
}
