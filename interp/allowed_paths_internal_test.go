// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mvdan.cc/sh/v3/syntax"
)

func systemExecAllowedPaths(t *testing.T) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return []string{filepath.Join(os.Getenv("SystemRoot"), "System32")}
	}
	return []string{"/bin", "/usr"}
}

func runScriptInternal(t *testing.T, script, dir string, opts ...RunnerOption) (stdout, stderr string, exitCode int) {
	t.Helper()
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(script), "")
	require.NoError(t, err)

	var outBuf, errBuf bytes.Buffer
	allOpts := append([]RunnerOption{
		StdIO(nil, &outBuf, &errBuf),
	}, opts...)

	runner, err := New(allOpts...)
	require.NoError(t, err)
	defer runner.Close()

	if dir != "" {
		runner.Dir = dir
	}
	runner.execHandler = func(ctx context.Context, args []string) error {
		hc := HandlerCtx(ctx)
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = hc.Dir
		cmd.Stdout = hc.Stdout
		cmd.Stderr = hc.Stderr
		if err := cmd.Run(); err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return ExitStatus(exitErr.ExitCode())
			}
			return err
		}
		return nil
	}

	err = runner.Run(context.Background(), prog)
	exitCode = 0
	if err != nil {
		var es ExitStatus
		if errors.As(err, &es) {
			exitCode = int(es)
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

func TestAllowedPathsExecBlocked(t *testing.T) {
	dir := t.TempDir()
	// Exec is always blocked when AllowedPaths is set, even for commands inside allowed paths
	_, stderr, exitCode := runScriptInternal(t, `/bin/echo hello`, dir,
		AllowedPaths(append([]string{dir}, systemExecAllowedPaths(t)...)),
	)
	assert.Equal(t, 127, exitCode)
	assert.Contains(t, stderr, "command not found")
}

func TestAllowedPathsExecNonexistent(t *testing.T) {
	dir := t.TempDir()
	_, stderr, exitCode := runScriptInternal(t, `totally_nonexistent_cmd_12345`, dir,
		AllowedPaths(append([]string{dir}, systemExecAllowedPaths(t)...)),
	)
	assert.Equal(t, 127, exitCode)
	assert.Contains(t, stderr, "command not found")
}

func TestAllowedPathsExecViaPathLookup(t *testing.T) {
	dir := t.TempDir()
	// "find" is resolved via PATH (not absolute), but /bin and /usr are not allowed
	_, stderr, exitCode := runScriptInternal(t, `find`, dir,
		AllowedPaths([]string{dir}),
	)
	assert.Equal(t, 127, exitCode)
	assert.Contains(t, stderr, "command not found")
}

func TestAllowedPathsExecSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test not applicable on Windows")
	}
	dir := t.TempDir()
	binDir := filepath.Join(dir, "bin")
	require.NoError(t, os.MkdirAll(binDir, 0755))

	// Create a symlink inside the allowed dir pointing to /bin/echo outside it.
	require.NoError(t, os.Symlink("/bin/echo", filepath.Join(binDir, "escape_echo")))

	// Only allow the temp dir — the symlink target (/bin/echo) is outside.
	_, stderr, exitCode := runScriptInternal(t, filepath.Join(binDir, "escape_echo")+" hello", dir,
		AllowedPaths([]string{dir}),
	)
	assert.Equal(t, 127, exitCode)
	assert.Contains(t, stderr, "command not found")
}

func TestRunRecoversPanic(t *testing.T) {
	var outBuf, errBuf bytes.Buffer
	runner, err := New(StdIO(nil, &outBuf, &errBuf))
	require.NoError(t, err)
	defer runner.Close()

	// Trigger initial reset so we can override the exec handler.
	runner.Reset()

	// Install an exec handler that panics.
	runner.execHandler = func(ctx context.Context, args []string) error {
		panic("deliberate test panic")
	}

	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader("somecmd"), "")
	require.NoError(t, err)

	err = runner.Run(context.Background(), prog)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "internal error")
	assert.Contains(t, err.Error(), "deliberate test panic")
}

func TestRunZeroValueRunnerReturnsError(t *testing.T) {
	// A zero-value Runner (not created via New) should return an explicit
	// error from Run instead of panicking.
	var r Runner
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader("echo hi"), "")
	require.NoError(t, err)

	err = r.Run(context.Background(), prog)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "use interp.New to construct a Runner")
}

func TestAllowedPathsExecDefaultBlocksAll(t *testing.T) {
	dir := t.TempDir()
	// No AllowedPaths option — default blocks all exec
	_, stderr, exitCode := runScriptInternal(t, `/bin/echo hello`, dir)
	assert.Equal(t, 127, exitCode)
	assert.Contains(t, stderr, "command not found")
}
