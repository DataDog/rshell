// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package find

import (
	iofs "io/fs"
	"os/user"
	"strconv"
	"syscall"
)

// fileUid extracts the file owner's UID from FileInfo on Unix.
func fileUid(info iofs.FileInfo) (uint32, bool) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Uid, true
	}
	return 0, false
}

// fileGid extracts the file's group GID from FileInfo on Unix.
func fileGid(info iofs.FileInfo) (uint32, bool) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Gid, true
	}
	return 0, false
}

// fileNlink extracts the hard link count from FileInfo on Unix.
func fileNlink(info iofs.FileInfo) (uint64, bool) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink), true
	}
	return 0, false
}

// fileIno extracts the inode number from FileInfo on Unix.
func fileIno(info iofs.FileInfo) (uint64, bool) {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st.Ino, true
	}
	return 0, false
}

// lookupUidByName looks up a user by name and returns their UID.
func lookupUidByName(name string) (uint32, bool) {
	u, err := user.Lookup(name)
	if err != nil {
		return 0, false
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(uid), true
}

// lookupGidByName looks up a group by name and returns its GID.
func lookupGidByName(name string) (uint32, bool) {
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, false
	}
	gid, err := strconv.ParseUint(g.Gid, 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(gid), true
}

// uidHasPasswdEntry checks if a UID has a corresponding user entry.
func uidHasPasswdEntry(uid uint32) bool {
	_, err := user.LookupId(strconv.FormatUint(uint64(uid), 10))
	return err == nil
}

// gidHasGroupEntry checks if a GID has a corresponding group entry.
func gidHasGroupEntry(gid uint32) bool {
	_, err := user.LookupGroupId(strconv.FormatUint(uint64(gid), 10))
	return err == nil
}
