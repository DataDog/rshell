// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package ls

import (
	"fmt"
	iofs "io/fs"
	"os/user"
	"syscall"
)

// fileOwner returns the owner name, group name, and hard link count for the
// given FileInfo by extracting UID/GID from Stat_t and resolving via os/user.
func fileOwner(info iofs.FileInfo) (owner, group string, nlink uint64) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "?", "?", 0
	}

	owner = lookupUID(st.Uid)
	group = lookupGID(st.Gid)
	nlink = uint64(st.Nlink)
	return owner, group, nlink
}

func lookupUID(uid uint32) string {
	u, err := user.LookupId(fmt.Sprintf("%d", uid))
	if err != nil {
		return fmt.Sprintf("%d", uid)
	}
	return u.Username
}

func lookupGID(gid uint32) string {
	g, err := user.LookupGroupId(fmt.Sprintf("%d", gid))
	if err != nil {
		return fmt.Sprintf("%d", gid)
	}
	return g.Name
}
