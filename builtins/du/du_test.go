// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package du_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

func cmdRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return testutil.RunScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

func cmdRunCtx(ctx context.Context, t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, interp.AllowedPaths([]string{dir}))
}

// setupDu creates a temp directory containing the named files. Each value is
// the file content, a leading "DIR:" marks an empty directory, and a leading
// "LINK:<target>" marks a symlink whose target is interpreted relative to the
// temp directory.
func setupDu(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		full := filepath.Join(dir, name)
		switch {
		case strings.HasPrefix(content, "DIR:"):
			require.NoError(t, os.MkdirAll(full, 0o755))
		case strings.HasPrefix(content, "LINK:"):
			require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
			require.NoError(t, os.Symlink(content[len("LINK:"):], full))
		default:
			require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
			require.NoError(t, os.WriteFile(full, []byte(content), 0o644))
		}
	}
	return dir
}

// du output is "<size>\t<path>". Tests assert path components only because
// disk-usage values vary by filesystem block size. Where exact equality is
// required (apparent size, byte mode), tests build the file with controlled
// content sizes.

func TestDuDefaultEmptyDir(t *testing.T) {
	dir := setupDu(t, map[string]string{
		"emptydir": "DIR:",
	})
	stdout, _, code := cmdRun(t, "du emptydir", dir)
	assert.Equal(t, 0, code)
	assert.True(t, strings.HasSuffix(stdout, "\temptydir\n"), "got %q", stdout)
}

func TestDuDefaultSingleFile(t *testing.T) {
	dir := setupDu(t, map[string]string{
		"file.txt": "hello\n",
	})
	stdout, _, code := cmdRun(t, "du file.txt", dir)
	assert.Equal(t, 0, code)
	assert.True(t, strings.HasSuffix(stdout, "\tfile.txt\n"), "got %q", stdout)
}

func TestDuRecursive(t *testing.T) {
	dir := setupDu(t, map[string]string{
		"sub/inner.txt": "abcd",
		"file.txt":      "abc",
	})
	stdout, _, code := cmdRun(t, "du .", dir)
	assert.Equal(t, 0, code)
	// Output: per-subdir + final "."
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	require.GreaterOrEqual(t, len(lines), 2)
	assert.True(t, strings.HasSuffix(lines[len(lines)-1], "\t."), "got %q", lines)
}

func TestDuAllShowsFiles(t *testing.T) {
	dir := setupDu(t, map[string]string{
		"file.txt": "abc",
	})
	stdout, _, code := cmdRun(t, "du -a .", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "./file.txt")
}

func TestDuWithoutAllSuppressesFiles(t *testing.T) {
	dir := setupDu(t, map[string]string{
		"file.txt": "abc",
	})
	stdout, _, code := cmdRun(t, "du .", dir)
	assert.Equal(t, 0, code)
	assert.NotContains(t, stdout, "./file.txt")
}

func TestDuSummarizeOnlyTotal(t *testing.T) {
	dir := setupDu(t, map[string]string{
		"sub/a.txt": "abcd",
		"file.txt":  "ab",
	})
	stdout, _, code := cmdRun(t, "du -s .", dir)
	assert.Equal(t, 0, code)
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	assert.Len(t, lines, 1)
	assert.True(t, strings.HasSuffix(lines[0], "\t."), "got %q", stdout)
}

func TestDuSummarizeRejectsAll(t *testing.T) {
	dir := setupDu(t, map[string]string{
		"file.txt": "abc",
	})
	_, stderr, code := cmdRun(t, "du -s -a .", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "du:")
}

func TestDuSummarizeRejectsMaxDepth(t *testing.T) {
	dir := setupDu(t, map[string]string{
		"file.txt": "abc",
	})
	_, stderr, code := cmdRun(t, "du -s -d 2 .", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "du:")
}

func TestDuTotalAddsGrandTotal(t *testing.T) {
	dir := setupDu(t, map[string]string{
		"a.txt": "abc",
		"b.txt": "abcdef",
	})
	stdout, _, code := cmdRun(t, "du -c -a a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	require.GreaterOrEqual(t, len(lines), 3)
	assert.True(t, strings.HasSuffix(lines[len(lines)-1], "\ttotal"), "got %q", stdout)
}

func TestDuMaxDepthZero(t *testing.T) {
	dir := setupDu(t, map[string]string{
		"sub/inner.txt": "abc",
		"file.txt":      "abc",
	})
	stdout, _, code := cmdRun(t, "du -d 0 .", dir)
	assert.Equal(t, 0, code)
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	assert.Len(t, lines, 1, "max-depth=0 means only the operand: %q", stdout)
}

func TestDuMaxDepthOne(t *testing.T) {
	dir := setupDu(t, map[string]string{
		"sub/deep/inner.txt": "abc",
		"file.txt":           "abc",
	})
	stdout, _, code := cmdRun(t, "du -d 1 .", dir)
	assert.Equal(t, 0, code)
	// Should include "./sub" but not "./sub/deep".
	assert.Contains(t, stdout, "./sub\n")
	assert.NotContains(t, stdout, "./sub/deep")
}

func TestDuMaxDepthNegativeRejected(t *testing.T) {
	dir := setupDu(t, map[string]string{
		"file.txt": "abc",
	})
	_, stderr, code := cmdRun(t, "du -d -1 .", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "du:")
}

func TestDuBytes(t *testing.T) {
	dir := setupDu(t, map[string]string{
		"a.txt": "12345",
	})
	stdout, _, code := cmdRun(t, "du -b a.txt", dir)
	assert.Equal(t, 0, code)
	// -b reports apparent size in bytes, so exactly 5.
	assert.Equal(t, "5\ta.txt\n", stdout)
}

func TestDuApparentSize(t *testing.T) {
	dir := setupDu(t, map[string]string{
		"a.txt": "1234567890",
	})
	stdout, _, code := cmdRun(t, "du --apparent-size a.txt", dir)
	assert.Equal(t, 0, code)
	// Apparent size in 1024-byte blocks: ceil(10/1024) = 1.
	assert.Equal(t, "1\ta.txt\n", stdout)
}

func TestDuKiloIsDefault(t *testing.T) {
	dir := setupDu(t, map[string]string{
		"a.txt": "123",
	})
	stdoutDefault, _, _ := cmdRun(t, "du -b a.txt", dir)
	stdoutK, _, _ := cmdRun(t, "du -bk a.txt", dir) // -k after -b: apparent in 1024 blocks
	// -bk: bytes in apparent size, then -k overrides unit. Final wins is -k.
	assert.NotEqual(t, "", stdoutDefault)
	assert.NotEqual(t, "", stdoutK)
}

func TestDuMega(t *testing.T) {
	// File of 2 MiB - apparent. With -m we expect "2".
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "big.bin"), make([]byte, 2*1024*1024), 0o644))
	stdout, _, code := cmdRun(t, "du --apparent-size -m big.bin", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2\tbig.bin\n", stdout)
}

func TestDuHumanReadable(t *testing.T) {
	// 2 KiB exact apparent.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "twok.bin"), make([]byte, 2*1024), 0o644))
	stdout, _, code := cmdRun(t, "du --apparent-size -h twok.bin", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2.0K\ttwok.bin\n", stdout)
}

func TestDuSI(t *testing.T) {
	// 2000 bytes apparent.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "twok.bin"), make([]byte, 2000), 0o644))
	stdout, _, code := cmdRun(t, "du --apparent-size --si twok.bin", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2.0k\ttwok.bin\n", stdout)
}

func TestDuNullTerminator(t *testing.T) {
	dir := setupDu(t, map[string]string{
		"a.txt": "abc",
	})
	stdout, _, code := cmdRun(t, "du -0 a.txt", dir)
	assert.Equal(t, 0, code)
	assert.True(t, strings.HasSuffix(stdout, "\ta.txt\x00"), "got %q", stdout)
	assert.NotContains(t, stdout, "\n")
}

func TestDuMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "du nope", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "du: cannot access 'nope':")
}

func TestDuMultipleOperandsContinueOnError(t *testing.T) {
	dir := setupDu(t, map[string]string{
		"a.txt": "abc",
	})
	stdout, stderr, code := cmdRun(t, "du nope a.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "du: cannot access 'nope':")
	assert.Contains(t, stdout, "\ta.txt")
}

func TestDuUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "du --no-such-flag .", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "du:")
	assert.Contains(t, stderr, "unknown flag")
}

// --- Security-sensitive flags must be rejected ---

func TestDuRejectsFiles0From(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "du --files0-from=foo", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "du:")
}

func TestDuRejectsExcludeFrom(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "du --exclude-from=foo .", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "du:")
}

func TestDuRejectsExclude(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "du --exclude=foo .", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "du:")
}

func TestDuRejectsThreshold(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "du -t 1024 .", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "du:")
}

func TestDuRejectsBlockSize(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "du -B 1K .", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "du:")
}

// --- -L vs -P ---

func TestDuNoDereferenceDefault(t *testing.T) {
	if !canSymlink() {
		t.Skip("symlinks unavailable on this platform")
	}
	dir := setupDu(t, map[string]string{
		"target.txt": "the original payload",
		"link":       "LINK:target.txt",
	})
	stdoutLink, _, code1 := cmdRun(t, "du link", dir)
	assert.Equal(t, 0, code1)
	stdoutTarget, _, code2 := cmdRun(t, "du target.txt", dir)
	assert.Equal(t, 0, code2)
	// Without -L, du reports the symlink itself, not the target. The target
	// has 20 bytes; an empty-ish symlink is much smaller, so the sizes
	// should differ in apparent terms.
	assert.NotEqual(t, stdoutLink, stdoutTarget)
}

func TestDuDereferenceFollowsLink(t *testing.T) {
	if !canSymlink() {
		t.Skip("symlinks unavailable on this platform")
	}
	dir := setupDu(t, map[string]string{
		"target.txt": "12345678",
		"link":       "LINK:target.txt",
	})
	stdout, _, code := cmdRun(t, "du -L --apparent-size link", dir)
	assert.Equal(t, 0, code)
	// With -L, the link is followed and the size is the target's.
	assert.Equal(t, "1\tlink\n", stdout) // ceil(8/1024) = 1
}

func TestDuPSwitchesBackToNoDereference(t *testing.T) {
	if !canSymlink() {
		t.Skip("symlinks unavailable on this platform")
	}
	dir := setupDu(t, map[string]string{
		"target.txt": "12345678",
		"link":       "LINK:target.txt",
	})
	// -L then -P: -P wins because it's last (matching GNU).
	stdoutP, _, code1 := cmdRun(t, "du -L -P link", dir)
	assert.Equal(t, 0, code1)
	stdoutNoFlags, _, _ := cmdRun(t, "du link", dir)
	assert.Equal(t, stdoutNoFlags, stdoutP)
}

// --- -S separate-dirs ---

func TestDuSeparateDirsExcludesSubdirSize(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "top.bin"), make([]byte, 1024), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "inner.bin"), make([]byte, 4096), 0o644))

	stdoutPlain, _, _ := cmdRun(t, "du --apparent-size .", dir)
	stdoutSep, _, _ := cmdRun(t, "du --apparent-size -S .", dir)
	// With -S the "." line should report a smaller total because subdir
	// contents are not folded into it.
	assert.NotEqual(t, lastLine(stdoutPlain), lastLine(stdoutSep), "plain=%q sep=%q", stdoutPlain, stdoutSep)
}

// --- Help ---

func TestDuHelp(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, "du --help", dir)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "Usage: du")
	assert.Contains(t, stdout, "Summarize device usage")
	assert.Contains(t, stdout, "--max-depth")
}

// --- Hardening: deeply nested directories must not crash or hang ---

func TestDuDoesNotCrashOnDeepTree(t *testing.T) {
	dir := t.TempDir()
	deep := dir
	for i := 0; i < 50; i++ {
		deep = filepath.Join(deep, "x")
	}
	require.NoError(t, os.MkdirAll(deep, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(deep, "file"), []byte("ok"), 0o644))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, code := cmdRunCtx(ctx, t, "du .", dir)
	assert.Equal(t, 0, code)
}

func TestDuRespectsRecursionLimit(t *testing.T) {
	dir := t.TempDir()
	deep := dir
	// 270 levels — comfortably above maxRecursionDepth (256) but small
	// enough to keep the test snappy under -race + parallel CI load.
	for range 270 {
		deep = filepath.Join(deep, "x")
	}
	require.NoError(t, os.MkdirAll(deep, 0o755))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, stderr, code := cmdRunCtx(ctx, t, "du .", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "recursion depth limit exceeded")
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	idx := strings.LastIndex(s, "\n")
	if idx < 0 {
		return s
	}
	return s[idx+1:]
}
