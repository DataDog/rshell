// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePracticalAwkProgram(t *testing.T) {
	prog, err := parseProgram(`BEGIN { label = "sum=" } $2 > 1 { if ($1 == "skip") next; total += $2; printf "%s%d\n", label, total } END { print length($0), total }`)
	require.NoError(t, err)
	require.Len(t, prog.rules, 3)
	assert.Equal(t, ruleBegin, prog.rules[0].kind)
	assert.Equal(t, ruleNormal, prog.rules[1].kind)
	assert.Equal(t, ruleEnd, prog.rules[2].kind)
}

func TestParseRejectsUnsafeFeatures(t *testing.T) {
	for _, src := range []string{
		`{ system("sh") }`,
		`{ print $1 > "out" }`,
		`{ "cmd" | getline }`,
	} {
		_, err := parseProgram(src)
		require.Error(t, err, src)
	}
}
