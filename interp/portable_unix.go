// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package interp

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

func isErrIsDirectory(err error) bool {
	return errors.Is(err, syscall.EISDIR)
}

// effectiveHasPerm checks whether the current process has the requested
// permission (writeMask or execMask, each a 3-bit pattern like 0222 or 0111)
// by inspecting the file's owner/group/other permission class that applies to
// the effective UID and GID of the running process.
//
// On Unix this uses the Stat_t from info.Sys() to determine the owning
// UID/GID and then selects the owner, group, or other permission bits
// accordingly.  If the type assertion fails (should not happen in practice),
// it falls back to checking any-class bits.
func effectiveHasPerm(info fs.FileInfo, writeMask, execMask fs.FileMode, checkWrite, checkExec bool) bool {
	perm := info.Mode().Perm()

	// Determine which permission class applies to the current process.
	// Default to "other" bits and narrow down if we have Stat_t.
	ownerBits := fs.FileMode(0007) // other bits by default
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		uid := os.Getuid()
		gid := os.Getgid()
		switch {
		case uid == 0:
			// root can read/write anything; for execute, any x bit suffices.
			ownerBits = 0777
		case int(st.Uid) == uid:
			ownerBits = 0700
		case int(st.Gid) == gid:
			ownerBits = 0070
		default:
			ownerBits = 0007
		}
	}

	if checkWrite {
		// Intersect the write mask with the applicable owner bits.
		if perm&writeMask&ownerBits == 0 {
			return false
		}
	}
	if checkExec {
		if perm&execMask&ownerBits == 0 {
			return false
		}
	}
	return true
}
