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
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

func TestProtectEscapedLeftBraces(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   []string
	}{
		{
			name:   "escaped_unmatched_left_brace",
			script: `cmd \{`,
			want:   []string{"cmd", "{"},
		},
		{
			name:   "escaped_left_brace_disables_brace_expansion",
			script: `cmd \{a,b}`,
			want:   []string{"cmd", "{a,b}"},
		},
		{
			name:   "escaped_left_brace_in_middle_of_word",
			script: `cmd pre\{post`,
			want:   []string{"cmd", "pre{post"},
		},
		{
			name:   "even_backslashes_still_allow_brace_expansion",
			script: `cmd \\{a,b}`,
			want:   []string{"cmd", `\a`, `\b`},
		},
		{
			name:   "odd_backslashes_quote_left_brace",
			script: `cmd \\\{a,b}`,
			want:   []string{"cmd", `\{a,b}`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prog, err := syntax.NewParser().Parse(strings.NewReader(tt.script), "")
			require.NoError(t, err)
			require.Len(t, prog.Stmts, 1)

			call, ok := prog.Stmts[0].Cmd.(*syntax.CallExpr)
			require.True(t, ok)

			fields, err := expand.Fields(nil, protectEscapedLeftBraces(call.Args)...)
			require.NoError(t, err)
			assert.Equal(t, tt.want, fields)
		})
	}
}
