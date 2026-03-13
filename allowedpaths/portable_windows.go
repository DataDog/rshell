// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedpaths

import (
	"errors"
	"io/fs"
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
