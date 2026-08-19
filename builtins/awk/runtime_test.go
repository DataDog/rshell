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
	"unicode/utf8"

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

type cancelAfterChecksContext struct {
	context.Context
	mu        sync.Mutex
	done      chan struct{}
	remaining int
	canceled  bool
}

func newCancelAfterChecksContext(remaining int) *cancelAfterChecksContext {
	return &cancelAfterChecksContext{
		Context:   context.Background(),
		done:      make(chan struct{}),
		remaining: remaining,
	}
}

func (ctx *cancelAfterChecksContext) Done() <-chan struct{} {
	return ctx.done
}

func (ctx *cancelAfterChecksContext) Err() error {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if ctx.remaining > 0 {
		ctx.remaining--
		return nil
	}
	if !ctx.canceled {
		close(ctx.done)
		ctx.canceled = true
	}
	return context.Canceled
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
	prog, err := parseProgram(`BEGIN { getline x < "input"; zero = 0; print 1 % zero }`)
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

	assert.Equal(t, uint8(2), result.Code)
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
		RunScriptWithStdin: func(_ context.Context, _ string, _ string, _ []string, stdin io.Reader, stdout io.Writer) (uint8, error) {
			_, err := io.Copy(stdout, stdin)
			return 0, err
		},
	}

	result := newRuntime(callCtx, prog).run(context.Background(), nil)

	assert.Equal(t, uint8(2), result.Code)
	assert.Equal(t, "plain\npiped\n", stdout.String())
	assert.Contains(t, stderr.String(), "division by zero attempted")
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

func TestRuntimeBoundsCommandPipeStdoutAcrossReopens(t *testing.T) {
	prog, err := parseProgram(`BEGIN { print "" | "child"; close("child"); print "" | "child"; close("child"); print "" | "child"; close("child") }`)
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	calls := 0
	secondCanceled := false
	callCtx := &builtins.CallContext{
		Stdout: &stdout,
		Stderr: &stderr,
		RunScriptWithStdin: func(ctx context.Context, _ string, _ string, _ []string, _ io.Reader, stdout io.Writer) (uint8, error) {
			calls++
			_, err := io.WriteString(stdout, "xx")
			if calls == 2 {
				secondCanceled = ctx.Err() == context.Canceled
			}
			return 0, err
		},
	}
	rt := newRuntime(callCtx, prog)
	rt.stdoutBytes = MaxStdoutBytes - 3

	result := rt.run(context.Background(), nil)

	assert.Equal(t, uint8(1), result.Code)
	assert.Equal(t, 2, calls)
	assert.Equal(t, "xx", stdout.String())
	assert.True(t, secondCanceled)
	assert.Equal(t, "awk: stdout output exceeds 10485760 bytes\n", stderr.String())
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

func TestGetlineInputByteLimitIsFatal(t *testing.T) {
	rt := newRuntime(&builtins.CallContext{}, &program{})
	rt.inputBytes = maxInputBytes - 1
	rt.fileInputs["input"] = rt.newBufferedRecordSource("input", io.NopCloser(strings.NewReader("x\n")))

	_, status, err := rt.getlineFileRecord(context.Background(), "input")

	require.ErrorIs(t, err, errInputBytesExceeded)
	assert.Zero(t, status)
	assert.Empty(t, rt.getVar("ERRNO").String())
}

func TestGetlineScannerFailureCountsTowardInputByteLimit(t *testing.T) {
	input := strings.Repeat("x", MaxRecordBytes+utf8.UTFMax+1)

	t.Run("charges buffered bytes", func(t *testing.T) {
		rt := newRuntime(&builtins.CallContext{}, &program{})
		rt.fileInputs["input"] = rt.newBufferedRecordSource("input", io.NopCloser(strings.NewReader(input)))

		_, status, err := rt.getlineFileRecord(context.Background(), "input")

		require.NoError(t, err)
		assert.Equal(t, -1, status)
		assert.Equal(t, MaxRecordBytes+utf8.UTFMax, rt.inputBytes)
	})

	t.Run("limit is fatal", func(t *testing.T) {
		rt := newRuntime(&builtins.CallContext{}, &program{})
		rt.inputBytes = maxInputBytes - 1
		require.NoError(t, rt.setVar("ERRNO", stringValue("keep")))
		rt.fileInputs["input"] = rt.newBufferedRecordSource("input", io.NopCloser(strings.NewReader(input)))

		_, status, err := rt.getlineFileRecord(context.Background(), "input")

		require.ErrorIs(t, err, errInputBytesExceeded)
		assert.Zero(t, status)
		assert.Equal(t, maxInputBytes-1, rt.inputBytes)
		assert.Equal(t, "keep", rt.getVar("ERRNO").String())
	})
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

func TestRuntimeBoundsAggregateCommandInputPayload(t *testing.T) {
	record := append(bytes.Repeat([]byte("x"), (64<<10)-1), '\n')
	payload := bytes.Repeat(record, 48)
	callCtx := &builtins.CallContext{
		RunScriptWithStdin: func(_ context.Context, _ string, _ string, _ []string, _ io.Reader, stdout io.Writer) (uint8, error) {
			_, err := stdout.Write(payload)
			return 0, err
		},
	}
	rt := newRuntime(callCtx, &program{})

	_, err := rt.openCommandInput(context.Background(), "first")
	require.NoError(t, err)
	for {
		_, status, err := rt.getlineCommandRecord(context.Background(), "first")
		require.NoError(t, err)
		if status == 0 {
			break
		}
	}
	_, err = rt.openCommandInput(context.Background(), "second")
	require.NoError(t, err)
	_, _, err = rt.getlineCommandRecord(context.Background(), "second")
	require.ErrorContains(t, err, "command pipe output storage exceeds 5242880 bytes")
	assert.Equal(t, len(payload), rt.redirectPayload)
	assert.Equal(t, 1, rt.redirections)

	_, ok, err := rt.closeCommandInput("first")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Zero(t, rt.redirectPayload)

	_, err = rt.openCommandInput(context.Background(), "second")
	require.NoError(t, err)
	_, ok, err = rt.closeCommandInput("second")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Zero(t, rt.redirectPayload)
}

func TestCommandInputStreamsBeforeChildExits(t *testing.T) {
	childExited := make(chan struct{})
	callCtx := &builtins.CallContext{
		RunScriptWithStdin: func(ctx context.Context, _ string, _ string, _ []string, _ io.Reader, stdout io.Writer) (uint8, error) {
			if _, err := io.WriteString(stdout, "ready\n"); err != nil {
				close(childExited)
				return 1, err
			}
			<-ctx.Done()
			close(childExited)
			return 0, ctx.Err()
		},
	}
	rt := newRuntime(callCtx, &program{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type getlineResult struct {
		record string
		status int
		err    error
	}
	got := make(chan getlineResult, 1)
	go func() {
		record, status, err := rt.getlineCommandRecord(ctx, "child")
		got <- getlineResult{record: record, status: status, err: err}
	}()

	select {
	case result := <-got:
		require.NoError(t, result.err)
		assert.Equal(t, "ready", result.record)
		assert.Equal(t, 1, result.status)
	case <-time.After(time.Second):
		t.Fatal("getline did not return the available record while the child was running")
	}
	select {
	case <-childExited:
		t.Fatal("child exited before getline returned")
	default:
	}

	status, ok, err := rt.closeCommandInput("child")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, uint8(0), status)
	select {
	case <-childExited:
	case <-time.After(time.Second):
		t.Fatal("close did not cancel and wait for the child")
	}
}

func TestCloseCommandInputPreservesChildError(t *testing.T) {
	childErr := fmt.Errorf("nested command not allowed")
	callCtx := &builtins.CallContext{
		RunScriptWithStdin: func(ctx context.Context, _ string, _ string, _ []string, _ io.Reader, stdout io.Writer) (uint8, error) {
			_, err := io.WriteString(stdout, "ready\n")
			if err != nil {
				return 1, err
			}
			<-ctx.Done()
			return 1, childErr
		},
	}
	rt := newRuntime(callCtx, &program{})

	record, status, err := rt.getlineCommandRecord(context.Background(), "child")
	require.NoError(t, err)
	assert.Equal(t, "ready", record)
	assert.Equal(t, 1, status)

	closeStatus, ok, err := rt.closeCommandInput("child")
	require.ErrorIs(t, err, childErr)
	require.True(t, ok)
	assert.Equal(t, uint8(1), closeStatus)
}

func TestCloseCommandRedirectionPreservesAutoFlushedOutputOrder(t *testing.T) {
	const command = "read x"
	for _, tc := range []struct {
		name  string
		reuse bool
	}{{name: "stored"}, {name: "reused", reuse: true}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			rt := newRuntime(&builtins.CallContext{
				RunScriptWithStdin: func(context.Context, string, string, []string, io.Reader, io.Writer) (uint8, error) {
					status := uint8(calls % 2)
					calls++
					return status, nil
				},
			}, &program{})
			require.NoError(t, rt.writeCommandPipe(context.Background(), &stringExpr{value: command}, "x\n"))
			creationOrder := rt.pipes[command].creationOrder
			require.NoError(t, rt.flushCommandPipesForStdout(context.Background(), stmtFuture{}))
			require.Equal(t, creationOrder, rt.flushedPipes[command].creationOrder)
			_, err := rt.openCommandInput(context.Background(), command)
			require.NoError(t, err)
			if tc.reuse {
				require.NoError(t, rt.writeCommandPipe(context.Background(), &stringExpr{value: command}, "x\n"))
				require.Equal(t, creationOrder, rt.pipes[command].creationOrder)
			}

			for _, want := range []uint8{1, 0} {
				status, ok, err := rt.closeCommandRedirection(context.Background(), command, true)
				require.NoError(t, err)
				require.True(t, ok)
				assert.Equal(t, want, status)
			}
			_, ok, err := rt.closeCommandRedirection(context.Background(), command, true)
			require.NoError(t, err)
			assert.False(t, ok)
		})
	}
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

func TestSplitRegexDoesNotCountIgnoredEmptyMatches(t *testing.T) {
	rt := newRuntime(&builtins.CallContext{}, &program{})
	fields, err := rt.splitAwkRegex(strings.Repeat(" ", MaxFields), "x*")
	require.NoError(t, err)
	assert.Equal(t, []string{strings.Repeat(" ", MaxFields)}, fields)
}

func TestSplitRegexByteModeAdvancesPastEmptyMatchesByRune(t *testing.T) {
	rt := newRuntime(&builtins.CallContext{}, &program{})
	fields, err := rt.splitAwkRegex("é\xfe", `\376|x*`)
	require.NoError(t, err)
	assert.Equal(t, []string{"é", ""}, fields)
}

func TestByteRegexMarkersDoNotMatchValidPrivateUseRunes(t *testing.T) {
	re, err := compileRegex(`\377`)
	require.NoError(t, err)

	assert.False(t, re.MatchString(string(rune(0xe0ff))))
	assert.True(t, re.MatchString("\xff"))
	assert.Nil(t, re.FindStringIndex(string(rune(0xe0ff))))
	assert.Equal(t, []int{0, 1}, re.FindStringIndex("\xff"))
}

func TestByteRegexMarkersDoNotCaseFoldOrCollide(t *testing.T) {
	re, err := compileRegexWithOptions(`\260`, true)
	require.NoError(t, err)

	assert.False(t, re.MatchString(string([]rune{'\ue000', '\U000104b0'})))
	assert.False(t, re.MatchString(string([]rune{'\ue000', '\U000f00b0'})))
	assert.True(t, re.MatchString("\xb0"))
}

func TestNormalizeAwkRegexCanonicalizesMaxSizeIntervals(t *testing.T) {
	count := MaxRegexBytes / len("a{01}")
	normalized, byteMode, ok, err := normalizeAwkRegex(strings.Repeat("a{01}", count))
	require.NoError(t, err)
	require.True(t, ok)
	assert.False(t, byteMode)
	assert.Equal(t, strings.Repeat("a{1}", count), normalized)
}

func TestByteRegexReaderPreservesRangesAndOffsets(t *testing.T) {
	re, err := compileRegex(`[a-\377]$`)
	require.NoError(t, err)
	assert.False(t, re.MatchString("𐀀"))
	assert.False(t, re.MatchString("\ue000𐀀"))

	re, err = compileRegex(string('\U000f00ff') + "|\xfe")
	require.NoError(t, err)
	assert.False(t, re.MatchString("\xff"))
	assert.True(t, re.MatchString(string('\U000f00ff')))

	re, err = compileRegex(`(\377)|(.)`)
	require.NoError(t, err)
	assert.Equal(t, [][]int{{0, 2, -1, -1, 0, 2}, {2, 3, 2, 3, -1, -1}}, re.FindAllStringSubmatchIndex("é\xff", -1))
}

func TestByteRegexRepeatedMatchesPreserveContext(t *testing.T) {
	plain, err := compileRegex(`^|P`)
	require.NoError(t, err)
	bytes, err := compileRegex(`^|P|\377`)
	require.NoError(t, err)
	assert.Equal(t, plain.FindAllStringIndex("ab\nP", -1), bytes.FindAllStringIndex("ab\nP", -1))

	plain, err = compileRegex(`(P)|(^)`)
	require.NoError(t, err)
	bytes, err = compileRegex(`(P)|(^)|(\377)`)
	require.NoError(t, err)
	got := bytes.FindAllStringSubmatchIndex("ab\nP", -1)
	for i := range got {
		got[i] = got[i][:6]
	}
	assert.Equal(t, plain.FindAllStringSubmatchIndex("ab\nP", -1), got)
}

func TestRegexAtNestingLimitCompiles(t *testing.T) {
	pattern := "a"
	for range 999 {
		pattern = "(" + pattern + ")"
	}
	re, err := compileRegex(pattern)
	require.NoError(t, err)
	assert.True(t, re.MatchString("a"))
}

func TestSplitRegexWithDeepNesting(t *testing.T) {
	pattern := "x"
	for range 499 {
		pattern = "(" + pattern + ")*"
	}
	_, err := compileRegex(pattern)
	require.NoError(t, err)

	rt := newRuntime(&builtins.CallContext{}, &program{})
	fields, err := rt.splitAwkRegex("xx", pattern)
	require.NoError(t, err)
	assert.Equal(t, []string{"", ""}, fields)

	_, err = rt.splitAwkRegex(strings.Repeat("x ", MaxFields+1), pattern)
	require.ErrorIs(t, err, errTooManyFields)
}

func TestSplitRegexPreservesStartAnchor(t *testing.T) {
	rt := newRuntime(&builtins.CallContext{}, &program{})
	for _, tc := range []struct {
		name    string
		input   string
		pattern string
		want    []string
	}{
		{name: "text", input: "ab", pattern: `^b|x*`, want: []string{"ab"}},
		{name: "byte mode", input: "é", pattern: `^\376|x*`, want: []string{"é"}},
		{name: "anchored alternative", input: "xb", pattern: `x|^b`, want: []string{"", "b"}},
		{name: "word boundary", input: "xb", pattern: `x|\134bb`, want: []string{"", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields, err := rt.splitAwkRegex(tc.input, tc.pattern)
			require.NoError(t, err)
			assert.Equal(t, tc.want, fields)
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

func TestAutomaticFieldSplitUsesRunContext(t *testing.T) {
	count := (MaxRegexBytes - 32) / 2
	fs := `\y` + strings.Repeat("a?", count)
	prog, err := parseProgram(`{ print NF }`)
	require.NoError(t, err)

	var stderr bytes.Buffer
	rt := newRuntime(&builtins.CallContext{
		Stdin:  strings.NewReader(strings.Repeat("a", count) + "\n"),
		Stderr: &stderr,
	}, prog)
	require.NoError(t, rt.setVar("FS", stringValue(fs)))
	_, err = rt.compileRegex(fs)
	require.NoError(t, err)

	ctx := newCancelAfterChecksContext(3)
	started := time.Now()
	result := rt.run(ctx, nil)

	assert.Less(t, time.Since(started), time.Second)
	assert.Equal(t, uint8(1), result.Code)
	assert.Equal(t, "awk: context canceled\n", stderr.String())
}

func TestOrdinaryRegexChecksCancellationDuringMatch(t *testing.T) {
	const count = 1024
	re, err := compileRegex(strings.Repeat("a?", count))
	require.NoError(t, err)

	ctx := newCancelAfterChecksContext(3)
	re.ctx = ctx

	assert.Nil(t, re.FindStringIndex(strings.Repeat("a", count)))
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestBoundaryRegexChecksCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rt := newRuntime(&builtins.CallContext{}, &program{})
	rt.ctx = ctx
	count := (MaxRegexBytes - 32) / 2
	re, err := rt.compileRegex(`\y` + strings.Repeat("a?", count))
	require.NoError(t, err)

	done := make(chan bool, 1)
	go func() {
		done <- re.MatchString(strings.Repeat("a", count))
	}()
	select {
	case matched := <-done:
		assert.False(t, matched)
	case <-time.After(time.Second):
		t.Fatal("boundary regular expression did not observe cancellation")
	}
}

func TestBoundaryRegexCaptureCompactionChecksCancellation(t *testing.T) {
	re, err := compileRegexWithOptions(`\B(.)*`, false)
	require.NoError(t, err)
	machine := newAwkBoundaryMachine(re.boundaryProg)
	machine.recordCaptures = true
	machine.captureGCAt = 0
	machine.matchHistory = -1
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	machine.ctx = ctx
	for i := range 1024 {
		machine.captures = append(machine.captures, awkBoundaryCapture{slot: 2, pos: i, previous: i - 1})
	}
	queue := awkBoundaryQueue{threads: []awkBoundaryThread{{history: len(machine.captures) - 1}}}

	machine.compactCaptureHistory(&queue)

	assert.True(t, machine.canceled)
}

func TestBoundaryRegexCompactsCaptureHistory(t *testing.T) {
	rt := newRuntime(&builtins.CallContext{}, &program{})
	alternatives := strings.TrimSuffix(strings.Repeat(`(.*)z|`, 20), "|")
	re, err := rt.compileRegex(`\y(` + alternatives + `)`)
	require.NoError(t, err)

	assert.Nil(t, re.FindStringSubmatchIndex(strings.Repeat("a", 1000)))
	require.NotNil(t, re.boundaryMachine)
	assert.Less(t, len(re.boundaryMachine.captures), awkBoundaryCaptureGCMinimum)
}

func TestBoundaryRegexPrunesOverwrittenCaptureHistory(t *testing.T) {
	pattern := "."
	for range 20 {
		pattern = "(" + pattern + ")"
	}
	rt := newRuntime(&builtins.CallContext{}, &program{})
	re, err := rt.compileRegex(`\B` + pattern + `*`)
	require.NoError(t, err)

	input := strings.Repeat(" ", 110)
	match := re.FindStringSubmatchIndex(input)
	require.Len(t, match, 42)
	assert.Equal(t, []int{0, len(input)}, match[:2])
	for i := 2; i < len(match); i += 2 {
		assert.Equal(t, []int{len(input) - 1, len(input)}, match[i:i+2])
	}
	assert.Less(t, len(re.boundaryMachine.captures), awkBoundaryCaptureGCMinimum)
}

func TestRuntimeRegexCacheKeysOptionsAndBoundsStorage(t *testing.T) {
	rt := newRuntime(&builtins.CallContext{}, &program{})

	first, err := rt.compileRegex("abc")
	require.NoError(t, err)
	again, err := rt.compileRegex("abc")
	require.NoError(t, err)
	assert.Same(t, first, again)

	require.NoError(t, rt.setVar("IGNORECASE", numberValue(1)))
	folded, err := rt.compileRegex("abc")
	require.NoError(t, err)
	assert.NotSame(t, first, folded)

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

func TestAsortiChargesAggregateSortedKeyWork(t *testing.T) {
	rt := newRuntime(&builtins.CallContext{}, &program{})
	rt.arrays["items"] = map[string]value{"a": numberValue(1), "b": numberValue(2)}
	rt.arrays["sorted"] = map[string]value{"keep": numberValue(1)}
	rt.stringWorkBytes = maxStringProcessingBytes - 1

	_, err := rt.evalAsorti(&callExpr{args: []expr{
		&varExpr{name: "items"},
		&varExpr{name: "sorted"},
	}})

	require.EqualError(t, err, "string processing limit exceeded (maximum 67108864 bytes)")
	require.Equal(t, map[string]value{"keep": numberValue(1)}, rt.arrays["sorted"])
}

func TestCommandEnvironmentChargesAggregateSortedWork(t *testing.T) {
	rt := newRuntime(&builtins.CallContext{}, &program{})
	rt.environSet = true
	rt.arrays["ENVIRON"] = map[string]value{
		"A": stringValue("abc"),
		"B": stringValue("defgh"),
	}
	rt.stringWorkBytes = maxStringProcessingBytes - 24

	env, bytesUsed, err := rt.commandEnvironment()
	require.NoError(t, err)
	assert.Equal(t, []string{"A=abc", "B=defgh"}, env)
	assert.Equal(t, 12, bytesUsed)
	assert.Equal(t, maxStringProcessingBytes, rt.stringWorkBytes)

	_, _, err = rt.commandEnvironment()
	require.EqualError(t, err, "string processing limit exceeded (maximum 67108864 bytes)")
	assert.Equal(t, maxStringProcessingBytes, rt.stringWorkBytes)
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
		{name: "command environment", call: func() error { _, _, err := rt.commandEnvironment(); return err }},
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

func TestFailedFileOpenAttemptsAreBounded(t *testing.T) {
	openCalls := 0
	rt := newRuntime(&builtins.CallContext{
		OpenFile: func(context.Context, string, int, os.FileMode) (io.ReadWriteCloser, error) {
			openCalls++
			return nil, os.ErrNotExist
		},
	}, &program{})
	rt.fileOpenAttempts = maxFileOpenAttempts - 2

	for range 2 {
		_, status, err := rt.getlineFileRecord(context.Background(), "missing")
		require.NoError(t, err)
		assert.Equal(t, -1, status)
	}
	_, status, err := rt.getlineFileRecord(context.Background(), "missing")
	require.EqualError(t, err, "file open attempt limit exceeded (maximum 1024)")
	assert.Equal(t, 0, status)
	assert.Equal(t, 2, openCalls)
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

func TestGensubPreservesStartAnchor(t *testing.T) {
	re, err := compileRegex(`^`)
	require.NoError(t, err)

	got, err := gensubAwk(context.Background(), re, "ab\nc", "X", stringValue("g"))
	require.NoError(t, err)
	assert.Equal(t, "Xab\nc", got)
}

func TestGensubChecksCancellation(t *testing.T) {
	re, err := compileRegex(strings.Repeat("()", 64) + "x")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = gensubAwk(ctx, re, strings.Repeat("x", 4096), "&", stringValue("g"))
	require.ErrorIs(t, err, context.Canceled)
}
