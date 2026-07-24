// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build darwin

package fsstat

import (
	"os"

	"golang.org/x/sys/unix"
)

func read(root *os.Root, relPath string) (Info, error) {
	before, err := root.Lstat(relPath)
	if err != nil {
		return Info{}, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return Info{}, ErrPathChanged
	}

	// O_EVTONLY obtains a metadata-only descriptor. O_NONBLOCK is still
	// necessary for FIFOs because Darwin otherwise waits for a peer despite
	// O_EVTONLY. Limit it to non-regular files because combining the flags
	// makes Darwin apply ordinary read-permission checks to regular files.
	flags := unix.O_EVTONLY
	if !before.Mode().IsRegular() {
		flags |= unix.O_NONBLOCK
	}
	// os.Root performs each path traversal relative to its pinned descriptor
	// and prevents escapes.
	f, err := root.OpenFile(relPath, flags, 0)
	if err != nil {
		return Info{}, err
	}
	defer f.Close() //nolint:errcheck

	opened, err := f.Stat()
	if err != nil {
		return Info{}, err
	}
	if opened.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, opened) {
		return Info{}, ErrPathChanged
	}

	var st unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &st); err != nil {
		return Info{}, err
	}

	after, err := root.Lstat(relPath)
	if err != nil {
		return Info{}, ErrPathChanged
	}
	if after.Mode()&os.ModeSymlink != 0 || !os.SameFile(opened, after) {
		return Info{}, ErrPathChanged
	}

	typeID := uint64(st.Type)
	return Info{
		ID:                   fsid(st.Fsid.Val[0], st.Fsid.Val[1]),
		IDAvailable:          true,
		NameMaxAvailable:     false,
		TypeID:               typeID,
		TypeIDAvailable:      true,
		TypeName:             unix.ByteSliceToString(st.Fstypename[:]),
		IOBlockSize:          nonnegative(int64(st.Iosize)),
		FundamentalBlockSize: uint64(st.Bsize),
		Blocks:               st.Blocks,
		BlocksFree:           st.Bfree,
		BlocksAvailable:      st.Bavail,
		Files:                st.Files,
		FilesFree:            st.Ffree,
		FilesAvailable:       true,
	}, nil
}

func nonnegative(value int64) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value)
}

func fsid(high, low int32) uint64 {
	return uint64(uint32(high))<<32 | uint64(uint32(low))
}
