// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package interp

import (
	"context"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

func nestedStdinFile(ctx context.Context, stdin io.Reader) (*os.File, bool, bool, error) {
	original, callerOwned := stdin.(*os.File)
	if !callerOwned {
		f, err := stdinFile(ctx, stdin)
		return f, f != nil, false, err
	}

	var duplicate int
	var duplicateErr error
	raw, err := original.SyscallConn()
	if err != nil {
		return nil, false, false, err
	}
	err = raw.Control(func(fd uintptr) {
		duplicate, duplicateErr = unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 0)
	})
	if err != nil {
		return nil, false, false, err
	}
	if duplicateErr != nil {
		return nil, false, false, duplicateErr
	}
	return os.NewFile(uintptr(duplicate), original.Name()), true, true, nil
}
