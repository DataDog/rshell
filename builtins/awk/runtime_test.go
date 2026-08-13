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

func TestRuntimeBoundsAggregateCommandInputPayload(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 3<<20)
	callCtx := &builtins.CallContext{
		RunScriptWithStdin: func(_ context.Context, _ string, _ string, _ []string, _ io.Reader, stdout io.Writer) (uint8, error) {
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
	plain, err := compileRegex(`(?m)^|P`)
	require.NoError(t, err)
	bytes, err := compileRegex(`(?m)^|P|\377`)
	require.NoError(t, err)
	assert.Equal(t, plain.FindAllStringIndex("ab\nP", -1), bytes.FindAllStringIndex("ab\nP", -1))

	plain, err = compileRegex(`(P)|((?m)^)`)
	require.NoError(t, err)
	bytes, err = compileRegex(`(P)|((?m)^)|(\377)`)
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

func TestSplitRegexAtNestingLimit(t *testing.T) {
	pattern := "x"
	for range 999 {
		pattern = "(?:" + pattern + ")*"
	}
	re, err := compileRegex(pattern)
	require.NoError(t, err)
	assert.Nil(t, re.continuation)

	rt := newRuntime(&builtins.CallContext{}, &program{})
	fields, err := rt.splitAwkRegex("xx", pattern)
	require.NoError(t, err)
	assert.Equal(t, []string{"", ""}, fields)
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
		{name: "multiline", input: "xb", pattern: `x|(?m)^b`, want: []string{"", "b"}},
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
