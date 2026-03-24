// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedpaths

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// fileOnlyMatch reports whether rel matches the fileOnly name.
// Exact match is used even on Windows because NTFS supports per-directory
// case-sensitive mode (e.g. WSL). The inode-pinning check
// (verifyFileIdentity) provides the authoritative identity guarantee.
func fileOnlyMatch(rel, fileOnly string) bool {
	return rel == fileOnly
}

// fileIdentity extracts the canonical file identity (volume serial + file
// index) for a file within an os.Root using GetFileInformationByHandle.
// Used to pin file-only allowlist entries at construction time and verify
// they haven't been replaced.
func fileIdentity(r *os.Root, relPath string) (uint64, uint64, bool) {
	f, err := r.OpenFile(relPath, os.O_RDONLY, 0)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	h := syscall.Handle(f.Fd())
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &d); err != nil {
		return 0, 0, false
	}
	return uint64(d.VolumeSerialNumber), uint64(d.FileIndexHigh)<<32 | uint64(d.FileIndexLow), true
}

// fileIdentityAndMode extracts file identity and mode from a single open+fstat,
// ensuring both are captured atomically from the same inode. Used by New()
// to verify regularity and capture identity without a TOCTOU gap.
func fileIdentityAndMode(r *os.Root, relPath string) (uint64, uint64, fs.FileMode, bool) {
	f, err := r.OpenFile(relPath, os.O_RDONLY, 0)
	if err != nil {
		return 0, 0, 0, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, 0, 0, false
	}
	h := syscall.Handle(f.Fd())
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &d); err != nil {
		return 0, 0, 0, false
	}
	return uint64(d.VolumeSerialNumber), uint64(d.FileIndexHigh)<<32 | uint64(d.FileIndexLow), info.Mode(), true
}

// fileIdentityFromInfo extracts file identity from FileInfo on Windows.
// Windows FileInfo does not expose volume/file-index through Sys(), so
// this always returns false. Use fileIdentityFromFile for opened handles.
func fileIdentityFromInfo(_ fs.FileInfo) (uint64, uint64, bool) {
	return 0, 0, false
}

// fileIdentityFromFile extracts file identity from an opened fd using
// GetFileInformationByHandle. Used for atomic post-open identity verification.
func fileIdentityFromFile(f *os.File) (uint64, uint64, bool) {
	h := syscall.Handle(f.Fd())
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &d); err != nil {
		return 0, 0, false
	}
	return uint64(d.VolumeSerialNumber), uint64(d.FileIndexHigh)<<32 | uint64(d.FileIndexLow), true
}

// IsErrIsDirectory checks if the error is the Windows equivalent of EISDIR.
// On Windows, reading a directory handle returns ERROR_INVALID_FUNCTION (errno 1).
func IsErrIsDirectory(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.Errno(1) // ERROR_INVALID_FUNCTION
	}
	return false
}

// FileIdentity extracts canonical file identity on Windows using
// GetFileInformationByHandle (volume serial + file index).
// The path and sandbox are needed to open the file through the sandbox.
func FileIdentity(absPath string, _ fs.FileInfo, sandbox *Sandbox) (uint64, uint64, bool) {
	entry, relPath, ok := sandbox.resolveEntry(absPath)
	if !ok {
		return 0, 0, false
	}
	f, err := entry.root.OpenFile(relPath, os.O_RDONLY, 0)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	h := syscall.Handle(f.Fd())
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &d); err != nil {
		return 0, 0, false
	}
	return uint64(d.VolumeSerialNumber), uint64(d.FileIndexHigh)<<32 | uint64(d.FileIndexLow), true
}

// accessCheck verifies the path is inside the sandbox via os.Root.Stat,
// then checks read permission by attempting to open the file through
// os.Root. This respects NTFS ACLs — the kernel denies the open if
// the current user lacks read permission. Named pipes cannot appear in
// regular directories on Windows, so this cannot block.
//
//   - Read: verified by opening through os.Root (respects NTFS ACLs).
//   - Write: checked via mode bits from Stat. On Windows,
//     FILE_ATTRIBUTE_READONLY clears the write permission bits in
//     Mode().Perm(), so mode-bit inspection is reliable.
//   - Execute: Windows has no POSIX execute bits. The check always
//     returns ErrPermission so that test -x behaves like a POSIX shell.
func (r *root) accessCheck(rel string, checkRead, checkWrite, checkExec bool) (fs.FileInfo, error) {
	info, err := r.root.Stat(rel)
	if err != nil {
		return nil, err
	}

	// Windows has no POSIX execute bits — always deny execute checks.
	if checkExec {
		return info, os.ErrPermission
	}

	// On Windows, FILE_ATTRIBUTE_READONLY clears the write permission
	// bits in Mode().Perm(). Check them for write access.
	if checkWrite && info.Mode().Perm()&0200 == 0 {
		return info, os.ErrPermission
	}

	if checkRead && !info.IsDir() {
		f, err := r.root.OpenFile(rel, os.O_RDONLY, 0)
		if err != nil {
			return info, os.ErrPermission
		}
		if err := f.Close(); err != nil {
			return info, err
		}
	}

	return info, nil
}
