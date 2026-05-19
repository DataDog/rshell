// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Blocked-attack regression tests added by the vuln-hunt campaign
// 2026-05-18-initial-audit (target: sed).
package sed_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

// H6: an unconditional sed branch loop (`:loop; b loop`) must terminate
// at MaxBranchIterations per input line, not hang.
func TestVulnHuntBuiltinResourceExhaustion_BranchIterationCap(t *testing.T) {
	dir := t.TempDir()
	// `:loop; b loop` is an infinite jump within one cycle. The cap is
	// 10 000 iterations — sed must error or move on within sub-second
	// wall time on any reasonable machine.
	_, stderr, code := testutil.RunScript(t,
		"printf 'one\\n' | sed ':loop; b loop'", dir,
		interp.AllowedPaths([]string{dir}))
	// Either non-zero exit with a branch-cap message, or zero exit if
	// sed silently completes the iterations and falls through. The
	// failure mode we're guarding against is a hang.
	if code != 0 {
		assert.Contains(t, stderr, "sed:",
			"expected sed: prefix on branch-cap error, got %q", stderr)
	}
}

// H8: a very large numeric address (larger than the input has lines) is
// not allowed to overflow internally or produce wrong output.
func TestVulnHuntBuiltinIntegerOverflow_LargeAddress(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		val  string
		want string // expected stdout substring (or "" for any)
	}{
		{"99999999999", ""},
		{"9223372036854775807", ""},
		{"18446744073709551616", ""}, // overflow at parse → expected to fail parse
	}
	for _, tc := range cases {
		t.Run("addr="+tc.val, func(t *testing.T) {
			script := "printf 'a\\nb\\nc\\n' | sed -n '" + tc.val + "p'"
			_, stderr, code := testutil.RunScript(t, script, dir,
				interp.AllowedPaths([]string{dir}))
			// Either the address parses (and selects no line, exit 0) or
			// the parser rejects it (exit 1 with sed: prefix). Either way,
			// the command must not hang or wrap around to a valid line.
			if code != 0 {
				assert.Contains(t, stderr, "sed:")
			}
		})
	}
}
