// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package allowedpaths

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// posixACLAccessXattr and posixACLDefaultXattr are the fixed extended
// attribute names glibc/libacl use to store POSIX ACLs. See the acl
// package's doc comment for the binary format stored in their values.
const (
	posixACLAccessXattr  = "system.posix_acl_access"
	posixACLDefaultXattr = "system.posix_acl_default"
)

// maxACLXattrSize bounds the buffer GetACL grows to when reading an ACL
// xattr, guarding against a pathological or corrupt on-disk value. Real ACL
// xattrs are a handful of 8-byte entries plus a 4-byte header; this is a
// generous ceiling.
const maxACLXattrSize = 64 * 1024

// GetACL reads the system.posix_acl_access and (for directories)
// system.posix_acl_default extended attributes of the file at path,
// returning their raw xattr bytes. A missing attribute (no ACL set, or a
// default ACL requested on a non-directory) is reported as a nil slice with
// no error, not ENODATA/ENOATTR — callers use acl.MinimalACLFromMode as the
// base in that case.
//
// Like Truncate, the target is resolved through resolveWriteTarget and
// opened via root.openWriteFile, so GetACL inherits the same TOCTOU-safe,
// no-follow, hard-link-rejecting guard SetACL uses: reading through a
// hard-linked write target reads the same inode's ACL that a subsequent
// SetACL on the same path would refuse to mutate, so gating the read
// identically avoids acting on a target the write path is about to reject
// anyway.
func (s *Sandbox) GetACL(path, cwd string) (access, def []byte, err error) {
	if s == nil {
		return nil, nil, &os.PathError{Op: "getxattr", Path: path, Err: os.ErrPermission}
	}
	if s.readOnly {
		return nil, nil, &os.PathError{Op: "getxattr", Path: path, Err: os.ErrPermission}
	}

	absPath := toAbs(path, cwd)
	ar, relPath, ok := s.resolveWriteTarget(absPath)
	if !ok {
		return nil, nil, &os.PathError{Op: "getxattr", Path: path, Err: os.ErrPermission}
	}

	f, err := ar.openWriteFile(relPath, os.O_RDONLY, 0)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close() //nolint:errcheck

	access, err = getxattr(f, posixACLAccessXattr)
	if err != nil {
		return nil, nil, &os.PathError{Op: "getxattr", Path: path, Err: err}
	}

	info, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		def, err = getxattr(f, posixACLDefaultXattr)
		if err != nil {
			return nil, nil, &os.PathError{Op: "getxattr", Path: path, Err: err}
		}
	}
	return access, def, nil
}

// SetACL writes the system.posix_acl_access extended attribute of the file
// at path to access, and (when def is non-nil) the system.posix_acl_default
// attribute to def. Both writes target the same open, guarded descriptor
// obtained via root.openWriteFile — the identical TOCTOU-safe, no-follow,
// hard-link-rejecting choke point Truncate and write-mode Open already use
// — since an ACL xattr is a property of the inode and must be gated exactly
// like file content: setting it through one hard-linked name changes it for
// every name that inode has.
func (s *Sandbox) SetACL(path, cwd string, access, def []byte) error {
	if s == nil {
		return &os.PathError{Op: "setxattr", Path: path, Err: os.ErrPermission}
	}
	if s.readOnly {
		return &os.PathError{Op: "setxattr", Path: path, Err: os.ErrPermission}
	}

	absPath := toAbs(path, cwd)
	ar, relPath, ok := s.resolveWriteTarget(absPath)
	if !ok {
		return &os.PathError{Op: "setxattr", Path: path, Err: os.ErrPermission}
	}

	// O_RDONLY is enough: setxattr(2)/fsetxattr(2) only requires the fd to
	// refer to the target, not to be opened for writing the file's own
	// content. Using O_RDONLY here (rather than O_WRONLY) also sidesteps
	// EISDIR, since directories cannot be opened O_WRONLY but do need a
	// default-ACL xattr set on them.
	f, err := ar.openWriteFile(relPath, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck

	if err := unix.Fsetxattr(int(f.Fd()), posixACLAccessXattr, access, 0); err != nil {
		return &os.PathError{Op: "setxattr", Path: path, Err: err}
	}
	if def != nil {
		if err := unix.Fsetxattr(int(f.Fd()), posixACLDefaultXattr, def, 0); err != nil {
			return &os.PathError{Op: "setxattr", Path: path, Err: err}
		}
	}
	return nil
}

// getxattr reads the named extended attribute from f, growing the buffer on
// ERANGE up to maxACLXattrSize. A missing attribute (ENODATA, reported as
// unix.ENODATA; some filesystems/older kernels report ENOATTR, which on
// Linux is an alias for the same errno value) is reported as (nil, nil).
func getxattr(f *os.File, name string) ([]byte, error) {
	size := 256
	for {
		buf := make([]byte, size)
		n, err := unix.Fgetxattr(int(f.Fd()), name, buf)
		if err == nil {
			return buf[:n], nil
		}
		if errors.Is(err, unix.ENODATA) {
			return nil, nil
		}
		if errors.Is(err, unix.ERANGE) && size < maxACLXattrSize {
			size *= 4
			if size > maxACLXattrSize {
				size = maxACLXattrSize
			}
			continue
		}
		return nil, err
	}
}
