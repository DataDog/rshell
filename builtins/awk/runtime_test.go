// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins"
)

type closeTrackedFile struct {
	*strings.Reader
	closed bool
}

func (f *closeTrackedFile) Write([]byte) (int, error) {
	return 0, os.ErrInvalid
}

func (f *closeTrackedFile) Close() error {
	f.closed = true
	return nil
}

func TestRuntimeClosesInputsOnError(t *testing.T) {
	prog, err := parseProgram(`BEGIN { getline x < "input"; print 1 / 0 }`)
	require.NoError(t, err)

	opened := &closeTrackedFile{Reader: strings.NewReader("row\n")}
	var stderr bytes.Buffer
	callCtx := &builtins.CallContext{
		Stderr: &stderr,
		OpenFile: func(context.Context, string, int, os.FileMode) (io.ReadWriteCloser, error) {
			return opened, nil
		},
	}

	result := newRuntime(callCtx, prog).run(context.Background(), nil)

	assert.Equal(t, uint8(1), result.Code)
	assert.Contains(t, stderr.String(), "division by zero attempted")
	assert.True(t, opened.closed)
}

func TestRuntimeBoundsAggregateCommandInputPayload(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 3<<20)
	callCtx := &builtins.CallContext{
		RunScriptWithStdin: func(_ context.Context, _ string, _ string, _ io.Reader, stdout io.Writer) (uint8, error) {
			_, err := stdout.Write(payload)
			return 0, err
		},
	}
	rt := newRuntime(callCtx, &program{})

	_, err := rt.openCommandInput(context.Background(), "first")
	require.NoError(t, err)
	_, err = rt.openCommandInput(context.Background(), "second")
	require.ErrorContains(t, err, "command pipe output storage exceeds 5242880 bytes")
	assert.Equal(t, len(payload), rt.redirectPayload)
	assert.Equal(t, 1, rt.redirections)

	_, ok, err := rt.closeCommandInput("first")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Zero(t, rt.redirectPayload)

	_, err = rt.openCommandInput(context.Background(), "second")
	require.NoError(t, err)
}

func TestSplitRegexDoesNotCountIgnoredEmptyMatches(t *testing.T) {
	rt := newRuntime(&builtins.CallContext{}, &program{})
	fields, err := rt.splitAwkRegex(strings.Repeat(" ", MaxFields), "x*")
	require.NoError(t, err)
	assert.Equal(t, []string{strings.Repeat(" ", MaxFields)}, fields)
}

func TestSplitRegexByteModeAdvancesPastEmptyMatchesByByte(t *testing.T) {
	rt := newRuntime(&builtins.CallContext{}, &program{})
	fields, err := rt.splitAwkRegex("é", `\251|x*`)
	require.NoError(t, err)
	assert.Equal(t, []string{"\xc3", ""}, fields)
}

func TestSplitRegexPreservesStartAnchor(t *testing.T) {
	rt := newRuntime(&builtins.CallContext{}, &program{})
	for _, tc := range []struct {
		name    string
		input   string
		pattern string
	}{
		{name: "text", input: "ab", pattern: `^b|x*`},
		{name: "byte mode", input: "é", pattern: `^\251|x*`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields, err := rt.splitAwkRegex(tc.input, tc.pattern)
			require.NoError(t, err)
			assert.Equal(t, []string{tc.input}, fields)
		})
	}
}

func TestSplitRegexChecksCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rt := newRuntime(&builtins.CallContext{}, &program{})
	rt.ctx = ctx

	_, err := rt.splitAwkRegex(strings.Repeat(" ", MaxFields), `\377|x*`)
	require.ErrorIs(t, err, context.Canceled)
}

func TestGensubBoundsAggregateMatchIndexStorage(t *testing.T) {
	re, err := compileRegex(strings.Repeat("()", 64) + "x")
	require.NoError(t, err)
	matchLimit := gensubMatchLimit(re)
	atLimit := strings.Repeat("x", matchLimit)

	got, err := gensubAwk(context.Background(), re, atLimit, "&", stringValue("g"))
	require.NoError(t, err)
	assert.Equal(t, atLimit, got)

	input := atLimit + "x"

	_, err = gensubAwk(context.Background(), re, input, "&", stringValue("g"))
	require.EqualError(t, err, "gensub match index storage exceeds 2097154 indices")

	got, err = gensubAwk(context.Background(), re, input, "&", numberValue(1))
	require.NoError(t, err)
	assert.Equal(t, input, got)
}

func TestGensubPreservesMultilineAnchors(t *testing.T) {
	re, err := compileRegex(`(?m)^`)
	require.NoError(t, err)

	got, err := gensubAwk(context.Background(), re, "ab\nc", "X", stringValue("g"))
	require.NoError(t, err)
	assert.Equal(t, "Xab\nXc", got)
}

func TestGensubChecksCancellation(t *testing.T) {
	re, err := compileRegex(strings.Repeat("()", 64) + "x")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = gensubAwk(ctx, re, strings.Repeat("x", 4096), "&", stringValue("g"))
	require.ErrorIs(t, err, context.Canceled)
}
