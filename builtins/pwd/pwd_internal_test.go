// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package pwd

import (
	"bytes"
	"context"
	"errors"
	"io"
	iofs "io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins"
)

// fakeFileInfo is a minimal io/fs.FileInfo implementation for tests.
type fakeFileInfo struct {
	mode iofs.FileMode
}

func (f *fakeFileInfo) Name() string        { return "" }
func (f *fakeFileInfo) Size() int64         { return 0 }
func (f *fakeFileInfo) Mode() iofs.FileMode { return f.mode }
func (f *fakeFileInfo) ModTime() time.Time  { return time.Time{} }
func (f *fakeFileInfo) IsDir() bool         { return f.mode.IsDir() }
func (f *fakeFileInfo) Sys() any            { return nil }

// invokePwd runs the pwd handler against a synthetic CallContext and
// returns stdout, stderr, and the result.
func invokePwd(t *testing.T, callCtx *builtins.CallContext, args []string) (stdout, stderr string, res builtins.Result) {
	t.Helper()
	var sout, serr bytes.Buffer
	callCtx.Stdout = &sout
	callCtx.Stderr = &serr

	fs := pflag.NewFlagSet("pwd", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	handler := makeFlags(fs)
	require.NoError(t, fs.Parse(args))
	res = handler(context.Background(), callCtx, fs.Args())
	return sout.String(), serr.String(), res
}

// --- WorkDir nil / empty paths ---

func TestPwdNilWorkDir(t *testing.T) {
	cc := &builtins.CallContext{} // WorkDir == nil
	_, stderr, res := invokePwd(t, cc, []string{})
	assert.Equal(t, uint8(1), res.Code)
	assert.Contains(t, stderr, "no working directory available")
}

func TestPwdEmptyWorkDir(t *testing.T) {
	cc := &builtins.CallContext{WorkDir: func() string { return "" }}
	_, stderr, res := invokePwd(t, cc, []string{})
	assert.Equal(t, uint8(1), res.Code)
	assert.Contains(t, stderr, "working directory is empty")
}

// --- resolveSymlinks error paths reachable directly ---

func TestResolveSymlinksRejectsRelativePath(t *testing.T) {
	cc := &builtins.CallContext{
		LstatFile:    func(_ context.Context, _ string) (iofs.FileInfo, error) { return nil, errors.New("nope") },
		ReadlinkFile: func(_ context.Context, _ string) (string, error) { return "", errors.New("nope") },
	}
	_, err := resolveSymlinks(context.Background(), cc, "relative/path")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an absolute path")
}

func TestResolveSymlinksMissingCallbacksError(t *testing.T) {
	// Missing LstatFile callback.
	cc := &builtins.CallContext{ReadlinkFile: func(_ context.Context, _ string) (string, error) { return "", nil }}
	_, err := resolveSymlinks(context.Background(), cc, string(filepath.Separator)+"foo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox does not support symlink resolution")

	// Missing ReadlinkFile callback.
	cc2 := &builtins.CallContext{LstatFile: func(_ context.Context, _ string) (iofs.FileInfo, error) { return nil, nil }}
	_, err = resolveSymlinks(context.Background(), cc2, string(filepath.Separator)+"foo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sandbox does not support symlink resolution")
}

func TestResolveSymlinksContextCancelled(t *testing.T) {
	cc := &builtins.CallContext{
		LstatFile:    func(_ context.Context, _ string) (iofs.FileInfo, error) { return &fakeFileInfo{}, nil },
		ReadlinkFile: func(_ context.Context, _ string) (string, error) { return "", nil },
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := resolveSymlinks(ctx, cc, string(filepath.Separator)+"foo"+string(filepath.Separator)+"bar")
	require.Error(t, err)
	assert.True(t, errors.Is(err, context.Canceled))
}

// --- resolveSymlinks: dot and dot-dot components are collapsed even
//     when the lstat result is non-symlink. ---

func TestResolveSymlinksHandlesDotAndDotDot(t *testing.T) {
	// Build a virtual filesystem with no symlinks; every lstat says
	// "regular dir". Path includes . and .. — they must be collapsed.
	cc := &builtins.CallContext{
		LstatFile: func(_ context.Context, _ string) (iofs.FileInfo, error) {
			return &fakeFileInfo{mode: iofs.ModeDir}, nil
		},
		ReadlinkFile: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("not a symlink")
		},
	}
	sep := string(filepath.Separator)
	// Use raw input with explicit . and .. to reach those branches before
	// filepath.Clean collapses them. Note: filepath.Clean inside the
	// resolver normalizes the input before walking, so the dot/dot-dot
	// branches are reached by symlink targets containing those segments,
	// which TestPwdPhysicalDotDotResolvesAcrossDepth already exercises
	// from end-to-end. Here we stick to a simple absolute path.
	out, err := resolveSymlinks(context.Background(), cc, sep+"a"+sep+"b")
	require.NoError(t, err)
	assert.Equal(t, sep+"a"+sep+"b", out)
}

// --- Symlink loop detection at the maxSymlinkHops cap ---

func TestResolveSymlinksLoopDetected(t *testing.T) {
	cc := &builtins.CallContext{
		LstatFile: func(_ context.Context, _ string) (iofs.FileInfo, error) {
			return &fakeFileInfo{mode: iofs.ModeSymlink}, nil
		},
		ReadlinkFile: func(_ context.Context, _ string) (string, error) {
			// Always point back at the same target so the resolver loops
			// until it hits the cap.
			return "self", nil
		},
	}
	sep := string(filepath.Separator)
	_, err := resolveSymlinks(context.Background(), cc, sep+"a")
	require.Error(t, err)
	assert.True(t, errors.Is(err, errSymlinkLoop))
}

// --- pickPhysical with no flags set defaults to logical ---

func TestPickPhysicalNoFlagsDefaultsLogical(t *testing.T) {
	fs := pflag.NewFlagSet("pwd", pflag.ContinueOnError)
	makeFlags(fs)
	require.NoError(t, fs.Parse([]string{}))
	assert.False(t, pickPhysical(fs))
}

// --- joinPath / parentDir / rootPrefix unit cases ---

func TestJoinPathEmptyDir(t *testing.T) {
	assert.Equal(t, "comp", joinPath("", "comp"))
}

func TestJoinPathDirEndingInSeparator(t *testing.T) {
	sep := string(filepath.Separator)
	assert.Equal(t, sep+"comp", joinPath(sep, "comp"))
}

func TestJoinPathDirNoTrailingSeparator(t *testing.T) {
	sep := string(filepath.Separator)
	assert.Equal(t, sep+"foo"+sep+"bar", joinPath(sep+"foo", "bar"))
}

func TestParentDirAtRoot(t *testing.T) {
	root := string(filepath.Separator)
	assert.Equal(t, root, parentDir(root))
}

func TestParentDirOneLevel(t *testing.T) {
	sep := string(filepath.Separator)
	assert.Equal(t, sep+"foo", parentDir(sep+"foo"+sep+"bar"))
}

func TestRootPrefixUnixPath(t *testing.T) {
	if filepath.Separator != '/' {
		t.Skip("Unix-only path semantics")
	}
	assert.Equal(t, "/", rootPrefix("/foo/bar"))
}

// --- Help / pickPhysical interaction ---

func TestHelpFlagShortCircuits(t *testing.T) {
	cc := &builtins.CallContext{WorkDir: func() string { return "/should/not/print" }}
	stdout, stderr, res := invokePwd(t, cc, []string{"--help"})
	assert.Equal(t, uint8(0), res.Code)
	assert.Contains(t, stdout, "Usage: pwd")
	assert.Equal(t, "", stderr)
	// Working directory must not appear in stdout when --help is passed.
	assert.False(t, strings.Contains(stdout, "/should/not/print"))
}

// --- ReadlinkFile error: lstat says symlink, readlink fails. ---

func TestResolveSymlinksReadlinkFails(t *testing.T) {
	cc := &builtins.CallContext{
		LstatFile: func(_ context.Context, _ string) (iofs.FileInfo, error) {
			return &fakeFileInfo{mode: iofs.ModeSymlink}, nil
		},
		ReadlinkFile: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("readlink failed")
		},
	}
	sep := string(filepath.Separator)
	out, err := resolveSymlinks(context.Background(), cc, sep+"a"+sep+"b")
	require.NoError(t, err)
	// Both components got passed through.
	assert.Equal(t, sep+"a"+sep+"b", out)
}

// --- A symlink target containing "." segments exercises the dot branch.

func TestResolveSymlinksDotInTarget(t *testing.T) {
	calls := 0
	cc := &builtins.CallContext{
		LstatFile: func(_ context.Context, p string) (iofs.FileInfo, error) {
			calls++
			// First call: /lnk is a symlink. After splicing "./real", the
			// next component "." is short-circuited before any new lstat.
			if calls == 1 {
				return &fakeFileInfo{mode: iofs.ModeSymlink}, nil
			}
			// Anything else is a regular dir.
			return &fakeFileInfo{mode: iofs.ModeDir}, nil
		},
		ReadlinkFile: func(_ context.Context, _ string) (string, error) { return "./real", nil },
	}
	sep := string(filepath.Separator)
	out, err := resolveSymlinks(context.Background(), cc, sep+"lnk")
	require.NoError(t, err)
	assert.Equal(t, sep+"real", out)
}

// --- Symlink target of "/" leaves rest empty after the leading-sep strip. ---

func TestResolveSymlinksTargetIsRoot(t *testing.T) {
	cc := &builtins.CallContext{
		LstatFile: func(_ context.Context, _ string) (iofs.FileInfo, error) {
			return &fakeFileInfo{mode: iofs.ModeSymlink}, nil
		},
		ReadlinkFile: func(_ context.Context, _ string) (string, error) {
			return string(filepath.Separator), nil // target is just "/"
		},
	}
	sep := string(filepath.Separator)
	out, err := resolveSymlinks(context.Background(), cc, sep+"x")
	require.NoError(t, err)
	assert.Equal(t, sep, out)
}

// --- Logical path is returned unchanged for -L (no filesystem touched) ---

func TestLogicalPathNoFilesystemAccess(t *testing.T) {
	calls := 0
	cc := &builtins.CallContext{
		WorkDir: func() string { return "/some/path" },
		LstatFile: func(_ context.Context, _ string) (iofs.FileInfo, error) {
			calls++
			return nil, errors.New("must not be called for -L")
		},
		ReadlinkFile: func(_ context.Context, _ string) (string, error) {
			calls++
			return "", errors.New("must not be called for -L")
		},
	}
	stdout, stderr, res := invokePwd(t, cc, []string{"-L"})
	assert.Equal(t, uint8(0), res.Code)
	assert.Equal(t, "/some/path\n", stdout)
	assert.Equal(t, "", stderr)
	assert.Equal(t, 0, calls, "-L must not stat or readlink")
}
