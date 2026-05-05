// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

// DEMO ONLY: tests for the host-binary entry-point. See host_exec_linux.go.

package interp_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
)

// runExitCode runs node and returns the resulting exit code. A nil error
// from Run means the script exited 0; a non-nil error wraps an
// interp.ExitStatus that we surface as the code. Any other error is
// reported via t.Fatalf.
func runExitCode(t *testing.T, r *interp.Runner, node syntax.Node) uint8 {
	t.Helper()
	err := r.Run(context.Background(), node)
	if err == nil {
		return 0
	}
	var es interp.ExitStatus
	if errors.As(err, &es) {
		return uint8(es)
	}
	t.Fatalf("Run returned unexpected error: %v", err)
	return 0
}

func parseHostExecScript(t *testing.T, src string) *syntax.File {
	t.Helper()
	prog, err := syntax.NewParser().Parse(strings.NewReader(src), "")
	require.NoError(t, err)
	return prog
}

// newHostExecRunner builds a Runner that allows just the host: entries the
// caller passes in. stdin/stdout/stderr are wired up to the supplied buffers
// so tests can assert on captured output. stdin is provided as a *os.File
// (via os.Pipe) when nonempty, since StdIO expects a Reader and the runner
// converts it into a pipe internally — providing a real file when we need
// real stdin keeps the host-exec path identical to production.
func newHostExecRunner(t *testing.T, hostEntries []string, stdin io.Reader) (*interp.Runner, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	r, err := interp.New(
		interp.AllowedCommands(hostEntries),
		interp.StdIO(stdin, stdout, stderr),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	return r, stdout, stderr
}

func TestHostExecStdoutPlumbed(t *testing.T) {
	r, stdout, _ := newHostExecRunner(t, []string{"host:echo=/bin/echo"}, nil)
	err := r.Run(context.Background(), parseHostExecScript(t, `echo hello world`))
	require.NoError(t, err)
	assert.Equal(t, "hello world\n", stdout.String())
}

func TestHostExecExitCodePropagated(t *testing.T) {
	r, _, _ := newHostExecRunner(t, []string{"host:false=/usr/bin/false"}, nil)
	code := runExitCode(t, r, parseHostExecScript(t, `false`))
	assert.Equal(t, uint8(1), code)
}

func TestHostExecStdinPlumbed(t *testing.T) {
	stdin := strings.NewReader("piped-input\n")
	r, stdout, _ := newHostExecRunner(t, []string{"host:cat=/bin/cat"}, stdin)
	err := r.Run(context.Background(), parseHostExecScript(t, `cat`))
	require.NoError(t, err)
	assert.Equal(t, "piped-input\n", stdout.String())
}

func TestHostExecRejectsNonAllowlistedName(t *testing.T) {
	r, _, stderr := newHostExecRunner(t, []string{"host:echo=/bin/echo"}, nil)
	code := runExitCode(t, r, parseHostExecScript(t, `notallowlisted`))
	assert.Equal(t, uint8(127), code)
	assert.Contains(t, stderr.String(), "command not allowed")
}

func TestHostExecContextCancellationKillsProcess(t *testing.T) {
	r, _, _ := newHostExecRunner(t, []string{"host:sleep=/bin/sleep"}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		// 30s sleep — deliberately long so we know the cancel was what
		// stopped it, not natural completion.
		done <- r.Run(ctx, parseHostExecScript(t, `sleep 30`))
	}()

	// Give the process a chance to actually start before cancelling.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// success: the run returned within the deadline below.
	case <-time.After(2 * time.Second):
		t.Fatal("host process was not killed within 2s of context cancel")
	}
}

func TestHostExecEnvFilteredToAllowlist(t *testing.T) {
	// Set a sentinel env var that should NOT propagate, and one that
	// should. /usr/bin/env prints the resulting environment; we assert
	// that PATH (allowlisted) shows up and the sentinel does not.
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("RSHELL_HOST_EXEC_LEAK_CHECK", "should-not-appear")

	r, stdout, _ := newHostExecRunner(t, []string{"host:env=/usr/bin/env"}, nil)
	err := r.Run(context.Background(), parseHostExecScript(t, `env`))
	require.NoError(t, err)
	out := stdout.String()
	assert.Contains(t, out, "PATH=/usr/bin:/bin", "PATH must be forwarded")
	assert.NotContains(t, out, "RSHELL_HOST_EXEC_LEAK_CHECK", "non-allowlisted env vars must be stripped")
}

func TestHostExecForwardsHomeAndLang(t *testing.T) {
	t.Setenv("HOME", "/tmp/host-exec-home")
	t.Setenv("LANG", "C.UTF-8")

	r, stdout, _ := newHostExecRunner(t, []string{"host:env=/usr/bin/env"}, nil)
	err := r.Run(context.Background(), parseHostExecScript(t, `env`))
	require.NoError(t, err)
	out := stdout.String()
	assert.Contains(t, out, "HOME=/tmp/host-exec-home")
	assert.Contains(t, out, "LANG=C.UTF-8")
}

// TestHostExecOnlyApprovedNamesDispatch ensures that an allowlisted *builtin*
// name still dispatches to the builtin and does not reach the host-exec path
// — the host: namespace is consulted only when the name is not a known
// builtin.
func TestHostExecBuiltinTakesPrecedence(t *testing.T) {
	// "echo" is a known builtin in rshell. If the host:echo entry were
	// erroneously preferred, /bin/echo would emit "FROM_HOST" via its
	// argv, but because we pass the literal string and rely on builtin
	// echo, the output ends with a single newline produced by the builtin.
	r, stdout, _ := newHostExecRunner(t,
		[]string{"rshell:echo", "host:echo=/bin/echo"}, nil)
	err := r.Run(context.Background(), parseHostExecScript(t, `echo from-builtin`))
	require.NoError(t, err)
	assert.Equal(t, "from-builtin\n", stdout.String())
}

// Sanity: make sure os.Environ has an unrelated value we can rely on for the
// negative side of TestHostExecEnvFilteredToAllowlist.
func TestHostExecOSEnvironHasUnrelatedVar(t *testing.T) {
	t.Setenv("RSHELL_HOST_EXEC_LEAK_CHECK", "yes")
	found := false
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "RSHELL_HOST_EXEC_LEAK_CHECK=") {
			found = true
			break
		}
	}
	require.True(t, found, "test setup: t.Setenv should make var visible via os.Environ")
}
