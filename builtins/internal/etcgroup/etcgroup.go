// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package etcgroup resolves a POSIX group name to its numeric GID, and adds
// a user to a group's supplementary member list, by reading and (for
// AddMember) rewriting /etc/group directly.
//
// This package lives under builtins/internal/ and is therefore exempt from
// the builtinAllowedSymbols allowlist check. It may use OS-specific APIs
// freely.
//
// # Sandbox bypass
//
// The Linux backend reads and writes /etc/group via direct os calls,
// intentionally bypassing the AllowedPaths sandbox (callCtx.OpenFile /
// callCtx.Truncate / callCtx.Remove). The path is hardcoded by this package
// and never derived from user-supplied input, so AllowedPaths restrictions
// do not apply. This matches the documented exception used by the ss, ip
// route, df, free, and uptime builtins for reads; AddMember extends the same
// exception to a bounded, narrowly-scoped write. See the usermod builtin's
// package doc comment for the full write-path design rationale (atomic
// rename, lack of a lock-file convention, Landlock/seccomp implications).
//
// # Platform support
//
// Only Linux is supported; other platforms return ErrNotSupported. This
// mirrors setfacl and usermod, the consumers of this package, which are
// themselves Linux-only (setfacl: POSIX ACL xattrs; usermod: /etc/group is a
// Linux/glibc convention with no portable equivalent this shell targets).
package etcgroup

import "errors"

// ErrGroupNotFound is returned by LookupGID and AddMember when no line in
// /etc/group names the requested group.
var ErrGroupNotFound = errors.New("group not found")

// ErrNotSupported is returned by LookupGID and AddMember on platforms
// without a backend.
var ErrNotSupported = errors.New("not supported on this platform")

// LookupGID returns the numeric GID for the named POSIX group, read
// directly from /etc/group.
func LookupGID(name string) (uint32, error) {
	return lookupGIDImpl(name)
}

// AddMember appends user to the supplementary member list of the group
// named groupName in /etc/group, and atomically rewrites the file.
//
// If user is already a member, AddMember returns nil without modifying the
// file (idempotent no-op), matching real usermod -aG's behavior. Every
// other line, and every other field of the target group's own line (GID,
// password, group name), is preserved exactly as it appears in the existing
// file. AddMember does not verify that user exists in /etc/passwd; callers
// that need that check (e.g. the usermod builtin) must perform it
// separately via builtins/internal/etcpasswd.
//
// AddMember does not accept or validate a caller-supplied path: it always
// targets the real, hardcoded /etc/group (see the package doc comment for
// why that bypasses AllowedPaths). Tests exercise the underlying rewrite
// logic against a temporary file via addMemberAtPath in the *_test.go files
// of this package, which is the package-internal seam for that purpose.
func AddMember(groupName, user string) error {
	return addMemberImpl(groupName, user)
}
