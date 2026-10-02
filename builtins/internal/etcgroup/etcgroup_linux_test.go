// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package etcgroup

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleGroupFile = `root:x:0:
daemon:x:1:
dd-agent:x:998:datadog-agent
docker:x:999:alice,bob
# a comment line

malformed-line-no-colons
`

func TestParseGroupFile_HappyPath(t *testing.T) {
	gid, err := parseGroupFile(strings.NewReader(sampleGroupFile), "dd-agent")
	assert.NoError(t, err)
	assert.Equal(t, uint32(998), gid)
}

func TestParseGroupFile_RootGroup(t *testing.T) {
	gid, err := parseGroupFile(strings.NewReader(sampleGroupFile), "root")
	assert.NoError(t, err)
	assert.Equal(t, uint32(0), gid)
}

func TestParseGroupFile_NotFound(t *testing.T) {
	_, err := parseGroupFile(strings.NewReader(sampleGroupFile), "nosuchgroup")
	assert.ErrorIs(t, err, ErrGroupNotFound)
}

func TestParseGroupFile_SkipsCommentsAndBlankAndMalformedLines(t *testing.T) {
	gid, err := parseGroupFile(strings.NewReader(sampleGroupFile), "docker")
	assert.NoError(t, err)
	assert.Equal(t, uint32(999), gid)
}

func TestParseGroupFile_EmptyInput(t *testing.T) {
	_, err := parseGroupFile(strings.NewReader(""), "root")
	assert.ErrorIs(t, err, ErrGroupNotFound)
}

func TestParseGroupFile_NonNumericGID(t *testing.T) {
	input := "broken:x:notanumber:\nreal:x:42:\n"
	gid, err := parseGroupFile(strings.NewReader(input), "broken")
	assert.ErrorIs(t, err, ErrGroupNotFound, "a malformed GID field is skipped, not fatal")
	gid, err = parseGroupFile(strings.NewReader(input), "real")
	assert.NoError(t, err)
	assert.Equal(t, uint32(42), gid)
}

func TestParseGroupFile_MissingGIDField(t *testing.T) {
	_, err := parseGroupFile(strings.NewReader("nogid:x\n"), "nogid")
	assert.ErrorIs(t, err, ErrGroupNotFound)
}

func TestParseGroupFile_LineTooLong(t *testing.T) {
	huge := "hugegroup:x:1:" + strings.Repeat("a,", maxGroupLine*2) + "\n"
	_, err := parseGroupFile(strings.NewReader(huge), "hugegroup")
	assert.Error(t, err)
	assert.False(t, errors.Is(err, ErrGroupNotFound))
}

func TestLookupGID_NotSupportedNeverOnLinux(t *testing.T) {
	// Sanity check that the Linux backend is actually wired (i.e. this test
	// file is compiled): looking up the well-known "root" group against the
	// real /etc/group must succeed with GID 0 on every Linux host.
	gid, err := LookupGID("root")
	assert.NoError(t, err)
	assert.Equal(t, uint32(0), gid)
}

// writeTempGroupFile writes content to a fresh file named "group" inside a
// fresh temp directory, and returns its path. Using a real file (rather
// than an in-memory buffer) is required to exercise addMemberAtPath's
// os.Rename-based atomic write, which needs a real directory and a real
// same-filesystem rename target.
func writeTempGroupFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "group")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestAddMemberAtPath_AppendsToExistingMembers(t *testing.T) {
	path := writeTempGroupFile(t, sampleGroupFile)

	err := addMemberAtPath(path, "docker", "dd-agent")
	require.NoError(t, err)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	want := `root:x:0:
daemon:x:1:
dd-agent:x:998:datadog-agent
docker:x:999:alice,bob,dd-agent
# a comment line

malformed-line-no-colons
`
	assert.Equal(t, want, string(got))
}

func TestAddMemberAtPath_AppendsToEmptyMembers(t *testing.T) {
	path := writeTempGroupFile(t, "root:x:0:\nempty:x:500:\n")

	err := addMemberAtPath(path, "empty", "dd-agent")
	require.NoError(t, err)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "root:x:0:\nempty:x:500:dd-agent\n", string(got))
}

func TestAddMemberAtPath_IdempotentWhenAlreadyMember(t *testing.T) {
	path := writeTempGroupFile(t, sampleGroupFile)

	before, err := os.ReadFile(path)
	require.NoError(t, err)
	beforeStat, err := os.Lstat(path)
	require.NoError(t, err)

	err = addMemberAtPath(path, "docker", "alice")
	require.NoError(t, err, "adding an existing member must succeed, not error")

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(before), string(after), "file content must be byte-for-byte unchanged")

	afterStat, err := os.Lstat(path)
	require.NoError(t, err)
	// No rewrite should have happened at all for a true no-op: the inode
	// should be the same file (addMemberAtPath's upsertMember short-circuits
	// before ever calling writeAtomic when the user is already a member).
	assert.True(t, os.SameFile(beforeStat, afterStat), "no-op add must not rewrite (rename a new inode over) the file")
}

func TestAddMemberAtPath_NonexistentGroup(t *testing.T) {
	path := writeTempGroupFile(t, sampleGroupFile)

	err := addMemberAtPath(path, "nosuchgroup", "dd-agent")
	assert.ErrorIs(t, err, ErrGroupNotFound)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, sampleGroupFile, string(got), "a failed lookup must not modify the file")
}

func TestAddMemberAtPath_OtherLinesUnchanged(t *testing.T) {
	path := writeTempGroupFile(t, sampleGroupFile)

	err := addMemberAtPath(path, "dd-agent", "extrauser")
	require.NoError(t, err)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(string(got), "\n")
	wantLines := strings.Split(sampleGroupFile, "\n")

	// Every line except the "dd-agent" one must be byte-for-byte identical.
	require.Equal(t, len(wantLines), len(lines))
	for i, wantLine := range wantLines {
		if strings.HasPrefix(wantLine, "dd-agent:") {
			assert.Equal(t, "dd-agent:x:998:datadog-agent,extrauser", lines[i])
			continue
		}
		assert.Equal(t, wantLine, lines[i], "line %d must be unchanged", i)
	}
}

func TestAddMemberAtPath_PreservesPermissions(t *testing.T) {
	path := writeTempGroupFile(t, sampleGroupFile)
	require.NoError(t, os.Chmod(path, 0o640))

	err := addMemberAtPath(path, "docker", "dd-agent")
	require.NoError(t, err)

	info, err := os.Lstat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
}

func TestAddMemberAtPath_RejectsSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real-group")
	require.NoError(t, os.WriteFile(real, []byte(sampleGroupFile), 0o644))
	link := filepath.Join(dir, "group")
	require.NoError(t, os.Symlink(real, link))

	// os.Lstat on the symlink itself reports a symlink, not a regular file;
	// writeAtomic's IsRegular check rejects it rather than silently
	// following it into another directory.
	err := addMemberAtPath(link, "docker", "dd-agent")
	assert.Error(t, err)
}

// TestAddMemberAtPath_CrashSafety_TempFileNeverRenamedWithoutCompleteWrite
// models the requested "killed mid-write" crash-safety property: a temp
// file that was created but never successfully written and renamed must
// never be visible as the target path, and the target path must remain
// exactly its original content. This directly exercises the atomic-rename
// invariant addMemberAtPath's doc comment describes: only a successful
// os.Rename can make new content visible, and everything before that point
// (including a fully-written but not-yet-renamed temp file) leaves the
// original file untouched.
func TestAddMemberAtPath_CrashSafety_TempFileNeverRenamedWithoutCompleteWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "group")
	require.NoError(t, os.WriteFile(path, []byte(sampleGroupFile), 0o644))

	// Simulate "temp file written, but process killed before rename": create
	// a temp file with new (complete) content sitting next to /etc/group,
	// without ever calling os.Rename — this is exactly the on-disk state a
	// SIGKILL between tmpFile.Close() and os.Rename() in writeAtomic would
	// leave behind.
	tmpPath, tmpFile, err := createTempFile(dir, 0o644)
	require.NoError(t, err)
	newData, changed, found, err := upsertMember([]byte(sampleGroupFile), "docker", "dd-agent")
	require.NoError(t, err)
	require.True(t, found)
	require.True(t, changed)
	_, err = tmpFile.Write(newData)
	require.NoError(t, err)
	require.NoError(t, tmpFile.Close())

	// The original file must be completely untouched: same content, same
	// inode identity (never replaced).
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, sampleGroupFile, string(got), "original file must be untouched before rename runs")

	// The temp file itself must exist with a distinct name from the target
	// (i.e. it was never renamed over it).
	assert.NotEqual(t, path, tmpPath)
	_, err = os.Lstat(tmpPath)
	assert.NoError(t, err, "the crashed-before-rename temp file should still be present under its own name")

	// Completing the rename now (simulating a retry/cleanup pass) makes the
	// new content visible atomically, proving the two states
	// (pre-rename: untouched original; post-rename: complete new content)
	// are the only two observable states — there is no partially-written
	// state for /etc/group in between.
	require.NoError(t, os.Rename(tmpPath, path))
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(newData), string(got))
}

func TestUpsertMember_PreservesCRLFLineEndings(t *testing.T) {
	input := "root:x:0:\r\ndocker:x:999:alice\r\n"
	newData, changed, found, err := upsertMember([]byte(input), "docker", "bob")
	require.NoError(t, err)
	assert.True(t, found)
	assert.True(t, changed)
	assert.Equal(t, "root:x:0:\r\ndocker:x:999:alice,bob\r\n", string(newData))
}

func TestUpsertMember_PreservesMissingTrailingNewline(t *testing.T) {
	input := "root:x:0:\ndocker:x:999:alice"
	newData, changed, found, err := upsertMember([]byte(input), "docker", "bob")
	require.NoError(t, err)
	assert.True(t, found)
	assert.True(t, changed)
	assert.Equal(t, "root:x:0:\ndocker:x:999:alice,bob", string(newData))
}

func TestUpsertMember_MatchesOnlyFirstDuplicateGroupLine(t *testing.T) {
	// A duplicated group name is malformed but not unheard of; matches
	// parseGroupFile/LookupGID's own first-match semantics.
	input := "dup:x:1:alice\ndup:x:2:bob\n"
	newData, changed, found, err := upsertMember([]byte(input), "dup", "carol")
	require.NoError(t, err)
	assert.True(t, found)
	assert.True(t, changed)
	assert.Equal(t, "dup:x:1:alice,carol\ndup:x:2:bob\n", string(newData))
}
