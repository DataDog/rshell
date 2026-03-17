// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package ls

import (
	"fmt"
	iofs "io/fs"
	"syscall"
)

// fileOwner returns the numeric UID, GID, and hard link count for the given
// FileInfo. Names are not resolved to avoid reading /etc/passwd or triggering
// NSS/LDAP lookups outside the sandbox.
func fileOwner(path string, info iofs.FileInfo) (owner, group string, nlink uint64) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "?", "?", 0
	}

	owner = fmt.Sprintf("%d", st.Uid)
	group = fmt.Sprintf("%d", st.Gid)
	nlink = uint64(st.Nlink)
	return owner, group, nlink
}

// fileBlocks returns the number of 512-byte blocks allocated for the file.
func fileBlocks(info iofs.FileInfo) int64 {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return st.Blocks
}
