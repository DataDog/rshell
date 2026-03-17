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

// fileBlocks estimates the number of 512-byte blocks allocated for the file.
// Windows does not expose block allocation through standard Go APIs, so we
// estimate using the NTFS default cluster size of 4096 bytes. This may be
// inaccurate for sparse, compressed, or non-default cluster-size volumes.
func fileBlocks(info iofs.FileInfo) int64 {
	size := info.Size()
	if size <= 0 {
		return 0
	}
	// Round up to nearest 4096-byte cluster, convert to 512-byte blocks.
	return ((size + 4095) / 4096) * 8
}
