// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package interp

import (
	"errors"
	"io/fs"
	"os"
)

// portableErrMsg returns a POSIX-style error message for the given error,
// normalizing platform-specific syscall messages to consistent strings.
// This ensures shell error output is identical across Linux, macOS, and Windows.
func portableErrMsg(err error) string {
	if err == nil {
		return ""
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "no such file or directory"
	case errors.Is(err, fs.ErrPermission):
		return "permission denied"
	case errors.Is(err, fs.ErrExist):
		return "file exists"
	case isErrIsDirectory(err):
		return "is a directory"
	}
	return err.Error()
}

// portablePathError returns a *os.PathError with a normalized error message.
// If the error is not a *os.PathError, it is returned as-is.
// Only the Err field is normalized; the Path and Op fields are preserved as-is.
func portablePathError(err error) error {
	if err == nil {
		return nil
	}
	var pe *os.PathError
	if !errors.As(err, &pe) {
		return err
	}
	return &os.PathError{
		Op:   pe.Op,
		Path: toSlash(pe.Path),
		Err:  errors.New(portableErrMsg(pe.Err)),
	}
}
