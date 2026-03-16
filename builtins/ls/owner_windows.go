// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ls

import iofs "io/fs"

// fileOwner returns the owner name, group name, and hard link count.
// On Windows, file ownership requires the Windows Security API which is
// not available here, so we return empty strings and 0.
func fileOwner(info iofs.FileInfo) (owner, group string, nlink uint64) {
	return "", "", 0
}

// fileBlocks returns the number of 512-byte blocks allocated for the file.
// On Windows, this information is not available, so we return 0.
func fileBlocks(info iofs.FileInfo) int64 {
	return 0
}
