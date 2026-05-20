// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tests_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
)

// redirRun runs a script with the given dir as working directory and allowed path.
func redirRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return redirRunWithOpts(t, script, dir, interp.AllowedPaths([]string{dir}), interpoption.AllowAllCommands().(interp.RunnerOption))
}

// redirRunNoAllowed runs a script with no allowed paths.
func redirRunNoAllowed(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return redirRunWithOpts(t, script, dir, interpoption.AllowAllCommands().(interp.RunnerOption))
}

func redirRunWithOpts(t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	stdout, stderr, err := redirRunErr(t, script, dir, opts...)
	exitCode := 0
	if err != nil {
		var es interp.ExitStatus
		if errors.As(err, &es) {
			exitCode = int(es)
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	return stdout, stderr, exitCode
}

func redirRunErr(t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, error) {
	t.Helper()
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(script), "")
	require.NoError(t, err)

	var outBuf, errBuf bytes.Buffer
	allOpts := append([]interp.RunnerOption{interp.StdIO(nil, &outBuf, &errBuf)}, opts...)

	runner, err := interp.New(allOpts...)
	require.NoError(t, err)
	defer runner.Close()

	if dir != "" {
		runner.Dir = dir
	}

	err = runner.Run(context.Background(), prog)
	return outBuf.String(), errBuf.String(), err
}

// --- Stdout redirect to /dev/null ---

func TestRedirStdoutToDevNull(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRun(t, "echo hello >/dev/null", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
}

func TestRedirStdoutToDevNullWithSpace(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRun(t, "echo hello > /dev/null", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
}

func TestRedirExplicitFd1ToDevNull(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRun(t, "echo hello 1>/dev/null", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
}

// --- Stderr redirect to /dev/null ---

func TestRedirStderrToDevNull(t *testing.T) {
	dir := t.TempDir()
	// cat on a nonexistent file produces stderr; 2>/dev/null suppresses it
	stdout, stderr, code := redirRun(t, "cat nonexistent 2>/dev/null", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
}

func TestRedirStderrToDevNullWithSpace(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRun(t, "cat nonexistent 2> /dev/null", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
}

// --- Both stdout+stderr redirect (&>) ---

func TestRedirBothToDevNull(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRun(t, "echo hello &>/dev/null", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
}

// --- Append redirect (>>) ---

func TestRedirAppendToDevNull(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRun(t, "echo hello >>/dev/null", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
}

func TestRedirAppendBothToDevNull(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRun(t, "echo hello &>>/dev/null", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
}

// --- Fd duplication ---

func TestRedirDupStderrToStdout(t *testing.T) {
	dir := t.TempDir()
	// >/dev/null 2>&1: first redirect stdout to /dev/null, then dup stderr to stdout
	// Both stdout and stderr go to /dev/null
	stdout, stderr, code := redirRun(t, "cat nonexistent >/dev/null 2>&1", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
}

func TestRedirDupStderrToStdoutFileRejected(t *testing.T) {
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "output.txt"), []byte("keep\n"), 0644))
	stdout, stderr, code := redirRun(t, "cat nonexistent > output.txt 2>&1", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "stderr file redirection via fd duplication is not supported")
	data, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	require.NoError(t, err)
	assert.Equal(t, "keep\n", string(data))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "append.txt"), []byte("keep\n"), 0644))
	stdout, stderr, code = redirRun(t, "cat nonexistent >> append.txt 2>&1", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "stderr file redirection via fd duplication is not supported")
	data, err = os.ReadFile(filepath.Join(dir, "append.txt"))
	require.NoError(t, err)
	assert.Equal(t, "keep\n", string(data))

	stdout, stderr, code = redirRun(t, "cat nonexistent > created.txt 2>&1", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "stderr file redirection via fd duplication is not supported")
	_, err = os.Stat(filepath.Join(dir, "created.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRedirDupStderrToStdoutFileRejectedBeforeCommandExpansion(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, code := redirRun(t, `echo "$(echo side | write_file side.txt)" > output.txt 2>&1`, dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "stderr file redirection via fd duplication is not supported")
	_, err := os.Stat(filepath.Join(dir, "output.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(dir, "side.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRedirDupStderrToDynamicStdoutFileRejectedBeforeCommandExpansion(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, code := redirRun(t, `TARGET=output.txt; echo "$(echo side | write_file side.txt)" > "$TARGET" 2>&1`, dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "stderr file redirection via fd duplication is not supported")
	_, err := os.Stat(filepath.Join(dir, "output.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(dir, "side.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRedirDupStderrToDynamicStdoutFileBlockedCommandRejectedBeforeRedirectExpansion(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, code := redirRunWithOpts(t,
		`echo x > "$(printf output.txt; printf side | write_file side.txt >/dev/null)" 2>&1`,
		dir,
		interp.AllowedPaths([]string{dir}),
		interp.AllowedCommands([]string{"rshell:printf", "rshell:write_file"}),
	)
	assert.Equal(t, 127, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "rshell: echo: command not allowed")
	_, err := os.Stat(filepath.Join(dir, "output.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = os.Stat(filepath.Join(dir, "side.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestRedirDupStderrToDynamicDevNull(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, code := redirRun(t, `TARGET=/dev/null; cat missing > "$TARGET" 2>&1`, dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
}

func TestRedirDupStderrToOriginalStdoutBeforeFileRedirect(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, code := redirRun(t, "cat nonexistent 2>&1 > output.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, "nonexistent")
	assert.Equal(t, "", stderr)
	data, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	require.NoError(t, err)
	assert.Equal(t, "", string(data))
}

func TestRedirDupStdoutToStderr(t *testing.T) {
	dir := t.TempDir()
	// >&2 redirects stdout to stderr
	stdout, stderr, code := redirRun(t, "echo hello >&2", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "hello\n", stderr)
}

// --- Exit code preservation ---

func TestRedirDevNullPreservesExitCode(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := redirRun(t, "true >/dev/null; echo $?", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "0\n", stdout)
}

func TestRedirDevNullPreservesFailureExitCode(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := redirRun(t, "false >/dev/null; echo $?", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout)
}

// --- File output redirects ---

func TestRedirStdoutToFileCreates(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRun(t, "echo hello > output.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	data, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(data))
}

func TestRedirStdoutToFileOverwrites(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "output.txt"), []byte("old\n"), 0644))

	stdout, stderr, code := redirRun(t, "echo new > output.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	data, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(data))
}

func TestRedirAppendToFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "output.txt"), []byte("old\n"), 0644))

	stdout, stderr, code := redirRun(t, "echo new >> output.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	data, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	require.NoError(t, err)
	assert.Equal(t, "old\nnew\n", string(data))
}

func TestRedirStdoutToFileRespectsOutputCap(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("A", 1<<20)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mb.txt"), []byte(content), 0644))

	script := "cat" + strings.Repeat(" mb.txt", 11) + " > output.txt"
	stdout, stderr, err := redirRunErr(t, script, dir,
		interp.AllowedPaths([]string{dir}),
		interpoption.AllowAllCommands().(interp.RunnerOption))
	assert.ErrorIs(t, err, interp.ErrOutputLimitExceeded)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)

	info, statErr := os.Stat(filepath.Join(dir, "output.txt"))
	require.NoError(t, statErr)
	assert.Equal(t, int64(10*1024*1024), info.Size())
}

func TestRedirStdoutToFilesShareOutputCap(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("A", 1<<20)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mb.txt"), []byte(content), 0644))

	sixMiB := "cat" + strings.Repeat(" mb.txt", 6)
	script := sixMiB + " > one.txt\n" + sixMiB + " > two.txt"
	stdout, stderr, err := redirRunErr(t, script, dir,
		interp.AllowedPaths([]string{dir}),
		interpoption.AllowAllCommands().(interp.RunnerOption))
	assert.ErrorIs(t, err, interp.ErrOutputLimitExceeded)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)

	one, statErr := os.Stat(filepath.Join(dir, "one.txt"))
	require.NoError(t, statErr)
	assert.Equal(t, int64(6*1024*1024), one.Size())
	two, statErr := os.Stat(filepath.Join(dir, "two.txt"))
	require.NoError(t, statErr)
	assert.Equal(t, int64(4*1024*1024), two.Size())
}

func TestRedirTargetCommandSubstitutionRunsOnce(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, code := redirRun(t, `echo data > "$(echo hit >> marker; echo output.txt)"`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)

	marker, err := os.ReadFile(filepath.Join(dir, "marker"))
	require.NoError(t, err)
	assert.Equal(t, "hit\n", string(marker))
	output, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	require.NoError(t, err)
	assert.Equal(t, "data\n", string(output))
}

func TestRedirDeniedCommandDoesNotCreateOrModifyFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "output.txt")
	require.NoError(t, os.WriteFile(target, []byte("keep\n"), 0644))

	stdout, stderr, code := redirRunWithOpts(t, "nope > created.txt", dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 127, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "rshell: nope: command not allowed")
	_, err := os.Stat(filepath.Join(dir, "created.txt"))
	assert.True(t, os.IsNotExist(err), "denied command created redirected file")

	stdout, stderr, code = redirRunWithOpts(t, "echo new > output.txt", dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 127, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "rshell: echo: command not allowed")
	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "keep\n", string(data))

	stdout, stderr, code = redirRunWithOpts(t, "cmd=echo; $cmd new >> output.txt", dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 127, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "rshell: echo: command not allowed")
	data, err = os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "keep\n", string(data))
}

func TestRedirCommandlessFileOutputBlockedBeforeOpen(t *testing.T) {
	tests := []struct {
		name   string
		script string
	}{
		{"bare_redirect", "> output.txt"},
		{"assignment_redirect", "VAR=x > output.txt"},
		{"expanded_empty_command", "cmd=; $cmd > output.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "output.txt")
			require.NoError(t, os.WriteFile(target, []byte("keep\n"), 0644))

			stdout, stderr, code := redirRunWithOpts(t, tt.script, dir, interp.AllowedPaths([]string{dir}))
			assert.Equal(t, 2, code)
			assert.Equal(t, "", stdout)
			assert.Contains(t, stderr, "stdout file redirection without a command is not supported")
			data, err := os.ReadFile(target)
			require.NoError(t, err)
			assert.Equal(t, "keep\n", string(data))
		})
	}

	dir := t.TempDir()
	stdout, stderr, code := redirRunWithOpts(t, "> created.txt", dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 2, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "stdout file redirection without a command is not supported")
	_, err := os.Stat(filepath.Join(dir, "created.txt"))
	assert.True(t, os.IsNotExist(err), "commandless redirect created redirected file")
}

func TestRedirCompoundCommandFileOutputBlockedBeforeOpen(t *testing.T) {
	tests := []struct {
		name   string
		script string
	}{
		{"brace_group", "{ nope; } > output.txt"},
		{"subshell", "(nope) > output.txt"},
		{"while_loop", "while false; do echo new; done > output.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "output.txt")
			require.NoError(t, os.WriteFile(target, []byte("keep\n"), 0644))

			stdout, stderr, code := redirRunWithOpts(t, tt.script, dir, interp.AllowedPaths([]string{dir}))
			assert.Equal(t, 2, code)
			assert.Equal(t, "", stdout)
			assert.Contains(t, stderr, "stdout file redirection on compound commands is not supported")
			data, err := os.ReadFile(target)
			require.NoError(t, err)
			assert.Equal(t, "keep\n", string(data))
		})
	}
}

func TestRedirStdoutToFileRejectsTrailingSeparator(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "output.txt")
	require.NoError(t, os.WriteFile(target, []byte("keep\n"), 0644))

	stdout, stderr, code := redirRun(t, "echo new > output.txt/", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "not a directory")

	stdout, stderr, code = redirRun(t, "echo new >> output.txt/", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "not a directory")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "keep\n", string(data))
}

func TestRedirStdoutToFileRejectsSymlinkTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior is platform-specific on Windows")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	require.NoError(t, os.WriteFile(target, []byte("keep\n"), 0644))
	require.NoError(t, os.Symlink("target.txt", filepath.Join(dir, "link.txt")))

	stdout, stderr, code := redirRun(t, "echo new > link.txt", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "permission denied")

	stdout, stderr, code = redirRun(t, "echo new >> link.txt", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "permission denied")

	data, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "keep\n", string(data))
}

func TestRedirStdoutToFileRejectsFIFOWithoutBlocking(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFOs are Unix-specific")
	}
	dir := t.TempDir()
	require.NoError(t, mkfifo(filepath.Join(dir, "pipe"), 0644))

	type result struct {
		stdout string
		stderr string
		code   int
	}
	done := make(chan result, 1)
	go func() {
		stdout, stderr, code := redirRun(t, "echo new > pipe", dir)
		done <- result{stdout: stdout, stderr: stderr, code: code}
	}()

	select {
	case res := <-done:
		assert.Equal(t, 1, res.code)
		assert.Equal(t, "", res.stdout)
		assert.Contains(t, res.stderr, "permission denied")
	case <-time.After(2 * time.Second):
		t.Fatal("stdout redirect blocked on FIFO")
	}
}

func TestRedirExplicitFd1ToFile(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRun(t, "echo hello 1>output.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	data, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(data))
}

func TestRedirVariableTargetAllowedWhenPathAllowed(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRun(t, "TARGET=output.txt; echo hello > $TARGET", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	data, err := os.ReadFile(filepath.Join(dir, "output.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(data))
}

func TestRedirToFileWithoutAllowedPathsBlocked(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRunNoAllowed(t, "echo hello > /tmp/output.txt", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "permission denied")
}

func TestRedirStderrToFileStillBlocked(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRunNoAllowed(t, "echo hello 2> /tmp/errors.txt", dir)
	assert.Equal(t, 2, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "2> output fd redirection is not supported")
}

func TestRedirAppendToFileWithoutAllowedPathsBlocked(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRunNoAllowed(t, "echo hello >> /tmp/output.txt", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "permission denied")
}

func TestRedirAllToFileStillBlocked(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRunNoAllowed(t, "echo hello &> /tmp/output.txt", dir)
	assert.Equal(t, 2, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "file redirection is not supported")
}

// --- Path traversal via /dev/null ---

func TestRedirDevNullPathTraversalBlocked(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRunNoAllowed(t, "echo hello > /dev/null/../../../tmp/evil", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "permission denied")
}

func TestRedirDevNullExtraSlashBlocked(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRunNoAllowed(t, "echo hello > /dev//null", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "permission denied")
}

// --- Unsupported fd numbers ---

func TestRedirFd3Blocked(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRunNoAllowed(t, "echo hello 3>/dev/null", dir)
	assert.Equal(t, 2, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "3> output fd redirection is not supported")
}

func TestRedirDupFd3Blocked(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRunNoAllowed(t, "echo hello 3>&1", dir)
	assert.Equal(t, 2, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "fd duplication is not supported")
}

// --- Combination with pipes ---

func TestRedirDevNullWithPipe(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("hello world\nfoo bar\n"), 0644)
	require.NoError(t, err)

	stdout, stderr, code := redirRun(t, "cat data.txt 2>/dev/null | grep hello", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\n", stdout)
	assert.Equal(t, "", stderr)
}

// --- Multiple redirects on same command ---

func TestRedirMultipleDevNull(t *testing.T) {
	dir := t.TempDir()
	// Redirect both stdout and stderr separately to /dev/null
	stdout, stderr, code := redirRun(t, "echo hello >/dev/null 2>/dev/null", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
}
