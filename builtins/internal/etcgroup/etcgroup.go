// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package etcgroup resolves a POSIX group name to its numeric GID by
// reading /etc/group directly.
//
// This package lives under builtins/internal/ and is therefore exempt from
// the builtinAllowedSymbols allowlist check. It may use OS-specific APIs
// freely.
//
// # Sandbox bypass
//
// The Linux backend reads /etc/group via os.Open directly, intentionally
// bypassing the AllowedPaths sandbox (callCtx.OpenFile). The path is
// hardcoded by this package and never derived from user-supplied input, so
// AllowedPaths restrictions do not apply. This matches the documented
// exception used by the ss, ip route, df, free, and uptime builtins: only a
// fixed, non-user-controllable path is read, and only the group-name-to-GID
// mapping is exposed — never full group membership lists or any other
// /etc/group field.
//
// # Platform support
//
// Only Linux is supported; other platforms return ErrNotSupported. This
// mirrors setfacl, the sole consumer of this package, which is itself
// Linux-only (POSIX ACL xattrs are a Linux-specific filesystem feature).
package etcgroup

import "errors"

// ErrGroupNotFound is returned by LookupGID when no line in /etc/group
// names the requested group.
var ErrGroupNotFound = errors.New("group not found")

// ErrNotSupported is returned by LookupGID on platforms without a backend.
var ErrNotSupported = errors.New("not supported on this platform")

// LookupGID returns the numeric GID for the named POSIX group, read
// directly from /etc/group.
func LookupGID(name string) (uint32, error) {
	return lookupGIDImpl(name)
}
