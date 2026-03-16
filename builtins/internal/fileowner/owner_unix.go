// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package fileowner

import (
	"fmt"
	"io/fs"
	"os/user"
	"syscall"
)

// Lookup returns the owner name, group name, and hard link count for the
// given FileInfo. On Unix this extracts UID/GID from Stat_t and resolves
// them to names via os/user. If name resolution fails, the numeric ID is
// returned as a string.
func Lookup(info fs.FileInfo) (owner, group string, nlink uint64) {
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
	u, err := user.LookupId(uintString(uid))
	if err != nil {
		return uintString(uid)
	}
	return u.Username
}

func lookupGID(gid uint32) string {
	g, err := user.LookupGroupId(uintString(gid))
	if err != nil {
		return uintString(gid)
	}
	return g.Name
}

func uintString(v uint32) string {
	return fmt.Sprintf("%d", v)
}
