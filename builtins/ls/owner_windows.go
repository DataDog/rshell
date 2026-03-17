// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ls

import (
	iofs "io/fs"
	"os"
	"syscall"
)

// fileOwner returns the owner, group, and hard link count.
// On Windows, UID/GID do not exist so owner and group are returned as "?".
// Hard link count is resolved via GetFileInformationByHandle.
func fileOwner(path string, info iofs.FileInfo) (owner, group string, nlink uint64) {
	owner = "?"
	group = "?"
	nlink = getNlink(path)
	return owner, group, nlink
}

// getNlink opens the file at path and queries GetFileInformationByHandle
// to retrieve the hard link count. Returns 1 on any failure.
func getNlink(path string) uint64 {
	if path == "" {
		return 1
	}
	f, err := os.Open(path)
	if err != nil {
		return 1
	}
	defer f.Close()

	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &d); err != nil {
		return 1
	}
	return uint64(d.NumberOfLinks)
}

// fileBlocks returns the number of 512-byte blocks allocated for the file.
// On Windows this information is not available, so we return -1 to signal
// that the total line should be suppressed.
func fileBlocks(info iofs.FileInfo) int64 {
	return -1
}
