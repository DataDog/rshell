// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package fileowner

import "io/fs"

// Lookup returns the owner name, group name, and hard link count for the
// given FileInfo. On Windows, file ownership requires the Windows Security
// API which is not available here, so we return empty strings and 0.
func Lookup(info fs.FileInfo) (owner, group string, nlink uint64) {
	return "", "", 0
}
