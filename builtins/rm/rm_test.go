// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package rm_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// cmdRun runs a script in remediation mode with dir as the AllowedPaths root.
func cmdRun(t *testing.T, script, dir string) (stdout, stderr string, code int) {
	t.Helper()
	return runScript(t, script, dir,
		interp.AllowedPaths([]string{dir}),
		interp.WithMode(interp.ModeRemediation),
	)
}

// setup creates a temp dir with the given files (path → content) and returns
// the dir path.
func setup(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
		require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	}
	return dir
}

// ============================================================================
// Basic removal
// ============================================================================

func TestRmBasicRemovesFile(t *testing.T) {
	dir := setup(t, map[string]string{"target.txt": "data"})
	_, stderr, code := cmdRun(t, "rm target.txt", dir)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	_, err := os.Stat(filepath.Join(dir, "target.txt"))
	assert.True(t, os.IsNotExist(err), "file should have been removed")
}

func TestRmMultipleFiles(t *testing.T) {
	dir := setup(t, map[string]string{"a.txt": "a", "b.txt": "b", "c.txt": "c"})
	_, stderr, code := cmdRun(t, "rm a.txt b.txt c.txt", dir)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		_, err := os.Stat(filepath.Join(dir, name))
		assert.True(t, os.IsNotExist(err), "%s should be removed", name)
	}
}

// ============================================================================
// --force flag
// ============================================================================

func TestRmForceMissingExits0(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "rm -f nosuchfile.txt", dir)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
}

func TestRmNoForce_MissingExits1(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "rm nosuchfile.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "nosuchfile.txt")
}

func TestRmForceLongForm(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "rm --force nosuchfile.txt", dir)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
}

// ============================================================================
// --verbose flag
// ============================================================================

func TestRmVerbosePrintsRemoved(t *testing.T) {
	dir := setup(t, map[string]string{"f.txt": "data"})
	stdout, _, code := cmdRun(t, "rm -v f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "removed")
	assert.Contains(t, stdout, "f.txt")
}

func TestRmVerboseLongForm(t *testing.T) {
	dir := setup(t, map[string]string{"f.txt": "data"})
	stdout, _, code := cmdRun(t, "rm --verbose f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "f.txt")
}

func TestRmNoVerboseNoOutput(t *testing.T) {
	dir := setup(t, map[string]string{"f.txt": "data"})
	stdout, stderr, code := cmdRun(t, "rm f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestRmForceMissingVerboseNoOutput(t *testing.T) {
	dir := t.TempDir()
	// -f suppresses error; no removal happened so -v should not print anything.
	stdout, stderr, code := cmdRun(t, "rm -fv nosuchfile.txt", dir)
	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

// ============================================================================
// --dir flag
// ============================================================================

func TestRmDirRemovesEmptyDir(t *testing.T) {
	dir := t.TempDir()
	emptyDir := filepath.Join(dir, "emptydir")
	require.NoError(t, os.MkdirAll(emptyDir, 0755))
	_, stderr, code := cmdRun(t, "rm -d emptydir", dir)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	_, err := os.Stat(emptyDir)
	assert.True(t, os.IsNotExist(err), "empty dir should be removed")
}

func TestRmDirRejectsNonEmptyDir(t *testing.T) {
	dir := setup(t, map[string]string{"nonempty/file.txt": "data"})
	_, stderr, code := cmdRun(t, "rm -d nonempty", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "nonempty")
}

func TestRmNoDirRejectsDirectory(t *testing.T) {
	dir := setup(t, map[string]string{"mydir/.keep": ""})
	_, stderr, code := cmdRun(t, "rm mydir", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "Is a directory")
}

func TestRmDirVerbose(t *testing.T) {
	dir := t.TempDir()
	emptyDir := filepath.Join(dir, "emptydir")
	require.NoError(t, os.MkdirAll(emptyDir, 0755))
	stdout, _, code := cmdRun(t, "rm -dv emptydir", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "emptydir")
}

// ============================================================================
// Error handling and partial failure
// ============================================================================

func TestRmMissingOperand(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "rm", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "missing operand")
}

func TestRmPartialFailureContinues(t *testing.T) {
	dir := setup(t, map[string]string{"good.txt": "data"})
	_, stderr, code := cmdRun(t, "rm nosuchfile.txt good.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "nosuchfile.txt")
	// good.txt must still be removed despite the earlier failure.
	_, err := os.Stat(filepath.Join(dir, "good.txt"))
	assert.True(t, os.IsNotExist(err), "good.txt should be removed even after earlier failure")
}

// ============================================================================
// Remediation mode gate
// ============================================================================

func TestRmReadOnlyModeRejected(t *testing.T) {
	dir := setup(t, map[string]string{"f.txt": "data"})
	// No ModeRemediation — callCtx.Remove will be nil.
	_, stderr, code := runScript(t, "rm f.txt", dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "remediation mode required")
	// File must be intact.
	_, err := os.Stat(filepath.Join(dir, "f.txt"))
	require.NoError(t, err, "file must not be touched in read-only mode")
}

func TestRmHelpReadOnlyModeRejected(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := runScript(t, "rm --help", dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "remediation mode required")
}

// ============================================================================
// Sandbox path restrictions
// ============================================================================

func TestRmSandboxBlocksPathTraversal(t *testing.T) {
	dir := t.TempDir()
	parent := filepath.Dir(dir)
	target := filepath.Join(parent, "pwned.txt")
	require.NoError(t, os.WriteFile(target, []byte("secret"), 0644))
	t.Cleanup(func() { os.Remove(target) })

	_, stderr, code := runScript(t, "rm ../pwned.txt", dir,
		interp.AllowedPaths([]string{dir}),
		interp.WithMode(interp.ModeRemediation),
	)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "rm")
	// Target must still exist.
	_, err := os.Stat(target)
	require.NoError(t, err, "file outside sandbox must not be removed")
}

func TestRmSandboxBlocksAbsolutePath(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()
	targetPath := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(targetPath, []byte("secret"), 0644))

	_, stderr, code := runScript(t, "rm "+targetPath, allowed,
		interp.AllowedPaths([]string{allowed}),
		interp.WithMode(interp.ModeRemediation),
	)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "rm")
	_, err := os.Stat(targetPath)
	require.NoError(t, err, "file outside sandbox must not be removed")
}

// ============================================================================
// Flag rejection (security)
// ============================================================================

func TestRmRecursiveRejected(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "rm -r dir", dir)
	assert.Equal(t, 1, code)
	assert.True(t, strings.Contains(stderr, "invalid option") || strings.Contains(stderr, "unrecognized option"),
		"expected flag rejection error, got: %q", stderr)
}

func TestRmUnknownFlagRejected(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "rm --no-preserve-root foo", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "unrecognized option")
}

func TestRmInteractiveFlagRejected(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "rm -i foo", dir)
	assert.Equal(t, 1, code)
	assert.True(t, strings.Contains(stderr, "invalid option") || strings.Contains(stderr, "unrecognized option"),
		"expected flag rejection error, got: %q", stderr)
}

// ============================================================================
// --help flag (remediation mode)
// ============================================================================

func TestRmHelp(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, "rm --help", dir)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "Usage: rm")
}

// ============================================================================
// Context cancellation
// ============================================================================

func TestRmContextCancellationSkipsFiles(t *testing.T) {
	// Set up many files; cancel the context before the run so the command
	// checks ctx.Err() in the loop and stops early. At least one file must
	// remain (not all can be removed because the first ctx.Err() check
	// returns Result{Code:1}).
	dir := t.TempDir()
	names := []string{"a.txt", "b.txt", "c.txt", "d.txt", "e.txt"}
	for _, n := range names {
		require.NoError(t, os.WriteFile(filepath.Join(dir, n), []byte("x"), 0644))
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the run

	// Use runScriptCtx; testutil returns exitCode=0 when ctx is cancelled
	// (the runner surfaces a context error, not an ExitStatus). We care that
	// the command did not silently remove all files despite cancellation.
	runScriptCtx(ctx, t, "rm a.txt b.txt c.txt d.txt e.txt", dir,
		interp.AllowedPaths([]string{dir}),
		interp.WithMode(interp.ModeRemediation),
	)
	// At least one file must still be present — the cancelled context should
	// have prevented all five from being removed.
	removed := 0
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(dir, n)); os.IsNotExist(err) {
			removed++
		}
	}
	assert.Less(t, removed, len(names), "cancelled context must not allow all files to be removed")
}
