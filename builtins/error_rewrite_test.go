// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import (
	"context"
	"errors"
	"testing"

	"github.com/spf13/pflag"
)

func TestTrialHelpTrimIndex(t *testing.T) {
	// factory installs a representative set of flag shapes on each
	// fresh FlagSet: bool no-arg long+short, single-char no-arg short,
	// string value-taker. This mirrors how real builtins compose flags
	// and exercises every classification path in trial parsing.
	factory := func(fs *pflag.FlagSet) HandlerFunc {
		fs.BoolP("help", "", false, "")
		fs.BoolP("verbose", "v", false, "")
		fs.BoolP("quiet", "q", false, "")
		fs.StringP("name", "n", "", "")
		return func(ctx context.Context, cc *CallContext, args []string) Result { return Result{} }
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
		// Value-takers are now safe: pflag accepts any string in Set,
		// and builtins are expected to validate before --help fires.
		{"--help after value-taker (long form)", []string{"-n", "5", "--help"}, 2, true},
		{"--help after value-taker (any string)", []string{"-n", "nope", "--help"}, 2, true},
		{"--help after =value (valid)", []string{"--name=foo", "--help"}, 1, true},
		// `-n --help` — even though pflag would consume `--help` as
		// `-n`'s value at parse time, the trim still recognises the
		// literal token and stops there. The real parse will then see
		// `--help` as the value of `-n` and the handler's pre-help
		// validation catches the bogus value (GNU: "invalid number of
		// lines: '-help'"). Either way the invocation errors.
		{"--help follows value-taker with no value", []string{"-n", "--help", "--bogus"}, 1, true},
		// Errors at parse time keep the suffix unstripped so the real
		// parse surfaces the same error.
		{"--help after unknown flag is unsafe", []string{"--bogus", "--help"}, 0, false},
		{"--help after --bool=garbage is unsafe", []string{"--verbose=garbage", "--help"}, 0, false},
		// `--` ends option parsing, so any `--help` after it is a
		// positional. We must not trim.
		{"--help after -- is unsafe", []string{"--", "--help"}, 0, false},
		// Positionals before --help are fine for pflag (they're just
		// collected) so trim is allowed.
		{"--help after positional", []string{"foo", "--help"}, 1, true},
		{"no --help in args", []string{"--verbose"}, 0, false},
		{"empty args", nil, 0, false},
		// Multi-byte UTF-8 in a shorthand cluster MUST not crash: pflag
		// would panic on a multi-byte ShorthandLookup, but trial.Parse
		// errors cleanly with "unknown shorthand flag", which we return
		// as (0, false). Regression for FuzzPwdArgs/9386e59311458487.
		{"non-ASCII byte in cluster", []string{"-˞", "--help"}, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, ok := trialHelpTrimIndex("t", factory, tt.args)
			if idx != tt.wantIdx || ok != tt.wantOK {
				t.Errorf("trialHelpTrimIndex(%v) = (%d, %v), want (%d, %v)", tt.args, idx, ok, tt.wantIdx, tt.wantOK)
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
