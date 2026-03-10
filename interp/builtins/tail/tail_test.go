// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tail_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/interp"
	"github.com/DataDog/rshell/interp/builtins/testutil"
)

// runScriptCtx runs a shell script with a context and returns stdout, stderr,
// and the exit code.
func runScriptCtx(ctx context.Context, t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, opts...)
}

// runScript runs a shell script and returns stdout, stderr, and the exit code.
func runScript(t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScript(t, script, dir, opts...)
}

// cmdRun runs a tail command with AllowedPaths set to dir.
func cmdRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return runScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

// writeFile creates a file in dir with the given content and returns its name.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
	return name
}

// fiveLines is a 5-line file used across multiple tests.
const fiveLines = "alpha\nbeta\ngamma\ndelta\nepsilon\n"

// twelveLines is a 12-line file used to test the default 10-line limit.
const twelveLines = "line01\nline02\nline03\nline04\nline05\nline06\nline07\nline08\nline09\nline10\nline11\nline12\n"

// --- Default behavior ---

func TestTailDefaultTenLines(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", twelveLines)
	stdout, _, code := cmdRun(t, "tail file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line03\nline04\nline05\nline06\nline07\nline08\nline09\nline10\nline11\nline12\n", stdout)
}

func TestTailFileShorterThanDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, fiveLines, stdout)
}

func TestTailEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "empty.txt", "")
	stdout, _, code := cmdRun(t, "tail empty.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

// --- -n / --lines flag ---

func TestTailLinesN3(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -n 3 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "gamma\ndelta\nepsilon\n", stdout)
}

func TestTailLinesN0(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -n 0 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

func TestTailLinesLargerThanFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -n 100 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, fiveLines, stdout)
}

func TestTailLinesLongForm(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail --lines=3 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "gamma\ndelta\nepsilon\n", stdout)
}

func TestTailLinesGlued(t *testing.T) {
	// -n3 (value glued to flag) is supported by pflag.
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -n3 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "gamma\ndelta\nepsilon\n", stdout)
}

// --- +N offset mode ---

func TestTailLinesOffsetPlus1(t *testing.T) {
	// +1 means start from line 1 = all lines.
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -n +1 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, fiveLines, stdout)
}

func TestTailLinesOffsetPlus2(t *testing.T) {
	// +2 means skip line 1, start from line 2.
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -n +2 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "beta\ngamma\ndelta\nepsilon\n", stdout)
}

func TestTailLinesOffsetPlus0(t *testing.T) {
	// +0 means skip 0 lines = output all (same as +1).
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -n +0 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, fiveLines, stdout)
}

func TestTailLinesOffsetBeyondFile(t *testing.T) {
	// +N where N > line count: no output.
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -n +99 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

// --- No trailing newline preservation ---

func TestTailNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "no newline at end")
	stdout, _, code := cmdRun(t, "tail -n 1 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "no newline at end", stdout)
}

func TestTailLastLineNoNewline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "line1\nline2")
	stdout, _, code := cmdRun(t, "tail -n 1 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line2", stdout)
}

func TestTailLastTwoLinesSecondNoNewline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "line1\nline2")
	stdout, _, code := cmdRun(t, "tail -n 2 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line1\nline2", stdout)
}

// --- -c / --bytes flag ---

func TestTailBytesN5(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", fiveLines)
	// fiveLines = "alpha\nbeta\ngamma\ndelta\nepsilon\n"
	// "epsilon\n" = e(1)p(2)s(3)i(4)l(5)o(6)n(7)\n(8) = 8 bytes.
	// Last 5 bytes: i-l-o-n-\n = "ilon\n"
	stdout, _, code := cmdRun(t, "tail -c 5 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "ilon\n", stdout)
}

func TestTailBytesN0(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -c 0 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

func TestTailBytesLargerThanFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -c 9999 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, fiveLines, stdout)
}

func TestTailBytesLongForm(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "abcde")
	stdout, _, code := cmdRun(t, "tail --bytes=3 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "cde", stdout)
}

func TestTailBytesOffsetPlus3(t *testing.T) {
	// +3 means skip first 2 bytes, emit from byte 3.
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "abcde")
	stdout, _, code := cmdRun(t, "tail -c +3 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "cde", stdout)
}

func TestTailBytesOffsetPlus1(t *testing.T) {
	// +1 means skip 0 bytes = output all.
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "abcde")
	stdout, _, code := cmdRun(t, "tail -c +1 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "abcde", stdout)
}

func TestTailBytesBinaryContent(t *testing.T) {
	dir := t.TempDir()
	// content = "a\x00b\x00c\x00d" = 7 bytes; last 5 = "b\x00c\x00d"
	content := "a\x00b\x00c\x00d"
	writeFile(t, dir, "file.bin", content)
	stdout, _, code := cmdRun(t, "tail -c 5 file.bin", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "b\x00c\x00d", stdout)
}

// --- Last flag wins (-n vs -c) ---

func TestTailLastFlagWinsBytes(t *testing.T) {
	// -n 2 -c 5: last flag is -c, so byte mode with 5 bytes.
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "abcdefgh")
	stdout, _, code := cmdRun(t, "tail -n 2 -c 5 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "defgh", stdout)
}

func TestTailLastFlagWinsLines(t *testing.T) {
	// -c 5 -n 2: last flag is -n, so line mode with 2 lines.
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -c 5 -n 2 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "delta\nepsilon\n", stdout)
}

// --- Headers (-v / -q / --silent) ---

func TestTailVerboseSingleFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "one.txt", "only one line\n")
	stdout, _, code := cmdRun(t, "tail -v one.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "==> one.txt <==\nonly one line\n", stdout)
}

func TestTailTwoFilesDefaultHeaders(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "alpha\nbeta\n")
	writeFile(t, dir, "b.txt", "gamma\n")
	stdout, _, code := cmdRun(t, "tail -n 2 a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "==> a.txt <==\nalpha\nbeta\n\n==> b.txt <==\ngamma\n", stdout)
}

func TestTailQuietTwoFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "alpha\nbeta\n")
	writeFile(t, dir, "b.txt", "gamma\n")
	stdout, _, code := cmdRun(t, "tail -q -n 2 a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "alpha\nbeta\ngamma\n", stdout)
}

func TestTailSilentAlias(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "alpha\nbeta\n")
	writeFile(t, dir, "b.txt", "gamma\n")
	stdout, _, code := cmdRun(t, "tail --silent -n 2 a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "alpha\nbeta\ngamma\n", stdout)
}

func TestTailVerboseTwoFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "alpha\n")
	writeFile(t, dir, "b.txt", "beta\n")
	stdout, _, code := cmdRun(t, "tail -v -n 1 a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "==> a.txt <==\nalpha\n\n==> b.txt <==\nbeta\n", stdout)
}

// --- Stdin ---

func TestTailStdinImplicit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -n 2 < src.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "delta\nepsilon\n", stdout)
}

func TestTailStdinDash(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src.txt", fiveLines)
	stdout, _, code := cmdRun(t, "tail -n 2 - < src.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "delta\nepsilon\n", stdout)
}

func TestTailStdinVerbose(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src.txt", "hello\n")
	stdout, _, code := cmdRun(t, "tail -v - < src.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "==> standard input <==\nhello\n", stdout)
}

// --- Help ---

func TestTailHelp(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, "tail --help", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage:")
	assert.Contains(t, stdout, "--lines")
	assert.Contains(t, stdout, "--bytes")
	assert.Empty(t, stderr)
}

func TestTailHelpShort(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, "tail -h", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage:")
	assert.Empty(t, stderr)
}

// --- Error cases ---

func TestTailMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "tail nonexistent.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tail:")
}

func TestTailDirectory(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "subdir"), 0755))
	_, stderr, code := cmdRun(t, "tail subdir", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tail:")
}

func TestTailUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "tail --follow file.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tail:")
}

func TestTailUnknownShortFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "tail -f file.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tail:")
}

func TestTailInvalidCountString(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", fiveLines)
	_, stderr, code := cmdRun(t, "tail -n abc file.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tail:")
}

func TestTailNegativeCount(t *testing.T) {
	// GNU tail -n -N means "all but last N lines" — we do NOT support that.
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", fiveLines)
	_, _, code := cmdRun(t, "tail -n -1 file.txt", dir)
	assert.Equal(t, 1, code)
}

func TestTailOutsideAllowedPaths(t *testing.T) {
	allowed := t.TempDir()
	secret := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(secret, "secret.txt"), []byte("secret"), 0644))

	secretPath := strings.ReplaceAll(filepath.Join(secret, "secret.txt"), `\`, `/`)
	_, stderr, code := runScript(t, "tail "+secretPath, allowed, interp.AllowedPaths([]string{allowed}))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tail:")
}

func TestTailMultipleFilesSomeFailSomeSucceed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "good.txt", "hello\n")
	stdout, stderr, code := cmdRun(t, "tail -n 1 good.txt nonexistent.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, "hello")
	assert.Contains(t, stderr, "tail:")
}

// --- RULES.md compliance ---

func TestTailLargeCountClamped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "small.txt", "tiny\n")
	stdout, _, code := cmdRun(t, "tail -n 9999999999 small.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "tiny\n", stdout)
}

func TestTailLargeByteCountClamped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "small.txt", "tiny")
	stdout, _, code := cmdRun(t, "tail -c 9999999999 small.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "tiny", stdout)
}

func TestTailContextCancellation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "data.txt", fiveLines)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, code := runScriptCtx(ctx, t, "tail -n 3 data.txt", dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 0, code)
}

func TestTailDoubleDash(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "-n", "flag-looking-name\n")
	stdout, _, code := cmdRun(t, "tail -- -n", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "flag-looking-name\n", stdout)
}

func TestTailNullBytesInContent(t *testing.T) {
	dir := t.TempDir()
	content := "a\x00b\x00c\x00\n"
	writeFile(t, dir, "binary.bin", content)
	stdout, _, code := cmdRun(t, "tail -n 1 binary.bin", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, content, stdout)
}

func TestTailCRLFPreserved(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "crlf.txt", "line1\r\nline2\r\nline3\r\n")
	stdout, _, code := cmdRun(t, "tail -n 2 crlf.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line2\r\nline3\r\n", stdout)
}

func TestTailPipeInput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", twelveLines)
	stdout, _, code := cmdRun(t, "cat file.txt | tail -n 3", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line10\nline11\nline12\n", stdout)
}

func TestTailContextPreCancelled(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", fiveLines)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		runScriptCtx(ctx, t, "tail -n 5 file.txt", dir, interp.AllowedPaths([]string{dir}))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("tail with pre-cancelled context did not return within 5s")
	}
}

func TestTailNilStdin(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := runScript(t, "tail -", dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
}

func TestTailNilStdinVerbose(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := runScript(t, "tail -v -", dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 0, code)
	assert.Equal(t, "==> standard input <==\n", stdout)
	assert.Equal(t, "", stderr)
}

func TestTailNoOctalInterpretation08(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", twelveLines)
	stdout, _, code := cmdRun(t, "tail -n 08 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, 8, strings.Count(stdout, "\n"))
}

// --- -z / --zero-terminated ---

func TestTailZeroTerminatedBasic(t *testing.T) {
	dir := t.TempDir()
	// Three NUL-terminated records: "rec1\x00rec2\x00rec3\x00"
	content := "rec1\x00rec2\x00rec3\x00"
	writeFile(t, dir, "nul.bin", content)
	stdout, _, code := cmdRun(t, "tail -z -n 2 nul.bin", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "rec2\x00rec3\x00", stdout)
}

func TestTailZeroTerminatedOffset(t *testing.T) {
	dir := t.TempDir()
	content := "rec1\x00rec2\x00rec3\x00"
	writeFile(t, dir, "nul.bin", content)
	stdout, _, code := cmdRun(t, "tail -z -n +2 nul.bin", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "rec2\x00rec3\x00", stdout)
}

// --- Bad UTF-8 / binary passthrough ---

func TestTailBadUTF8ByteMode(t *testing.T) {
	dir := t.TempDir()
	content := []byte{0xfc, 0x80, 0x80, 0x80, 0x80, 0xaf}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.bin"), content, 0644))
	stdout, _, code := cmdRun(t, "tail -c 6 bad.bin", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, string(content), stdout)
}

func TestTailBadUTF8LineMode(t *testing.T) {
	dir := t.TempDir()
	badSeq := []byte{0xfc, 0x80, 0x80, 0x80, 0x80, 0xaf}
	line1 := append(append([]byte(nil), badSeq...), '\n')
	line2 := append(append([]byte("b"), badSeq...), '\n')
	line3 := append([]byte("c"), badSeq...)
	input := append(append(append([]byte(nil), line1...), line2...), line3...)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bad.bin"), input, 0644))

	expected := append(append([]byte(nil), line2...), line3...)
	stdout, _, code := cmdRun(t, "tail -n 2 bad.bin", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, string(expected), stdout)
}

// --- Multi-file edge cases ---

func TestTailTwoEmptyFilesHeaders(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "")
	writeFile(t, dir, "b.txt", "")
	stdout, _, code := cmdRun(t, "tail a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "==> a.txt <==\n\n==> b.txt <==\n", stdout)
}

func TestTailAllNonexistentFiles(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, "tail missing1.txt missing2.txt", dir)
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "missing1.txt")
	assert.Contains(t, stderr, "missing2.txt")
}
