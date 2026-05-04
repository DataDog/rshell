// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp_test

import (
	"bytes"
	"context"
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
	// authorisations. Combined with an empty AllowedCommands, no command
	// runs — that's the existing default-deny behaviour.
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
	_, err := interp.New(interp.AllowedCommandPatterns([][]string{{"kubectl", ""}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pattern 0 token 1 is empty")
}

func TestAllowedCommandPatternsRejectsLeadingEmptyToken(t *testing.T) {
	_, err := interp.New(interp.AllowedCommandPatterns([][]string{{"", "get"}}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pattern 0 token 0 is empty")
}

func TestAllowedCommandPatternsAcceptsSingleTokenPattern(t *testing.T) {
	_, err := interp.New(interp.AllowedCommandPatterns([][]string{{"echo"}}))
	require.NoError(t, err)
}

func TestAllowedCommandPatternsAcceptsMultiTokenPattern(t *testing.T) {
	_, err := interp.New(interp.AllowedCommandPatterns([][]string{{"kubectl", "get"}}))
	require.NoError(t, err)
}

// --- End-to-end pattern matching ---

// runWithPatterns runs a script with the given AllowedCommands and
// AllowedCommandPatterns. AllowedPaths is set to the working directory so
// builtins that touch the filesystem don't fail for unrelated reasons.
func runWithPatterns(t *testing.T, script string, allowedCommands []string, patterns [][]string) (stdout, stderr string, code int) {
	t.Helper()

	prog, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	require.NoError(t, err)

	var outBuf, errBuf bytes.Buffer
	opts := []interp.RunnerOption{
		interp.StdIO(nil, &outBuf, &errBuf),
		interp.AllowedPaths([]string{t.TempDir()}),
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

func TestPatternsAllowMatchingArgv(t *testing.T) {
	stdout, _, code := runWithPatterns(t,
		`echo hello world`,
		nil, // no name-allowlist; pattern is the only authorisation
		[][]string{{"echo", "hello"}},
	)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\n", stdout)
}

func TestPatternsBlockNonMatchingArgv(t *testing.T) {
	_, stderr, code := runWithPatterns(t,
		`echo goodbye`,
		nil,
		[][]string{{"echo", "hello"}},
	)
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr, "command not allowed")
}

func TestPatternsBlockNameWhenNoArgsMatch(t *testing.T) {
	// Pattern is multi-token; bare "echo" without args has argv ["echo"]
	// which is shorter than the pattern, so it must not match.
	_, stderr, code := runWithPatterns(t,
		`echo`,
		nil,
		[][]string{{"echo", "hello"}},
	)
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr, "command not allowed")
}

func TestPatternsAndAllowedCommandsAreUnion(t *testing.T) {
	// "cat" is allowed by name (any args).
	// "echo" is only allowed when argv begins with ["echo", "hello"].
	// Both should work side by side.
	stdout, _, code := runWithPatterns(t,
		`echo hello there`,
		[]string{"rshell:cat"},
		[][]string{{"echo", "hello"}},
	)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello there\n", stdout)
}

func TestPatternsDoNotShadowAllowedCommands(t *testing.T) {
	// "echo" allowed by name; even though no pattern matches, the name
	// allowlist alone authorises the call.
	stdout, _, code := runWithPatterns(t,
		`echo whatever`,
		[]string{"rshell:echo"},
		[][]string{{"kubectl", "get"}},
	)
	assert.Equal(t, 0, code)
	assert.Equal(t, "whatever\n", stdout)
}

// --- The architectural test: substitution-defeat ---

// TestPatternsBlockSubstitutionEscape is the canonical demonstration that
// argv-prefix pattern matching enforces post-expansion. The script forms
// the command name via $(printf echo) — opaque to any static caller — and
// then attempts to invoke it with an argv that does NOT match the pattern.
// The matcher sees the resolved argv ["echo","goodbye"] at execve time
// and refuses.
func TestPatternsBlockSubstitutionEscape(t *testing.T) {
	_, stderr, code := runWithPatterns(t,
		`$(printf echo) goodbye`,
		[]string{"rshell:printf"}, // printf must be allowed for the $(...) to succeed
		[][]string{{"echo", "hello"}},
	)
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr, "command not allowed")
}

// TestPatternsAllowSubstitutionWhenArgvMatches is the partner case: a
// substitution that produces an argv matching the pattern is allowed.
// Confirms the matcher isn't blanket-rejecting interpolation — it inspects
// the expanded argv.
func TestPatternsAllowSubstitutionWhenArgvMatches(t *testing.T) {
	stdout, _, code := runWithPatterns(t,
		`$(printf echo) hello world`,
		[]string{"rshell:printf"},
		[][]string{{"echo", "hello"}},
	)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\n", stdout)
}
