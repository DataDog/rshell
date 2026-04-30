// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tests_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// File-target output redirects (>, >>, 2>, &>, &>>) write through the
// AllowedPaths sandbox. These tests verify behaviour that scenario tests
// cannot express directly: that the file handle is closed before the next
// command runs (so reads from disk see the written bytes), that file mode
// is 0644, and that the sandbox boundary is enforced when the open fails.

func TestRedirTruncateWritesAndCloses(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := redirRun(t, "echo hi > out.txt", dir)
	require.Equal(t, 0, code)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)

	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hi\n", string(got))
}

func TestRedirAppendPreservesExisting(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "out.txt"), []byte("first\n"), 0644))
	_, _, code := redirRun(t, "echo second >> out.txt", dir)
	require.Equal(t, 0, code)

	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	require.NoError(t, err)
	assert.Equal(t, "first\nsecond\n", string(got))
}

func TestRedirTruncateOverwritesExistingShorter(t *testing.T) {
	dir := t.TempDir()
	original := "this string is intentionally longer than the new contents"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "out.txt"), []byte(original), 0644))
	_, _, code := redirRun(t, "echo new > out.txt", dir)
	require.Equal(t, 0, code)

	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	require.NoError(t, err)
	assert.Equal(t, "new\n", string(got))
}

func TestRedirCreatesFileWithMode0644(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file mode bits are not honoured on Windows")
	}
	dir := t.TempDir()
	_, _, code := redirRun(t, "echo hi > out.txt", dir)
	require.Equal(t, 0, code)

	info, err := os.Stat(filepath.Join(dir, "out.txt"))
	require.NoError(t, err)
	// Permission bits depend on the test process umask. The interpreter
	// passes 0644 to os.OpenFile; assert no executable or extra bits are
	// set, which is the practical guarantee callers depend on.
	mode := info.Mode().Perm()
	assert.Zero(t, mode&0111, "file should not be executable, got %o", mode)
	assert.NotZero(t, mode&0400, "owner should have read access, got %o", mode)
	assert.NotZero(t, mode&0200, "owner should have write access, got %o", mode)
}

func TestRedirHeredocAndFileTargetCombine(t *testing.T) {
	dir := t.TempDir()
	script := "cat <<EOF > out.txt\nfoo\nbar\nEOF"
	_, _, code := redirRun(t, script, dir)
	require.Equal(t, 0, code)

	got, err := os.ReadFile(filepath.Join(dir, "out.txt"))
	require.NoError(t, err)
	assert.Equal(t, "foo\nbar\n", string(got))
}

func TestRedirStderrToFileSeparatesStreams(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := redirRun(t, "cat missing 2> err.log; echo ok", dir)
	assert.Contains(t, stdout, "ok")

	got, err := os.ReadFile(filepath.Join(dir, "err.log"))
	require.NoError(t, err)
	// rshell's cat normalises errno messages via PortableErrMsg.
	assert.Contains(t, string(got), "no such file or directory")
	assert.Equal(t, 0, code, "trailing echo overrides cat's exit status")
}

func TestRedirBothStreamsToSameFile(t *testing.T) {
	dir := t.TempDir()
	_, _, code := redirRun(t, "echo only-stdout &> combined.log", dir)
	require.Equal(t, 0, code)

	got, err := os.ReadFile(filepath.Join(dir, "combined.log"))
	require.NoError(t, err)
	assert.Equal(t, "only-stdout\n", string(got))
}

func TestRedirOutsideAllowedPathsDoesNotCreateFile(t *testing.T) {
	allowed := t.TempDir()
	other := t.TempDir()
	target := filepath.Join(other, "evil.txt")

	stdout, stderr, code := redirRun(t, "echo hi > "+target, allowed)
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "permission denied")

	_, err := os.Stat(target)
	assert.True(t, os.IsNotExist(err), "redirect must not create files outside AllowedPaths, got err=%v", err)
}

func TestRedirFailureDoesNotRunCommand(t *testing.T) {
	allowed := t.TempDir()
	// The first redirect succeeds (creates ok.txt), but the second points
	// outside the sandbox. The command should not run, and ok.txt should
	// remain empty (created and closed) since the second redirect aborts
	// before echo executes.
	other := t.TempDir()
	stderr := other + "/blocked.log"
	stdout, errOut, code := redirRun(t, "echo hi > ok.txt 2> "+stderr, allowed)
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, errOut, "permission denied")

	got, err := os.ReadFile(filepath.Join(allowed, "ok.txt"))
	require.NoError(t, err)
	assert.Empty(t, string(got), "echo should not have written to ok.txt")
}

func TestRedirSandboxBlockedNoFileCreated(t *testing.T) {
	dir := t.TempDir()
	// No allowed paths configured — every write must fail.
	stdout, stderr, code := redirRunNoAllowed(t, "echo evil > evil.txt", dir)
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "permission denied")

	_, err := os.Stat(filepath.Join(dir, "evil.txt"))
	assert.True(t, os.IsNotExist(err), "no file should have been created, got err=%v", err)
}
