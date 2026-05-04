// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package cd

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/rshell/builtins"
)

// --- lpFlag ---

func TestLPFlagSetTrue(t *testing.T) {
	var last cdMode
	f := newLPFlag(&last, modePhysical)
	assert.NoError(t, f.Set(""))
	assert.Equal(t, modePhysical, last)
	assert.Equal(t, "true", f.String())

	assert.NoError(t, f.Set("true"))
	assert.Equal(t, "true", f.String())
}

func TestLPFlagSetFalseDoesNotUpdateLast(t *testing.T) {
	var last cdMode
	f := newLPFlag(&last, modeLogical)
	assert.NoError(t, f.Set("false"))
	// Setting the flag to false does not claim last-wins precedence —
	// `--logical=false` is not a request for logical handling.
	assert.Equal(t, modeUnset, last)
	assert.Equal(t, "false", f.String())
}

func TestLPFlagSetFalseClearsActive(t *testing.T) {
	var last cdMode
	f := newLPFlag(&last, modePhysical)
	assert.NoError(t, f.Set("true"))
	assert.Equal(t, modePhysical, last)
	assert.NoError(t, f.Set("false"))
	// Explicit deactivation of the active flag returns to default.
	assert.Equal(t, modeUnset, last)
}

func TestLPFlagSetInvalid(t *testing.T) {
	var last cdMode
	f := newLPFlag(&last, modeLogical)
	err := f.Set("not-a-bool")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid boolean value")
}

func TestLPFlagType(t *testing.T) {
	var last cdMode
	f := newLPFlag(&last, modeLogical)
	assert.Equal(t, "bool", f.Type())
	assert.True(t, f.IsBoolFlag())
}

func TestIsReservedWindowsPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		// Non-Windows: function is always false.
		assert.False(t, isReservedWindowsPath("CON"))
		assert.False(t, isReservedWindowsPath("nul.txt"))
		return
	}
	assert.True(t, isReservedWindowsPath("CON"))
	assert.True(t, isReservedWindowsPath("nul"))
	assert.True(t, isReservedWindowsPath("Aux.txt"))
	assert.True(t, isReservedWindowsPath("dir/COM1/file"))
	assert.False(t, isReservedWindowsPath("ordinary"))
	assert.False(t, isReservedWindowsPath("CONS")) // not the reserved name
}

// --- formatErr ---

func TestFormatErrNil(t *testing.T) {
	cc := &builtins.CallContext{}
	assert.Equal(t, "", formatErr(cc, nil))
}

func TestFormatErrPathErrorWithPortable(t *testing.T) {
	cc := &builtins.CallContext{
		PortableErr: func(err error) string {
			if errors.Is(err, fs.ErrNotExist) {
				return "no such file or directory"
			}
			return ""
		},
	}
	pe := &os.PathError{Op: "stat", Path: "x", Err: fs.ErrNotExist}
	assert.Equal(t, "no such file or directory", formatErr(cc, pe))
}

func TestFormatErrPathErrorWithoutPortable(t *testing.T) {
	cc := &builtins.CallContext{}
	pe := &os.PathError{Op: "stat", Path: "x", Err: errors.New("custom")}
	assert.Equal(t, "custom", formatErr(cc, pe))
}

func TestFormatErrNonPathErrorNotExist(t *testing.T) {
	cc := &builtins.CallContext{}
	assert.Equal(t, "no such file or directory", formatErr(cc, fs.ErrNotExist))
}

func TestFormatErrNonPathErrorPermission(t *testing.T) {
	cc := &builtins.CallContext{}
	assert.Equal(t, "permission denied", formatErr(cc, fs.ErrPermission))
}

func TestFormatErrPortableFallback(t *testing.T) {
	cc := &builtins.CallContext{
		PortableErr: func(err error) string { return "portable: " + err.Error() },
	}
	custom := errors.New("plain error")
	assert.Equal(t, "portable: plain error", formatErr(cc, custom))
}

func TestFormatErrFinalFallback(t *testing.T) {
	cc := &builtins.CallContext{
		PortableErr: func(err error) string { return "" },
	}
	custom := errors.New("raw error")
	assert.Equal(t, "raw error", formatErr(cc, custom))
}

// --- lookupVar ---

func TestLookupVarMissingCallback(t *testing.T) {
	cc := &builtins.CallContext{}
	v, ok := lookupVar(cc, "HOME")
	assert.Equal(t, "", v)
	assert.False(t, ok)
}

func TestLookupVarPresent(t *testing.T) {
	cc := &builtins.CallContext{
		LookupVar: func(name string) (string, bool) {
			if name == "HOME" {
				return "/h", true
			}
			return "", false
		},
	}
	v, ok := lookupVar(cc, "HOME")
	assert.True(t, ok)
	assert.Equal(t, "/h", v)
}

// --- resolvePhysical ---

func TestResolvePhysicalCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cc := &builtins.CallContext{
		LstatFile:    func(_ context.Context, _ string) (fs.FileInfo, error) { return nil, nil },
		ReadlinkFile: func(_ context.Context, _ string) (string, error) { return "", nil },
	}
	_, err := resolvePhysical(ctx, cc, "/foo")
	assert.ErrorIs(t, err, context.Canceled)
}

func TestResolvePhysicalNoCallbacks(t *testing.T) {
	cc := &builtins.CallContext{} // LstatFile / ReadlinkFile both nil
	got, err := resolvePhysical(context.Background(), cc, "/abs/path")
	assert.NoError(t, err)
	assert.Equal(t, "/abs/path", got)
}

// fakeInfo is a minimal fs.FileInfo whose only meaningful field is mode;
// resolvePhysical inspects only Mode() to detect symlinks.
type fakeInfo struct{ mode fs.FileMode }

func (f fakeInfo) Name() string       { return "" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeInfo) Sys() any           { return nil }

func TestResolvePhysicalLoop(t *testing.T) {
	// Build absolute, platform-correct symlink keys so the test runs on
	// Windows too (filepath.IsAbs("/a") is false on Windows, which would
	// otherwise cause resolvePhysical to take the relative-target branch
	// and never hit the seeded loop). The values are arbitrary keys —
	// only their relative shape matters for the loop.
	keyA := filepath.Join(t.TempDir(), "a")
	keyB := filepath.Join(filepath.Dir(keyA), "b")
	links := map[string]string{keyA: keyB, keyB: keyA}
	cc := &builtins.CallContext{
		LstatFile: func(_ context.Context, p string) (fs.FileInfo, error) {
			if _, ok := links[p]; ok {
				return symlinkInfo(), nil
			}
			return fakeInfo{}, nil
		},
		ReadlinkFile: func(_ context.Context, p string) (string, error) {
			return links[p], nil
		},
	}
	_, err := resolvePhysical(context.Background(), cc, keyA)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too many levels of symbolic links")
}

func TestResolvePhysicalRelativeTarget(t *testing.T) {
	// /alias -> sub (relative target). After one hop resolvePhysical should
	// land on /sub, which is a regular dir and ends the walk.
	cc := &builtins.CallContext{
		LstatFile: func(_ context.Context, p string) (fs.FileInfo, error) {
			if p == "/alias" {
				return symlinkInfo(), nil
			}
			return fakeInfo{}, nil
		},
		ReadlinkFile: func(_ context.Context, _ string) (string, error) {
			return "sub", nil
		},
	}
	got, err := resolvePhysical(context.Background(), cc, "/alias")
	assert.NoError(t, err)
	assert.Equal(t, "/sub", got)
}

func TestResolvePhysicalTargetTooLong(t *testing.T) {
	// A symlink that resolves to a long absolute path must be rejected
	// without further recursion.
	long := "/" + stringOfLength(maxPathBytes+1)
	cc := &builtins.CallContext{
		LstatFile: func(_ context.Context, _ string) (fs.FileInfo, error) {
			return symlinkInfo(), nil
		},
		ReadlinkFile: func(_ context.Context, _ string) (string, error) {
			return long, nil
		},
	}
	_, err := resolvePhysical(context.Background(), cc, "/start")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path too long")
}

func TestResolvePhysicalLstatError(t *testing.T) {
	cc := &builtins.CallContext{
		LstatFile: func(_ context.Context, _ string) (fs.FileInfo, error) {
			return nil, errors.New("boom")
		},
		ReadlinkFile: func(_ context.Context, _ string) (string, error) { return "", nil },
	}
	_, err := resolvePhysical(context.Background(), cc, "/p")
	assert.Error(t, err)
}

func TestResolvePhysicalReadlinkError(t *testing.T) {
	cc := &builtins.CallContext{
		LstatFile: func(_ context.Context, _ string) (fs.FileInfo, error) {
			return symlinkInfo(), nil
		},
		ReadlinkFile: func(_ context.Context, _ string) (string, error) {
			return "", errors.New("boom")
		},
	}
	_, err := resolvePhysical(context.Background(), cc, "/p")
	assert.Error(t, err)
}

func symlinkInfo() fs.FileInfo { return fakeInfo{mode: fs.ModeSymlink} }

func stringOfLength(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}
