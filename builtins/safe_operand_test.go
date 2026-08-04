// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import "testing"

func TestSafeOperand(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"plain", "foo.txt", "foo.txt"},
		{"newline", "foo\nbar", `foo\nbar`},
		{"carriage return", "foo\rbar", `foo\rbar`},
		{"tab", "foo\tbar", `foo\tbar`},
		{"esc ansi sequence", "foo\x1b[31mRED\x1b[0m", `foo\x1b[31mRED\x1b[0m`},
		{"backslash", `foo\bar`, `foo\\bar`},
		{"bell and other control bytes", "foo\x07\x00bar", `foo\x07\x00bar`},
		{"unicode preserved", "café🎉", "café🎉"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SafeOperand(tt.in); got != tt.want {
				t.Fatalf("SafeOperand(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSafeOperandNeverEmitsRawControlBytes(t *testing.T) {
	in := "line1\nline2\rline3\x1b[2Jline4"
	got := SafeOperand(in)
	for _, r := range got {
		if r == '\n' || r == '\r' || r == 0x1b {
			t.Fatalf("SafeOperand output still contains a raw control byte: %q", got)
		}
	}
}
