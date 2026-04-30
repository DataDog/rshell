// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package du

import (
	iofs "io/fs"
)

// infoBlocks always returns false on Windows: the standard
// FileInfo.Sys() exposes Win32FileAttributeData which lacks an
// allocation-size field, and GetFileInformationByHandleEx requires
// `unsafe`, which is permanently banned by the symbol allowlist. Callers
// fall back to the apparent-size approximation in entrySize().
func infoBlocks(_ iofs.FileInfo) (int64, bool) {
	return 0, false
}

// infoNlink returns 1 on Windows because hard-link counts cannot be
// obtained without the GetFileInformationByHandle path (used by ls/wc),
// and du never opens individual files by handle. 1 means "treat as a
// unique inode," which prevents accidental dedup of distinct files. This
// is conservative and matches the apparent-size accounting we already
// fall back to on Windows.
func infoNlink(_ iofs.FileInfo) uint64 {
	return 1
}
