// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNestedAwkCommandScriptDepthSurvivesCommandSubstitution(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	// The first command script enters at the limit; its command substitution
	// must retain that depth so the inner "echo ok" script is rejected.
	ctx := context.WithValue(t.Context(), nestedScriptDepthKey{}, maxNestedScriptDepth-1)
	prog := parseScript(t, `X='BEGIN { "echo ok" | getline x; print x }'; awk 'BEGIN { "echo $(awk \"$X\")" | getline x; print "[" x "]" }'`)

	require.NoError(t, r.Run(ctx, prog))
	assert.Equal(t, "[]\n", stdout.String())
	assert.Equal(t, "awk: nested script execution depth limit exceeded (maximum 32)\n", stderr.String())
}
