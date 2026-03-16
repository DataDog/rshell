// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package find

import (
	iofs "io/fs"
	"syscall"
	"time"
)

// fileAtime extracts the last access time from FileInfo on Windows.
func fileAtime(info iofs.FileInfo) time.Time {
	if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return time.Unix(0, d.LastAccessTime.Nanoseconds())
	}
	return info.ModTime() // fallback
}

// fileCtime extracts the creation time from FileInfo on Windows.
// Note: Windows does not have a POSIX-style ctime (status change time).
// CreationTime is used instead, which is a documented divergence.
func fileCtime(info iofs.FileInfo) time.Time {
	if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return time.Unix(0, d.CreationTime.Nanoseconds())
	}
	return info.ModTime() // fallback
}
