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
