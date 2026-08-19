// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"

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
	prog := parseScript(t, `X='BEGIN { "echo ok" | getline x; print x }' awk 'BEGIN { "echo $(awk \"$X\")" | getline x; print "[" x "]" }'`)

	require.NoError(t, r.Run(ctx, prog))
	assert.Equal(t, "[]\n", stdout.String())
	assert.Equal(t, "awk: nested script execution depth limit exceeded (maximum 32)\n", stderr.String())
}

func TestNestedAwkCommandScriptsShareRunWideLimit(t *testing.T) {
	var stderr bytes.Buffer
	recursiveProgram := `BEGIN { for (i = 0; i < 2; i++) print "" | ("awk \"$X\" || echo " i) }`
	r, err := New(
		StdIO(nil, io.Discard, &stderr),
		Env("X="+recursiveProgram),
		allowAllCommandsOpt(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	ctx := context.WithValue(t.Context(), nestedScriptDepthKey{}, maxNestedScriptDepth-10)
	prog := parseScript(t, `awk "$X"`)

	err = r.Run(ctx, prog)
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	assert.Equal(t, ExitStatus(1), status)
	assert.Equal(t, int64(maxNestedScriptExecutionsPerRun), r.nestedScriptCount.Load())
	assert.Contains(t, stderr.String(), "nested script execution limit exceeded (maximum 1024 per run)")
}

func TestNestedAwkCommandScriptStartsWithoutPositionalParameters(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, r.Close()) })
	r.Params = []string{"parent-only"}

	prog := parseScript(t, `awk 'BEGIN { cmd = "for x; do echo \"$x\"; done"; print (cmd | getline x), "[" x "]"; close(cmd) }'`)

	require.NoError(t, r.Run(t.Context(), prog))
	assert.Equal(t, "0 []\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestNestedAwkConcurrentCommandInputCancellationPreservesStdin(t *testing.T) {
	stdin, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = stdin.Close() })
	t.Cleanup(func() { _ = writer.Close() })
	var stderr bytes.Buffer

	r, err := New(
		StdIO(stdin, io.Discard, &stderr),
		MaxExecutionTime(100*time.Millisecond),
		allowAllCommandsOpt(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	prog := parseScript(t, `awk 'BEGIN { a = "echo one; cat"; b = "echo two; cat"; a | getline; b | getline; a | getline }'`)
	done := make(chan error, 1)
	go func() {
		done <- r.Run(context.Background(), prog)
	}()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(time.Second):
		_ = writer.Close()
		<-done
		t.Fatal("nested stdin read did not observe cancellation")
	}
	assert.NotContains(t, stderr.String(), "cat:")

	_, err = writer.WriteString("x")
	require.NoError(t, err)
	buf := make([]byte, 1)
	_, err = io.ReadFull(stdin, buf)
	require.NoError(t, err)
	assert.Equal(t, "x", string(buf))
}
