// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import (
	"errors"
	"testing"
)

func TestRewritePflagError(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "unknown long flag",
			in:   "unknown flag: --no-such-flag",
			want: "unrecognized option '--no-such-flag'",
		},
		{
			name: "unknown shorthand single",
			in:   "unknown shorthand flag: 'X' in -X",
			want: "invalid option -- 'X'",
		},
		{
			name: "unknown shorthand within group",
			in:   "unknown shorthand flag: 'X' in -aX",
			want: "invalid option -- 'X'",
		},
		{
			name: "missing argument long",
			in:   "flag needs an argument: --type",
			want: "option '--type' requires an argument",
		},
		{
			name: "missing argument short",
			in:   "flag needs an argument: -t",
			want: "option '-t' requires an argument",
		},
		{
			name: "no-arg with long+short descriptor",
			in:   `invalid argument "true" for "-a, --all" flag: flag does not allow an argument`,
			want: "option '--all' doesn't allow an argument",
		},
		{
			name: "no-arg with long-only descriptor",
			in:   `invalid argument "true" for "--total" flag: flag does not allow an argument`,
			want: "option '--total' doesn't allow an argument",
		},
		{
			name: "no-arg with short-only descriptor",
			in:   `invalid argument "true" for "-k" flag: flag does not allow an argument`,
			want: "option '-k' doesn't allow an argument",
		},
		{
			name: "wrapped Var.Set error not the no-arg case is left alone",
			in:   `invalid argument "z" for "-t, --radix" flag: invalid radix`,
			want: `invalid argument "z" for "-t, --radix" flag: invalid radix`,
		},
		{
			name: "unknown pattern passes through",
			in:   "some custom error",
			want: "some custom error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewritePflagError(errors.New(tt.in))
			if got != tt.want {
				t.Errorf("rewritePflagError(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
