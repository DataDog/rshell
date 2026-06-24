// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import "errors"

// NoArgSentinel is the NoOptDefVal that no-argument flags registered via
// [NoArgBool] (and df's unitFlag) carry. pflag passes this exact string to
// the flag's Set when the user writes the bare flag (`-f`, `--force`); for
// the explicit-value form (`--force=false`) pflag passes the user's literal
// value instead.
//
// We use a single NUL byte so the two cases are distinguishable: NUL is the
// C-string terminator and POSIX execve(2) refuses to pass argv elements
// containing it, so the user cannot forge this string from a shell. That lets
// Set reject every `=value` form — including `=true` — to match GNU's
// "doesn't allow an argument" exit-1 error. The flagparser package recognises
// the "flag does not allow an argument" error these flags return and rewrites
// it to the GNU getopt wording.
const NoArgSentinel = "\x00"

// noArgBool is a pflag.Value that mirrors GNU getopt's no_argument behaviour:
// bare `--flag` and `-f` work, but `--flag=value` and `-f=value` are rejected
// with "flag does not allow an argument" for every value (including `=true`).
// pflag.BoolP treats the explicit-value form as a successful parse, which
// silently diverges from GNU — this type closes that gap.
type noArgBool struct {
	target *bool
}

func (b *noArgBool) String() string {
	if b.target != nil && *b.target {
		return "true"
	}
	return "false"
}

func (b *noArgBool) Type() string { return "bool" }

func (b *noArgBool) Set(s string) error {
	if s != NoArgSentinel {
		return errors.New("flag does not allow an argument")
	}
	*b.target = true
	return nil
}

// NoArgBool registers a GNU getopt no_argument-style boolean flag on fs and
// returns the *bool target, which the caller reads like an ordinary fs.Bool
// result. Bare `--name` / `-shorthand` set it to true; the explicit-value
// form (`--name=true`, `--name=false`, `-shorthand=x`) is rejected with the
// GNU "doesn't allow an argument" error. Pass an empty shorthand for
// long-only flags.
//
// Prefer this over pflag's BoolP/Bool for every no-argument boolean flag so
// the shell matches GNU coreutils, which reject `--flag=value` before acting
// on any operand.
func NoArgBool(fs *FlagSet, name, shorthand, usage string) *bool {
	target := new(bool)
	flag := fs.VarPF(&noArgBool{target: target}, name, shorthand, usage)
	flag.NoOptDefVal = NoArgSentinel
	return target
}

// PrintFlagDefaults writes fs's flag usage to fs's configured output (set via
// fs.SetOutput), mirroring fs.PrintDefaults but first clearing the NUL
// NoOptDefVal that [NoArgBool] flags carry. Without this, pflag would render
// the sentinel into the help text as `--flag[=\x00]` binary garbage. The
// original NoOptDefVal values are restored before returning, so calling this
// after Parse cannot affect the explicit-value rejection. Builtins that
// register no-argument boolean flags must print their flag help through this
// helper rather than calling fs.PrintDefaults directly.
func PrintFlagDefaults(fs *FlagSet) {
	saved := make(map[*Flag]string)
	fs.VisitAll(func(flag *Flag) {
		if flag.NoOptDefVal == NoArgSentinel {
			saved[flag] = flag.NoOptDefVal
			flag.NoOptDefVal = ""
		}
	})
	defer func() {
		for f, v := range saved {
			f.NoOptDefVal = v
		}
	}()
	fs.PrintDefaults()
}
