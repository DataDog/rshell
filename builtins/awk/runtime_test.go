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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins"
)

type closeTrackedFile struct {
	*strings.Reader
	closed bool
}

type manuallyReleasedReadCloser struct {
	started     chan struct{}
	release     chan struct{}
	closed      chan struct{}
	finished    chan struct{}
	startedOnce sync.Once
	closedOnce  sync.Once
	releaseOnce sync.Once
}

func newManuallyReleasedReadCloser() *manuallyReleasedReadCloser {
	return &manuallyReleasedReadCloser{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		closed:   make(chan struct{}),
		finished: make(chan struct{}),
	}
}

func (r *manuallyReleasedReadCloser) Read([]byte) (int, error) {
	r.startedOnce.Do(func() { close(r.started) })
	<-r.release
	close(r.finished)
	return 0, io.EOF
}

func (r *manuallyReleasedReadCloser) Write([]byte) (int, error) {
	return 0, os.ErrInvalid
}

func (r *manuallyReleasedReadCloser) Close() error {
	r.closedOnce.Do(func() { close(r.closed) })
	<-r.release
	return nil
}

func (r *manuallyReleasedReadCloser) unblock() {
	r.releaseOnce.Do(func() { close(r.release) })
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

func TestRuntimeFlushesBufferedStdoutOnError(t *testing.T) {
	prog, err := parseProgram(`BEGIN { print "piped" | "cat"; print "plain"; z = 0; x = 1 / z; print "later" | "cat" }`)
	require.NoError(t, err)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	callCtx := &builtins.CallContext{
		Stdout: &stdout,
		Stderr: &stderr,
	}

	result := newRuntime(callCtx, prog).run(context.Background(), nil)

	assert.Equal(t, uint8(1), result.Code)
	assert.Equal(t, "plain\n", stdout.String())
	assert.Contains(t, stderr.String(), "division by zero attempted")
}

func TestRecordSourceFallbackCancellationReturnsPromptly(t *testing.T) {
	reader := newManuallyReleasedReadCloser()
	t.Cleanup(reader.unblock)
	rt := newRuntime(&builtins.CallContext{}, &program{})
	src := rt.newRecordSource("input", reader)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := src.readRecord(ctx)
		done <- err
	}()

	<-reader.started
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("record read did not observe cancellation")
	}
	select {
	case <-reader.closed:
	case <-time.After(time.Second):
		t.Fatal("record source did not close its reader")
	}

	reader.unblock()
	select {
	case <-reader.finished:
	case <-time.After(time.Second):
		t.Fatal("fallback read goroutine did not exit")
	}
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

func TestRecordSourceAcceptsMaxRecordWithMultibyteSeparator(t *testing.T) {
	const separator = "\U0010ffff"
	record := strings.Repeat("x", MaxRecordBytes)
	rt := newRuntime(&builtins.CallContext{}, &program{})
	require.NoError(t, rt.setVar("RS", stringValue(separator)))
	src := rt.newRecordSource("input", io.NopCloser(strings.NewReader(record+separator)))
	t.Cleanup(src.close)

	got, ok, err := src.readRecord(context.Background())
	require.NoError(t, err)
	require.True(t, ok)
	assert.Len(t, got, MaxRecordBytes)

	_, ok, err = src.readRecord(context.Background())
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestRecordSourceRejectsRecordOverLimitWithLargerScanBuffer(t *testing.T) {
	const separator = "\U0010ffff"
	rt := newRuntime(&builtins.CallContext{}, &program{})
	require.NoError(t, rt.setVar("RS", stringValue(separator)))
	src := rt.newRecordSource("input", io.NopCloser(strings.NewReader(strings.Repeat("x", MaxRecordBytes+1))))
	t.Cleanup(src.close)

	_, _, err := src.readRecord(context.Background())
	require.EqualError(t, err, "record exceeds 1048576 bytes")
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
	require.EqualError(t, err, "substitution match index storage exceeds 32768 indices")

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
