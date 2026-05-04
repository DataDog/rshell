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
)

// TestHardenCompactShortFlag verifies that pflag's combined short-flag form
// (-cs0) is recognised: -c is boolean, -s takes a value, the value can be
// glued. POSIX-style short-flag chaining is part of pflag's contract; we
// just confirm we have not accidentally disabled it.
func TestHardenCompactShortFlag(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", "content")
	_, stderr, code := truncateRun(t, "truncate -cs0 a.txt", dir)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, stderr)
	}
	if got := fileSize(t, a); got != 0 {
		t.Errorf("size = %d, want 0", got)
	}
}

// TestHardenLastSizeWins verifies pflag's default last-value-wins behaviour
// for repeated flags. We document the behaviour in case a future change
// switches to first-value-wins or rejects duplicates.
func TestHardenLastSizeWins(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.txt", "abcdef")
	_, _, code := truncateRun(t, "truncate -s 100 -s 0 a.txt", dir)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := fileSize(t, a); got != 0 {
		t.Errorf("size = %d, want 0 (last -s wins)", got)
	}
}

// TestHardenDoubleDashTreatsLaterFlagsAsFiles verifies that "--" forces
// every following token to be treated as a positional argument, even
// tokens that look like flags. This is critical for safety: a user-
// supplied filename that happens to start with -- must not silently mutate
// behaviour.
func TestHardenDoubleDashTreatsLaterFlagsAsFiles(t *testing.T) {
	dir := t.TempDir()
	// Filename literally named "--size=99". Without -- it would be
	// parsed as the --size flag value.
	weird := writeFile(t, dir, "--size=99", "content")
	_, _, code := truncateRun(t, "truncate -s 0 -- '--size=99'", dir)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := fileSize(t, weird); got != 0 {
		t.Errorf("size of '--size=99' = %d, want 0", got)
	}
}

// TestHardenMissingSizeWithNoCreate verifies that -c alone (no -s) is
// rejected just like the no-flag case. -c is a modifier of behaviour,
// not a substitute for --size.
func TestHardenMissingSizeWithNoCreate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "abc")
	_, stderr, code := truncateRun(t, "truncate -c a.txt", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}
	if !strings.Contains(stderr, "--size") {
		t.Errorf("stderr should hint at --size: %q", stderr)
	}
}

// TestHardenLargeSparseSize covers the case where the user requests an
// extension to a very large but kernel-acceptable size (1 GiB sparse).
// The file system records the size in metadata only; no allocation
// happens. We use 1 << 30 to exercise the suffix path and confirm we
// don't crash for sizes larger than typical files.
func TestHardenLargeSparseSize(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "f.bin", "")
	_, stderr, code := truncateRun(t, "truncate -s 1G f.bin", dir)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, stderr)
	}
	if got := fileSize(t, a); got != 1<<30 {
		t.Errorf("size = %d, want %d", got, 1<<30)
	}
	// Sanity check: this should be a sparse file, but we don't assert on
	// disk usage because that depends on the filesystem (APFS, ext4, NTFS
	// all handle sparse differently). The stat'd size is what matters.
}

// TestHardenDirectoryTarget verifies that calling truncate on a directory
// returns exit 1 with a clear error and does not panic.
func TestHardenDirectoryTarget(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	_, stderr, code := truncateRun(t, "truncate -s 0 subdir", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "subdir") {
		t.Errorf("stderr should mention subdir: %q", stderr)
	}
}

// TestHardenCreatePreservesMode verifies that newly-created files use a
// reasonable default mode (0644 minus umask). This guards against an
// accidental switch to 0666 or 0777.
func TestHardenCreatePreservesMode(t *testing.T) {
	dir := t.TempDir()
	_, _, code := truncateRun(t, "truncate -s 0 fresh.bin", dir)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	info, err := os.Stat(filepath.Join(dir, "fresh.bin"))
	if err != nil {
		t.Fatal(err)
	}
	mode := info.Mode().Perm()
	// On Unix the umask reduces the requested 0644 to whatever the test
	// process's umask permits. We require: no setuid/setgid/sticky bits,
	// no group-/world-writable bits, no execute bits.
	if mode&0o111 != 0 {
		t.Errorf("created file should not be executable: mode=%o", mode)
	}
	if mode&0o022 != 0 {
		t.Errorf("created file should not be group/world writable: mode=%o", mode)
	}
	if info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		t.Errorf("created file should have no special bits: mode=%o", info.Mode())
	}
}

// TestHardenLargeSizeRejected verifies that truncate refuses to multiply
// past int64 — a request that overflows the multiplier ceiling fails
// before reaching the kernel.
func TestHardenLargeSizeRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "abc")
	// 8388608T = 8 EiB which is one above MaxInt64; parseSize should
	// reject before the kernel sees it.
	_, stderr, code := truncateRun(t, "truncate -s 8388608T a.txt", dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1; stderr=%q", code, stderr)
	}
	if !strings.Contains(stderr, "invalid size") {
		t.Errorf("stderr missing 'invalid size': %q", stderr)
	}
}

// TestHardenZeroSizeOnAlreadyEmpty verifies that truncating an already-
// empty file is a no-op, not a failure.
func TestHardenZeroSizeOnAlreadyEmpty(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "empty.txt", "")
	_, stderr, code := truncateRun(t, "truncate -s 0 empty.txt", dir)
	if code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, stderr)
	}
	if got := fileSize(t, a); got != 0 {
		t.Errorf("size = %d, want 0", got)
	}
}

// TestHardenSpecialCharsInFilename verifies that filenames with embedded
// spaces and unicode are handled correctly when shell-quoted.
func TestHardenSpecialCharsInFilename(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "weird name with spaces and é.txt", "abcdef")
	_, _, code := truncateRun(t, `truncate -s 0 'weird name with spaces and é.txt'`, dir)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got := fileSize(t, a); got != 0 {
		t.Errorf("size = %d, want 0", got)
	}
}
