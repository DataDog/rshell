// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package interp

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/DataDog/rshell/allowedpaths"
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
	childStdin := os.NewFile(uintptr(duplicate), original.Name())

	// dup shares the source's blocking mode, and closing it does not wake a
	// blocking Linux read. Require deadline support for producer-backed input.
	if ctx.Done() != nil {
		if err := childStdin.SetReadDeadline(time.Time{}); err != nil {
			info, statErr := childStdin.Stat()
			if statErr != nil {
				_ = childStdin.Close()
				return nil, false, false, fmt.Errorf("inspect nested command stdin: %w", statErr)
			}
			if !info.Mode().IsRegular() && !allowedpaths.IsDevNullFile(info) {
				_ = childStdin.Close()
				return nil, false, false, fmt.Errorf("nested command stdin does not support cancellable reads: %s", info.Mode().Type())
			}
		}
	}
	return childStdin, true, false, nil
}
