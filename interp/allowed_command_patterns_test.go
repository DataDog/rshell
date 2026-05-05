// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
)

// --- Option validation ---

func TestAllowedCommandPatternsEmptySliceIsValid(t *testing.T) {
	// Zero patterns is a valid configuration: it just contributes no
	// authorisations.
	_, err := interp.New(interp.AllowedCommandPatterns(nil))
	require.NoError(t, err)

	_, err = interp.New(interp.AllowedCommandPatterns([][]string{}))
	require.NoError(t, err)
}

func TestAllowedCommandPatternsRejectsEmptyPattern(t *testing.T) {
	_, err := interp.New(interp.AllowedCommandPatterns([][]string{{}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pattern 0 is empty")
}

func TestAllowedCommandPatternsRejectsEmptyToken(t *testing.T) {
	_, err := interp.New(interp.AllowedCommandPatterns([][]string{{"ip", ""}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pattern 0 token 1 is empty")
}

func TestAllowedCommandPatternsRejectsLeadingEmptyToken(t *testing.T) {
	_, err := interp.New(interp.AllowedCommandPatterns([][]string{{"", "route"}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pattern 0 token 0 is empty")
}

func TestAllowedCommandPatternsAcceptsSingleTokenWithoutSpec(t *testing.T) {
	// Single-token patterns don't need a spec — they only check argv[0].
	_, err := interp.New(interp.AllowedCommandPatterns([][]string{{"echo"}}))
	require.NoError(t, err)
}

func TestAllowedCommandPatternsAcceptsMultiTokenWithBuiltinSpec(t *testing.T) {
	// "ip" is in the built-in spec registry, so a (ip, route) pattern is
	// accepted out of the box.
	_, err := interp.New(interp.AllowedCommandPatterns([][]string{{"ip", "route"}}))
	require.NoError(t, err)
}

func TestAllowedCommandPatternsRejectsMultiTokenWithoutSpec(t *testing.T) {
	// "echo" has no spec; a multi-token pattern referencing it is rejected
	// at runner construction time so the misconfiguration is surfaced
	// early.
	_, err := interp.New(interp.AllowedCommandPatterns([][]string{{"echo", "hello"}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no registered CommandSpec")
	assert.Contains(t, err.Error(), `"echo"`)
}

func TestAllowedCommandPatternsAcceptsMultiTokenWithUserSpec(t *testing.T) {
	// Operator-supplied specs unlock multi-token patterns for any command.
	_, err := interp.New(
		interp.CommandSpecs(map[string]interp.CommandSpec{
			"echo": {}, // empty spec = no flags; positional-only command
		}),
		interp.AllowedCommandPatterns([][]string{{"echo", "hello"}}),
	)
	require.NoError(t, err)
}

func TestAllowedCommandPatternsValidationRunsRegardlessOfOptionOrder(t *testing.T) {
	// Patterns first, specs second — validation runs at the end of New(),
	// so the order doesn't matter.
	_, err := interp.New(
		interp.AllowedCommandPatterns([][]string{{"echo", "hello"}}),
		interp.CommandSpecs(map[string]interp.CommandSpec{
			"echo": {},
		}),
	)
	require.NoError(t, err)
}

// --- End-to-end pattern matching against the structural matcher ---

// runWithPatterns runs a script with the given AllowedCommands,
// AllowedCommandPatterns, and CommandSpecs. AllowedPaths is set to the
// working directory so builtins that touch the filesystem don't fail for
// unrelated reasons.
func runWithPatterns(t *testing.T, script string, allowedCommands []string, patterns [][]string, extraSpecs map[string]interp.CommandSpec) (stdout, stderr string, code int) {
	t.Helper()

	prog, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	require.NoError(t, err)

	var outBuf, errBuf bytes.Buffer
	opts := []interp.RunnerOption{
		interp.StdIO(nil, &outBuf, &errBuf),
		interp.AllowedPaths([]string{t.TempDir()}),
	}
	if extraSpecs != nil {
		opts = append(opts, interp.CommandSpecs(extraSpecs))
	}
	if allowedCommands != nil {
		opts = append(opts, interp.AllowedCommands(allowedCommands))
	}
	if patterns != nil {
		opts = append(opts, interp.AllowedCommandPatterns(patterns))
	}

	runner, err := interp.New(opts...)
	require.NoError(t, err)
	defer runner.Close()

	runErr := runner.Run(context.Background(), prog)
	exitCode := 0
	if runErr != nil {
		var es interp.ExitStatus
		if rerrAs(runErr, &es) {
			exitCode = int(es)
		} else {
			t.Fatalf("unexpected non-ExitStatus error: %v", runErr)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// rerrAs is a tiny wrapper over errors.As to keep test bodies tidy.
func rerrAs(err error, target *interp.ExitStatus) bool {
	type aser interface{ As(any) bool }
	for cur := err; cur != nil; {
		if es, ok := cur.(interp.ExitStatus); ok {
			*target = es
			return true
		}
		if a, ok := cur.(aser); ok && a.As(target) {
			return true
		}
		// Fall through to a single-level Unwrap.
		type unwrapper interface{ Unwrap() error }
		if u, ok := cur.(unwrapper); ok {
			cur = u.Unwrap()
			continue
		}
		break
	}
	return false
}

func TestPatternsAdmitMatchingSubcommand(t *testing.T) {
	// Pattern (ip, route) admits "ip route show". The builtin reports its
	// own platform-specific error (route table reading not supported on
	// macOS), but the policy gate did its job — exit code is whatever the
	// builtin returns, NOT 127.
	_, _, code := runWithPatterns(t,
		`ip route show`,
		nil,
		[][]string{{"ip", "route"}},
		nil,
	)
	assert.NotEqual(t, 127, code, "expected policy to ALLOW the call (non-127 exit)")
}

func TestPatternsBlockSiblingSubcommand(t *testing.T) {
	// Pattern (ip, route) does NOT admit "ip addr show": the structural
	// position 0 is "addr", not "route".
	_, stderr, code := runWithPatterns(t,
		`ip addr show`,
		nil,
		[][]string{{"ip", "route"}},
		nil,
	)
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr, "not permitted by policy")
}

func TestPatternsTolerateBooleanGlobalFlagBeforeSubcommand(t *testing.T) {
	// "-4" is a boolean global flag in the ip spec; it appears between
	// argv[0] and the subcommand. Pattern (ip, route) should still match
	// because the structural extractor skips -4.
	_, _, code := runWithPatterns(t,
		`ip -4 route show`,
		nil,
		[][]string{{"ip", "route"}},
		nil,
	)
	assert.NotEqual(t, 127, code, "expected boolean flag interleaving to be tolerated")
}

func TestPatternsTolerateMultipleBooleanFlags(t *testing.T) {
	// Two boolean flags both interleaved between argv[0] and the
	// subcommand.
	_, _, code := runWithPatterns(t,
		`ip -o -4 route show`,
		nil,
		[][]string{{"ip", "route"}},
		nil,
	)
	assert.NotEqual(t, 127, code)
}

func TestPatternsBlockedWhenSubcommandIsPositionalArgValue(t *testing.T) {
	// The structural matcher checks pattern[1..] against the LEADING
	// structural tokens, not against any structural token. A positional
	// arg whose value happens to equal a pattern token does NOT satisfy
	// the slot — this is the bypass that the spec-driven matcher closes.
	//
	// Here we register an "echo" spec (no flags, all tokens structural)
	// and pattern (echo, hello). argv ["echo","goodbye","hello"] has
	// structural tokens ["goodbye","hello"]; pattern[1]="hello" must
	// match structural[0]="goodbye" and does not. Block.
	_, stderr, code := runWithPatterns(t,
		`echo goodbye hello`,
		nil,
		[][]string{{"echo", "hello"}},
		map[string]interp.CommandSpec{"echo": {}},
	)
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr, "not permitted by policy")
}

func TestPatternsAndAllowedCommandsAreUnion(t *testing.T) {
	// "cat" is allowed by name (any args).
	// "ip" is only allowed when its argv satisfies (ip, route).
	stdout, _, code := runWithPatterns(t,
		`cat /dev/null && ip route show`,
		[]string{"rshell:cat"},
		[][]string{{"ip", "route"}},
		nil,
	)
	// cat /dev/null produces no output; the && short-circuits to ip route
	// show, which the policy admits. The eventual exit code is whatever
	// the ip builtin returns (1 on macOS), not 127.
	_ = stdout
	assert.NotEqual(t, 127, code)
}

func TestPatternsDoNotShadowAllowedCommands(t *testing.T) {
	// "echo" allowed by name; even though no pattern matches, the name
	// allowlist alone authorises the call.
	stdout, _, code := runWithPatterns(t,
		`echo whatever`,
		[]string{"rshell:echo"},
		[][]string{{"ip", "route"}},
		nil,
	)
	assert.Equal(t, 0, code)
	assert.Equal(t, "whatever\n", stdout)
}

// --- The architectural test: substitution-defeat ---

// TestPatternsBlockSubstitutionEscape proves that argv-prefix pattern
// matching enforces post-expansion. The script forms the command name via
// $(printf ip) — opaque to any static caller — and then attempts an addr
// invocation. The matcher sees the resolved argv ["ip","addr"] at execve
// time and refuses against pattern (ip, route).
func TestPatternsBlockSubstitutionEscape(t *testing.T) {
	_, stderr, code := runWithPatterns(t,
		`$(printf ip) addr`,
		[]string{"rshell:printf"}, // printf must be allowed for $(...) to succeed
		[][]string{{"ip", "route"}},
		nil,
	)
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr, "not permitted by policy")
}

// TestPatternsAllowSubstitutionWhenArgvMatches is the partner case: a
// substitution that produces a matching argv is allowed. Confirms the
// matcher inspects the expanded argv rather than blanket-rejecting
// interpolation.
func TestPatternsAllowSubstitutionWhenArgvMatches(t *testing.T) {
	_, _, code := runWithPatterns(t,
		`$(printf ip) route show`,
		[]string{"rshell:printf"},
		[][]string{{"ip", "route"}},
		nil,
	)
	assert.NotEqual(t, 127, code, "expected post-expansion matcher to admit the call")
}

func TestPatternsSingleTokenPatternAdmitsAnyArgs(t *testing.T) {
	// Single-token pattern (echo) — no spec required, no structural
	// extraction, just argv[0] equality.
	stdout, _, code := runWithPatterns(t,
		`echo whatever args you like`,
		nil,
		[][]string{{"echo"}},
		nil,
	)
	assert.Equal(t, 0, code)
	assert.Equal(t, "whatever args you like\n", stdout)
}

// --- Spec-driven flag classification (unit-level) ---

// TestSpecValueFlagSkipsTwoTokens checks that a registered ValueFlag
// consumes the next argv token (its value), so the structural-token
// stream begins after both. We use echo as the test binary with a
// synthetic spec that pretends -n is a value flag (the real echo's -n
// is boolean, but the policy gate doesn't care — the spec dictates
// classification).
//
// argv: ["echo","-n","ns","route","show"]. With ValueFlags={"-n"},
// the matcher consumes "-n" and "ns" as a flag/value pair, leaving
// structural ["route","show"]. Pattern (echo, route) matches.
func TestSpecValueFlagSkipsTwoTokens(t *testing.T) {
	_, _, code := runWithPatterns(t,
		`echo -n ns route show`,
		nil,
		[][]string{{"echo", "route"}},
		map[string]interp.CommandSpec{
			"echo": {ValueFlags: map[string]bool{"-n": true}},
		},
	)
	assert.Equal(t, 0, code, "value-flag pair should be skipped, leaving 'route' as the leading structural token")
}

// TestSpecValueFlagDoesNotMatchWhenValueIsExpectedSubcommand confirms
// the partner case: when the value-flag's value happens to equal what
// would have been a matching subcommand, the matcher correctly skips it
// rather than counting it as structural. argv ["echo","-n","route","show"]
// with -n as a value flag → structural ["show"] → pattern (echo, route)
// blocked because "route" was consumed as -n's value, not as the
// subcommand.
func TestSpecValueFlagDoesNotMatchWhenValueIsExpectedSubcommand(t *testing.T) {
	_, stderr, code := runWithPatterns(t,
		`echo -n route show`,
		nil,
		[][]string{{"echo", "route"}},
		map[string]interp.CommandSpec{
			"echo": {ValueFlags: map[string]bool{"-n": true}},
		},
	)
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr, "not permitted by policy")
}

// TestSpecLongFlagWithEqualsIsSelfContained checks that "--flag=value" is
// always treated as a single skip token regardless of the spec's
// classification of "--flag".
func TestSpecLongFlagWithEqualsIsSelfContained(t *testing.T) {
	// Spec doesn't even know about --output. The matcher treats any
	// "--key=value" token as a single skip and continues.
	_, _, code := runWithPatterns(t,
		`echo --output=json route show`,
		nil,
		[][]string{{"echo", "route"}},
		map[string]interp.CommandSpec{"echo": {}},
	)
	assert.Equal(t, 0, code)
}

// TestSpecUnknownFlagTreatedAsBoolean checks the conservative default:
// a flag-shaped token not in the spec is treated as boolean (skipped
// alone). This is more permissive than strictly correct but avoids
// breaking matches when the spec is incomplete.
func TestSpecUnknownFlagTreatedAsBoolean(t *testing.T) {
	// Spec has no flags. argv: echo --debug route show. --debug skipped
	// as boolean → structural ["route","show"] → pattern (echo, route)
	// matches.
	_, _, code := runWithPatterns(t,
		`echo --debug route show`,
		nil,
		[][]string{{"echo", "route"}},
		map[string]interp.CommandSpec{"echo": {}},
	)
	assert.Equal(t, 0, code)
}

// --- DeniedCommandPatterns ---

// runWithPolicy is a richer variant of runWithPatterns that also accepts
// denied patterns. Used exclusively by the deny-pattern tests so the
// existing helper signature stays stable for the allow-only suite.
func runWithPolicy(t *testing.T, script string, allowedCommands []string, allowedPatterns, deniedPatterns [][]string, extraSpecs map[string]interp.CommandSpec) (stdout, stderr string, code int) {
	t.Helper()

	prog, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	require.NoError(t, err)

	var outBuf, errBuf bytes.Buffer
	opts := []interp.RunnerOption{
		interp.StdIO(nil, &outBuf, &errBuf),
		interp.AllowedPaths([]string{t.TempDir()}),
	}
	if extraSpecs != nil {
		opts = append(opts, interp.CommandSpecs(extraSpecs))
	}
	if allowedCommands != nil {
		opts = append(opts, interp.AllowedCommands(allowedCommands))
	}
	if allowedPatterns != nil {
		opts = append(opts, interp.AllowedCommandPatterns(allowedPatterns))
	}
	if deniedPatterns != nil {
		opts = append(opts, interp.DeniedCommandPatterns(deniedPatterns))
	}

	runner, err := interp.New(opts...)
	require.NoError(t, err)
	defer runner.Close()

	runErr := runner.Run(context.Background(), prog)
	exitCode := 0
	if runErr != nil {
		var es interp.ExitStatus
		if rerrAs(runErr, &es) {
			exitCode = int(es)
		} else {
			t.Fatalf("unexpected non-ExitStatus error: %v", runErr)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

func TestDeniedPatternsValidationRejectsEmpty(t *testing.T) {
	_, err := interp.New(interp.DeniedCommandPatterns([][]string{{}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DeniedCommandPatterns: pattern 0 is empty")
}

func TestDeniedPatternsValidationRejectsEmptyToken(t *testing.T) {
	_, err := interp.New(interp.DeniedCommandPatterns([][]string{{"ip", ""}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DeniedCommandPatterns: pattern 0 token 1 is empty")
}

func TestDeniedPatternsRequiresSpecForMultiToken(t *testing.T) {
	// Same rule as allow patterns: multi-token deny needs a spec for
	// the command name. Surfaces misconfiguration at New() time.
	_, err := interp.New(interp.DeniedCommandPatterns([][]string{{"echo", "secret"}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "DeniedCommandPatterns")
	assert.Contains(t, err.Error(), "no registered CommandSpec")
}

func TestDeniedPatternsAcceptsSingleTokenWithoutSpec(t *testing.T) {
	// Single-token denies don't need a spec — they only check argv[0].
	_, err := interp.New(interp.DeniedCommandPatterns([][]string{{"echo"}}))
	require.NoError(t, err)
}

// TestDenyOverridesNameAllowlist is the headline use case: allow ip in
// general, but carve out ip route. ip addr admits; ip route is refused
// at the gate.
func TestDenyOverridesNameAllowlist(t *testing.T) {
	// ip addr show — allow rule (name) admits, no deny matches → run.
	_, _, code := runWithPolicy(t,
		`ip addr show`,
		[]string{"rshell:ip"}, // allow ip wholesale by name
		nil,
		[][]string{{"ip", "route"}}, // but block ip route
		nil,
	)
	assert.NotEqual(t, 127, code, "ip addr should not match the deny pattern")

	// ip route show — deny matches → block, regardless of name allowlist.
	_, stderr, code := runWithPolicy(t,
		`ip route show`,
		[]string{"rshell:ip"},
		nil,
		[][]string{{"ip", "route"}},
		nil,
	)
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr, "blocked by deny pattern")
	assert.Contains(t, stderr, `"ip route"`)
}

// TestDenyOverridesAllowPattern covers the dual case: allow patterns
// admit, but a more specific deny carves out a sub-subcommand. Pattern
// (ip, route) admits ip route show; deny pattern (ip, route, get)
// blocks just ip route get.
func TestDenyOverridesAllowPattern(t *testing.T) {
	// ip route show — allow admits, deny doesn't match.
	_, _, code := runWithPolicy(t,
		`ip route show`,
		nil,
		[][]string{{"ip", "route"}},
		[][]string{{"ip", "route", "get"}},
		nil,
	)
	assert.NotEqual(t, 127, code)

	// ip route get 8.8.8.8 — deny matches → block.
	_, stderr, code := runWithPolicy(t,
		`ip route get 8.8.8.8`,
		nil,
		[][]string{{"ip", "route"}},
		[][]string{{"ip", "route", "get"}},
		nil,
	)
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr, "blocked by deny pattern")
}

// TestDenySurvivesShellSubstitution confirms the architectural property
// extends to the deny axis: a substitution that resolves to a denied
// argv at execve time is blocked even though the literal text was
// opaque.
func TestDenySurvivesShellSubstitution(t *testing.T) {
	_, stderr, code := runWithPolicy(t,
		`$(printf ip) route show`,
		[]string{"rshell:ip", "rshell:printf"},
		nil,
		[][]string{{"ip", "route"}},
		nil,
	)
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr, "blocked by deny pattern")
}

// TestDenyDoesNotMatchWhenSubcommandAtPositionalSlot confirms the deny
// matcher uses the same structural rules as the allow matcher: a
// positional argument value at a non-leading structural position does
// not satisfy a deny pattern.
//
// Pattern deny (ip, route) should NOT block "ip addr show route" —
// "route" appears as a positional value at structural[2], not at the
// subcommand slot. Without this property, the deny axis would be even
// more permissive than positional-presence matching, which would be a
// regression.
func TestDenyDoesNotMatchWhenSubcommandAtPositionalSlot(t *testing.T) {
	_, _, code := runWithPolicy(t,
		`ip addr show route`,
		[]string{"rshell:ip"},
		nil,
		[][]string{{"ip", "route"}},
		nil,
	)
	assert.NotEqual(t, 127, code, "deny should not match when 'route' is a positional value, not the subcommand")
}

// TestDenyAppliesUnderAllowAllCommands — denies override even the
// permissive allow-all-commands escape hatch. Useful for "let me run
// anything except this dangerous thing" policies in dev environments.
func TestDenyAppliesUnderAllowAllCommands(t *testing.T) {
	prog, err := syntax.NewParser().Parse(strings.NewReader(`ip route show`), "")
	require.NoError(t, err)

	var outBuf, errBuf bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(nil, &outBuf, &errBuf),
		interp.AllowedPaths([]string{t.TempDir()}),
		// Bypass-everything switch on…
		interp.AllowedCommands([]string{"rshell:ip"}),
		// …but the deny still fires.
		interp.DeniedCommandPatterns([][]string{{"ip", "route"}}),
	)
	require.NoError(t, err)
	defer runner.Close()

	runErr := runner.Run(context.Background(), prog)
	var es interp.ExitStatus
	require.True(t, errors.As(runErr, &es))
	assert.Equal(t, 127, int(es))
	assert.Contains(t, errBuf.String(), "blocked by deny pattern")
}
