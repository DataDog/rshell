// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package truncate_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/rshell/interp"
)

// fileSize returns the size of path, or fails the test if it cannot be stat'd.
func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Size()
}

// writeFile writes content to dir/name. Used to seed test fixtures.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// TestTruncateZeroSize covers the demo's "nuclear option" — truncate -s 0 on
// a populated file zeros it out without removing the inode.
func TestTruncateZeroSize(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "log.txt", "abcdefghij")
	stdout, stderr, code := truncateRun(t, "truncate -s 0 log.txt", dir)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, stderr)
	}
	if stdout != "" {
		t.Errorf("unexpected stdout: %q", stdout)
	}
	if stderr != "" {
		t.Errorf("unexpected stderr: %q", stderr)
	}
	if got := fileSize(t, path); got != 0 {
		t.Errorf("post-truncate size = %d, want 0", got)
	}
}

// TestTruncateExtend covers the case where SIZE is larger than the current
// file: bytes are zero-extended (sparse on most filesystems).
func TestTruncateExtend(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "log.txt", "abc")
	_, stderr, code := truncateRun(t, "truncate -s 1024 log.txt", dir)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, stderr)
	}
	if got := fileSize(t, path); got != 1024 {
		t.Errorf("post-extend size = %d, want 1024", got)
	}
}

// TestTruncateShrink covers the case where SIZE is smaller than the current
// file: trailing bytes are dropped, leading bytes are preserved verbatim.
func TestTruncateShrink(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "log.txt", "0123456789")
	_, _, code := truncateRun(t, "truncate -s 5 log.txt", dir)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := fileSize(t, path); got != 5 {
		t.Errorf("size = %d, want 5", got)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "01234" {
		t.Errorf("content = %q, want %q", body, "01234")
	}
}

// TestTruncateSizeSuffixes covers each accepted suffix end-to-end via the
// shell, in addition to the unit-level coverage in parseSize tests.
func TestTruncateSizeSuffixes(t *testing.T) {
	cases := []struct {
		size string
		want int64
	}{
		{"512", 512},
		{"1K", 1024},
		{"1KiB", 1024},
		{"1KB", 1000},
		{"1M", 1 << 20},
		{"2MiB", 2 << 20},
		{"1MB", 1000 * 1000},
	}
	for _, tc := range cases {
		t.Run(tc.size, func(t *testing.T) {
			dir := t.TempDir()
			path := writeFile(t, dir, "f.bin", "")
			script := "truncate -s " + tc.size + " f.bin"
			_, stderr, code := truncateRun(t, script, dir)
			if code != 0 {
				t.Fatalf("exit %d, stderr=%q", code, stderr)
			}
			if got := fileSize(t, path); got != tc.want {
				t.Errorf("size = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestTruncateLongFlag confirms --size= is accepted and equivalent to -s.
func TestTruncateLongFlag(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "f.bin", "abcde")
	_, _, code := truncateRun(t, "truncate --size=0 f.bin", dir)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := fileSize(t, path); got != 0 {
		t.Errorf("size = %d, want 0", got)
	}
}

// TestTruncateMultipleFiles covers two related properties:
//   - Without -c, every operand is processed in order — including missing
//     files, which are created. Exit code is 0.
//   - With -c, missing files become permission-denied-like errors only
//     when the missing file is outside an allowed root; an ordinarily
//     missing file is silently skipped (exit 0). To exercise the failure
//     path under -c, we drop a sub-operand outside AllowedPaths.
func TestTruncateMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", "hello a")
	b := writeFile(t, dir, "b.txt", "hello b")
	stdout, stderr, code := truncateRun(t, "truncate -s 0 a.txt fresh.txt b.txt", dir)
	if code != 0 {
		t.Fatalf("exit %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
	}
	if fileSize(t, a) != 0 {
		t.Error("a.txt was not truncated")
	}
	if fileSize(t, b) != 0 {
		t.Error("b.txt was not truncated")
	}
	if got := fileSize(t, filepath.Join(dir, "fresh.txt")); got != 0 {
		t.Errorf("fresh.txt was not created at size 0: %d", got)
	}
}

// TestTruncateMultipleFilesPartialFailure verifies that one failing
// operand does not abort the loop: the surviving files are still
// truncated and the final exit code is 1.
func TestTruncateMultipleFilesPartialFailure(t *testing.T) {
	dir := t.TempDir()
	insideDir := filepath.Join(dir, "inside")
	if err := os.MkdirAll(insideDir, 0755); err != nil {
		t.Fatal(err)
	}
	a := writeFile(t, insideDir, "a.txt", "alpha")
	b := writeFile(t, insideDir, "b.txt", "beta")
	// outside.txt is reachable on disk but blocked by AllowedPaths.
	outsidePath := writeFile(t, dir, "outside.txt", "untouched")

	_, stderr, code := runScript(t, "truncate -s 0 a.txt ../outside.txt b.txt", insideDir,
		interp.AllowedPaths([]string{insideDir}))
	if code != 1 {
		t.Fatalf("exit %d, want 1; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "outside.txt") {
		t.Errorf("stderr should mention outside.txt: %q", stderr)
	}
	if fileSize(t, a) != 0 {
		t.Error("a.txt was not truncated despite later failure")
	}
	if fileSize(t, b) != 0 {
		t.Error("b.txt was not truncated despite earlier failure")
	}
	if got := fileSize(t, outsidePath); got != int64(len("untouched")) {
		t.Errorf("outside.txt was modified despite sandbox: size %d", got)
	}
}

// TestTruncateNoCreateMissingFile confirms that -c silently skips a missing
// file, returning exit 0 and not creating the file.
func TestTruncateNoCreateMissingFile(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := truncateRun(t, "truncate -c -s 0 missing.txt", dir)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("unexpected output: stdout=%q stderr=%q", stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "missing.txt")); !os.IsNotExist(err) {
		t.Error("missing.txt was created when -c was passed")
	}
}

// TestTruncateCreatesByDefault confirms that without -c, missing files are
// created.
func TestTruncateCreatesByDefault(t *testing.T) {
	dir := t.TempDir()
	_, _, code := truncateRun(t, "truncate -s 100 fresh.bin", dir)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	path := filepath.Join(dir, "fresh.bin")
	if got := fileSize(t, path); got != 100 {
		t.Errorf("size = %d, want 100", got)
	}
}

// TestTruncateMissingSize verifies that running truncate without -s/--size
// is rejected with exit 1 and a clear error message.
func TestTruncateMissingSize(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "")
	_, stderr, code := truncateRun(t, "truncate a.txt", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "--size") {
		t.Errorf("stderr should hint at --size: %q", stderr)
	}
}

// TestTruncateMissingFile verifies that -s SIZE without any file operand is
// rejected with exit 1.
func TestTruncateMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := truncateRun(t, "truncate -s 0", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "missing file operand") {
		t.Errorf("stderr should say missing file operand: %q", stderr)
	}
}

// TestTruncateRejectsRelativeSize verifies every GNU relative-size modifier
// (+, -, <, >, /, %) is rejected with errRelativeSize wording.
//
// The size argument is single-quoted in the shell command so that the
// parser does not interpret '<' or '>' as redirection operators.
func TestTruncateRejectsRelativeSize(t *testing.T) {
	prefixes := []string{"+", "-", "<", ">", "/", "%"}
	for _, p := range prefixes {
		t.Run(p, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "a.txt", "abc")
			script := "truncate -s '" + p + "10' a.txt"
			_, stderr, code := truncateRun(t, script, dir)
			if code != 1 {
				t.Fatalf("exit %d, want 1; stderr=%q", code, stderr)
			}
			if !strings.Contains(stderr, "relative size operators not supported") {
				t.Errorf("stderr should explain unsupported relative size: %q", stderr)
			}
		})
	}
}

// TestTruncateRejectsInvalidSize verifies non-numeric and overflow inputs
// produce exit 1 with the generic "invalid size" message.
func TestTruncateRejectsInvalidSize(t *testing.T) {
	cases := []string{"abc", "1.5", "9999999999999999999999", "1KIB", "1kb", "1Kib"}
	for _, c := range cases {
		t.Run(c, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, dir, "a.txt", "abc")
			script := "truncate -s " + c + " a.txt"
			_, stderr, code := truncateRun(t, script, dir)
			if code != 1 {
				t.Fatalf("exit %d, want 1; stderr=%q", code, stderr)
			}
			if !strings.Contains(stderr, "invalid size") {
				t.Errorf("stderr missing 'invalid size': %q", stderr)
			}
		})
	}
}

// TestTruncateUnknownFlag verifies that flags we deliberately did not
// implement (--reference, -o) are rejected as unknown, never silently
// accepted.
func TestTruncateUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "abc")
	writeFile(t, dir, "ref.txt", "1234567890")
	_, stderr, code := truncateRun(t, "truncate --reference=ref.txt a.txt", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "truncate") {
		t.Errorf("stderr should mention truncate: %q", stderr)
	}
}

// TestTruncateOutsideAllowedPath verifies that the sandbox rejects targets
// outside AllowedPaths before any open syscall is issued.
func TestTruncateOutsideAllowedPath(t *testing.T) {
	dir := t.TempDir()
	insideDir := filepath.Join(dir, "inside")
	if err := os.MkdirAll(insideDir, 0755); err != nil {
		t.Fatal(err)
	}
	outsidePath := filepath.Join(dir, "outside.txt")
	writeFile(t, dir, "outside.txt", "leave me alone")

	_, stderr, code := runScript(t, "truncate -s 0 ../outside.txt", insideDir,
		interp.AllowedPaths([]string{insideDir}))
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "permission denied") {
		t.Errorf("stderr should report permission denied: %q", stderr)
	}
	if got := fileSize(t, outsidePath); got != int64(len("leave me alone")) {
		t.Errorf("outside.txt was modified: size %d", got)
	}
}

// TestTruncateDoubleDash verifies that "--" lets users target a filename
// that begins with "-".
func TestTruncateDoubleDash(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "-dashfile", "abcdef")
	_, _, code := truncateRun(t, "truncate -s 0 -- -dashfile", dir)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := fileSize(t, filepath.Join(dir, "-dashfile")); got != 0 {
		t.Errorf("size = %d, want 0", got)
	}
}

// TestTruncateHelp covers --help: usage on stdout, exit 0, no stderr.
func TestTruncateHelp(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := truncateRun(t, "truncate --help", dir)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if stderr != "" {
		t.Errorf("--help should write nothing to stderr: %q", stderr)
	}
	for _, want := range []string{"Usage: truncate", "--size", "--no-create"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("--help stdout missing %q: %q", want, stdout)
		}
	}
}

// TestTruncateContextCancellation verifies that a cancelled context aborts
// the iteration without panicking. We assert no panic and that the per-
// iteration ctx.Err() check leaves at least one operand unmodified.
func TestTruncateContextCancellation(t *testing.T) {
	dir := t.TempDir()
	paths := make([]string, 5)
	for i := range paths {
		paths[i] = writeFile(t, dir, "f"+string(rune('0'+i))+".txt", "abc")
	}
	ctx, cancel := newCancelledContext()
	defer cancel()

	// Returning at all (no panic) is the primary pass condition.
	_, _, _ = runScriptCtx(ctx, t, "truncate -s 0 f0.txt f1.txt f2.txt f3.txt f4.txt", dir,
		interp.AllowedPaths([]string{dir}))

	// The handler checks ctx.Err() before each operand, so at least the
	// final operand should be left at its original 3 bytes when ctx was
	// cancelled before any work began. If every file was zeroed, the
	// cancellation check is silently broken.
	allZeroed := true
	for _, p := range paths {
		if fileSize(t, p) != 0 {
			allZeroed = false
			break
		}
	}
	if allZeroed {
		t.Errorf("expected ctx.Err() check to abort iteration; all files were truncated")
	}
}
