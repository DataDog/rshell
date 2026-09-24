// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package etcgroup

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// etcGroupPath is the file read (and, by AddMember, rewritten) by this
// package. It is hardcoded — never derived from user input — so it is
// exempt from the AllowedPaths sandbox. See the package doc comment for the
// rationale.
const etcGroupPath = "/etc/group"

// maxGroupLine caps the per-line buffer size when scanning /etc/group. Real
// lines (even with a long member list) are well under a few KiB; this is a
// generous bound against a pathological /etc/group.
const maxGroupLine = 64 * 1024

// maxGroupLines caps the total number of lines scanned, bounding CPU time on
// a pathological input with many short lines.
const maxGroupLines = 1_000_000

// maxGroupFileSize bounds the amount of data AddMember will read into memory
// to rewrite /etc/group. Real /etc/group files are tiny (a few KiB to a few
// hundred KiB on the largest fleets); this is a generous bound against a
// pathological file consuming unbounded worker memory.
const maxGroupFileSize = 64 * 1024 * 1024

// lookupGIDImpl opens and scans /etc/group for name.
func lookupGIDImpl(name string) (uint32, error) {
	f, err := os.Open(etcGroupPath)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", etcGroupPath, err)
	}
	defer f.Close() //nolint:errcheck

	gid, err := parseGroupFile(f, name)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", etcGroupPath, err)
	}
	return gid, nil
}

// parseGroupFile scans /etc/group-formatted lines from r looking for a
// group named name, returning its GID. Split out from lookupGIDImpl so
// tests can exercise the parsing logic directly against a strings.Reader
// instead of the real /etc/group.
//
// Each non-comment, non-empty line is expected in the standard
// "name:passwd:gid:members" form. Lines that don't parse cleanly are
// skipped rather than treated as fatal, matching glibc's tolerant
// nss_files behaviour.
func parseGroupFile(r io.Reader, name string) (uint32, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 1024), maxGroupLine)

	lines := 0
	for scanner.Scan() {
		lines++
		if lines > maxGroupLines {
			break
		}
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		groupName, rest, ok := strings.Cut(line, ":")
		if !ok || groupName != name {
			continue
		}
		_, gidField, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}
		gidStr, _, _ := strings.Cut(gidField, ":")
		gid, err := strconv.ParseUint(gidStr, 10, 32)
		if err != nil {
			continue
		}
		return uint32(gid), nil
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	return 0, ErrGroupNotFound
}

// addMemberImpl rewrites /etc/group so that user is a member of groupName.
// See addMemberAtPath for the implementation; this wrapper pins the real,
// hardcoded system path.
func addMemberImpl(groupName, user string) error {
	return addMemberAtPath(etcGroupPath, groupName, user)
}

// addMemberAtPath is the path-parameterized core of AddMember. It is
// exported to package-internal tests (via the etcgroup test files) so they
// can exercise the full read/rewrite/rename logic against a temporary file
// instead of the real /etc/group; it is never called with a caller-supplied
// path from a builtin.
//
// # Write design
//
// The whole file is read into memory (bounded by maxGroupFileSize),
// line-by-line, preserving every byte of every line except the one target
// group line: its member list is the only thing that changes, and every
// other field of that line (name, password placeholder, GID) is copied
// through unmodified. Line terminators (bare "\n" or "\r\n") and the
// presence or absence of a final trailing newline are preserved exactly, so
// a diff of the rewritten file against the original touches only the target
// group's member list.
//
// The new content is written to a fresh temporary file created in the same
// directory as /etc/group (so the final rename is same-filesystem and
// therefore atomic — see os.Rename/rename(2)), then renamed over /etc/group.
// The file is never truncated or edited in place: if the process is killed,
// panics, or the host loses power at any point up to and including a
// successful write() of the temp file's content, /etc/group itself has not
// been touched, and the temp file (if it exists at all) is either absent,
// incomplete, or ignored — rename(2) is the only step that can make the new
// content visible as /etc/group, and it is atomic: a reader (including a
// concurrent nss_files getgrent()) always sees either the complete old file
// or the complete new file, never a partial one.
//
// # No lock-file convention
//
// Some system tools (glibc's lckpwdf(3), used by the shadow-utils
// useradd/usermod/groupadd family) additionally serialize password/group
// database edits with an flock on /etc/.pwd.lock before editing, precisely
// to avoid two concurrent editors racing to rename their own temp file over
// /etc/group and one edit silently clobbering the other's.
//
// This package deliberately does not take that lock. Two reasons:
//
//  1. /etc/.pwd.lock lives outside /etc/group. Taking that lock would
//     require the privileged worker's Landlock policy to grant access to a
//     second fixed system path beyond the one this builtin's remediation
//     actually needs to touch, widening the trusted-path exception for a
//     lock convention this one-shot worker gets "for free" another way (see
//     below) — the opposite of the narrow-scoping principle already
//     established for every other trusted-path grant in this codebase.
//  2. The privileged worker that runs this code is a disposable, one-shot
//     process handling exactly one verified command per invocation (see
//     cmd/rshell/privileged_worker_linux.go and this package's doc
//     comment). It does not itself run concurrent /etc/group edits from
//     multiple goroutines. The residual race this package accepts is
//     therefore narrower than the general lckpwdf problem: a *different*,
//     independent process (a real system useradd/usermod, or another rshell
//     remediation invocation started at nearly the same instant) renaming
//     its own edit over /etc/group at the same moment as this one. In that
//     narrow window the atomic rename still guarantees /etc/group is never
//     corrupted or torn — the loser's rename simply loses its update
//     (last-writer-wins on the directory entry), exactly as if the two edits
//     had been serialized microseconds apart. That is a data-loss risk
//     shared by every unlocked reader/writer of a POSIX file via
//     rename-in-place, not a corruption risk, and it is judged acceptable
//     for a narrowly-scoped, one-remediation-at-a-time fleet workflow. A
//     future change that needs true cross-tool serialization should revisit
//     this and add the /etc/.pwd.lock grant explicitly, rather than
//     silently relying on this decision.
func addMemberAtPath(path, groupName, user string) error {
	data, err := readBounded(path, maxGroupFileSize)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}

	newData, changed, found, err := upsertMember(data, groupName, user)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if !found {
		return ErrGroupNotFound
	}
	if !changed {
		// user is already a member: idempotent no-op, matching real
		// usermod -aG's behavior of silently succeeding.
		return nil
	}

	return writeAtomic(path, newData)
}

// readBounded reads the entire content of path, rejecting files larger
// than maxSize to bound worker memory against a pathological input.
func readBounded(path string, maxSize int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > maxSize {
		return nil, fmt.Errorf("file too large (%d bytes, max %d)", info.Size(), maxSize)
	}

	return io.ReadAll(io.LimitReader(f, maxSize+1))
}

// upsertMember rewrites the single line in data naming groupName so that
// user is present in its comma-separated member list, preserving every
// other byte of the file exactly. It returns the rewritten content, whether
// a change was actually made (false if user was already a member), and
// whether a line naming groupName was found at all.
//
// Only the first line naming groupName is considered, matching
// parseGroupFile/LookupGID's own first-match semantics (and glibc's
// nss_files, which returns the first matching entry for a duplicated group
// name).
func upsertMember(data []byte, groupName, user string) (newData []byte, changed, found bool, err error) {
	content := string(data)

	// Split preserving each line's own terminator, so unmatched lines
	// (including trailing-newline presence/absence) are reproduced
	// byte-for-byte.
	entries := splitLinesKeepEnds(content)

	var out strings.Builder
	out.Grow(len(content) + 64)

	for _, entry := range entries {
		if found {
			out.WriteString(entry)
			continue
		}

		body, term := entry, ""
		if strings.HasSuffix(body, "\n") {
			body, term = body[:len(body)-1], "\n"
		}
		cr := ""
		if strings.HasSuffix(body, "\r") {
			body, cr = body[:len(body)-1], "\r"
		}

		name, rest, ok := strings.Cut(body, ":")
		if !ok || name != groupName {
			out.WriteString(entry)
			continue
		}
		passwd, rest2, ok := strings.Cut(rest, ":")
		if !ok {
			// Group-name-shaped but missing the GID/members fields: not a
			// well-formed group line, so it is not a match for rewriting
			// (matches parseGroupFile's tolerant skip-on-malformed-line
			// behavior for the read side).
			out.WriteString(entry)
			continue
		}
		gidStr, membersStr, ok := strings.Cut(rest2, ":")
		if !ok {
			out.WriteString(entry)
			continue
		}

		found = true

		members := splitMembers(membersStr)
		if containsMember(members, user) {
			// Already a member: write the line back unchanged and report
			// no change.
			out.WriteString(entry)
			continue
		}
		members = append(members, user)
		changed = true

		out.WriteString(name)
		out.WriteByte(':')
		out.WriteString(passwd)
		out.WriteByte(':')
		out.WriteString(gidStr)
		out.WriteByte(':')
		out.WriteString(strings.Join(members, ","))
		out.WriteString(cr)
		out.WriteString(term)
	}

	return []byte(out.String()), changed, found, nil
}

// splitLinesKeepEnds splits s into a sequence of substrings, each retaining
// its own trailing "\n" (if any). The final element has no trailing "\n"
// only when s does not end in one (including the empty-string case for an
// empty file), so re-concatenating every element reproduces s exactly.
func splitLinesKeepEnds(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.SplitAfter(s, "\n")
	// strings.SplitAfter on a string ending in the separator produces a
	// trailing empty final element; drop it so callers don't see a
	// spurious empty "line".
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}

// splitMembers splits a /etc/group members field into its comma-separated
// entries, treating an empty field as zero members (strings.Split("", ",")
// would otherwise yield one empty-string member).
func splitMembers(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

// containsMember reports whether user appears exactly in members.
func containsMember(members []string, user string) bool {
	for _, m := range members {
		if m == user {
			return true
		}
	}
	return false
}

// writeAtomic writes data to a fresh temporary file in the same directory
// as path, then renames it over path. See addMemberAtPath's doc comment for
// the full crash-safety rationale.
//
// The temp file is created with path's exact existing permission bits
// (obtained via Lstat before creating the temp file) by resetting the
// process umask to 0 for the duration of the O_CREATE|O_EXCL open and
// restoring it immediately afterward, so the requested mode passes through
// to the kernel unmasked. This achieves an exact permission match without
// a separate chmod/fchmod syscall — see the usermod builtin's package doc
// comment for why that syscall-avoidance was a deliberate design goal (no
// seccomp denylist carve-out is needed for this builtin). Ownership is not
// explicitly set: the temp file is created by the process's effective
// uid/gid, which is root:root in the privileged worker, matching /etc/group's
// virtually universal root:root ownership on every mainstream Linux
// distribution (unlike /etc/shadow, /etc/group carries no restricted group
// ownership convention). A host with genuinely non-standard /etc/group
// ownership is out of scope for this narrow builtin.
func writeAtomic(path string, data []byte) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", path)
	}
	mode := info.Mode().Perm()
	dir := filepath.Dir(path)

	tmpPath, tmpFile, err := createTempFile(dir, mode)
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	// Best-effort cleanup: once the rename below succeeds, tmpPath no
	// longer exists under its temp name, and this Remove is a harmless
	// ENOENT no-op.
	defer os.Remove(tmpPath) //nolint:errcheck

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close() //nolint:errcheck
		return fmt.Errorf("write temp file %s: %w", tmpPath, err)
	}
	if err := tmpFile.Sync(); err != nil {
		tmpFile.Close() //nolint:errcheck
		return fmt.Errorf("sync temp file %s: %w", tmpPath, err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("close temp file %s: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmpPath, path, err)
	}
	return nil
}

// createTempFile creates a new, exclusively-created regular file in dir
// with exactly mode's permission bits (umask reset to 0 for the duration of
// the create so the requested mode is not masked), retrying with a fresh
// random name on a name collision. It returns the created file's path and
// open handle (positioned at offset 0); the caller owns closing it.
func createTempFile(dir string, mode os.FileMode) (string, *os.File, error) {
	const maxAttempts = 16
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		suffix, err := randomSuffix()
		if err != nil {
			return "", nil, err
		}
		candidate := filepath.Join(dir, "group."+suffix)

		oldUmask := unix.Umask(0)
		f, openErr := os.OpenFile(candidate, os.O_RDWR|os.O_CREATE|os.O_EXCL, mode)
		unix.Umask(oldUmask)

		if openErr == nil {
			return candidate, f, nil
		}
		if os.IsExist(openErr) {
			lastErr = openErr
			continue
		}
		return "", nil, openErr
	}
	return "", nil, fmt.Errorf("could not create a unique temp file after %d attempts: %w", maxAttempts, lastErr)
}

// randomSuffix returns an 8-byte-hex-encoded cryptographically random
// string suitable for a temp file name suffix, matching mktemp-style
// unpredictability (O_EXCL already prevents a symlink race regardless, but
// an unpredictable name avoids handing an adversary a stable name to
// pre-create).
func randomSuffix() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate random suffix: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
