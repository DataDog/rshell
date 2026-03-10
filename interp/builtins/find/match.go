// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package find

import (
	iofs "io/fs"
	"path"
	"strings"
)

// matchGlob matches a name against a glob pattern using path.Match.
func matchGlob(pattern, name string) bool {
	matched, err := path.Match(pattern, name)
	if err != nil {
		return false
	}
	return matched
}

// matchGlobFold matches a name against a glob pattern case-insensitively.
func matchGlobFold(pattern, name string) bool {
	matched, err := path.Match(strings.ToLower(pattern), strings.ToLower(name))
	if err != nil {
		return false
	}
	return matched
}

// matchType checks if a file's type matches the -type argument.
// typeArg may contain comma-separated types (GNU extension).
func matchType(info iofs.FileInfo, typeArg string) bool {
	fileType := fileTypeChar(info)

	// Handle comma-separated types.
	for i := 0; i < len(typeArg); i++ {
		c := typeArg[i]
		if c == ',' {
			continue
		}
		if c == fileType {
			return true
		}
	}
	return false
}

// fileTypeChar returns the find type character for a file's mode.
// Accepts FileInfo (not FileMode) to avoid adding io/fs.FileMode to the
// import allowlist — matches the pattern used by ls.go.
func fileTypeChar(info iofs.FileInfo) byte {
	mode := info.Mode()
	switch {
	case mode.IsRegular():
		return 'f'
	case mode&iofs.ModeDir != 0:
		return 'd'
	case mode&iofs.ModeSymlink != 0:
		return 'l'
	case mode&iofs.ModeNamedPipe != 0:
		return 'p'
	case mode&iofs.ModeSocket != 0:
		return 's'
	default:
		return '?'
	}
}

// sizeBlockSize returns the block size for rounding up in exact comparisons.
func sizeBlockSize(unit byte) int64 {
	switch unit {
	case 'c':
		return 1
	case 'w':
		return 2
	case 'b':
		return 512
	case 'k':
		return 1024
	case 'M':
		return 1024 * 1024
	case 'G':
		return 1024 * 1024 * 1024
	default:
		return 512
	}
}

// compareSize checks if fileSize matches the size predicate.
// GNU find rounds up to units for exact match: a 1-byte file is +0c, 1c, -2c.
func compareSize(fileSize int64, su sizeUnit) bool {
	blockSz := sizeBlockSize(su.unit)
	// Round file size up to the next block.
	fileBlocks := (fileSize + blockSz - 1) / blockSz
	if fileSize == 0 {
		fileBlocks = 0
	}

	switch su.cmp {
	case 1: // +n: strictly greater than n units
		return fileBlocks > su.n
	case -1: // -n: strictly less than n units
		return fileBlocks < su.n
	default: // exactly n units
		return fileBlocks == su.n
	}
}

// compareNumeric compares a value with the cmp operator.
func compareNumeric(actual, target int64, cmp int) bool {
	switch cmp {
	case 1: // +n: strictly greater
		return actual > target
	case -1: // -n: strictly less
		return actual < target
	default: // exactly n
		return actual == target
	}
}

// baseName returns the last element of a path.
func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}
