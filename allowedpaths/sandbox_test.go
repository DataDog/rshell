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
	"runtime"
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

// TestSandboxDefaultReadOnly verifies that a freshly created sandbox blocks
// write opens without an explicit SetWritable call — defense-in-depth so
// that even if the interpreter accidentally passes write flags in read-only
// mode, the sandbox rejects them.
func TestSandboxDefaultReadOnly(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("data"), 0644))

	sb, _, err := New([]string{dir + ":rw"})
	require.NoError(t, err)
	defer sb.Close()

	writeFlags := []int{
		os.O_WRONLY,
		os.O_WRONLY | os.O_CREATE | os.O_TRUNC,
		os.O_WRONLY | os.O_APPEND,
		os.O_WRONLY | os.O_CREATE,
	}
	for _, flag := range writeFlags {
		f, err := sb.Open("existing.txt", dir, flag, 0644)
		assert.Nil(t, f, "read-only sandbox must reject flag %d", flag)
		assert.ErrorIs(t, err, os.ErrPermission, "read-only sandbox must return ErrPermission for flag %d", flag)
	}
}

func TestSandboxWriteAllowedPath(t *testing.T) {
	dir := t.TempDir()

	sb, _, err := New([]string{dir + ":rw"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

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

	sb, _, err := New([]string{allowed + ":rw"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

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

	sb, _, err := New([]string{dir + ":rw"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

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

	sb, _, err := New([]string{dir + ":rw"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

	f, err := sb.Open("data.txt", dir, os.O_WRONLY|os.O_TRUNC, 0)
	require.NoError(t, err)
	_, err = f.Write([]byte("short"))
	require.NoError(t, err)
	require.NoError(t, f.Close())

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "short", string(got), "O_TRUNC must replace, not append to, the original content")
}

func TestSandboxRemove(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.txt")
	require.NoError(t, os.WriteFile(path, []byte("data"), 0644))

	sb, _, err := New([]string{dir + ":rw"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

	require.NoError(t, sb.Remove("data.txt", dir))

	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "file should be removed")
}

func TestSandboxRemoveReadOnlyRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.txt")
	require.NoError(t, os.WriteFile(path, []byte("data"), 0644))

	sb, _, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()
	// No SetWritable() — sandbox defaults to read-only.

	err = sb.Remove("data.txt", dir)
	assert.ErrorIs(t, err, os.ErrPermission)
	_, statErr := os.Stat(path)
	assert.NoError(t, statErr, "file must survive a rejected remove")
}

func TestSandboxRemoveOutsideAllowedPathRejected(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(target, []byte("secret"), 0644))

	sb, _, err := New([]string{allowed + ":rw"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

	err = sb.Remove(target, allowed)
	assert.ErrorIs(t, err, os.ErrPermission)
	_, statErr := os.Stat(target)
	assert.NoError(t, statErr, "file outside the allowlist must survive")
}

func TestSandboxRemoveReadOnlyRootRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.txt")
	require.NoError(t, os.WriteFile(path, []byte("data"), 0644))

	// Root configured read-only (":ro", or bare with no suffix) rejects
	// removal even though the sandbox overall is writable.
	sb, _, err := New([]string{dir + ":ro"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

	err = sb.Remove("data.txt", dir)
	assert.ErrorIs(t, err, os.ErrPermission)
	_, statErr := os.Stat(path)
	assert.NoError(t, statErr)
}

func TestSandboxRemoveDirectoryRejected(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "subdir")
	require.NoError(t, os.Mkdir(sub, 0755))

	sb, _, err := New([]string{dir + ":rw"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

	err = sb.Remove("subdir", dir)
	assert.Error(t, err)
	_, statErr := os.Stat(sub)
	assert.NoError(t, statErr, "directory must survive a rejected remove")
}

func TestSandboxRemoveMissingFile(t *testing.T) {
	dir := t.TempDir()

	sb, _, err := New([]string{dir + ":rw"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

	err = sb.Remove("missing.txt", dir)
	assert.ErrorIs(t, err, os.ErrNotExist)
}

// TestSandboxRemoveSymlinkRemovesLinkNotTarget verifies unlink(2) semantics:
// the symlink itself is deleted, its referent is left untouched. This is the
// key difference between Remove and Truncate/Open's write-target resolution,
// which reject the final component being a symlink.
func TestSandboxRemoveSymlinkRemovesLinkNotTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	require.NoError(t, os.WriteFile(target, []byte("keep me"), 0644))
	link := filepath.Join(dir, "link.txt")
	require.NoError(t, os.Symlink("target.txt", link))

	sb, _, err := New([]string{dir + ":rw"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

	require.NoError(t, sb.Remove("link.txt", dir))

	_, linkErr := os.Lstat(link)
	assert.True(t, os.IsNotExist(linkErr), "symlink should be removed")
	got, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "keep me", string(got), "symlink target must survive")
}

func TestSandboxRemoveDanglingSymlink(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "dangling")
	require.NoError(t, os.Symlink("does-not-exist", link))

	sb, _, err := New([]string{dir + ":rw"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

	require.NoError(t, sb.Remove("dangling", dir))
	_, linkErr := os.Lstat(link)
	assert.True(t, os.IsNotExist(linkErr))
}

// TestSandboxRemoveSymlinkTargetOutsideSandboxStillRemovable regression-tests
// the bug in an earlier version of Remove that reused resolveWriteTarget,
// which follows the final path component. unlink(2) never dereferences the
// final component, so a symlink whose target lies outside every allowed
// root (or doesn't exist) must still be removable by name — Remove must not
// require its target to resolve anywhere.
func TestSandboxRemoveSymlinkTargetOutsideSandboxStillRemovable(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(dir, "escape_link")
	require.NoError(t, os.Symlink(filepath.Join(outside, "does-not-exist"), link))

	sb, _, err := New([]string{dir + ":rw"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

	require.NoError(t, sb.Remove("escape_link", dir))
	_, lstatErr := os.Lstat(link)
	assert.True(t, os.IsNotExist(lstatErr))
}

// TestSandboxRemoveSelfReferentialSymlinkNoHang regression-tests the bug in
// an earlier version of Remove that reused resolveWriteTarget: following a
// self-referential symlink ("loop" -> "loop") during resolution either
// errors out via ELOOP or, in the worst case, could hang. Remove must
// succeed immediately since unlink(2) never follows the final component.
func TestSandboxRemoveSelfReferentialSymlinkNoHang(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "loop")
	require.NoError(t, os.Symlink("loop", link))

	sb, _, err := New([]string{dir + ":rw"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

	require.NoError(t, sb.Remove("loop", dir))
	_, lstatErr := os.Lstat(link)
	assert.True(t, os.IsNotExist(lstatErr))
}

// TestSandboxRemoveThroughSymlinkedIntermediateDirRejected ensures a symlink
// used as an intermediate path component (not the final component) cannot be
// used to escape the sandbox root, mirroring the write-target protection in
// resolveWriteTarget/rejectSymlinkWriteTarget.
func TestSandboxRemoveThroughSymlinkedIntermediateDirRejected(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "victim.txt")
	require.NoError(t, os.WriteFile(target, []byte("victim"), 0644))

	linkDir := filepath.Join(allowed, "linkdir")
	require.NoError(t, os.Symlink(outside, linkDir))

	sb, _, err := New([]string{allowed + ":rw"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

	err = sb.Remove(filepath.Join(allowed, "linkdir", "victim.txt"), allowed)
	assert.Error(t, err)
	_, statErr := os.Stat(target)
	assert.NoError(t, statErr, "file behind a symlinked intermediate directory must survive")
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

	sb, _, err := New([]string{allowed + ":rw"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

	f, err := sb.Open("escape", allowed, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	assert.Nil(t, f)
	assert.Error(t, err)

	_, statErr := os.Stat(filepath.Join(outside, "target.txt"))
	assert.True(t, os.IsNotExist(statErr), "symlink target must not be created outside the sandbox")
}

func TestSandboxWriteRejectsUnknownFlag(t *testing.T) {
	dir := t.TempDir()

	sb, _, err := New([]string{dir + ":rw"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

	// Pick a high bit that is not in allowedOpenFlags. We OR with
	// O_WRONLY so the access mode itself is valid; only the unknown bit
	// should trigger rejection.
	const unknownFlag = 1 << 30
	f, err := sb.Open("x.txt", dir, os.O_WRONLY|os.O_CREATE|unknownFlag, 0644)
	assert.Nil(t, f)
	assert.ErrorIs(t, err, os.ErrPermission)
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

func TestParseAllowedPathMode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		path string
		mode pathMode
	}{
		{name: "default read-only", in: "/var/log", path: "/var/log", mode: pathModeReadOnly},
		{name: "explicit read-only", in: "/var/log:ro", path: "/var/log", mode: pathModeReadOnly},
		{name: "explicit read-write", in: "/var/log:rw", path: "/var/log", mode: pathModeReadWrite},
		{name: "last terminal suffix wins", in: "/var/log:rw:ro", path: "/var/log:rw", mode: pathModeReadOnly},
		{name: "middle suffix is path text", in: "/var/log:rw/datadog", path: "/var/log:rw/datadog", mode: pathModeReadOnly},
		{name: "unknown suffix is path text", in: "/var/log:rx", path: "/var/log:rx", mode: pathModeReadOnly},
		{name: "bare ro suffix is path text", in: ":ro", path: ":ro", mode: pathModeReadOnly},
		{name: "bare rw suffix is path text", in: ":rw", path: ":rw", mode: pathModeReadOnly},
		{name: "empty path", in: "", path: "", mode: pathModeReadOnly},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, mode := parseAllowedPathMode(tt.in)
			assert.Equal(t, tt.path, path)
			assert.Equal(t, tt.mode, mode)
		})
	}
}

func TestResolveAllowedPathModePreservesExistingLiteralPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("literal paths ending in :rw/:ro are POSIX-only")
	}

	dir := t.TempDir()
	literal := filepath.Join(dir, "tenant:rw")
	require.NoError(t, os.Mkdir(literal, 0755))

	path, mode := resolveAllowedPathMode(literal)
	assert.Equal(t, literal, path)
	assert.Equal(t, pathModeReadOnly, mode)
}

func TestAllowedPathModesAreStoredAfterSuffixStripping(t *testing.T) {
	dir := t.TempDir()

	sb, _, err := New([]string{dir + ":rw"})
	require.NoError(t, err)
	defer sb.Close()

	assert.Equal(t, []string{dir}, sb.Paths())

	root, _, ok := sb.resolve(dir)
	require.True(t, ok)
	assert.Equal(t, pathModeReadWrite, root.mode)
}

func TestPathAccessesIncludeModes(t *testing.T) {
	readOnly := t.TempDir()
	readWrite := t.TempDir()

	sb, _, err := New([]string{readOnly, readWrite + ":rw"})
	require.NoError(t, err)
	defer sb.Close()

	assert.Equal(t, []PathAccess{
		{Path: readOnly, ReadWrite: false},
		{Path: readWrite, ReadWrite: true},
	}, sb.PathAccesses())
}

func TestAllowedPathModeMostSpecificRootWins(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "datadog")
	require.NoError(t, os.Mkdir(child, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(child, "agent.log"), []byte("data"), 0644))

	sb, _, err := New([]string{dir + ":rw", child + ":ro"})
	require.NoError(t, err)
	defer sb.Close()

	root, _, ok := sb.resolve(filepath.Join(child, "agent.log"))
	require.True(t, ok)
	assert.Equal(t, child, root.absPath)
	assert.Equal(t, pathModeReadOnly, root.mode)
}

func TestAllowedPathModeMostSpecificReadWriteWins(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "datadog")
	require.NoError(t, os.Mkdir(child, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(child, "agent.log"), []byte("data"), 0644))

	sb, _, err := New([]string{dir + ":ro", child + ":rw"})
	require.NoError(t, err)
	defer sb.Close()

	root, _, ok := sb.resolve(filepath.Join(child, "agent.log"))
	require.True(t, ok)
	assert.Equal(t, child, root.absPath)
	assert.Equal(t, pathModeReadWrite, root.mode)
}

func TestAllowedPathReadWriteModeDoesNotEnableWriteOpen(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.txt"), []byte("data"), 0644))

	sb, _, err := New([]string{dir + ":rw"})
	require.NoError(t, err)
	defer sb.Close()

	f, err := sb.Open("test.txt", dir, os.O_RDWR, 0)
	assert.Nil(t, f)
	assert.ErrorIs(t, err, os.ErrPermission)
}

func TestAllowedPathReadOnlyModeRejectsWriteOpenInWritableSandbox(t *testing.T) {
	tests := []struct {
		name         string
		pathSuffix   string
		wantRootMode pathMode
	}{
		{name: "default read-only", wantRootMode: pathModeReadOnly},
		{name: "explicit read-only", pathSuffix: ":ro", wantRootMode: pathModeReadOnly},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			target := filepath.Join(dir, "test.txt")
			require.NoError(t, os.WriteFile(target, []byte("data"), 0644))

			sb, _, err := New([]string{dir + tt.pathSuffix})
			require.NoError(t, err)
			defer sb.Close()
			sb.SetWritable()

			root, _, ok := sb.resolve(target)
			require.True(t, ok)
			require.Equal(t, tt.wantRootMode, root.mode)

			f, err := sb.Open("test.txt", dir, os.O_WRONLY|os.O_TRUNC, 0)
			assert.Nil(t, f)
			assert.ErrorIs(t, err, os.ErrPermission)

			got, err := os.ReadFile(target)
			require.NoError(t, err)
			assert.Equal(t, "data", string(got))
		})
	}
}

func TestAllowedPathMostSpecificReadOnlyModeRejectsWriteOpen(t *testing.T) {
	parent := t.TempDir()
	child := filepath.Join(parent, "child")
	require.NoError(t, os.Mkdir(child, 0755))
	parentTarget := filepath.Join(parent, "parent.txt")
	childTarget := filepath.Join(child, "child.txt")
	require.NoError(t, os.WriteFile(parentTarget, []byte("parent"), 0644))
	require.NoError(t, os.WriteFile(childTarget, []byte("child"), 0644))

	sb, _, err := New([]string{parent + ":rw", child + ":ro"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

	childFile, err := sb.Open("child.txt", child, os.O_WRONLY|os.O_TRUNC, 0)
	assert.Nil(t, childFile)
	assert.ErrorIs(t, err, os.ErrPermission)

	parentFile, err := sb.Open("parent.txt", parent, os.O_WRONLY|os.O_TRUNC, 0)
	require.NoError(t, err)
	_, err = parentFile.Write([]byte("updated"))
	require.NoError(t, err)
	require.NoError(t, parentFile.Close())

	gotChild, err := os.ReadFile(childTarget)
	require.NoError(t, err)
	assert.Equal(t, "child", string(gotChild))
	gotParent, err := os.ReadFile(parentTarget)
	require.NoError(t, err)
	assert.Equal(t, "updated", string(gotParent))
}

func TestAllowedPathReadOnlyModeRejectsTruncateInWritableSandbox(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(target, []byte("data"), 0644))

	sb, _, err := New([]string{dir + ":ro"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

	err = sb.Truncate("test.txt", dir, 0, false)
	assert.ErrorIs(t, err, os.ErrPermission)

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "data", string(got))
}

func TestAllowedPathReadWriteModeAllowsTruncateInWritableSandbox(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(target, []byte("data"), 0644))

	sb, _, err := New([]string{dir + ":rw"})
	require.NoError(t, err)
	defer sb.Close()
	sb.SetWritable()

	require.NoError(t, sb.Truncate("test.txt", dir, 2, false))

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "da", string(got))
}

func TestAllowedPathModeDoesNotWidenExistingLiteralSuffixPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("literal paths ending in :rw/:ro are POSIX-only")
	}

	parent := t.TempDir()
	base := filepath.Join(parent, "tenant")
	literal := base + ":rw"
	require.NoError(t, os.Mkdir(base, 0755))
	require.NoError(t, os.Mkdir(literal, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(base, "base.txt"), []byte("base"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(literal, "literal.txt"), []byte("literal"), 0644))

	sb, _, err := New([]string{literal})
	require.NoError(t, err)
	defer sb.Close()

	assert.Equal(t, []string{literal}, sb.Paths())

	root, _, ok := sb.resolve(filepath.Join(literal, "literal.txt"))
	require.True(t, ok)
	assert.Equal(t, literal, root.absPath)
	assert.Equal(t, pathModeReadOnly, root.mode)

	f, err := sb.Open(filepath.Join(literal, "literal.txt"), "/", os.O_RDONLY, 0)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, err = sb.Open(filepath.Join(base, "base.txt"), "/", os.O_RDONLY, 0)
	assert.ErrorIs(t, err, os.ErrPermission)
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
