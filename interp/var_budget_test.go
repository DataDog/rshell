// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

func strVar(s string) expand.Variable {
	return expand.Variable{Set: true, Kind: expand.String, Str: s}
}

// TestVarBudgetSharedByConcurrentOverlays checks the budget directly: two
// sibling overlays cannot together exceed the cap, and a released sibling's
// storage becomes available again.
func TestVarBudgetSharedByConcurrentOverlays(t *testing.T) {
	root := &overlayEnviron{}
	root.resetAccounting()
	require.NoError(t, root.Set("BASE", strVar(strings.Repeat("b", 100<<10))))

	left := newOverlayEnviron(root, true)
	right := newOverlayEnviron(root, true)
	half := strings.Repeat("x", 500<<10)
	require.NoError(t, left.Set("X", strVar(half)))
	err := right.Set("X", strVar(half))
	var storageErr *errTotalVarStorageExceeded
	require.ErrorAs(t, err, &storageErr, "siblings must share one budget")

	left.release()
	require.NoError(t, right.Set("X", strVar(half)), "released storage must be reusable")

	// A subshell of a stage sees its ancestors' storage through its seed,
	// and its sibling's storage through the budget.
	nested := newOverlayEnviron(right, false)
	assert.ErrorAs(t, nested.Set("Y", strVar(half)), &storageErr)
	require.NoError(t, nested.Set("Y", strVar(strings.Repeat("y", 100<<10))))
	nested.release()
	right.release()
	assert.Equal(t, root.charged, root.budget.live)
}

// TestVarBudgetReturnsToRootAfterRun checks that every subshell and pipeline
// stage releases what it charged, so only the root's own storage remains.
func TestVarBudgetReturnsToRootAfterRun(t *testing.T) {
	script := `A=1
x=$(B=2; echo $B)
( C=3 )
{ D=4; echo $D; } | { E=5; cat; } | { F=$(G=6; echo $G); cat; }
for i in 1 2 3; do H=$i | cat; done
`
	prog, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	require.NoError(t, err)
	var out bytes.Buffer
	r, err := New(StdIO(nil, &out, &out), AllowedCommands([]string{"rshell:echo", "rshell:cat"}))
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })
	require.NoError(t, r.Run(context.Background(), prog), out.String())

	root := r.writeEnv.(*overlayEnviron)
	assert.Equal(t, root.charged, root.budget.live)
	assert.Equal(t, root.total, root.charged, "the root's seed is empty")
}
