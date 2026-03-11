// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package find

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPathGlobMatchTrailingBackslash(t *testing.T) {
	assert.True(t, pathGlobMatch(`abc\`, `abc\`))
	assert.False(t, pathGlobMatch(`abc\`, `abcd`))
	assert.False(t, pathGlobMatch(`abc\`, `abc`))
}

func TestMatchGlobMalformedBracket(t *testing.T) {
	// Malformed bracket patterns should fall back to literal comparison.
	assert.True(t, matchGlob("[", "["))
	assert.False(t, matchGlob("[", "a"))
	assert.True(t, matchGlob("[abc", "[abc"))
	assert.False(t, matchGlob("[abc", "a"))
}

func TestMatchGlobFoldMalformedBracket(t *testing.T) {
	assert.True(t, matchGlobFold("[", "["))
	assert.False(t, matchGlobFold("[", "a"))
}

func TestBaseNameEdgeCases(t *testing.T) {
	assert.Equal(t, "dir", baseName("dir"))
	assert.Equal(t, "dir", baseName("dir/"))
	assert.Equal(t, "dir", baseName("dir//"))
	assert.Equal(t, "dir", baseName("/path/to/dir"))
	assert.Equal(t, "dir", baseName("/path/to/dir/"))
	assert.Equal(t, "/", baseName("/"))
	assert.Equal(t, "/", baseName("///"))
	assert.Equal(t, "file", baseName("file"))
	assert.Equal(t, ".", baseName("."))
	assert.Equal(t, ".", baseName("./"))
	assert.Equal(t, "b", baseName("a/b"))
	assert.Equal(t, "b", baseName("a/b/"))
}

func TestMatchClassEdgeCases(t *testing.T) {
	// Valid class
	matched, width := matchClass("[abc]", 'a')
	assert.True(t, matched)
	assert.Equal(t, 5, width)

	// Non-matching valid class
	matched, width = matchClass("[abc]", 'z')
	assert.False(t, matched)
	assert.Equal(t, 5, width)

	// Negated class
	matched, width = matchClass("[!abc]", 'z')
	assert.True(t, matched)
	assert.Equal(t, 6, width)

	matched, width = matchClass("[^abc]", 'a')
	assert.False(t, matched)
	assert.Equal(t, 6, width)

	// Range
	matched, width = matchClass("[a-z]", 'm')
	assert.True(t, matched)
	assert.Equal(t, 5, width)

	matched, width = matchClass("[a-z]", 'A')
	assert.False(t, matched)
	assert.Equal(t, 5, width)

	// Malformed (unclosed)
	matched, width = matchClass("[abc", 'a')
	assert.False(t, matched)
	assert.Equal(t, 0, width)

	// Single char "[" — too short
	matched, width = matchClass("[", 'a')
	assert.False(t, matched)
	assert.Equal(t, 0, width)

	// "]" as first char in class (literal, not closing)
	matched, width = matchClass("[]abc]", ']')
	assert.True(t, matched)
	assert.Equal(t, 6, width)
}

func TestCompareNumeric(t *testing.T) {
	// Exact match
	assert.True(t, compareNumeric(5, 5, 0))
	assert.False(t, compareNumeric(5, 6, 0))

	// Greater than
	assert.True(t, compareNumeric(6, 5, 1))
	assert.False(t, compareNumeric(5, 5, 1))
	assert.False(t, compareNumeric(4, 5, 1))

	// Less than
	assert.True(t, compareNumeric(4, 5, -1))
	assert.False(t, compareNumeric(5, 5, -1))
	assert.False(t, compareNumeric(6, 5, -1))
}

func TestPathGlobMatchMalformedBracket(t *testing.T) {
	assert.True(t, pathGlobMatch("[", "["))
	assert.False(t, pathGlobMatch("[", "a"))
	assert.True(t, pathGlobMatch("dir/[sub/file", "dir/[sub/file"))
	assert.False(t, pathGlobMatch("dir/[sub/file", "dir/asub/file"))
	// Star followed by malformed bracket (backtracking interaction).
	assert.True(t, pathGlobMatch("*/[", "dir/["))
	assert.False(t, pathGlobMatch("*/[", "dir/a"))
}
