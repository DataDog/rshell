// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package find

import (
	"context"
	"io"
	iofs "io/fs"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/rshell/builtins"
	"github.com/stretchr/testify/assert"
)

// TestCompareSizeOverflow verifies overflow-safe ceiling division.
func TestCompareSizeOverflow(t *testing.T) {
	tests := []struct {
		name     string
		fileSize int64
		su       sizeUnit
		matched  bool
	}{
		// Normal cases
		{"0 bytes exact 0c", 0, sizeUnit{n: 0, cmp: cmpExact, unit: 'c'}, true},
		{"1 byte exact 1c", 1, sizeUnit{n: 1, cmp: cmpExact, unit: 'c'}, true},
		{"512 bytes exact 1b", 512, sizeUnit{n: 1, cmp: cmpExact, unit: 'b'}, true},
		{"1 byte rounds up to 1 block", 1, sizeUnit{n: 1, cmp: cmpExact, unit: 'b'}, true},
		{"513 bytes rounds up to 2 blocks", 513, sizeUnit{n: 2, cmp: cmpExact, unit: 'b'}, true},

		// Edge: zero-byte file
		{"0 bytes +0c", 0, sizeUnit{n: 0, cmp: cmpMore, unit: 'c'}, false},
		{"0 bytes -1c", 0, sizeUnit{n: 1, cmp: cmpLess, unit: 'c'}, true},

		// Large files near MaxInt64 (overflow protection)
		{"MaxInt64 bytes +0c", 1<<63 - 1, sizeUnit{n: 0, cmp: cmpMore, unit: 'c'}, true},
		{"MaxInt64 bytes exact in blocks", 1<<63 - 1, sizeUnit{n: (1<<63 - 1) / 512, cmp: cmpMore, unit: 'b'}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareSize(tt.fileSize, tt.su)
			assert.Equal(t, tt.matched, got)
		})
	}
}

// TestEvalEmpty verifies the -empty predicate for directories, regular files,
// and other file types. Scenario tests cannot create empty dirs (setup.files
// requires a file), so directory emptiness must be tested here.
func TestEvalEmpty(t *testing.T) {
	t.Run("empty directory matches", func(t *testing.T) {
		called := false
		ec := &evalContext{
			ctx:       context.Background(),
			info:      &fakeFileInfo{isDir: true},
			printPath: "emptydir",
			callCtx: &builtins.CallContext{
				Stderr: io.Discard,
				IsDirEmpty: func(_ context.Context, _ string) (bool, error) {
					called = true
					return true, nil
				},
			},
		}
		assert.True(t, evalEmpty(ec), "empty directory should match -empty")
		assert.True(t, called, "IsDirEmpty must be called for directories")
	})

	t.Run("non-empty directory does not match", func(t *testing.T) {
		ec := &evalContext{
			ctx:       context.Background(),
			info:      &fakeFileInfo{isDir: true},
			printPath: "nonemptydir",
			callCtx: &builtins.CallContext{
				Stderr: io.Discard,
				IsDirEmpty: func(_ context.Context, _ string) (bool, error) {
					return false, nil
				},
			},
		}
		assert.False(t, evalEmpty(ec), "non-empty directory should not match -empty")
	})

	t.Run("IsDirEmpty receives correct path", func(t *testing.T) {
		var gotPath string
		ec := &evalContext{
			ctx:       context.Background(),
			info:      &fakeFileInfo{isDir: true},
			printPath: "some/nested/dir",
			callCtx: &builtins.CallContext{
				Stderr: io.Discard,
				IsDirEmpty: func(_ context.Context, path string) (bool, error) {
					gotPath = path
					return true, nil
				},
			},
		}
		evalEmpty(ec)
		assert.Equal(t, "some/nested/dir", gotPath, "IsDirEmpty should receive printPath")
	})

	t.Run("IsDirEmpty error sets failed and returns false", func(t *testing.T) {
		var stderr strings.Builder
		ec := &evalContext{
			ctx:       context.Background(),
			info:      &fakeFileInfo{isDir: true},
			printPath: "baddir",
			callCtx: &builtins.CallContext{
				Stderr: &stderr,
				IsDirEmpty: func(_ context.Context, _ string) (bool, error) {
					return false, &iofs.PathError{Op: "readdir", Path: "baddir", Err: iofs.ErrPermission}
				},
				PortableErr: func(err error) string { return err.Error() },
			},
		}
		assert.False(t, evalEmpty(ec), "error should return false")
		assert.True(t, ec.failed, "error should set failed flag")
		assert.Contains(t, stderr.String(), "baddir", "error should mention the path on stderr")
	})

	t.Run("empty regular file matches", func(t *testing.T) {
		ec := &evalContext{
			ctx:  context.Background(),
			info: &fakeFileInfo{size: 0, isDir: false},
		}
		assert.True(t, evalEmpty(ec), "zero-byte regular file should match -empty")
	})

	t.Run("non-empty regular file does not match", func(t *testing.T) {
		ec := &evalContext{
			ctx:  context.Background(),
			info: &fakeFileInfo{size: 42, isDir: false},
		}
		assert.False(t, evalEmpty(ec), "non-empty regular file should not match -empty")
	})

	t.Run("symlink does not match", func(t *testing.T) {
		ec := &evalContext{
			ctx:  context.Background(),
			info: &fakeFileInfo{mode: iofs.ModeSymlink},
		}
		assert.False(t, evalEmpty(ec), "symlink should not match -empty")
	})

	t.Run("IsDirEmpty not called for regular files", func(t *testing.T) {
		called := false
		ec := &evalContext{
			ctx:  context.Background(),
			info: &fakeFileInfo{size: 0, isDir: false},
			callCtx: &builtins.CallContext{
				IsDirEmpty: func(_ context.Context, _ string) (bool, error) {
					called = true
					return true, nil
				},
			},
		}
		evalEmpty(ec)
		assert.False(t, called, "IsDirEmpty should not be called for regular files")
	})
}

// fakeDirEntry implements a minimal fs.DirEntry for testing.
type fakeDirEntry struct{}

func (fakeDirEntry) Name() string                 { return "file.txt" }
func (fakeDirEntry) IsDir() bool                  { return false }
func (fakeDirEntry) Type() iofs.FileMode          { return 0 }
func (fakeDirEntry) Info() (iofs.FileInfo, error) { return nil, nil }

// fakeFileInfo implements the minimal fs.FileInfo interface for testing.
type fakeFileInfo struct {
	modTime time.Time
	size    int64
	isDir   bool
	mode    iofs.FileMode // when set, Mode() returns this directly
}

func (f *fakeFileInfo) Name() string       { return "fake" }
func (f *fakeFileInfo) Size() int64        { return f.size }
func (f *fakeFileInfo) ModTime() time.Time { return f.modTime }
func (f *fakeFileInfo) IsDir() bool        { return f.isDir }
func (f *fakeFileInfo) Sys() any           { return nil }

// Mode returns a basic file mode for testing. If mode is explicitly set,
// it is returned directly; otherwise a default is derived from isDir.
func (f *fakeFileInfo) Mode() iofs.FileMode {
	if f.mode != 0 {
		return f.mode
	}
	if f.isDir {
		return iofs.ModeDir | 0o755
	}
	return 0o644
}
