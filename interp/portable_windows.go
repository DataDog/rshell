// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"errors"
	"io/fs"
	"syscall"

	"github.com/DataDog/rshell/interp/builtins"
)

func fileIdentity(path string, _ fs.FileInfo) (builtins.FileID, bool) {
	pathp, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return builtins.FileID{}, false
	}
	// FILE_FLAG_BACKUP_SEMANTICS is required to open directory handles.
	// dwDesiredAccess=0 queries metadata only, minimising permission requirements.
	h, err := syscall.CreateFile(
		pathp,
		0,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return builtins.FileID{}, false
	}
	defer syscall.CloseHandle(h)

	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &d); err != nil {
		return builtins.FileID{}, false
	}
	return builtins.FileID{
		Dev: uint64(d.VolumeSerialNumber),
		Ino: uint64(d.FileIndexHigh)<<32 | uint64(d.FileIndexLow),
	}, true
}

// isErrIsDirectory checks if the error is the Windows equivalent of EISDIR.
// On Windows, reading a directory handle returns ERROR_INVALID_FUNCTION (errno 1).
func isErrIsDirectory(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.Errno(1) // ERROR_INVALID_FUNCTION
	}
	return false
}

// effectiveHasPerm checks whether the current process has the requested
// permission on Windows.  Windows does not use Unix UID/GID permission classes,
// so we fall back to checking any-class bits (0222 / 0111) as before.
func effectiveHasPerm(info fs.FileInfo, writeMask, execMask fs.FileMode, checkWrite, checkExec bool) bool {
	perm := info.Mode().Perm()
	if checkWrite && perm&writeMask == 0 {
		return false
	}
	return !(checkExec && perm&execMask == 0)
}
