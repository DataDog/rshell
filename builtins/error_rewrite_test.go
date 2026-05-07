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
		args []string
		want string
	}{
		{
			name: "unknown long flag",
			in:   "unknown flag: --no-such-flag",
			args: []string{"--no-such-flag"},
			want: "unrecognized option '--no-such-flag'",
		},
		{
			name: "unknown long flag with =value preserved",
			in:   "unknown flag: --no-such",
			args: []string{"--no-such=value"},
			want: "unrecognized option '--no-such=value'",
		},
		{
			name: "unknown long flag with empty =",
			in:   "unknown flag: --no-such",
			args: []string{"--no-such="},
			want: "unrecognized option '--no-such='",
		},
		{
			name: "unknown long flag — args lookup stops at --",
			in:   "unknown flag: --no-such",
			args: []string{"--no-such", "--", "--no-such=other"},
			want: "unrecognized option '--no-such'",
		},
		{
			name: "unknown shorthand single",
			in:   "unknown shorthand flag: 'X' in -X",
			args: []string{"-X"},
			want: "invalid option -- 'X'",
		},
		{
			name: "unknown shorthand within group",
			in:   "unknown shorthand flag: 'X' in -aX",
			args: []string{"-aX"},
			want: "invalid option -- 'X'",
		},
		{
			name: "missing argument long",
			in:   "flag needs an argument: --type",
			args: []string{"--type"},
			want: "option '--type' requires an argument",
		},
		{
			name: "missing argument short",
			in:   "flag needs an argument: -t",
			args: []string{"-t"},
			want: "option '-t' requires an argument",
		},
		{
			name: "no-arg with long+short descriptor",
			in:   `invalid argument "true" for "-a, --all" flag: flag does not allow an argument`,
			args: []string{"--all=true"},
			want: "option '--all' doesn't allow an argument",
		},
		{
			name: "no-arg with long-only descriptor",
			in:   `invalid argument "true" for "--total" flag: flag does not allow an argument`,
			args: []string{"--total=true"},
			want: "option '--total' doesn't allow an argument",
		},
		{
			name: "no-arg with short-only descriptor",
			in:   `invalid argument "true" for "-k" flag: flag does not allow an argument`,
			args: []string{"-k=true"},
			want: "option '-k' doesn't allow an argument",
		},
		{
			name: "wrapped Var.Set error not the no-arg case is left alone",
			in:   `invalid argument "z" for "-t, --radix" flag: invalid radix`,
			args: []string{"-t", "z"},
			want: `invalid argument "z" for "-t, --radix" flag: invalid radix`,
		},
		{
			name: "unknown pattern passes through",
			in:   "some custom error",
			args: nil,
			want: "some custom error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewritePflagError(errors.New(tt.in), tt.args)
			if got != tt.want {
				t.Errorf("rewritePflagError(%q, %v) = %q, want %q", tt.in, tt.args, got, tt.want)
			}
		})
	}
}
