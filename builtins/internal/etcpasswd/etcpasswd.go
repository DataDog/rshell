// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package etcpasswd resolves whether a POSIX user account exists by reading
// /etc/passwd directly.
//
// This package deliberately does not use os/user.Lookup. os/user's pure-Go
// backend does parse /etc/passwd without cgo, but which backend is actually
// compiled in depends on build tags or the "osusergo"/"netgo"-style build
// constraints of the enclosing binary, and Go's cgo-based backend (used
// automatically on some platforms/configurations when cgo is available and
// CGO_ENABLED is not forced off in every build environment this project
// ships from) calls into glibc NSS, which can consult more than
// /etc/passwd (e.g. LDAP/NSS modules configured in /etc/nsswitch.conf) —
// behavior this package must not depend on implicitly. This package always
// reads exactly the fixed local file, on every build, matching etcgroup's
// existing approach for /etc/group and avoiding any ambiguity about which
// resolution path is actually exercised in this project's static,
// CGO_ENABLED=0 builds.
//
// This package lives under builtins/internal/ and is therefore exempt from
// the builtinAllowedSymbols allowlist check. It may use OS-specific APIs
// freely.
//
// # Sandbox bypass
//
// The Linux backend reads /etc/passwd via os.Open directly, intentionally
// bypassing the AllowedPaths sandbox (callCtx.OpenFile). The path is
// hardcoded by this package and never derived from user-supplied input, so
// AllowedPaths restrictions do not apply. This matches the documented
// exception used by etcgroup for /etc/group, and by the ss, ip route, df,
// free, and uptime builtins for their own fixed system paths: only
// existence of a named account is exposed — never the password/shell/home
// fields or any other /etc/passwd data.
//
// # Platform support
//
// Only Linux is supported; other platforms return ErrNotSupported. This
// mirrors etcgroup, whose sole consumer (setfacl) is itself Linux-only; this
// package's sole consumer (usermod) is Linux-only for the same reason
// /etc/group and /etc/passwd are Linux/glibc conventions with no portable
// equivalent this shell targets.
package etcpasswd

import "errors"

// ErrNotSupported is returned by UserExists on platforms without a backend.
var ErrNotSupported = errors.New("not supported on this platform")

// UserExists reports whether name is a valid, existing account name in
// /etc/passwd.
func UserExists(name string) (bool, error) {
	return userExistsImpl(name)
}
