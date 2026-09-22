// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tee_test

import (
	"os"
	"path/filepath"
	"syscall"
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

func TestTeeDashOperandIsStdoutAgain(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := teeRunStdin(t, "tee -", dir, "viadash\n")
	assert.Equal(t, 0, code)
	// callCtx.Stdout receives the chunk once per destination entry: the
	// implicit stdout destination, plus "-" resolving to stdout again.
	assert.Equal(t, "viadash\nviadash\n", stdout)
}

func TestTeeDashOperandMixedWithFile(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := teeRunStdin(t, "tee - out.txt", dir, "mixed\n")
	assert.Equal(t, 0, code)
	assert.Equal(t, "mixed\nmixed\n", stdout)
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

// --- Hard link write-target rejection ---

func TestTeeRejectsHardLinkedWriteTarget(t *testing.T) {
	dir := t.TempDir()
	orig := writeFile(t, dir, "orig.txt", "original content")
	linked := filepath.Join(dir, "linked.txt")
	require.NoError(t, os.Link(orig, linked))

	_, stderr, code := teeRunStdin(t, "tee linked.txt", dir, "new\n")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "hard links are not supported")
	// The original file must be untouched — the write must never reach
	// the shared inode.
	assert.Equal(t, "original content", readFile(t, orig))
}

// --- FIFO write-target rejection (unix only: Mkfifo is unix-specific) ---

func TestTeeRejectsFIFOWriteTargetNoReader(t *testing.T) {
	if _, ok := os.LookupEnv("CI_WINDOWS"); ok {
		t.Skip("FIFOs are unix-specific")
	}
	dir := t.TempDir()
	fifoPath := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifoPath, 0600); err != nil {
		t.Skipf("mkfifo not supported: %v", err)
	}
	stdout, stderr, code := teeRunStdin(t, "tee pipe", dir, "data\n")
	assert.Equal(t, 1, code, "tee on a FIFO with no reader must fail, not hang")
	assert.Contains(t, stderr, "not a regular file")
	// stdout must still receive the data even though the FIFO destination
	// was rejected.
	assert.Equal(t, "data\n", stdout)
}
