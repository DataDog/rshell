// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"bytes"
	"context"
	"fmt"
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

type closeInterruptedReader struct {
	started     chan struct{}
	closed      chan struct{}
	finished    chan struct{}
	startedOnce sync.Once
	closedOnce  sync.Once
}

func newManuallyReleasedReadCloser() *manuallyReleasedReadCloser {
	return &manuallyReleasedReadCloser{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		closed:   make(chan struct{}),
		finished: make(chan struct{}),
	}
}

func newCloseInterruptedReader() *closeInterruptedReader {
	return &closeInterruptedReader{
		started:  make(chan struct{}),
		closed:   make(chan struct{}),
		finished: make(chan struct{}),
	}
}

func (r *closeInterruptedReader) Read([]byte) (int, error) {
	r.startedOnce.Do(func() { close(r.started) })
	<-r.closed
	close(r.finished)
	return 0, os.ErrClosed
}

func (r *closeInterruptedReader) Close() error {
	r.closedOnce.Do(func() { close(r.closed) })
	return nil
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
	prog, err := parseProgram(`{ zero = 0; print 1 % zero }`)
	require.NoError(t, err)

	opened := &closeTrackedFile{Reader: strings.NewReader("row\n")}
	var stderr bytes.Buffer
	callCtx := &builtins.CallContext{
		Stderr: &stderr,
		OpenFile: func(context.Context, string, int, os.FileMode) (io.ReadWriteCloser, error) {
			return opened, nil
		},
	}

	result := newRuntime(callCtx, prog).run(context.Background(), []string{"input"})

	assert.Equal(t, uint8(2), result.Code)
	assert.Contains(t, stderr.String(), "division by zero attempted")
	assert.True(t, opened.closed)
}

func TestRuntimeBoundsAggregateStatementExecutions(t *testing.T) {
	prog, err := parseProgram(`BEGIN { print "at-limit"; print "unreachable" }`)
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	rt := newRuntime(&builtins.CallContext{Stdout: &stdout, Stderr: &stderr}, prog)
	rt.stmtExecutions = maxStatementExecutions - 1

	result := rt.run(context.Background(), nil)

	assert.Equal(t, uint8(1), result.Code)
	assert.Equal(t, "at-limit\n", stdout.String())
	assert.Equal(t, "awk: statement execution limit exceeded (maximum 1048576)\n", stderr.String())
}

func TestRuntimeBoundsAggregateInputBytes(t *testing.T) {
	prog, err := parseProgram(`0`)
	require.NoError(t, err)

	var stderr bytes.Buffer
	rt := newRuntime(&builtins.CallContext{
		Stdin:  strings.NewReader("x\n"),
		Stderr: &stderr,
	}, prog)
	rt.inputBytes = maxInputBytes - 1

	result := rt.run(context.Background(), nil)

	assert.Equal(t, uint8(1), result.Code)
	assert.Equal(t, "awk: -: input byte limit exceeded (maximum 67108864 bytes)\n", stderr.String())
	assert.Equal(t, maxInputBytes-1, rt.inputBytes)
	assert.Zero(t, rt.inputRecords)
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

func TestMainStdinCancellationClosesUnderlyingReader(t *testing.T) {
	reader := newCloseInterruptedReader()
	t.Cleanup(func() { _ = reader.Close() })
	rt := newRuntime(&builtins.CallContext{Stdin: reader}, &program{})
	src, err := rt.openRecordSource(context.Background(), "-")
	require.NoError(t, err)
	rt.mainInput = src
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := rt.readMainRecord(ctx)
		done <- err
	}()

	<-reader.started
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("main stdin read did not observe cancellation")
	}
	select {
	case <-reader.finished:
	case <-time.After(time.Second):
		t.Fatal("main stdin read did not exit after closing its reader")
	}
}

func TestMainStdinCleanupDoesNotCloseCallerReader(t *testing.T) {
	reader := &closeTrackedFile{Reader: strings.NewReader("")}
	rt := newRuntime(&builtins.CallContext{Stdin: reader}, &program{})
	src, err := rt.openRecordSource(context.Background(), "-")
	require.NoError(t, err)

	src.close()

	assert.False(t, reader.closed)
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

func TestRecordRebuildChargesAggregateStringWorkBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		assign func(*runtime) error
	}{
		{name: "field", assign: func(rt *runtime) error { return rt.setField(1, stringValue("x")) }},
		{name: "NF", assign: func(rt *runtime) error { return rt.setNF(numberValue(2)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := newRuntime(&builtins.CallContext{}, &program{})
			rt.record = "a b c"
			rt.fields = []string{"a", "b", "c"}
			rt.stringWorkBytes = maxStringProcessingBytes - 2

			err := tc.assign(rt)

			require.EqualError(t, err, "string processing limit exceeded (maximum 67108864 bytes)")
			assert.Equal(t, "a b c", rt.record)
			assert.Equal(t, []string{"a", "b", "c"}, rt.fields)
		})
	}
}

func TestRuntimeRegexCacheBoundsStorage(t *testing.T) {
	rt := newRuntime(&builtins.CallContext{}, &program{})

	first, err := rt.compileRegex("abc")
	require.NoError(t, err)
	again, err := rt.compileRegex("abc")
	require.NoError(t, err)
	assert.Same(t, first, again)

	for i := 0; i <= maxRegexCacheEntries; i++ {
		rt.rememberRegex(regexCacheKey{pattern: fmt.Sprintf("pattern-%d", i)}, &awkRegex{})
	}
	assert.LessOrEqual(t, len(rt.regexCache), maxRegexCacheEntries)
	assert.LessOrEqual(t, rt.regexCacheBytes, maxRegexCacheBytes)
}

func TestRegexCacheMissesChargeAggregateWork(t *testing.T) {
	rt := newRuntime(&builtins.CallContext{}, &program{})
	rt.stringWorkBytes = maxStringProcessingBytes - minRegexCompileWork

	first, err := rt.compileRegex("x")
	require.NoError(t, err)
	used := rt.stringWorkBytes
	again, err := rt.compileRegex("x")
	require.NoError(t, err)
	assert.Same(t, first, again)
	assert.Equal(t, used, rt.stringWorkBytes)

	_, err = rt.compileRegex("y")
	require.EqualError(t, err, "string processing limit exceeded (maximum 67108864 bytes)")
}

func TestEnvironInitializationAccountsStorageAndCachesLimitError(t *testing.T) {
	envCalls := 0
	yields := 0
	stopped := false
	rt := newRuntime(&builtins.CallContext{
		Env: func(yield func(name, value string) bool) {
			envCalls++
			for _, entry := range [][2]string{{"A", "x"}, {"B", "y"}, {"C", "z"}} {
				yields++
				if !yield(entry[0], entry[1]) {
					stopped = true
					return
				}
			}
		},
	}, &program{})
	rt.varBytes = MaxVariableBytes - 2
	wantErr := fmt.Sprintf("variable storage limit exceeded (%d bytes total)", MaxVariableBytes+2)

	_, err := rt.evalLength(&callExpr{args: []expr{&varExpr{name: "ENVIRON"}}})
	require.EqualError(t, err, wantErr)
	assert.Equal(t, 1, envCalls)
	assert.Equal(t, 2, yields)
	assert.True(t, stopped)
	assert.Equal(t, MaxVariableBytes, rt.varBytes)
	assert.Equal(t, 2, rt.arraySizes[arraySlot{name: "ENVIRON", key: "A"}])
	assert.Equal(t, inputStringValue("x"), rt.arrays["ENVIRON"]["A"])
	assert.NotContains(t, rt.arrays["ENVIRON"], "B")

	accesses := []struct {
		name string
		call func() error
	}{
		{name: "indexed read", call: func() error { _, err := rt.getArrayElem("ENVIRON", "A"); return err }},
		{name: "membership", call: func() error { _, err := rt.hasArrayElem("ENVIRON", "A"); return err }},
		{name: "assignment", call: func() error { return rt.setArrayElem("ENVIRON", "A", stringValue("new")) }},
		{name: "element deletion", call: func() error { return rt.deleteArrayElem("ENVIRON", "A") }},
		{name: "array deletion", call: func() error { return rt.deleteArray("ENVIRON") }},
		{name: "iteration", call: func() error { _, err := rt.arrayKeys("ENVIRON"); return err }},
	}
	for _, access := range accesses {
		t.Run(access.name, func(t *testing.T) {
			require.EqualError(t, access.call(), wantErr)
		})
	}
	assert.Equal(t, 1, envCalls)
	assert.Equal(t, 2, yields)
}

func TestEnvironInitializationHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	envCalls := 0
	yields := 0
	stopped := false
	rt := newRuntime(&builtins.CallContext{
		Env: func(yield func(name, value string) bool) {
			envCalls++
			yields++
			if !yield("A", "x") {
				stopped = true
				return
			}
			cancel()
			yields++
			if !yield("B", "y") {
				stopped = true
				return
			}
			yields++
			yield("C", "z")
		},
	}, &program{})
	rt.ctx = ctx

	_, err := rt.arrayStorage("ENVIRON")
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, envCalls)
	assert.Equal(t, 2, yields)
	assert.True(t, stopped)
	assert.Equal(t, 2, rt.varBytes)
	assert.Equal(t, 2, rt.arraySizes[arraySlot{name: "ENVIRON", key: "A"}])
	assert.Equal(t, inputStringValue("x"), rt.arrays["ENVIRON"]["A"])
	assert.NotContains(t, rt.arrays["ENVIRON"], "B")

	_, err = rt.hasArrayElem("ENVIRON", "A")
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, envCalls)
	assert.Equal(t, 2, yields)
}

func TestEmptyMainInputFileOpenAttemptsAreBounded(t *testing.T) {
	openCalls := 0
	rt := newRuntime(&builtins.CallContext{
		OpenFile: func(context.Context, string, int, os.FileMode) (io.ReadWriteCloser, error) {
			openCalls++
			return &closeTrackedFile{Reader: strings.NewReader("")}, nil
		},
	}, &program{})
	rt.inputArgs = []string{"empty", "empty", "empty"}
	rt.fileOpenAttempts = maxFileOpenAttempts - 2

	_, ok, err := rt.readMainRecord(context.Background())

	require.EqualError(t, err, "file open attempt limit exceeded (maximum 1024)")
	assert.False(t, ok)
	assert.Equal(t, 2, openCalls)
	assert.Equal(t, maxFileOpenAttempts, rt.fileOpenAttempts)
}
