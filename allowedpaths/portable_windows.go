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
	root, relPath, ok := sandbox.resolve(absPath)
	if !ok {
		return 0, 0, false
	}
	f, err := root.OpenFile(relPath, os.O_RDONLY, 0)
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

// accessCheck checks permissions using mode bits.
// Windows does not support access(2); mode-bit inspection is the best
// available approximation. rootAbsPath and rel are accepted for
// interface compatibility but not used on Windows.
func accessCheck(_, _ string, info fs.FileInfo, checkRead, checkWrite, checkExec bool) error {
	if !effectiveHasPerm(info, checkRead, checkWrite, checkExec) {
		return os.ErrPermission
	}
	return nil
}

// effectiveHasPerm checks whether the current process has the requested
// permission on Windows.  Windows does not use Unix UID/GID permission classes,
// so we fall back to checking any-class bits (0444 / 0222 / 0111).
func effectiveHasPerm(info fs.FileInfo, checkRead, checkWrite, checkExec bool) bool {
	perm := info.Mode().Perm()
	if checkRead && perm&0444 == 0 {
		return false
	}
	if checkWrite && perm&0222 == 0 {
		return false
	}
	return !(checkExec && perm&0111 == 0)
}
