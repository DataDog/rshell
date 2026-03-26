// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"mvdan.cc/sh/v3/expand"
)

// TestOverlayEnvironUnsetFreesStorage verifies that setting a variable to the
// unset state in overlayEnviron correctly subtracts its bytes from totalBytes,
// freeing space for subsequent assignments.
func TestOverlayEnvironUnsetFreesStorage(t *testing.T) {
	o := newOverlayEnviron(expand.ListEnviron(), false)

	// Fill to near the cap with a single large variable.
	large := make([]byte, MaxTotalVarsBytes-100)
	for i := range large {
		large[i] = 'x'
	}
	err := o.Set("A", expand.Variable{Set: true, Kind: expand.String, Str: string(large)})
	require.NoError(t, err, "initial large assignment must succeed")
	assert.Equal(t, len(large), o.totalBytes, "totalBytes must equal the assigned size")

	// Now unset A — this should subtract its bytes from totalBytes.
	err = o.Set("A", expand.Variable{})
	require.NoError(t, err, "unsetting A must not return an error")
	assert.Equal(t, 0, o.totalBytes, "totalBytes must drop to 0 after unsetting the only variable")

	// A subsequent large assignment must succeed because the space was freed.
	err = o.Set("B", expand.Variable{Set: true, Kind: expand.String, Str: string(large)})
	assert.NoError(t, err, "assignment after unset must succeed when freed space is within the cap")
}

// TestSetUncappedNoTombstone verifies that setUncapped deletes the map entry
// when the variable being restored is unset, rather than leaving a zero-value
// tombstone in the overlay.  Tombstones allow unbounded map growth for inline
// command vars (FOO=bar cmd) where FOO was previously unset.
func TestSetUncappedNoTombstone(t *testing.T) {
	o := newOverlayEnviron(expand.ListEnviron(), false)

	// Simulate the restore path: setUncapped called with an unset variable for a name
	// that was never in the overlay (i.e. FOO was unset before the inline assignment).
	o.setUncapped("FOO", expand.Variable{})
	_, inOverlay := o.values["FOO"]
	assert.False(t, inOverlay, "setUncapped with unset variable must not leave a tombstone entry in values")
}
