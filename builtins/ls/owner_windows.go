// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ls

import (
	"context"
	iofs "io/fs"
	"os"
	"syscall"

	"github.com/DataDog/rshell/builtins"
)

// fileOwner returns the owner, group, and hard link count.
// On Windows, UID/GID do not exist so owner and group are returned as "?".
// Hard link count is resolved via GetFileInformationByHandle, opening the
// file through the sandbox (callCtx.OpenFile) to respect AllowedPaths.
func fileOwner(ctx context.Context, callCtx *builtins.CallContext, path string, info iofs.FileInfo) (owner, group string, nlink uint64) {
	owner = "?"
	group = "?"
	nlink = 1
	if n, ok := getNlink(ctx, callCtx, path); ok {
		nlink = n
	}
	return owner, group, nlink
}

// getNlink opens the file through the sandbox and queries
// GetFileInformationByHandle to retrieve the hard link count.
func getNlink(ctx context.Context, callCtx *builtins.CallContext, path string) (uint64, bool) {
	if path == "" || callCtx == nil || callCtx.OpenFile == nil {
		return 0, false
	}
	rc, err := callCtx.OpenFile(ctx, path, os.O_RDONLY, 0)
	if err != nil {
		return 0, false
	}
	defer rc.Close()

	// The sandbox returns an *os.File wrapped as io.ReadWriteCloser.
	// Type-assert to get the file descriptor for the Windows API call.
	type fder interface{ Fd() uintptr }
	fd, ok := rc.(fder)
	if !ok {
		return 0, false
	}

	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(fd.Fd()), &d); err != nil {
		return 0, false
	}
	return uint64(d.NumberOfLinks), true
}

// fileBlocks returns the number of 512-byte blocks allocated for the file.
// On Windows this information is not available through standard Go APIs,
// so we return -1 to signal that the total line should be suppressed.
func fileBlocks(info iofs.FileInfo) int64 {
	return -1
}
