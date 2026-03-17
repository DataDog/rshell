// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package allowedpaths

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// IsErrIsDirectory reports whether err is an "is a directory" error.
func IsErrIsDirectory(err error) bool {
	return errors.Is(err, syscall.EISDIR)
}

// FileIdentity extracts canonical file identity (dev+inode) from FileInfo.
// On Unix, this is extracted directly from Stat_t via info.Sys().
func FileIdentity(_ string, info fs.FileInfo, _ *Sandbox) (uint64, uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return uint64(st.Dev), uint64(st.Ino), true
}

func (r *root) accessCheck(rel string, checkRead, checkWrite, checkExec bool) (fs.FileInfo, error) {
	info, err := r.root.Stat(rel)
	if err != nil {
		return nil, err
	}

	// For read checks on regular files, attempt to open through os.Root.
	// This is fd-relative (openat) and respects POSIX ACLs. FIFOs and
	// directories are excluded: OpenFile blocks on FIFOs without a
	// writer, and directories return a handle rather than a permission
	// error.
	if checkRead && info.Mode().IsRegular() {
		f, err := r.root.OpenFile(rel, os.O_RDONLY, 0)
		if err != nil {
			return info, os.ErrPermission
		}
		f.Close()
		if !checkWrite && !checkExec {
			return info, nil
		}
	}

	// For write, execute, directory read, and FIFO read checks, fall
	// back to mode-bit inspection on the Stat result (which came from
	// the fd-relative fstatat, so no TOCTOU).
	if !effectiveHasPerm(info, checkRead && !info.Mode().IsRegular(), checkWrite, checkExec) {
		return info, os.ErrPermission
	}
	return info, nil
}

// effectiveHasPerm checks whether the current process has the requested
// permission by inspecting the file's owner/group/other permission class
// that applies to the effective UID and GID of the running process.
//
// On Unix this uses the Stat_t from info.Sys() to determine the owning
// UID/GID and then selects the owner, group, or other permission bits
// accordingly.  If the type assertion fails (should not happen in practice),
// it falls back to checking any-class bits.
func effectiveHasPerm(info fs.FileInfo, checkRead, checkWrite, checkExec bool) bool {
	perm := info.Mode().Perm()

	// Determine which permission class applies to the current process.
	// Default to "other" bits and narrow down if we have Stat_t.
	ownerBits := fs.FileMode(0007) // other bits by default
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		uid := os.Getuid()
		if uid == 0 {
			// Root bypasses read/write permission checks (CAP_DAC_OVERRIDE).
			// Execute still requires at least one x bit to be set.
			if checkExec && perm&0111 == 0 {
				return false
			}
			return true
		}
		gid := os.Getgid()
		switch {
		case int(st.Uid) == uid:
			ownerBits = 0700
		case int(st.Gid) == gid:
			ownerBits = 0070
		default:
			ownerBits = 0007
			// Check supplementary groups — the process may belong to
			// additional groups beyond the primary GID.
			if groups, err := os.Getgroups(); err == nil {
				for _, g := range groups {
					if int(st.Gid) == g {
						ownerBits = 0070
						break
					}
				}
			}
		}
	}

	if checkRead {
		if perm&0444&ownerBits == 0 {
			return false
		}
	}
	if checkWrite {
		if perm&0222&ownerBits == 0 {
			return false
		}
	}
	if checkExec {
		if perm&0111&ownerBits == 0 {
			return false
		}
	}
	return true
}
