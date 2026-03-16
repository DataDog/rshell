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
	// A trailing backslash is a dangling escape (no character to escape).
	// GNU find's fnmatch treats this as non-matching for any input,
	// including a literal backslash character.
	assert.False(t, pathGlobMatch(`abc\`, `abc\`))
	assert.False(t, pathGlobMatch(`abc\`, `abcd`))
	assert.False(t, pathGlobMatch(`abc\`, `abc`))
	assert.False(t, pathGlobMatch(`\`, `\`))
	assert.False(t, pathGlobMatch(`*\`, `abc\`))

	// Properly escaped backslash (\\) DOES match a literal backslash.
	assert.True(t, pathGlobMatch(`abc\\`, `abc\`))
	assert.True(t, pathGlobMatch(`\\`, `\`))
	assert.True(t, pathGlobMatch(`*\\`, `abc\`))
}

func TestMatchGlobMalformedBracket(t *testing.T) {
	// Unclosed bracket patterns fall back to literal comparison.
	assert.True(t, matchGlob("[", "["))
	assert.False(t, matchGlob("[", "a"))
	assert.True(t, matchGlob("[abc", "[abc"))
	assert.False(t, matchGlob("[abc", "a"))

	// Incomplete range (trailing dash) — non-matching per GNU fnmatch.
	assert.False(t, matchGlob("[a-", "[a-"))
	assert.False(t, matchGlob("[a-", "a"))
	assert.False(t, matchGlob("[ab-", "[ab-"))
}

func TestMatchGlobFoldMalformedBracket(t *testing.T) {
	assert.True(t, matchGlobFold("[", "["))
	assert.False(t, matchGlobFold("[", "a"))

	// Incomplete range — non-matching.
	assert.False(t, matchGlobFold("[a-", "[a-"))
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

	// Backslash escape inside class: [\]] matches literal ]
	matched, width = matchClass("[\\]]", ']')
	assert.True(t, matched)
	assert.Equal(t, 4, width)

	matched, width = matchClass("[\\]]", 'a')
	assert.False(t, matched)
	assert.Equal(t, 4, width)

	// Backslash escape: [a\]] matches a or ]
	matched, width = matchClass("[a\\]]", ']')
	assert.True(t, matched)
	assert.Equal(t, 5, width)

	matched, width = matchClass("[a\\]]", 'a')
	assert.True(t, matched)
	assert.Equal(t, 5, width)

	// Backslash escape: [\\a] matches \ or a
	matched, width = matchClass("[\\\\a]", '\\')
	assert.True(t, matched)
	assert.Equal(t, 5, width)

	matched, width = matchClass("[\\\\a]", 'a')
	assert.True(t, matched)
	assert.Equal(t, 5, width)

	matched, width = matchClass("[\\\\a]", 'z')
	assert.False(t, matched)
	assert.Equal(t, 5, width)

	// Escaped multi-byte character inside class: [\é] matches é
	matched, width = matchClass(`[\é]`, 'é')
	assert.True(t, matched)
	assert.Equal(t, 5, width) // [ + \ + é(2 bytes) + ] = 5

	matched, width = matchClass(`[\é]`, 'a')
	assert.False(t, matched)
	assert.Equal(t, 5, width)

	// Escaped multi-byte range endpoints: [\é-\ü]
	matched, width = matchClass(`[\é-\ü]`, 'ö') // ö is between é and ü
	assert.True(t, matched)

	matched, _ = matchClass(`[\é-\ü]`, 'a')
	assert.False(t, matched)
}

func TestCompareNumeric(t *testing.T) {
	// Exact match
	assert.True(t, compareNumeric(5, 5, cmpExact))
	assert.False(t, compareNumeric(5, 6, cmpExact))

	// Greater than
	assert.True(t, compareNumeric(6, 5, cmpMore))
	assert.False(t, compareNumeric(5, 5, cmpMore))
	assert.False(t, compareNumeric(4, 5, cmpMore))

	// Less than
	assert.True(t, compareNumeric(4, 5, cmpLess))
	assert.False(t, compareNumeric(5, 5, cmpLess))
	assert.False(t, compareNumeric(6, 5, cmpLess))
}

func TestPathGlobMatchMalformedBracket(t *testing.T) {
	// Unclosed bracket patterns fall back to literal comparison.
	assert.True(t, pathGlobMatch("[", "["))
	assert.False(t, pathGlobMatch("[", "a"))
	assert.True(t, pathGlobMatch("dir/[sub/file", "dir/[sub/file"))
	assert.False(t, pathGlobMatch("dir/[sub/file", "dir/asub/file"))
	// Star followed by malformed bracket (backtracking interaction).
	assert.True(t, pathGlobMatch("*/[", "dir/["))
	assert.False(t, pathGlobMatch("*/[", "dir/a"))

	// Incomplete range (trailing dash) — non-matching per GNU fnmatch.
	assert.False(t, pathGlobMatch("[a-", "[a-"))
	assert.False(t, pathGlobMatch("dir/[a-", "dir/[a-"))

	// Escaped multi-byte character in bracket class.
	assert.True(t, pathGlobMatch(`[\é]`, "é"))
	assert.False(t, pathGlobMatch(`[\é]`, "a"))
}
