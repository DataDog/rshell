// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tee_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/interp"
)

// --- Happy path ---

func TestTeeWritesFileAndStdout(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := teeRunStdin(t, "tee out.txt", dir, "hello\n")
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
	assert.Equal(t, "hello\n", readFile(t, filepath.Join(dir, "out.txt")))
}

func TestTeeMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := teeRunStdin(t, "tee a.txt b.txt", dir, "hi\n")
	assert.Equal(t, 0, code)
	assert.Equal(t, "hi\n", stdout)
	assert.Equal(t, "hi\n", readFile(t, filepath.Join(dir, "a.txt")))
	assert.Equal(t, "hi\n", readFile(t, filepath.Join(dir, "b.txt")))
}

func TestTeeNoFileOperandsIsPassthrough(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := teeRunStdin(t, "tee", dir, "just stdout\n")
	assert.Equal(t, 0, code)
	assert.Equal(t, "just stdout\n", stdout)
	assert.Equal(t, "", stderr)
}

func TestTeeOverwritesByDefault(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "out.txt", "old content that is longer")
	_, _, code := teeRunStdin(t, "tee out.txt", dir, "new\n")
	assert.Equal(t, 0, code)
	assert.Equal(t, "new\n", readFile(t, path))
}

func TestTeeAppendFlag(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "out.txt", "first\n")
	_, _, code := teeRunStdin(t, "tee -a out.txt", dir, "second\n")
	assert.Equal(t, 0, code)
	assert.Equal(t, "first\nsecond\n", readFile(t, path))
}

func TestTeeAppendLongFlag(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "out.txt", "first\n")
	_, _, code := teeRunStdin(t, "tee --append out.txt", dir, "second\n")
	assert.Equal(t, 0, code)
	assert.Equal(t, "first\nsecond\n", readFile(t, path))
}

func TestTeeCreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, _, code := teeRunStdin(t, "tee newfile.txt", dir, "content\n")
	assert.Equal(t, 0, code)
	assert.Equal(t, "content\n", readFile(t, filepath.Join(dir, "newfile.txt")))
}

func TestTeeEmptyStdin(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := teeRunStdin(t, "tee out.txt", dir, "")
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", readFile(t, filepath.Join(dir, "out.txt")))
}

func TestTeeNoStdinAtAll(t *testing.T) {
	// When stdin is not wired at all (callCtx.Stdin == nil, matching
	// interp.StdIO(nil, ...)), tee must not error or hang; it behaves as
	// if reading an empty source.
	dir := t.TempDir()
	stdout, stderr, code := teeRun(t, "tee out.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "", readFile(t, filepath.Join(dir, "out.txt")))
}

// TestTeeDashOperandIsLiteralFile pins current GNU tee behavior: "-" names
// an actual file called "-", opened through the sandbox like any other
// operand. GNU tee treated "-" as a stdout alias in coreutils 5.3.0 through
// 8.23, but that special case was removed in 8.24 (POSIX-mandated), and
// real bash/GNU tee on the CI runner confirms the current behavior.
func TestTeeDashOperandIsLiteralFile(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := teeRunStdin(t, "tee -", dir, "viadash\n")
	assert.Equal(t, 0, code)
	assert.Equal(t, "viadash\n", stdout)
	assert.Equal(t, "viadash\n", readFile(t, filepath.Join(dir, "-")))
}

func TestTeeDashOperandMixedWithFile(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := teeRunStdin(t, "tee - out.txt", dir, "mixed\n")
	assert.Equal(t, 0, code)
	assert.Equal(t, "mixed\n", stdout)
	assert.Equal(t, "mixed\n", readFile(t, filepath.Join(dir, "-")))
	assert.Equal(t, "mixed\n", readFile(t, filepath.Join(dir, "out.txt")))
}

// --- Help ---

func TestTeeHelp(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := teeRun(t, "tee --help", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Contains(t, stdout, "Usage: tee")
	assert.Contains(t, stdout, "--append")
}

func TestTeeHelpDeniedOutsideRemediation(t *testing.T) {
	// --help must be refused exactly like any other invocation in
	// read-only mode, matching the RULES.md dispatch-gate requirement for
	// RemediationOnly builtins.
	dir := t.TempDir()
	stdout, stderr, code := runScript(t, "tee --help", dir, interp.AllowedPaths([]string{dir + ":rw"}))
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "tee:")
}

// --- Remediation mode gating ---

func TestTeeDeniedOutsideRemediationMode(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := runScript(t, "echo hi | tee out.txt", dir, interp.AllowedPaths([]string{dir + ":rw"}))
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "tee:")
	assert.NoFileExists(t, filepath.Join(dir, "out.txt"))
}

func TestTeeDeniedWithNoFileOperandsOutsideRemediation(t *testing.T) {
	// Even with no FILE operands (pure passthrough), tee must still be
	// refused outside remediation mode rather than silently succeeding.
	dir := t.TempDir()
	_, stderr, code := runScript(t, "echo hi | tee", dir, interp.AllowedPaths([]string{dir + ":rw"}))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tee:")
}

func TestTeeDeniedOnReadOnlyAllowedPath(t *testing.T) {
	// Remediation mode is on, but the AllowedPaths root has no :rw access.
	dir := t.TempDir()
	_, stderr, code := runScript(t, "echo hi | tee out.txt", dir,
		interp.AllowedPaths([]string{dir}),
		interp.WithMode(interp.ModeRemediation),
	)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "permission denied")
	assert.NoFileExists(t, filepath.Join(dir, "out.txt"))
}

// --- Resource limits ---

func TestTeeRejectsTooManyFileOperands(t *testing.T) {
	dir := t.TempDir()
	names := make([]string, 0, 1025)
	for i := 0; i < 1025; i++ {
		names = append(names, fmt.Sprintf("f%d.txt", i))
	}
	script := "tee " + strings.Join(names, " ")
	_, stderr, code := teeRunStdin(t, script, dir, "hi\n")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "too many operands")
	// No destination must have been opened.
	assert.NoFileExists(t, filepath.Join(dir, "f0.txt"))
}

func TestTeeAllowsExactlyMaxFileOperands(t *testing.T) {
	dir := t.TempDir()
	names := make([]string, 0, 1024)
	for i := 0; i < 1024; i++ {
		names = append(names, fmt.Sprintf("f%d.txt", i))
	}
	script := "tee " + strings.Join(names, " ")
	stdout, stderr, code := teeRunStdin(t, script, dir, "hi\n")
	assert.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Equal(t, "hi\n", stdout)
	assert.Equal(t, "hi\n", readFile(t, filepath.Join(dir, "f0.txt")))
	assert.Equal(t, "hi\n", readFile(t, filepath.Join(dir, "f1023.txt")))
}

// --- Sandbox containment ---

func TestTeeRejectsPathOutsideAllowedRoots(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "escape.txt")
	_, stderr, code := teeRunStdin(t, "tee "+target, dir, "data\n")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tee:")
	assert.NoFileExists(t, target)
}

// --- Partial failure semantics ---

func TestTeeContinuesToOtherDestinationsAfterOneFails(t *testing.T) {
	// A file outside the sandbox fails to open, but the sandboxed
	// destination and stdout must still receive the data (GNU tee
	// semantics: one bad destination does not stop the others).
	dir := t.TempDir()
	outside := t.TempDir()
	badTarget := filepath.Join(outside, "denied.txt")
	stdout, stderr, code := teeRunStdin(t, "tee "+badTarget+" good.txt", dir, "payload\n")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tee:")
	assert.Equal(t, "payload\n", stdout)
	assert.Equal(t, "payload\n", readFile(t, filepath.Join(dir, "good.txt")))
}

func TestTeeMissingParentDirectoryFails(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := teeRunStdin(t, "tee missing_dir/out.txt", dir, "data\n")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tee:")
	// stdout still receives the data even though the file destination failed.
	assert.Equal(t, "data\n", stdout)
}

func TestTeeDirectoryAsTargetFails(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "adir"), 0755))
	_, stderr, code := teeRunStdin(t, "tee adir", dir, "data\n")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tee:")
}

// --- Unknown flags ---

func TestTeeRejectsUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := teeRun(t, "tee --ignore-interrupts out.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "unrecognized option")
}

func TestTeeRejectsOutputErrorFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := teeRun(t, "tee -p out.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "invalid option -- 'p'")
}

// Hard-link write-target rejection is exercised in
// hardlink_notwindows_test.go: the guard is unix-only (Windows cannot report
// a link count from an open handle, see AGENTS.md's hard-link entry), so it
// is not a platform-agnostic assertion. FIFO write-target rejection is
// exercised in tee_unix_test.go (Mkfifo is unix-specific and does not
// compile on Windows) for the same reason.
