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

func TestSandboxOpenRejectsWriteFlags(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.txt"), []byte("data"), 0644))

	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	writeFlags := []int{
		os.O_WRONLY,
		os.O_RDWR,
		os.O_APPEND,
		os.O_CREATE,
		os.O_TRUNC,
		os.O_WRONLY | os.O_CREATE | os.O_TRUNC,
	}
	for _, flag := range writeFlags {
		f, err := sb.Open("test.txt", dir, flag, 0644)
		assert.Nil(t, f, "open with flag %d should return nil", flag)
		assert.ErrorIs(t, err, os.ErrPermission, "open with flag %d should be denied", flag)
	}

	// Read-only should still work.
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

	sb, err := New([]string{dir})
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
