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
	return newHostExecRunnerWithEnv(t, hostEntries, stdin, nil)
}

// newHostExecRunnerWithEnv is like newHostExecRunner but also seeds the
// runner environment via interp.Env. The host-exec env-filtering tests
// must provide env this way (NOT via t.Setenv) because the
// implementation reads from r.writeEnv, not the ambient Go process env.
func newHostExecRunnerWithEnv(t *testing.T, hostEntries []string, stdin io.Reader, envPairs []string) (*interp.Runner, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	if stdin == nil {
		stdin = strings.NewReader("")
	}
	opts := []interp.RunnerOption{
		interp.AllowedCommands(hostEntries),
		interp.StdIO(stdin, stdout, stderr),
	}
	if envPairs != nil {
		opts = append(opts, interp.Env(envPairs...))
	}
	r, err := interp.New(opts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	return r, stdout, stderr
}

// Tests below intentionally use command names that do NOT collide with
// rshell builtins (echo/cat/false/true are all registered builtins). The
// !isKnown gate in call() means a host: entry is honored only when the
// name does not resolve to a builtin, so binding host:echo=/bin/echo and
// then running `echo` would dispatch the rshell echo builtin and never
// touch the host path under test.

func TestHostExecStdoutPlumbed(t *testing.T) {
	r, stdout, _ := newHostExecRunner(t, []string{"host:hostecho=/bin/echo"}, nil)
	err := r.Run(context.Background(), parseHostExecScript(t, `hostecho hello world`))
	require.NoError(t, err)
	assert.Equal(t, "hello world\n", stdout.String())
}

func TestHostExecExitCodePropagated(t *testing.T) {
	r, _, _ := newHostExecRunner(t, []string{"host:hostfalse=/usr/bin/false"}, nil)
	code := runExitCode(t, r, parseHostExecScript(t, `hostfalse`))
	assert.Equal(t, uint8(1), code)
}

func TestHostExecStdinPlumbed(t *testing.T) {
	stdin := strings.NewReader("piped-input\n")
	r, stdout, _ := newHostExecRunner(t, []string{"host:hostcat=/bin/cat"}, stdin)
	err := r.Run(context.Background(), parseHostExecScript(t, `hostcat`))
	require.NoError(t, err)
	assert.Equal(t, "piped-input\n", stdout.String())
}

func TestHostExecRejectsNonAllowlistedName(t *testing.T) {
	r, _, stderr := newHostExecRunner(t, []string{"host:hostecho=/bin/echo"}, nil)
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
	case err := <-done:
		// The cancellation must propagate as context.Canceled, not as a
		// numeric exit status. Otherwise the CLI's timeout/cancel path
		// would be skipped and run telemetry would be misclassified.
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("host process was not killed within 2s of context cancel")
	}
}

// TestHostExecDeadlineSurfacesAsDeadlineExceeded verifies that a parent
// context deadline kills the host binary AND surfaces as
// context.DeadlineExceeded back through Run(), so that the CLI's
// --timeout path ("execution timed out after Xms" / exit 124) and
// run-span outcome classification both work for host-binary invocations
// the same way they do for builtins.
func TestHostExecDeadlineSurfacesAsDeadlineExceeded(t *testing.T) {
	r, _, _ := newHostExecRunner(t, []string{"host:sleep=/bin/sleep"}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := r.Run(ctx, parseHostExecScript(t, `sleep 30`))
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.DeadlineExceeded,
		"host-exec context deadline must surface as context.DeadlineExceeded, "+
			"not ExitStatus(130). Got %v", err)
	assert.Less(t, elapsed, 2*time.Second,
		"deadline should fire within ~100ms, but Run() took %s", elapsed)
}

func TestHostExecEnvFilteredToAllowlist(t *testing.T) {
	// Env values come from the runner's environment overlay (interp.Env),
	// not the ambient Go process env. Use t.Setenv to plant a sentinel in
	// the process env that must NOT leak through, and provide a
	// runner-scoped PATH that the host binary should see.
	t.Setenv("RSHELL_HOST_EXEC_LEAK_CHECK", "should-not-appear")

	r, stdout, _ := newHostExecRunnerWithEnv(t,
		[]string{"host:env=/usr/bin/env"}, nil,
		[]string{"PATH=/usr/bin:/bin"},
	)
	err := r.Run(context.Background(), parseHostExecScript(t, `env`))
	require.NoError(t, err)
	out := stdout.String()
	assert.Contains(t, out, "PATH=/usr/bin:/bin", "PATH must be forwarded from runner env")
	assert.NotContains(t, out, "RSHELL_HOST_EXEC_LEAK_CHECK",
		"ambient Go process env must NOT leak — host binaries see only the runner env")
}

func TestHostExecForwardsHomeAndLang(t *testing.T) {
	r, stdout, _ := newHostExecRunnerWithEnv(t,
		[]string{"host:env=/usr/bin/env"}, nil,
		[]string{"HOME=/tmp/host-exec-home", "LANG=C.UTF-8"},
	)
	err := r.Run(context.Background(), parseHostExecScript(t, `env`))
	require.NoError(t, err)
	out := stdout.String()
	assert.Contains(t, out, "HOME=/tmp/host-exec-home")
	assert.Contains(t, out, "LANG=C.UTF-8")
}

// TestHostExecDefaultEnvIsEmpty verifies the runner's "no host env
// inherited by default" guarantee: with no interp.Env option, even
// allowlisted names (PATH/HOME/LANG) must not be forwarded to the host
// binary, regardless of the ambient Go process env. Regression for the
// previous os.LookupEnv-based filter that ignored runner env.
func TestHostExecDefaultEnvIsEmpty(t *testing.T) {
	// Plant ambient env that must be ignored.
	t.Setenv("PATH", "/should/not/leak")
	t.Setenv("HOME", "/should/not/leak/home")
	t.Setenv("LANG", "should-not-leak")

	r, stdout, _ := newHostExecRunner(t, []string{"host:env=/usr/bin/env"}, nil)
	err := r.Run(context.Background(), parseHostExecScript(t, `env`))
	require.NoError(t, err)
	out := stdout.String()
	assert.NotContains(t, out, "/should/not/leak",
		"runner default env is empty; host binary must not see ambient PATH/HOME/LANG")
}

// TestHostExecInlineAssignmentTakesEffect verifies that inline command
// assignments (PATH=/safe hostcmd) are visible to host binaries. They
// flow through r.writeEnv before call() dispatches, which is exactly
// where filterHostEnv now reads from.
func TestHostExecInlineAssignmentTakesEffect(t *testing.T) {
	r, stdout, _ := newHostExecRunnerWithEnv(t,
		[]string{"host:env=/usr/bin/env"}, nil,
		[]string{"PATH=/initial"},
	)
	err := r.Run(context.Background(), parseHostExecScript(t, `PATH=/inline-override env`))
	require.NoError(t, err)
	out := stdout.String()
	assert.Contains(t, out, "PATH=/inline-override",
		"inline assignment must override the runner env for that command")
	assert.NotContains(t, out, "PATH=/initial",
		"the original PATH must not also appear")
}

// TestHostExecBuiltinTakesPrecedence verifies that when a name is both an
// allowlisted builtin (rshell:echo) AND a configured host: entry, the
// builtin runs and the host binary is never exec'd. The host: entry
// points at /usr/bin/false on purpose: if the host path were
// erroneously preferred, the script would exit 1; because the builtin
// wins it exits 0 with the expected stdout.
func TestHostExecBuiltinTakesPrecedence(t *testing.T) {
	r, stdout, _ := newHostExecRunner(t,
		[]string{"rshell:echo", "host:echo=/usr/bin/false"}, nil)
	err := r.Run(context.Background(), parseHostExecScript(t, `echo from-builtin`))
	require.NoError(t, err, "builtin echo must win — non-zero exit means /usr/bin/false ran")
	assert.Equal(t, "from-builtin\n", stdout.String())
}

// Sanity: make sure t.Setenv actually plants the var in the ambient Go
// process env. Used as a baseline for tests that assert host binaries
// do NOT see ambient process env (so we can be sure the negative
// assertion isn't trivially satisfied by t.Setenv being a no-op).
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
