// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build darwin

package find

import (
	iofs "io/fs"
	"syscall"
	"time"
)

// fileAtime extracts the access time from FileInfo on macOS.
func fileAtime(info iofs.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Atimespec.Sec, st.Atimespec.Nsec)
	}
	return info.ModTime() // fallback
}

// fileCtime extracts the status change time from FileInfo on macOS.
func fileCtime(info iofs.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Ctimespec.Sec, st.Ctimespec.Nsec)
	}
	return info.ModTime() // fallback
}
