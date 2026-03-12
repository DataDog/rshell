// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"errors"
	"io/fs"
	"os"
	"syscall"

	"github.com/DataDog/rshell/interp/builtins"
)

func fileIdentity(absPath string, _ fs.FileInfo, sandbox *pathSandbox) (builtins.FileID, bool) {
	// Open through the sandbox to enforce the allowlist. The sandbox's
	// resolve validates the absolute path against the allowed roots and
	// returns an os.Root + relative path. os.Root.OpenFile on Windows
	// already uses FILE_FLAG_BACKUP_SEMANTICS for directories.
	root, relPath, ok := sandbox.resolve(absPath)
	if !ok {
		return builtins.FileID{}, false
	}
	f, err := root.OpenFile(relPath, os.O_RDONLY, 0)
	if err != nil {
		return builtins.FileID{}, false
	}
	defer f.Close()

	h := syscall.Handle(f.Fd())
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
