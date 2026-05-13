// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedpaths

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeDirEntry is a minimal fs.DirEntry for testing CollectDirEntries.
type fakeDirEntry struct {
	name string
}

func (f fakeDirEntry) Name() string               { return f.name }
func (f fakeDirEntry) IsDir() bool                { return false }
func (f fakeDirEntry) Type() fs.FileMode          { return 0 }
func (f fakeDirEntry) Info() (fs.FileInfo, error) { return fakeFileInfo{name: f.name}, nil }

type fakeFileInfo struct{ name string }

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() fs.FileMode  { return 0644 }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }

func TestSandboxWriteAllowedPath(t *testing.T) {
	dir := t.TempDir()

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	// O_CREATE|O_WRONLY for a new file inside the allowlist should succeed
	// and the file's contents should reflect the write.
	f, err := sb.Open("created.txt", dir, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	require.NoError(t, err)
	n, err := f.Write([]byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, 5, n)
	require.NoError(t, f.Close())

	got, err := os.ReadFile(filepath.Join(dir, "created.txt"))
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got))
}

func TestSandboxWriteOutsideAllowedPath(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()

	sb, _, err := New([]string{allowed})
	require.NoError(t, err)
	defer sb.Close()

	// Absolute path outside the allowlist must be rejected for writes.
	target := filepath.Join(outside, "should-not-exist.txt")
	f, err := sb.Open(target, allowed, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	assert.Nil(t, f)
	assert.ErrorIs(t, err, os.ErrPermission)

	_, statErr := os.Stat(target)
	assert.True(t, os.IsNotExist(statErr), "file outside allowlist must not be created")
}

func TestSandboxAppend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.txt")
	require.NoError(t, os.WriteFile(path, []byte("first\n"), 0644))

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	f, err := sb.Open("log.txt", dir, os.O_WRONLY|os.O_APPEND, 0)
	require.NoError(t, err)
	_, err = f.Write([]byte("second\n"))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "first\nsecond\n", string(got))
}

func TestSandboxTruncate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.txt")
	require.NoError(t, os.WriteFile(path, []byte("original-content"), 0644))

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	f, err := sb.Open("data.txt", dir, os.O_WRONLY|os.O_TRUNC, 0)
	require.NoError(t, err)
	_, err = f.Write([]byte("short"))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "short", string(got), "O_TRUNC must replace, not append to, the original content")
}

// TestSandboxWriteThroughSymlinkEscapeRejected ensures the cross-root
// symlink fallback is not used for write opens. Following a symlink that
// escapes its os.Root and then performing a create or truncate is the
// classic TOCTOU footgun: the link target can be swapped between
// resolution and open. Writes must stay inside a single os.Root.
func TestSandboxWriteThroughSymlinkEscapeRejected(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()

	// Symlink inside the allowlist that points to a path *outside* the
	// allowlist. os.Root will reject this with a path-escape error;
	// the sandbox must then refuse to fall back for writes.
	linkPath := filepath.Join(allowed, "escape")
	require.NoError(t, os.Symlink(filepath.Join(outside, "target.txt"), linkPath))

	sb, _, err := New([]string{allowed})
	require.NoError(t, err)
	defer sb.Close()

	f, err := sb.Open("escape", allowed, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	assert.Nil(t, f)
	assert.Error(t, err)

	_, statErr := os.Stat(filepath.Join(outside, "target.txt"))
	assert.True(t, os.IsNotExist(statErr), "symlink target must not be created outside the sandbox")
}

func TestSandboxWriteRejectsUnknownFlag(t *testing.T) {
	dir := t.TempDir()

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	// Pick a high bit that is not in allowedOpenFlags. We OR with
	// O_WRONLY so the access mode itself is valid; only the unknown bit
	// should trigger rejection.
	const unknownFlag = 1 << 30
	f, err := sb.Open("x.txt", dir, os.O_WRONLY|os.O_CREATE|unknownFlag, 0644)
	assert.Nil(t, f)
	assert.ErrorIs(t, err, os.ErrPermission)
}

// TestSandboxTruncateMethodShrink covers the happy path of the new
// Sandbox.Truncate API: an existing file in an allowed root is shrunk to
// the requested size, leaving the leading bytes intact.
func TestSandboxTruncateMethodShrink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.txt")
	require.NoError(t, os.WriteFile(path, []byte("0123456789"), 0644))

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	require.NoError(t, sb.Truncate("log.txt", dir, 4, false))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "0123", string(got))
}

// TestSandboxTruncateMethodExtend covers the case where SIZE is larger
// than the current file: the file is zero-extended.
func TestSandboxTruncateMethodExtend(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.txt")
	require.NoError(t, os.WriteFile(path, []byte("abc"), 0644))

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	require.NoError(t, sb.Truncate("log.txt", dir, 1024, false))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, int64(1024), info.Size())
}

// TestSandboxTruncateMethodCreates covers the create-by-default behaviour
// used when truncate is invoked without -c.
func TestSandboxTruncateMethodCreates(t *testing.T) {
	dir := t.TempDir()

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	require.NoError(t, sb.Truncate("fresh.txt", dir, 100, true))

	info, err := os.Stat(filepath.Join(dir, "fresh.txt"))
	require.NoError(t, err)
	assert.Equal(t, int64(100), info.Size())
}

// TestSandboxTruncateMethodNoCreate covers create=false: the call returns
// os.ErrNotExist for missing files (the truncate -c silent-skip path
// depends on errors.Is matching).
func TestSandboxTruncateMethodNoCreate(t *testing.T) {
	dir := t.TempDir()

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	err = sb.Truncate("missing.txt", dir, 0, false)
	assert.ErrorIs(t, err, fs.ErrNotExist)
	_, statErr := os.Stat(filepath.Join(dir, "missing.txt"))
	assert.True(t, os.IsNotExist(statErr), "no-create must not create missing.txt")
}

// TestSandboxTruncateMethodOutsideAllowedPath verifies that paths outside
// the sandbox are rejected with a permission error before any I/O.
func TestSandboxTruncateMethodOutsideAllowedPath(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "log.txt")
	require.NoError(t, os.WriteFile(target, []byte("untouched"), 0644))

	sb, _, err := New([]string{allowed})
	require.NoError(t, err)
	defer sb.Close()

	err = sb.Truncate(target, allowed, 0, true)
	assert.ErrorIs(t, err, os.ErrPermission)

	got, ferr := os.ReadFile(target)
	require.NoError(t, ferr)
	assert.Equal(t, "untouched", string(got), "outside file must not be touched")
}

// TestSandboxTruncateMethodNegativeSize verifies that negative sizes are
// rejected with EINVAL.
func TestSandboxTruncateMethodNegativeSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.txt")
	require.NoError(t, os.WriteFile(path, []byte("data"), 0644))

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	err = sb.Truncate("log.txt", dir, -1, false)
	assert.Error(t, err)
	got, ferr := os.ReadFile(path)
	require.NoError(t, ferr)
	assert.Equal(t, "data", string(got), "negative size must not modify the file")
}

// TestSandboxTruncateMethodSymlinkEscapeRejected mirrors
// TestSandboxWriteThroughSymlinkEscapeRejected for the new API: writes
// must not follow a symlink that escapes the os.Root, even via the
// Truncate code path.
func TestSandboxTruncateMethodSymlinkEscapeRejected(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()
	linkPath := filepath.Join(allowed, "escape")
	target := filepath.Join(outside, "target.txt")
	require.NoError(t, os.WriteFile(target, []byte("untouched"), 0644))
	require.NoError(t, os.Symlink(target, linkPath))

	sb, _, err := New([]string{allowed})
	require.NoError(t, err)
	defer sb.Close()

	err = sb.Truncate("escape", allowed, 0, true)
	assert.Error(t, err)

	got, ferr := os.ReadFile(target)
	require.NoError(t, ferr)
	assert.Equal(t, "untouched", string(got), "symlink target must not be reachable for writes")
}

// TestSandboxTruncateIfLargerAboveThreshold verifies that a file at or
// above minSize is truncated to newSize and the pre-size is reported.
func TestSandboxTruncateIfLargerAboveThreshold(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.txt")
	require.NoError(t, os.WriteFile(path, []byte("0123456789"), 0644))

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	sizeBefore, truncated, err := sb.TruncateIfLarger("log.txt", dir, 5, 0, false)
	require.NoError(t, err)
	assert.Equal(t, int64(10), sizeBefore)
	assert.True(t, truncated)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, int64(0), info.Size())
}

// TestSandboxTruncateIfLargerBelowThreshold verifies that a file smaller
// than minSize is left untouched and (size, false, nil) is returned.
func TestSandboxTruncateIfLargerBelowThreshold(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.txt")
	require.NoError(t, os.WriteFile(path, []byte("abc"), 0644))

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	sizeBefore, truncated, err := sb.TruncateIfLarger("log.txt", dir, 1024, 0, false)
	require.NoError(t, err)
	assert.Equal(t, int64(3), sizeBefore)
	assert.False(t, truncated)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "abc", string(got), "below-threshold file must not be modified")
}

// TestSandboxTruncateIfLargerZeroMinSize verifies that minSize == 0
// is equivalent to Truncate: the file is always truncated.
func TestSandboxTruncateIfLargerZeroMinSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.txt")
	require.NoError(t, os.WriteFile(path, []byte("xyz"), 0644))

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	sizeBefore, truncated, err := sb.TruncateIfLarger("log.txt", dir, 0, 0, false)
	require.NoError(t, err)
	assert.Equal(t, int64(3), sizeBefore)
	assert.True(t, truncated)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, int64(0), info.Size())
}

// TestSandboxTruncateIfLargerOutsideAllowedPath verifies that paths
// outside the sandbox are rejected with permission denied before any I/O.
func TestSandboxTruncateIfLargerOutsideAllowedPath(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "log.txt")
	require.NoError(t, os.WriteFile(target, []byte("untouched"), 0644))

	sb, _, err := New([]string{allowed})
	require.NoError(t, err)
	defer sb.Close()

	_, _, err = sb.TruncateIfLarger(target, allowed, 0, 0, false)
	assert.ErrorIs(t, err, os.ErrPermission)

	got, ferr := os.ReadFile(target)
	require.NoError(t, ferr)
	assert.Equal(t, "untouched", string(got))
}

// TestSandboxTruncateIfLargerNoCreate verifies that missing files surface
// os.ErrNotExist when create=false.
func TestSandboxTruncateIfLargerNoCreate(t *testing.T) {
	dir := t.TempDir()

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	_, _, err = sb.TruncateIfLarger("missing.txt", dir, 0, 0, false)
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestSandboxOpenReadStillWorks(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.txt"), []byte("data"), 0644))

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	f, err := sb.Open("test.txt", dir, os.O_RDONLY, 0)
	require.NoError(t, err)
	f.Close()
}

func TestReadDirLimited(t *testing.T) {
	dir := t.TempDir()

	// Create 10 files.
	for i := range 10 {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d", i)), nil, 0644))
	}

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	t.Run("maxRead below count returns truncated with first N entries", func(t *testing.T) {
		entries, truncated, err := sb.ReadDirLimited(".", dir, 0, 5)
		require.NoError(t, err)
		assert.True(t, truncated)
		assert.Len(t, entries, 5)
		// Entries are sorted within the read window.
		for i := 1; i < len(entries); i++ {
			assert.True(t, entries[i-1].Name() < entries[i].Name(), "entries should be sorted")
		}
	})

	t.Run("maxRead above count returns all entries not truncated", func(t *testing.T) {
		entries, truncated, err := sb.ReadDirLimited(".", dir, 0, 20)
		require.NoError(t, err)
		assert.False(t, truncated)
		assert.Len(t, entries, 10)
	})

	t.Run("empty directory", func(t *testing.T) {
		emptyDir := filepath.Join(dir, "empty")
		require.NoError(t, os.Mkdir(emptyDir, 0755))

		entries, truncated, err := sb.ReadDirLimited("empty", dir, 0, 10)
		require.NoError(t, err)
		assert.False(t, truncated)
		assert.Empty(t, entries)
	})

	t.Run("path outside sandbox returns permission error", func(t *testing.T) {
		outsideDir := t.TempDir()
		_, _, err := sb.ReadDirLimited(outsideDir, dir, 0, 10)
		require.Error(t, err)
		assert.ErrorIs(t, err, os.ErrPermission)
	})

	t.Run("io.EOF is not returned as error", func(t *testing.T) {
		// Use a fresh directory to avoid interference from other subtests.
		eofDir := filepath.Join(dir, "eoftest")
		require.NoError(t, os.Mkdir(eofDir, 0755))
		for i := range 5 {
			require.NoError(t, os.WriteFile(filepath.Join(eofDir, fmt.Sprintf("g%02d", i)), nil, 0644))
		}
		entries, truncated, err := sb.ReadDirLimited("eoftest", dir, 0, 1000)
		require.NoError(t, err, "io.EOF should not be returned as error")
		assert.False(t, truncated)
		assert.Len(t, entries, 5)
	})

	t.Run("non-positive maxRead returns empty", func(t *testing.T) {
		entries, truncated, err := sb.ReadDirLimited(".", dir, 0, 0)
		require.NoError(t, err)
		assert.False(t, truncated)
		assert.Empty(t, entries)

		entries, truncated, err = sb.ReadDirLimited(".", dir, 0, -5)
		require.NoError(t, err)
		assert.False(t, truncated)
		assert.Empty(t, entries)
	})

	t.Run("offset skips entries", func(t *testing.T) {
		// Use a fresh directory to avoid interference from other subtests.
		offsetDir := filepath.Join(dir, "offsettest")
		require.NoError(t, os.Mkdir(offsetDir, 0755))
		for i := range 10 {
			require.NoError(t, os.WriteFile(filepath.Join(offsetDir, fmt.Sprintf("h%02d", i)), nil, 0644))
		}

		// Read all 10 entries with no offset for reference.
		all, _, err := sb.ReadDirLimited("offsettest", dir, 0, 100)
		require.NoError(t, err)
		assert.Len(t, all, 10)

		// Skip first 5 entries, read up to 100.
		entries, truncated, err := sb.ReadDirLimited("offsettest", dir, 5, 100)
		require.NoError(t, err)
		assert.False(t, truncated)
		assert.Len(t, entries, 5, "should return remaining 5 entries after skipping 5")
	})

	t.Run("offset beyond count returns empty", func(t *testing.T) {
		entries, truncated, err := sb.ReadDirLimited("offsettest", dir, 100, 10)
		require.NoError(t, err)
		assert.False(t, truncated)
		assert.Empty(t, entries)
	})

	t.Run("offset plus maxRead with truncation", func(t *testing.T) {
		// Skip 3, read 3 out of 10 => should get 3 entries, truncated.
		entries, truncated, err := sb.ReadDirLimited("offsettest", dir, 3, 3)
		require.NoError(t, err)
		assert.True(t, truncated, "should be truncated since 10 - 3 > 3")
		assert.Len(t, entries, 3)
		// Entries should be sorted within the window.
		for i := 1; i < len(entries); i++ {
			assert.True(t, entries[i-1].Name() < entries[i].Name(), "entries should be sorted")
		}
	})

	t.Run("negative offset clamped to zero", func(t *testing.T) {
		entries, truncated, err := sb.ReadDirLimited("offsettest", dir, -10, 100)
		require.NoError(t, err)
		assert.False(t, truncated)
		assert.Len(t, entries, 10, "negative offset should be treated as 0")
	})
}

func TestReadDirNCapExceeded(t *testing.T) {
	dir := t.TempDir()

	// Create 4 files so the directory exceeds a cap of 3.
	for i := range 4 {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d", i)), nil, 0644))
	}

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	t.Run("returns error when entries exceed cap", func(t *testing.T) {
		entries, err := sb.readDirN(".", dir, 3)
		assert.Nil(t, entries)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "too many entries")
	})

	t.Run("returns entries when count equals cap", func(t *testing.T) {
		entries, err := sb.readDirN(".", dir, 4)
		require.NoError(t, err)
		assert.Len(t, entries, 4)
	})

	t.Run("returns entries when count is below cap", func(t *testing.T) {
		entries, err := sb.readDirN(".", dir, 10)
		require.NoError(t, err)
		assert.Len(t, entries, 4)
	})
}

func TestCollectDirEntries(t *testing.T) {
	makeEntries := func(names ...string) []fs.DirEntry {
		out := make([]fs.DirEntry, len(names))
		for i, n := range names {
			out[i] = fakeDirEntry{name: n}
		}
		return out
	}

	t.Run("error in same batch as truncation is preserved", func(t *testing.T) {
		ioErr := errors.New("disk I/O error")
		callCount := 0
		reader := func(n int) ([]fs.DirEntry, error) {
			callCount++
			if callCount == 1 {
				return makeEntries("f01", "f02", "f03", "f04", "f05"), nil
			}
			return makeEntries("f06", "f07", "f08"), ioErr
		}

		entries, truncated, err := CollectDirEntries(reader, 10, 0, 6)
		assert.True(t, truncated, "should be truncated")
		assert.Len(t, entries, 6, "should trim to maxRead")
		assert.ErrorIs(t, err, ioErr, "I/O error must be preserved even when truncation occurs")
	})

	t.Run("EOF is not returned as error", func(t *testing.T) {
		callCount := 0
		reader := func(n int) ([]fs.DirEntry, error) {
			callCount++
			if callCount == 1 {
				return makeEntries("f01", "f02"), io.EOF
			}
			return nil, io.EOF
		}

		entries, truncated, err := CollectDirEntries(reader, 10, 0, 100)
		assert.False(t, truncated)
		assert.Len(t, entries, 2)
		assert.NoError(t, err, "io.EOF should not be returned as error")
	})

	t.Run("offset skips entries across batches", func(t *testing.T) {
		callCount := 0
		reader := func(n int) ([]fs.DirEntry, error) {
			callCount++
			if callCount == 1 {
				return makeEntries("f01", "f02", "f03"), nil
			}
			if callCount == 2 {
				return makeEntries("f04", "f05"), io.EOF
			}
			return nil, io.EOF
		}

		entries, truncated, err := CollectDirEntries(reader, 10, 2, 100)
		assert.False(t, truncated)
		assert.NoError(t, err)
		assert.Len(t, entries, 3, "should skip first 2, return remaining 3")
		assert.Equal(t, "f03", entries[0].Name())
		assert.Equal(t, "f04", entries[1].Name())
		assert.Equal(t, "f05", entries[2].Name())
	})

	t.Run("error without truncation is preserved", func(t *testing.T) {
		ioErr := errors.New("permission denied")
		reader := func(n int) ([]fs.DirEntry, error) {
			return makeEntries("f01", "f02"), ioErr
		}

		entries, truncated, err := CollectDirEntries(reader, 10, 0, 100)
		assert.False(t, truncated)
		assert.Len(t, entries, 2)
		assert.ErrorIs(t, err, ioErr)
	})

	t.Run("entries are sorted by name", func(t *testing.T) {
		reader := func(n int) ([]fs.DirEntry, error) {
			return makeEntries("cherry", "apple", "banana"), io.EOF
		}

		entries, truncated, err := CollectDirEntries(reader, 10, 0, 100)
		assert.False(t, truncated)
		assert.NoError(t, err)
		assert.Equal(t, "apple", entries[0].Name())
		assert.Equal(t, "banana", entries[1].Name())
		assert.Equal(t, "cherry", entries[2].Name())
	})
}

func TestNewSkipsNonexistentPaths(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.txt"), []byte("data"), 0644))

	sb, _, err := New([]string{"/nonexistent/path", dir})
	require.NoError(t, err, "nonexistent paths should be skipped")
	defer sb.Close()

	// The existing directory should still be accessible.
	f, err := sb.Open(filepath.Join(dir, "test.txt"), dir, os.O_RDONLY, 0)
	require.NoError(t, err)
	f.Close()
}

func TestNewAllPathsNonexistent(t *testing.T) {
	sb, _, err := New([]string{"/does/not/exist", "/also/missing"})
	require.NoError(t, err, "all-nonexistent paths should succeed with empty sandbox")
	defer sb.Close()

	// Sandbox should block all access.
	_, err = sb.Stat("/tmp", "/tmp")
	assert.ErrorIs(t, err, os.ErrPermission)
}

func TestNewEmptyPaths(t *testing.T) {
	sb, _, err := New([]string{})
	require.NoError(t, err, "empty path list should succeed")
	defer sb.Close()

	// Sandbox should block all access.
	_, err = sb.Stat("/tmp", "/tmp")
	assert.ErrorIs(t, err, os.ErrPermission)
}

func TestNewMixedExistingAndNonexistent(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dirA, "a.txt"), []byte("aaa"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dirB, "b.txt"), []byte("bbb"), 0644))

	sb, _, err := New([]string{dirA, "/nonexistent", dirB})
	require.NoError(t, err, "nonexistent path between valid dirs should be skipped")
	defer sb.Close()

	// Both existing directories should be accessible.
	f, err := sb.Open(filepath.Join(dirA, "a.txt"), dirA, os.O_RDONLY, 0)
	require.NoError(t, err, "first existing dir should work")
	f.Close()

	f, err = sb.Open(filepath.Join(dirB, "b.txt"), dirB, os.O_RDONLY, 0)
	require.NoError(t, err, "second existing dir should work")
	f.Close()
}
