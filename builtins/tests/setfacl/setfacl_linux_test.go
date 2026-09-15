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
	// The only real failure is the symlink skip. A directory's ReadDir(1)
	// legitimately ends in io.EOF once its listing is exhausted; that must
	// never be surfaced as a spurious "setfacl: PATH: EOF" line alongside
	// the genuine symlink error (see TestSetfaclRecursiveProducesNoErrorOutputOnSuccess
	// for the success-only case).
	if strings.Contains(stderr, "EOF") {
		t.Fatalf("stderr must not contain a spurious EOF line from directory traversal, got %q", stderr)
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

// TestSetfaclRecursiveProducesNoErrorOutputOnSuccess is a regression test for
// a bug where every directory visited during a -R walk (but never a file)
// printed a spurious "setfacl: PATH: EOF" line even though the ACL was
// correctly applied and the command exited 0. The root cause: ReadDir(1) on
// an fs.ReadDirFile returns io.EOF as the normal end-of-listing signal once a
// directory's entries are exhausted, but runRecursive's directory-iterator
// loop treated any non-ErrClosed ReadDir error as a real failure and
// reported it via callCtx.Errf. Only directories call ReadDir (files don't
// iterate their own listing), which is why only directories were affected.
//
// The tree here has multiple nesting levels (dir/mid/leaf) so every
// directory's iterator hits the same final ReadDir(1) that used to trigger
// the bug, and the target is a subdirectory of the AllowedPaths root (not
// the root itself) so this test exercises only the EOF-handling path.
func TestSetfaclRecursiveProducesNoErrorOutputOnSuccess(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	mid := filepath.Join(target, "mid")
	leaf := filepath.Join(mid, "leaf")
	if err := os.MkdirAll(leaf, 0o755); err != nil {
		t.Fatal(err)
	}
	targetFile := writeFile(t, target, "top.txt", "x\n")
	midFile := writeFile(t, mid, "mid.txt", "x\n")
	leafFile := writeFile(t, leaf, "leaf.txt", "x\n")

	stdout, stderr, code := setfaclRun(t, "setfacl -R -m g:root:rx "+target, root)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr %q)", code, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected no stderr output on success, got %q", stderr)
	}
	if stdout != "" {
		t.Fatalf("expected no stdout output, got %q", stdout)
	}

	for _, p := range []string{target, mid, leaf, targetFile, midFile, leafFile} {
		entries := decodeXattr(t, p, accessXattr)
		if _, ok := findGroupEntry(entries, 0); !ok {
			t.Fatalf("expected group:root entry on %s, got %+v", p, entries)
		}
	}
}

func TestSetfaclMultipleOperandsAllSucceed(t *testing.T) {
	dir := t.TempDir()
	f1 := writeFile(t, dir, "a.txt", "a\n")
	f2 := writeFile(t, dir, "b.txt", "b\n")
	f3 := writeFile(t, dir, "c.txt", "c\n")

	_, stderr, code := setfaclRun(t, "setfacl -m g:root:rx "+f1+" "+f2+" "+f3, dir)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr %q)", code, stderr)
	}

	for _, p := range []string{f1, f2, f3} {
		entries := decodeXattr(t, p, accessXattr)
		entry, ok := findGroupEntry(entries, 0)
		if !ok {
			t.Fatalf("expected group:root entry on %s, got %+v", p, entries)
		}
		if entry.Perm != acl.PermRead|acl.PermExecute {
			t.Fatalf("expected r-x perms on %s, got %v", p, entry.Perm)
		}
	}
}

// TestSetfaclMultipleOperandsPartialFailureContinues verifies that when one
// operand among several fails (here: a nonexistent path), the remaining
// valid operands still get the ACL applied, a per-path error is reported for
// the failing operand, and the overall exit code is non-zero — matching GNU
// setfacl's continue-on-failure semantics for multiple operands (mirroring
// rm's own operand loop; see rm.go).
func TestSetfaclMultipleOperandsPartialFailureContinues(t *testing.T) {
	dir := t.TempDir()
	f1 := writeFile(t, dir, "a.txt", "a\n")
	missing := filepath.Join(dir, "does-not-exist.txt")
	f2 := writeFile(t, dir, "b.txt", "b\n")

	_, stderr, code := setfaclRun(t, "setfacl -m g:root:rx "+f1+" "+missing+" "+f2, dir)
	if code == 0 {
		t.Fatalf("expected non-zero exit due to missing operand, got 0")
	}
	if !strings.Contains(stderr, "does-not-exist.txt") {
		t.Fatalf("expected error mentioning the missing path, got %q", stderr)
	}

	// Both valid operands, before and after the failing one, must still
	// have gotten the ACL applied — processing must not abort on the
	// first bad operand.
	for _, p := range []string{f1, f2} {
		entries := decodeXattr(t, p, accessXattr)
		if _, ok := findGroupEntry(entries, 0); !ok {
			t.Fatalf("expected group:root entry on valid operand %s, got %+v", p, entries)
		}
	}
}

// TestSetfaclMultipleOperandsRecursiveEachSubtreeIndependent verifies that
// -R with multiple top-level operands recurses each operand's own subtree
// independently: both directory trees receive the entry throughout, not just
// the first operand.
func TestSetfaclMultipleOperandsRecursiveEachSubtreeIndependent(t *testing.T) {
	dir := t.TempDir()
	treeA := filepath.Join(dir, "treeA")
	treeB := filepath.Join(dir, "treeB")
	for _, d := range []string{treeA, treeB} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fileA := writeFile(t, treeA, "a.txt", "a\n")
	fileB := writeFile(t, treeB, "b.txt", "b\n")

	_, stderr, code := setfaclRun(t, "setfacl -R -m g:root:rx "+treeA+" "+treeB, dir)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr %q)", code, stderr)
	}

	for _, p := range []string{treeA, fileA, treeB, fileB} {
		entries := decodeXattr(t, p, accessXattr)
		if _, ok := findGroupEntry(entries, 0); !ok {
			t.Fatalf("expected group:root entry on %s, got %+v", p, entries)
		}
	}
}

// TestSetfaclModifyFlagDoesNotConsumeSecondPathOperand is a regression guard
// for the flag parser: -m takes an explicit flag value ("g:root:r"), so a
// second bare path operand must never be mistaken for part of the -m
// argument. Both PATH operands must receive the ACL.
func TestSetfaclModifyFlagDoesNotConsumeSecondPathOperand(t *testing.T) {
	dir := t.TempDir()
	f1 := writeFile(t, dir, "one.txt", "1\n")
	f2 := writeFile(t, dir, "two.txt", "2\n")

	_, stderr, code := setfaclRun(t, "setfacl -m g:root:r "+f1+" "+f2, dir)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr %q)", code, stderr)
	}

	for _, p := range []string{f1, f2} {
		entries := decodeXattr(t, p, accessXattr)
		entry, ok := findGroupEntry(entries, 0)
		if !ok {
			t.Fatalf("expected group:root entry on %s, got %+v", p, entries)
		}
		if entry.Perm != acl.PermRead {
			t.Fatalf("expected r-only perms on %s, got %v", p, entry.Perm)
		}
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
