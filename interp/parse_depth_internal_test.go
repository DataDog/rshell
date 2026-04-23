// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScriptNestingDepth(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  int
	}{
		{"empty", "", 0},
		{"no parens", "echo hello", 0},
		{"single subshell", "(echo hi)", 1},
		{"single cmd subst", "echo $(echo hi)", 1},
		{"flat pair of subshells", "(a); (b)", 1},
		{"nested cmd subst depth 3", "echo $(echo $(echo $(echo hi)))", 3},
		{"mixed subshell and subst", "( $( ( $(x) ) ) )", 4},

		// Unbalanced ')' without matching '(' must not drive depth negative.
		{"leading close paren ignored", ")))(((", 3},

		// Max is tracked, not final depth.
		{"max before close", "(((a))) (b)", 3},

		// Loose scan: literal parens inside quotes are counted. This is an
		// intentional over-count — safe-by-construction (never under-counts,
		// so it cannot be bypassed) and harmless in practice because the
		// threshold sits far above any realistic script.
		{"single quoted parens count (over-count by design)", "'((('", 3},
		{"double quoted parens count (over-count by design)", `"((("`, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, scriptNestingDepth(tc.input))
		})
	}
}

// TestParseScriptRejectsDeepNesting verifies that ParseScript rejects scripts
// whose nesting depth exceeds MaxParseDepth, protecting the parser from the
// `runtime: stack overflow` panic that otherwise occurs at ~2·10^5 levels.
func TestParseScriptRejectsDeepNesting(t *testing.T) {
	depth := MaxParseDepth + 500
	script := "echo " + strings.Repeat("$(echo ", depth) + "hi" + strings.Repeat(")", depth)

	_, err := ParseScript(script, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nesting depth")
	assert.Contains(t, err.Error(), "exceeds maximum")
}

// TestParseScriptAcceptsDepthAtCap verifies the boundary: a script exactly at
// MaxParseDepth is accepted.
func TestParseScriptAcceptsDepthAtCap(t *testing.T) {
	script := "echo " + strings.Repeat("$(echo ", MaxParseDepth) + "hi" + strings.Repeat(")", MaxParseDepth)

	_, err := ParseScript(script, "")
	require.NoError(t, err)
}
