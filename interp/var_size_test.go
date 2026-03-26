// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/rshell/interp"
)

// TestOversizedInlineVarAbortsCommand verifies that an inline assignment whose
// value exceeds MaxVarBytes does NOT execute the following command and that the
// shell exits with a non-zero status.
func TestOversizedInlineVarAbortsCommand(t *testing.T) {
	large := strings.Repeat("x", interp.MaxVarBytes+1)
	script := fmt.Sprintf("X=%s echo SHOULD_NOT_RUN", large)

	stdout, stderr, code := runScript(t, script, "")

	assert.NotContains(t, stdout, "SHOULD_NOT_RUN", "command must not execute after oversized inline assignment")
	assert.Contains(t, stderr, "value too large")
	assert.NotEqual(t, 0, code, "exit code must be non-zero")
}

// TestTotalVarStorageCapEnforced verifies that creating many small variables
// eventually hits the MaxTotalVarsBytes limit and the shell exits with a
// non-zero status and an appropriate error message.
func TestTotalVarStorageCapEnforced(t *testing.T) {
	// Each variable holds 1024 bytes; 1025 variables would require > 1 MiB total.
	// Use a shell loop to assign them so that we exercise the interpreter's
	// variable-assignment path (not just Go-level API).
	//
	// We write the loop as a here-document-style multiline string passed to
	// runScript so that the script is parsed by the interpreter.
	//
	// The value is 1024 'x' characters; after ~1024 iterations the total
	// storage (1024 * 1024 = 1 MiB) should be exactly at the limit, and
	// the 1025th assignment should be rejected.
	value := strings.Repeat("x", 1024)
	// Build a script that assigns VAR_0 through VAR_1100 (enough to exceed 1 MiB).
	var sb strings.Builder
	for i := range 1101 {
		fmt.Fprintf(&sb, "VAR_%d=%s\n", i, value)
	}
	sb.WriteString("echo DONE\n")
	script := sb.String()

	stdout, stderr, code := runScript(t, script, "")

	assert.NotContains(t, stdout, "DONE", "echo must not run after total storage cap is exceeded")
	assert.Contains(t, stderr, "variable storage limit exceeded", "expected storage-cap error in stderr")
	assert.NotEqual(t, 0, code, "exit code must be non-zero after hitting total storage cap")
}

// TestSubshellTotalVarStorageDoubleCount demonstrates a bug where a non-background
// subshell double-counts variables that exist in both r.Env and the parent overlay.
//
// Root cause: overlayEnviron.Each calls parent.Each first, then iterates its own
// values. If a variable was set in the parent overlay (overriding an r.Env value),
// Each emits it twice. newOverlayEnviron seeds totalBytes by summing every value
// emitted by Each, so the child's counter starts at 2× the real storage. Any
// subsequent assignment with a positive delta then fails even though actual memory
// use is within MaxTotalVarsBytes.
//
// This test should FAIL on the current code (demonstrating the bug) and PASS once
// the seeding or Set accounting is fixed.
func TestSubshellTotalVarStorageDoubleCount(t *testing.T) {
	// X is 600 KiB in the initial environment — well under the 1 MiB cap.
	value600K := strings.Repeat("x", 600*1024)

	// The script overrides X in the parent shell, putting it into the overlay.
	// X now lives in both r.Env (600 KiB) and the parent overlay (600 KiB).
	// Each() emits it twice, so the subshell's seed is 1200 KiB > MaxTotalVarsBytes.
	// Any assignment inside the subshell is then rejected, even though real storage
	// is only 600 KiB.
	script := fmt.Sprintf("X=%s\n( Y=x; echo SUBSHELL_OK )\necho DONE\n", value600K)

	stdout, _, code := runScript(t, script, "", interp.Env("X="+value600K))

	assert.Contains(t, stdout, "SUBSHELL_OK", "subshell assignment should succeed: real storage is 600 KiB, within the 1 MiB cap")
	assert.Contains(t, stdout, "DONE")
	assert.Equal(t, 0, code)
}

// TestTotalVarStorageCapUpdateTracking verifies that updating an existing variable
// correctly adjusts the total byte counter (i.e. growing a variable counts against
// the cap, and shrinking it frees space).
func TestTotalVarStorageCapUpdateTracking(t *testing.T) {
	// Assign a large value (512 KiB) twice to the SAME variable.
	// The total should stay at ~512 KiB (not 1 MiB) because we're updating, not creating.
	// Then reassign to empty, which should free the space so another 512 KiB variable fits.
	value512K := strings.Repeat("x", 512*1024)
	script := fmt.Sprintf(
		"A=%s\nA=%s\nA=\nB=%s\necho OK\n",
		value512K, value512K, value512K,
	)

	stdout, _, code := runScript(t, script, "")

	assert.Contains(t, stdout, "OK", "expected OK after update/shrink cycle")
	assert.Equal(t, 0, code, "exit code must be zero when total storage stays within cap")
}
