// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"errors"
	"os"
	"syscall"
)

// isErrIsDirectory checks if the error is the Windows equivalent of EISDIR.
// On Windows, reading a directory handle returns ERROR_INVALID_FUNCTION (errno 1).
func isErrIsDirectory(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.Errno(1) // ERROR_INVALID_FUNCTION
	}
	return false
}

// checkAccess on Windows falls back to checking file existence since Windows
// does not have a direct equivalent of the Unix access(2) syscall.
func checkAccess(path string, mode uint32) error {
	_, err := os.Stat(path)
	return err
}
