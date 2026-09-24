// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package wineventlog

import (
	"strings"
	"testing"
)

func TestTSVEscape(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain ascii", "Service entered the running state.", "Service entered the running state."},
		{"non-ascii printable passes through", "café — naïve", "café — naïve"},
		{"backslash escapes", `path\to\file`, `path\\to\\file`},
		{"tab", "a\tb", `a\tb`},
		{"crlf", "line1\r\nline2", `line1\r\nline2`},
		{"nul", "before\x00after", `before\x00after`},
		{"form feed", "x\x0cy", `x\x0cy`},
		{"vertical tab", "x\x0by", `x\x0by`},
		{"del", "x\x7fy", `x\x7fy`},
		{"ltr mark escaped", "Foo‎Bar", "Foo\\u200eBar"},
		{"all controls 0-1f", string([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 11, 12, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31}),
			`\x00\x01\x02\x03\x04\x05\x06\x07\x08\x0b\x0c\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := TSVEscape(tc.in)
			if got != tc.want {
				t.Errorf("TSVEscape(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// No literal tab, NUL, or LRM in escaped output.
			if strings.ContainsAny(got, "\t\x00") || strings.ContainsRune(got, '‎') {
				t.Errorf("TSVEscape(%q) leaked structural char: %q", tc.in, got)
			}
		})
	}
}

// TestTSVEscapeFastPath confirms that strings without any escape-needing
// characters are returned unchanged (same string identity is not
// guaranteed, but no allocation should be performed for the common case).
func TestTSVEscapeFastPath(t *testing.T) {
	in := "no special chars in this message at all"
	out := TSVEscape(in)
	if out != in {
		t.Errorf("expected fast-path passthrough, got %q", out)
	}
}
