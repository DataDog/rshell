// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package find

import (
	iofs "io/fs"
)

// fileUid is not meaningful on Windows; always returns 0, false.
func fileUid(info iofs.FileInfo) (uint32, bool) {
	return 0, false
}

// fileGid is not meaningful on Windows; always returns 0, false.
func fileGid(info iofs.FileInfo) (uint32, bool) {
	return 0, false
}

// fileNlink returns 1 on Windows as a reasonable default.
func fileNlink(info iofs.FileInfo) (uint64, bool) {
	return 1, true
}

// fileIno is not available via basic FileInfo on Windows; returns 0, false.
func fileIno(info iofs.FileInfo) (uint64, bool) {
	return 0, false
}

// lookupUidByName is not supported on Windows.
func lookupUidByName(name string) (uint32, bool) {
	return 0, false
}

// lookupGidByName is not supported on Windows.
func lookupGidByName(name string) (uint32, bool) {
	return 0, false
}

// uidHasPasswdEntry always returns true on Windows (no passwd database).
func uidHasPasswdEntry(uid uint32) bool {
	return true
}

// gidHasGroupEntry always returns true on Windows (no group database).
func gidHasGroupEntry(gid uint32) bool {
	return true
}
