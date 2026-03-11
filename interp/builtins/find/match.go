// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package find

import (
	iofs "io/fs"
	"math"
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
	// Round file size up to the next block (ceiling division).
	// Guard against overflow: (fileSize + blockSz - 1) can exceed MaxInt64
	// when fileSize is close to MaxInt64.
	var fileBlocks int64
	if fileSize > 0 {
		if blockSz == 1 {
			fileBlocks = fileSize
		} else if fileSize <= math.MaxInt64-blockSz+1 {
			fileBlocks = (fileSize + blockSz - 1) / blockSz
		} else {
			// Overflow-safe ceiling division for very large file sizes.
			fileBlocks = fileSize/blockSz + 1
		}
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
// The shell normalises all paths to forward slashes on all platforms,
// so hardcoding '/' is correct even on Windows.
func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[i+1:]
		}
	}
	return p
}

// matchPathGlob matches a full path against a glob pattern where '*' crosses
// '/' (FNM_PATHNAME-free). This matches GNU find's -path behaviour.
func matchPathGlob(pattern, name string) bool {
	return pathGlobMatch(pattern, name)
}

// matchPathGlobFold is like matchPathGlob but case-insensitive.
func matchPathGlobFold(pattern, name string) bool {
	return pathGlobMatch(strings.ToLower(pattern), strings.ToLower(name))
}

// pathGlobMatch implements glob matching where '*' matches any character
// including '/', '?' matches exactly one character including '/', and
// '[...]' character classes work as in path.Match.
func pathGlobMatch(pattern, name string) bool {
	px, nx := 0, 0
	// nextPx/nextNx track the position to retry when a '*' fails to match.
	nextPx, nextNx := 0, 0
	starActive := false

	for px < len(pattern) || nx < len(name) {
		if px < len(pattern) {
			switch pattern[px] {
			case '*':
				// '*' matches zero or more of any character (including '/').
				// Record restart point and try matching zero chars first.
				starActive = true
				nextPx = px
				nextNx = nx + 1
				px++
				continue
			case '?':
				// '?' matches exactly one character (including '/').
				if nx < len(name) {
					px++
					nx++
					continue
				}
			case '[':
				// Character class — delegate to path.Match for the class portion.
				if nx < len(name) {
					matched, width := matchClass(pattern[px:], name[nx])
					if matched {
						px += width
						nx++
						continue
					}
				}
			case '\\':
				// Escape: next character is literal.
				px++
				if px >= len(pattern) {
					// Trailing backslash — treat as literal '\\'.
					if nx < len(name) && name[nx] == '\\' {
						nx++
						continue
					}
				} else if nx < len(name) && pattern[px] == name[nx] {
					px++
					nx++
					continue
				}
			default:
				if nx < len(name) && pattern[px] == name[nx] {
					px++
					nx++
					continue
				}
			}
		}
		// Current characters don't match. Backtrack to last '*' if possible.
		if starActive && nextNx <= len(name) {
			px = nextPx + 1
			nx = nextNx
			nextNx++
			continue
		}
		return false
	}
	return true
}

// matchClass tries to match a single character against a bracket expression
// starting at pattern[0] == '['. Returns (matched, width) where width is
// the number of bytes consumed from pattern (including the closing ']').
// On malformed classes, returns (false, 0).
func matchClass(pattern string, ch byte) (bool, int) {
	if len(pattern) < 2 || pattern[0] != '[' {
		return false, 0
	}
	i := 1
	negate := false
	if i < len(pattern) && pattern[i] == '^' {
		negate = true
		i++
	} else if i < len(pattern) && pattern[i] == '!' {
		negate = true
		i++
	}
	matched := false
	first := true
	for i < len(pattern) {
		if pattern[i] == ']' && !first {
			i++ // consume ']'
			if negate {
				return !matched, i
			}
			return matched, i
		}
		first = false
		lo := pattern[i]
		i++
		var hi byte
		if i+1 < len(pattern) && pattern[i] == '-' && pattern[i+1] != ']' {
			hi = pattern[i+1]
			i += 2
		} else {
			hi = lo
		}
		if lo <= ch && ch <= hi {
			matched = true
		}
	}
	// Unclosed bracket — malformed.
	return false, 0
}
