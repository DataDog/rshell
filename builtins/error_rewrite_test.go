// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import (
	"errors"
	"testing"

	"github.com/spf13/pflag"
)

func TestSafeHelpTrimIndex(t *testing.T) {
	makeFS := func() *pflag.FlagSet {
		fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
		fs.BoolP("help", "", false, "")
		fs.BoolP("verbose", "v", false, "")
		fs.BoolP("quiet", "q", false, "")
		fs.StringP("name", "n", "", "")
		return fs
	}

	tests := []struct {
		name    string
		args    []string
		wantIdx int
		wantOK  bool
	}{
		{"--help first", []string{"--help"}, 0, true},
		{"--help after no-arg long", []string{"--verbose", "--help", "--bogus"}, 1, true},
		{"--help after no-arg short", []string{"-q", "--help", "--bogus"}, 1, true},
		{"--help after cluster of no-arg shorts", []string{"-qv", "--help", "--bogus"}, 1, true},
		{"--help after value-taker is unsafe", []string{"-n", "5", "--help"}, 0, false},
		{"--help after =value is unsafe", []string{"--verbose=true", "--help"}, 0, false},
		{"--help after positional is unsafe", []string{"foo", "--help"}, 0, false},
		{"--help after unknown flag is unsafe", []string{"--bogus", "--help"}, 0, false},
		{"--help after -- is unsafe", []string{"--", "--help"}, 0, false},
		{"no --help in args", []string{"--verbose"}, 0, false},
		{"empty args", nil, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, ok := safeHelpTrimIndex(makeFS(), tt.args)
			if idx != tt.wantIdx || ok != tt.wantOK {
				t.Errorf("safeHelpTrimIndex(%v) = (%d, %v), want (%d, %v)", tt.args, idx, ok, tt.wantIdx, tt.wantOK)
			}
		})
	}
}

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
			name: "missing argument short single",
			in:   "flag needs an argument: 'n' in -n",
			args: []string{"-n"},
			want: "option requires an argument -- 'n'",
		},
		{
			name: "missing argument short combined",
			in:   "flag needs an argument: 'n' in -an",
			args: []string{"-an"},
			want: "option requires an argument -- 'n'",
		},
		{
			name: "no-arg with long+short descriptor",
			in:   `invalid argument "true" for "-a, --all" flag: flag does not allow an argument`,
			args: []string{"--all=true"},
			want: "option '--all' doesn't allow an argument",
		},
		{
			name: "shorthand =value form maps to GNU's invalid =",
			in:   `invalid argument "true" for "-h, --human-readable" flag: flag does not allow an argument`,
			args: []string{"-h=true"},
			want: "invalid option -- '='",
		},
		{
			name: "shorthand =value form requires shorthand match",
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
			name: "no-arg with short-only descriptor — argv used -X=value",
			in:   `invalid argument "true" for "-k" flag: flag does not allow an argument`,
			args: []string{"-k=true"},
			want: "invalid option -- '='",
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
