// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

// Go tests, rather than scenario tests, because these assert directly on the
// system.posix_acl_access / system.posix_acl_default extended attributes via
// golang.org/x/sys/unix.Getxattr — the scenario framework has no ACL
// assertion primitive, the same reason AGENTS.md records for `free`,
// `ip route`, and `lsof`. setfacl is also Linux-only (POSIX ACL xattrs are a
// Linux filesystem feature), so this whole package only runs there.
//
// Group resolution goes through the real /etc/group on the host running the
// test (etcGroupPath in builtins/internal/etcgroup is a hardcoded constant,
// not configurable), so these tests use "root", which is present with a
// well-known GID (0) on every Linux system.
package setfacl_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/rshell/builtins/internal/acl"
	"github.com/DataDog/rshell/interp"
)

const (
	accessXattr  = "system.posix_acl_access"
	defaultXattr = "system.posix_acl_default"
)

func decodeXattr(t *testing.T, path, name string) []acl.Entry {
	t.Helper()
	data := getxattr(t, path, name)
	if data == nil {
		return nil
	}
	entries, err := acl.DecodeACL(data)
	if err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return entries
}

func findGroupEntry(entries []acl.Entry, gid uint32) (acl.Entry, bool) {
	for _, e := range entries {
		if e.Tag == acl.TagGroup && e.ID == gid {
			return e, true
		}
	}
	return acl.Entry{}, false
}

func TestSetfaclAddsGroupEntryWithNoExistingACL(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "f.txt", "hi\n")

	_, stderr, code := setfaclRun(t, "setfacl -m g:root:rx "+path, dir)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr %q)", code, stderr)
	}

	entries := decodeXattr(t, path, accessXattr)
	entry, ok := findGroupEntry(entries, 0)
	if !ok {
		t.Fatalf("expected a group:root entry, got %+v", entries)
	}
	if entry.Perm != acl.PermRead|acl.PermExecute {
		t.Fatalf("expected r-x perms, got %v", entry.Perm)
	}
}

func TestSetfaclReplacesExistingGroupEntryWithoutDuplicating(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "f.txt", "hi\n")

	if _, stderr, code := setfaclRun(t, "setfacl -m g:root:r "+path, dir); code != 0 {
		t.Fatalf("first setfacl failed: code %d stderr %q", code, stderr)
	}
	_, stderr, code := setfaclRun(t, "setfacl -m g:root:rwx "+path, dir)
	if code != 0 {
		t.Fatalf("second setfacl failed: code %d stderr %q", code, stderr)
	}

	entries := decodeXattr(t, path, accessXattr)
	count := 0
	var perm uint16
	for _, e := range entries {
		if e.Tag == acl.TagGroup && e.ID == 0 {
			count++
			perm = e.Perm
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one group:root entry, got %d (%+v)", count, entries)
	}
	if perm != acl.PermRead|acl.PermWrite|acl.PermExecute {
		t.Fatalf("expected rwx perms after widening, got %v", perm)
	}
}

func TestSetfaclRecursiveAppliesToTreeAndSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	nested := writeFile(t, sub, "nested.txt", "x\n")
	top := writeFile(t, dir, "top.txt", "x\n")

	link := filepath.Join(dir, "link")
	if err := os.Symlink(top, link); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := setfaclRun(t, "setfacl -R -m g:root:rx "+dir, dir)
	if code == 0 {
		t.Fatalf("expected non-zero exit due to symlink skip, got 0 (stderr %q)", stderr)
	}
	if !strings.Contains(stderr, "link") {
		t.Fatalf("expected the symlink to be reported as failed, stderr %q", stderr)
	}

	for _, p := range []string{dir, sub, nested, top} {
		entries := decodeXattr(t, p, accessXattr)
		if _, ok := findGroupEntry(entries, 0); !ok {
			t.Fatalf("expected group:root entry on %s, got %+v", p, entries)
		}
	}

	linkTarget, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if linkTarget != top {
		t.Fatalf("symlink target changed unexpectedly: %s", linkTarget)
	}
}

func TestSetfaclDefaultACLOnDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := setfaclRun(t, "setfacl -d -m g:root:rx "+sub, dir)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr %q)", code, stderr)
	}

	entries := decodeXattr(t, sub, defaultXattr)
	entry, ok := findGroupEntry(entries, 0)
	if !ok {
		t.Fatalf("expected a default ACL group:root entry, got %+v", entries)
	}
	if entry.Perm != acl.PermRead|acl.PermExecute {
		t.Fatalf("expected r-x perms, got %v", entry.Perm)
	}

	// the access ACL must still be present and valid, since applyEntry
	// always rewrites it alongside the default ACL.
	if access := decodeXattr(t, sub, accessXattr); len(access) == 0 {
		t.Fatalf("expected access ACL to remain populated, got empty")
	}
}

func TestSetfaclDefaultACLOnFileWithoutRecursiveIsError(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "f.txt", "hi\n")

	_, stderr, code := setfaclRun(t, "setfacl -d -m g:root:rx "+path, dir)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0")
	}
	if !strings.Contains(stderr, "Only directories can have default ACLs") {
		t.Fatalf("expected default-ACL-not-a-directory error, got %q", stderr)
	}
}

func TestSetfaclRecursiveDefaultSkipsFilesSilently(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, sub, "nested.txt", "x\n")

	_, stderr, code := setfaclRun(t, "setfacl -R -d -m g:root:rx "+dir, dir)
	if code != 0 {
		t.Fatalf("expected exit 0 (files silently skipped for -d), got %d (stderr %q)", code, stderr)
	}

	for _, d := range []string{dir, sub} {
		entries := decodeXattr(t, d, defaultXattr)
		if _, ok := findGroupEntry(entries, 0); !ok {
			t.Fatalf("expected default ACL group:root entry on directory %s, got %+v", d, entries)
		}
	}
}

func TestSetfaclRequiresRemediationMode(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "f.txt", "hi\n")

	_, stderr, code := runScript(t, "setfacl -m g:root:r "+path, dir,
		interp.AllowedPaths([]string{dir + ":rw"}),
	)
	if code == 0 {
		t.Fatalf("expected non-zero exit outside remediation mode, got 0")
	}
	if !strings.Contains(stderr, "remediation mode required") {
		t.Fatalf("expected remediation-mode-required message, got %q", stderr)
	}
}

func TestSetfaclRequiresWritableRoot(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "f.txt", "hi\n")

	_, stderr, code := runScript(t, "setfacl -m g:root:r "+path, dir,
		interp.AllowedPaths([]string{dir + ":ro"}),
		interp.WithMode(interp.ModeRemediation),
	)
	if code == 0 {
		t.Fatalf("expected non-zero exit with no writable root, got 0")
	}
	if !strings.Contains(stderr, "no writable path is configured") {
		t.Fatalf("expected no-writable-root message, got %q", stderr)
	}
}

func TestSetfaclRejectsUnknownGroup(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "f.txt", "hi\n")

	_, stderr, code := setfaclRun(t, "setfacl -m g:nosuchgroupxyz:r "+path, dir)
	if code == 0 {
		t.Fatalf("expected non-zero exit for unknown group, got 0")
	}
	if !strings.Contains(stderr, "Invalid argument") {
		t.Fatalf("expected GNU-style Invalid argument message, got %q", stderr)
	}
}

func TestSetfaclRejectsMalformedEntry(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "f.txt", "hi\n")

	for _, entry := range []string{
		"u:root:r",
		"g:root",
		"g:root:rq",
		"g::r",
		"garbage",
	} {
		_, stderr, code := setfaclRun(t, "setfacl -m "+entry+" "+path, dir)
		if code == 0 {
			t.Fatalf("entry %q: expected non-zero exit, got 0", entry)
		}
		if !strings.Contains(stderr, "Invalid argument") {
			t.Fatalf("entry %q: expected Invalid argument message, got %q", entry, stderr)
		}
	}
}

func TestSetfaclMissingModifyFlagIsError(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "f.txt", "hi\n")

	_, stderr, code := setfaclRun(t, "setfacl "+path, dir)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0")
	}
	if !strings.Contains(stderr, "-m/--modify") {
		t.Fatalf("expected missing -m/--modify message, got %q", stderr)
	}
}

func TestSetfaclHelp(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, code := setfaclRun(t, "setfacl --help", dir)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr %q)", code, stderr)
	}
	if !strings.Contains(stdout, "Usage: setfacl") {
		t.Fatalf("expected usage line in stdout, got %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr for --help, got %q", stderr)
	}
}

func TestSetfaclRejectsHardLinkToFileOutsideSandbox(t *testing.T) {
	base := t.TempDir()
	inside := filepath.Join(base, "rsh")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{inside, outside} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	target := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(target, []byte("SENSITIVE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(inside, "hard.txt")
	if err := os.Link(target, link); err != nil {
		t.Skipf("hard links unsupported on this filesystem: %v", err)
	}

	_, stderr, code := runScript(t, "setfacl -m g:root:r "+link, inside,
		interp.AllowedPaths([]string{inside + ":rw"}),
		interp.WithMode(interp.ModeRemediation),
	)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0 (stderr %q)", stderr)
	}
	if !strings.Contains(stderr, "hard links are not supported as write targets") {
		t.Fatalf("expected hard-link rejection in stderr, got %q", stderr)
	}
	if data := getxattr(t, target, accessXattr); data != nil {
		t.Fatalf("out-of-sandbox file unexpectedly got an ACL xattr: %v", data)
	}
}

func TestSetfaclHardLinkGuardAllowsSingleLinkedFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "plain.txt", "hello\n")

	_, stderr, code := setfaclRun(t, "setfacl -m g:root:r "+path, dir)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr %q)", code, stderr)
	}
	entries := decodeXattr(t, path, accessXattr)
	if _, ok := findGroupEntry(entries, 0); !ok {
		t.Fatalf("expected group:root entry, got %+v", entries)
	}
}

func TestSetfaclSymlinkTargetIsRejected(t *testing.T) {
	dir := t.TempDir()
	target := writeFile(t, dir, "target.txt", "x\n")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := setfaclRun(t, "setfacl -m g:root:r "+link, dir)
	if code == 0 {
		t.Fatalf("expected non-zero exit for symlink target, got 0")
	}
	if !strings.Contains(stderr, "Operation not supported") {
		t.Fatalf("expected Operation not supported message, got %q", stderr)
	}
	if data := getxattr(t, target, accessXattr); data != nil {
		t.Fatalf("symlink target unexpectedly got an ACL xattr: %v", data)
	}
}
