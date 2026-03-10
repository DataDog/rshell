// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package interp

import (
	"errors"
	"syscall"
)

func isErrIsDirectory(err error) bool {
	return errors.Is(err, syscall.EISDIR)
}

// checkAccess uses the access(2) syscall to test real uid/gid permissions.
// mode: 0x04 = R_OK, 0x02 = W_OK, 0x01 = X_OK.
func checkAccess(path string, mode uint32) error {
	return syscall.Access(path, mode)
}
