// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package jq

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins"
)

type readWriteCloser struct{ io.Reader }

func (r *readWriteCloser) Write([]byte) (int, error) { return 0, errors.New("read-only test input") }
func (r *readWriteCloser) Close() error              { return nil }

type jqRunOptions struct {
	stdin  string
	opener func(context.Context, string) (io.ReadWriteCloser, error)
}

type heapSamplingContext struct {
	context.Context
	baseline uint64
	maxDelta uint64
	calls    int
}

func (c *heapSamplingContext) Err() error {
	c.calls++
	if c.calls%16 != 0 {
		return nil
	}
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	if stats.HeapAlloc > c.baseline && stats.HeapAlloc-c.baseline > c.maxDelta {
		c.maxDelta = stats.HeapAlloc - c.baseline
	}
	return nil
}

func runJQ(t *testing.T, options jqRunOptions, args ...string) (string, string, uint8) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	callCtx := &builtins.CallContext{
		Stdout:          &stdout,
		Stderr:          &stderr,
		Stdin:           strings.NewReader(options.stdin),
		OpenRegularFile: options.opener,
		PortableErr: func(err error) string {
			return err.Error()
		},
	}
	fs := pflag.NewFlagSet("jq", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	handler := Cmd.MakeFlags(fs)
	if Cmd.NormalizeArgs != nil {
		args = Cmd.NormalizeArgs(args)
	}
	if err := fs.Parse(args); err != nil {
		return stdout.String(), err.Error(), exitGeneric
	}
	result := handler(context.Background(), callCtx, fs.Args())
	return stdout.String(), stderr.String(), result.Code
}

func TestCoreFilters(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		filter string
		want   string
	}{
		{
			name:   "path index and iterator",
			input:  `{"name":"Ada","items":[1,2,3]}`,
			filter: `.name, .items[-1], .items[]`,
			want:   `"Ada"` + "\n3\n1\n2\n3\n",
		},
		{
			name:   "constructors and map",
			input:  `{"name":"Ada","items":[1,2,3]}`,
			filter: `{name: .name, selected: [.items[] | select(. >= 2)], doubled: (.items | map(. * 2))}`,
			want:   `{"name":"Ada","selected":[2,3],"doubled":[2,4,6]}` + "\n",
		},
		{
			name:   "introspection",
			input:  `null`,
			filter: `[("μ"|length),([1,2]|length),({"b":1,"a":2}|keys),({"a":null}|has("a")),([10,20][1.9]),([1]|has(0.9)),null[1.5],(null|has([])),(false|not),type]`,
			want:   `[1,2,["a","b"],true,20,true,null,false,true,"null"]` + "\n",
		},
		{
			name:   "optional preserves prior results",
			input:  `null`,
			filter: `(1, 1/0)?`,
			want:   "1\n",
		},
		{
			name:   "object value pipe",
			input:  `null`,
			filter: `{a: 1 | . + 1}`,
			want:   `{"a":2}` + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stdout, stderr, code := runJQ(t, jqRunOptions{stdin: tt.input}, "-c", tt.filter)
			assert.Equal(t, uint8(0), code)
			assert.Empty(t, stderr)
			assert.Equal(t, tt.want, stdout)
		})
	}

	stdout, stderr, code := runJQ(t, jqRunOptions{}, "-nc", `{(("a",0)):1}`)
	assert.Equal(t, uint8(exitRuntime), code)
	assert.Equal(t, "{\"a\":1}\n", stdout)
	assert.Contains(t, stderr, "object keys")
}

func TestStreamOrderingAndShortCircuit(t *testing.T) {
	stdout, stderr, code := runJQ(t, jqRunOptions{}, "-nc", `(1,2) + (10,20)`)
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Equal(t, "11\n12\n21\n22\n", stdout)

	stdout, stderr, code = runJQ(t, jqRunOptions{}, "-nc", `(1/0) + empty`)
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Empty(t, stdout)

	stdout, stderr, code = runJQ(t, jqRunOptions{}, "-nc", `(1/0)[empty]`)
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Empty(t, stdout)

	stdout, stderr, code = runJQ(t, jqRunOptions{}, "-nc", `1, (1 / 0)`)
	assert.Equal(t, uint8(exitRuntime), code)
	assert.Equal(t, "1\n", stdout)
	assert.Contains(t, stderr, "divide")
}

func TestPartialGeneratorErrors(t *testing.T) {
	tests := []struct {
		filter string
		want   string
	}{
		{`1,(2,1/0)`, "1\n2\n"},
		{`(1,1/0)|.+1`, "2\n"},
		{`({"a":1},1/0).a`, "1\n"},
		{`([1],1/0)[]`, "1\n"},
		{`(false,1/0)//3`, ""},
		{`(true,1/0) and (true,false)`, "true\nfalse\n"},
		{`select(true,1/0)`, "null\n"},
		{`[0]|has((0,1/0))`, "true\n"},
		{`{a:(1,1/0),b:2}`, "{\"a\":1,\"b\":2}\n"},
		{`{a:(1,1/0),a:0}`, "{\"a\":0}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.filter, func(t *testing.T) {
			stdout, stderr, code := runJQ(t, jqRunOptions{}, "-nc", tt.filter)
			assert.Equal(t, uint8(exitRuntime), code)
			assert.Equal(t, tt.want, stdout)
			assert.Contains(t, stderr, "divide")
		})
	}
}

func TestOptionalErrorScope(t *testing.T) {
	stdout, stderr, code := runJQ(t, jqRunOptions{}, "-nc", `(1,1/0)?`)
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Equal(t, "1\n", stdout)

	stdout, stderr, code = runJQ(t, jqRunOptions{}, "-nc", `(1,1/0).foo?`)
	assert.Equal(t, uint8(exitRuntime), code)
	assert.Contains(t, stderr, "divide")
	assert.Empty(t, stdout)

	stdout, stderr, code = runJQ(t, jqRunOptions{}, "-nc", `((1,1/0).foo)?`)
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Empty(t, stdout)

	stdout, stderr, code = runJQ(t, jqRunOptions{}, "-nc", `.[(0,1/0)]?`)
	assert.Equal(t, uint8(exitRuntime), code)
	assert.Contains(t, stderr, "divide")
	assert.Equal(t, "null\n", stdout)
}

func TestArithmeticOverloadsAndExactInteger(t *testing.T) {
	filter := `["a"+"b",[1]+[2],{"a":1}+{"b":2},[1,2,1]-[1],"ab"*2.5,"a,b"/",",({a:{x:1}}*{a:{y:2}}),5.5%2,123456789012345678901234567890+1]`
	stdout, stderr, code := runJQ(t, jqRunOptions{}, "-nc", filter)
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Equal(t, `["ab",[1,2],{"a":1,"b":2},[2],"abab",["a","b"],{"a":{"x":1,"y":2}},1,123456789012345678901234567891]`+"\n", stdout)

	stdout, stderr, code = runJQ(t, jqRunOptions{}, "-nc", `"" * 1e100, "x" * -1e100`)
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Equal(t, "\"\"\nnull\n", stdout)

	stdout, stderr, code = runJQ(t, jqRunOptions{}, "-nc", `-5`)
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Equal(t, "-5\n", stdout)

	stdout, stderr, code = runJQ(t, jqRunOptions{}, "-nc", `-.5`)
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Equal(t, "-0.5\n", stdout)

	stdout, _, code = runJQ(t, jqRunOptions{}, "-n", `--1`)
	assert.Equal(t, uint8(exitGeneric), code)
	assert.Empty(t, stdout)
	stdout, _, code = runJQ(t, jqRunOptions{}, "-n", `-length`)
	assert.Equal(t, uint8(exitGeneric), code)
	assert.Empty(t, stdout)
}

func TestArgumentsAreBoundedAndFirstWins(t *testing.T) {
	stdout, stderr, code := runJQ(t, jqRunOptions{},
		"-nrc", "--arg", "x", "first", "--argjson", "x", "not-json", "$x")
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Equal(t, "first\n", stdout)

	stdout, stderr, code = runJQ(t, jqRunOptions{},
		"-nc", "--arg", "name", "Ada", "--argjson", "count", "2", `{name:$name,count:$count}`)
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Equal(t, `{"name":"Ada","count":2}`+"\n", stdout)

	largeArray := "[" + strings.Repeat("0,", 32_999) + "0]"
	_, stderr, code = runJQ(t, jqRunOptions{},
		"-n", "--argjson", "a", largeArray, "--argjson", "b", largeArray, `$a,$b`)
	assert.Equal(t, uint8(exitGeneric), code)
	assert.Contains(t, stderr, "aggregate size limit")

	_, stderr, code = runJQ(t, jqRunOptions{}, "-n", "--argjson", "unused", "not-json", `.`)
	assert.Equal(t, uint8(exitSystem), code)
	assert.Contains(t, stderr, "invalid JSON")

	_, stderr, code = runJQ(t, jqRunOptions{}, "-n", "--argjson", "unused", "not-json", `1 < 2 < 3`)
	assert.Equal(t, uint8(exitSystem), code)
	assert.Contains(t, stderr, "invalid JSON")
	assert.NotContains(t, stderr, "compile error")

	_, stderr, code = runJQ(t, jqRunOptions{}, "--argjson", "unused", "not-json")
	assert.Equal(t, uint8(exitSystem), code)
	assert.Contains(t, stderr, "invalid JSON")
	assert.NotContains(t, stderr, "missing filter")
}

func TestInputModesAndExitStatus(t *testing.T) {
	stdout, stderr, code := runJQ(t, jqRunOptions{stdin: "1\n2\n3\n"}, "-sc", `map(. * 2)`)
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Equal(t, "[2,4,6]\n", stdout)

	stdout, stderr, code = runJQ(t, jqRunOptions{stdin: "one\r\ntwo\n"}, "-Rr", `.`)
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Equal(t, "one\r\ntwo\n", stdout)

	stdout, stderr, code = runJQ(t, jqRunOptions{stdin: "one\ntwo\n"}, "-Rsr", `.`)
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Equal(t, "one\ntwo\n\n", stdout)

	stdout, stderr, code = runJQ(t, jqRunOptions{stdin: string([]byte{0xff, 0xfe, '\n'})}, "-Rr", `.`)
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Equal(t, "��\n", stdout)

	_, _, code = runJQ(t, jqRunOptions{}, "-ne", `false`)
	assert.Equal(t, uint8(exitGeneric), code)
	_, _, code = runJQ(t, jqRunOptions{}, "-ne", `empty`)
	assert.Equal(t, uint8(exitNoValue), code)
}

func TestJSONStreamFraming(t *testing.T) {
	for _, input := range []string{"truefalse", "nullnull", "1-2", "01"} {
		stdout, stderr, code := runJQ(t, jqRunOptions{stdin: input}, "-c", `.`)
		assert.Equal(t, uint8(exitRuntime), code, input)
		assert.Empty(t, stdout, input)
		assert.Contains(t, stderr, "parse error", input)
	}

	stdout, stderr, code := runJQ(t, jqRunOptions{stdin: `{}[]true{}"a""b"`}, "-c", `.`)
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Equal(t, "{}\n[]\ntrue\n{}\n\"a\"\n\"b\"\n", stdout)
}

func TestFilesUseSandboxOpenerAndFormOneStream(t *testing.T) {
	files := map[string]string{"one": `{"joined":`, "two": `true}`}
	opened := make([]string, 0)
	opener := func(_ context.Context, path string) (io.ReadWriteCloser, error) {
		opened = append(opened, path)
		text, ok := files[path]
		if !ok {
			return nil, os.ErrNotExist
		}
		return &readWriteCloser{Reader: strings.NewReader(text)}, nil
	}
	stdout, stderr, code := runJQ(t, jqRunOptions{opener: opener}, "-c", `.`, "one", "two")
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Equal(t, `{"joined":true}`+"\n", stdout)
	assert.Equal(t, []string{"one", "two"}, opened)
}

func TestParseErrorAdvancesOnlyThroughUnterminatedToken(t *testing.T) {
	for _, slurp := range []bool{false, true} {
		name := "normal"
		if slurp {
			name = "slurp"
		}
		t.Run(name, func(t *testing.T) {
			files := map[string]string{
				"literal_unterminated":   `{"x":[1,bad`,
				"literal_terminated":     "{\"x\":[1,bad\n",
				"string_unterminated":    `{"x":"\qbad`,
				"string_terminated":      `{"x":"\qbad"`,
				"surrogate_unterminated": `"\udc00`,
				"surrogate_terminated":   `"\udc00"`,
				"high_unterminated":      `"\ud800x`,
				"high_terminated":        `"\ud800x"`,
			}
			for _, kind := range []string{"literal", "string", "surrogate", "high"} {
				opened := make([]string, 0)
				opener := func(_ context.Context, path string) (io.ReadWriteCloser, error) {
					opened = append(opened, path)
					text, ok := files[path]
					if !ok {
						return nil, os.ErrNotExist
					}
					return &readWriteCloser{Reader: strings.NewReader(text)}, nil
				}
				unterminated := kind + "_unterminated"
				args := []string{"-c", ".", unterminated, "missing"}
				if slurp {
					args[0] = "-sc"
				}
				_, stderr, code := runJQ(t, jqRunOptions{opener: opener}, args...)
				assert.Equal(t, uint8(exitSystem), code)
				assert.Contains(t, stderr, "missing")
				assert.Contains(t, stderr, "parse error")
				assert.Equal(t, []string{unterminated, "missing"}, opened)

				opened = opened[:0]
				terminated := kind + "_terminated"
				args[len(args)-2] = terminated
				_, stderr, code = runJQ(t, jqRunOptions{opener: opener}, args...)
				assert.Equal(t, uint8(exitRuntime), code)
				assert.NotContains(t, stderr, "missing")
				assert.Contains(t, stderr, "parse error")
				assert.Equal(t, []string{terminated}, opened)
			}
		})
	}
}

func TestInvalidSurrogatePreservesEarlierValues(t *testing.T) {
	stdout, stderr, code := runJQ(t, jqRunOptions{stdin: "\"before\"\n\"\\udc00\""}, "-c", ".")
	assert.Equal(t, uint8(exitRuntime), code)
	assert.Equal(t, "\"before\"\n", stdout)
	assert.Contains(t, stderr, "surrogate")
}

func TestSurrogatePairSpansFileOperands(t *testing.T) {
	files := map[string]string{"high": `"\ud83d`, "low": `\ude00"`}
	opened := make([]string, 0, len(files))
	opener := func(_ context.Context, path string) (io.ReadWriteCloser, error) {
		opened = append(opened, path)
		return &readWriteCloser{Reader: strings.NewReader(files[path])}, nil
	}

	stdout, stderr, code := runJQ(t, jqRunOptions{opener: opener}, "-c", ".", "high", "low")
	assert.Equal(t, uint8(0), code)
	assert.Equal(t, "\"😀\"\n", stdout)
	assert.Empty(t, stderr)
	assert.Equal(t, []string{"high", "low"}, opened)
}

func TestEarlierTokenErrorStopsBeforeLaterOperands(t *testing.T) {
	for _, slurp := range []bool{false, true} {
		for _, input := range []string{
			`t"`, `1e"`, `-"`, "{ bad\n\"", "{ 0,\n\"",
			"{ bad\nmore", "{ 0,\nbad", "{ []:\nbad", "{ [1]\n,bad",
		} {
			opened := make([]string, 0, 2)
			opener := func(_ context.Context, path string) (io.ReadWriteCloser, error) {
				opened = append(opened, path)
				if path == "source" {
					return &readWriteCloser{Reader: strings.NewReader(input)}, nil
				}
				return nil, os.ErrNotExist
			}
			args := []string{"-c", ".", "source", "missing"}
			if slurp {
				args[0] = "-sc"
			}

			_, stderr, code := runJQ(t, jqRunOptions{opener: opener}, args...)
			assert.Equal(t, uint8(exitRuntime), code)
			assert.NotContains(t, stderr, "missing")
			assert.Equal(t, []string{"source"}, opened)
		}
	}
}

func TestSyntaxErrorStillFinishesCurrentToken(t *testing.T) {
	for _, slurp := range []bool{false, true} {
		for _, input := range []string{
			`[1 "`, `{"x" "`, `[1 bad`, `{"x" bad`, `{"x":1 bad`,
			"{ 0\n\"", "{ 0\nt0", "{ true\nbad", "{ []\nbad", "{ {}\n\"", "{ [1\nbad",
			"{ { null bad", "{ [ { null bad", "{ { [] bad", "[{ { null bad",
			"{ 0\n", "{ true\n", "{ []\n", "{ {}\n", "{{", "{ [1\n",
		} {
			opened := make([]string, 0, 2)
			opener := func(_ context.Context, path string) (io.ReadWriteCloser, error) {
				opened = append(opened, path)
				if path == "source" {
					return &readWriteCloser{Reader: strings.NewReader(input)}, nil
				}
				return nil, os.ErrNotExist
			}
			args := []string{"-c", ".", "source", "missing"}
			if slurp {
				args[0] = "-sc"
			}

			_, stderr, code := runJQ(t, jqRunOptions{opener: opener}, args...)
			assert.Equal(t, uint8(exitSystem), code)
			assert.Contains(t, stderr, "missing")
			assert.Equal(t, []string{"source", "missing"}, opened)
		}
	}
}

func TestInvalidSurrogateStopsAtItsLexicalBoundary(t *testing.T) {
	for _, slurp := range []bool{false, true} {
		opened := make([]string, 0, 2)
		opener := func(_ context.Context, path string) (io.ReadWriteCloser, error) {
			opened = append(opened, path)
			if path == "source" {
				return &readWriteCloser{Reader: strings.NewReader(`{"x":"\ud800","y":"bad`)}, nil
			}
			return nil, os.ErrNotExist
		}
		args := []string{"-c", ".", "source", "missing"}
		if slurp {
			args[0] = "-sc"
		}

		_, stderr, code := runJQ(t, jqRunOptions{opener: opener}, args...)
		assert.Equal(t, uint8(exitRuntime), code)
		assert.NotContains(t, stderr, "missing")
		assert.Contains(t, stderr, "surrogate")
		assert.Equal(t, []string{"source"}, opened)
	}
}

func TestRawNULStopsBeforeLaterOperands(t *testing.T) {
	for _, slurp := range []bool{false, true} {
		opened := make([]string, 0, 2)
		opener := func(_ context.Context, path string) (io.ReadWriteCloser, error) {
			opened = append(opened, path)
			if path == "source" {
				return &readWriteCloser{Reader: strings.NewReader("{\"x\":\"a\x00b\"}")}, nil
			}
			return nil, os.ErrNotExist
		}
		args := []string{"-c", ".", "source", "missing"}
		if slurp {
			args[0] = "-sc"
		}

		_, stderr, code := runJQ(t, jqRunOptions{opener: opener}, args...)
		assert.Equal(t, uint8(exitRuntime), code)
		assert.NotContains(t, stderr, "missing")
		assert.Equal(t, []string{"source"}, opened)
	}
}

func TestNullInputDoesNotOpenOperands(t *testing.T) {
	opened := false
	opener := func(context.Context, string) (io.ReadWriteCloser, error) {
		opened = true
		return nil, errors.New("must not open")
	}
	stdout, stderr, code := runJQ(t, jqRunOptions{opener: opener}, "-nc", `.`, "ignored")
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Equal(t, "null\n", stdout)
	assert.False(t, opened)
}

func TestCompileAndRuntimeStatuses(t *testing.T) {
	_, stderr, code := runJQ(t, jqRunOptions{}, `$missing`)
	assert.Equal(t, uint8(exitCompile), code)
	assert.Contains(t, stderr, "not defined")

	_, stderr, code = runJQ(t, jqRunOptions{}, "-n", `1 < 2 < 3`)
	assert.Equal(t, uint8(exitCompile), code)
	assert.Contains(t, stderr, "cannot be chained")

	_, stderr, code = runJQ(t, jqRunOptions{stdin: `{"bad":"\ud800"}`}, `.`)
	assert.Equal(t, uint8(exitRuntime), code)
	assert.Contains(t, stderr, "surrogate")

	_, stderr, code = runJQ(t, jqRunOptions{stdin: `{`}, `.`)
	assert.Equal(t, uint8(exitRuntime), code)
	assert.Contains(t, stderr, "parse error")
}

func TestResourceCaps(t *testing.T) {
	_, stderr, code := runJQ(t, jqRunOptions{}, "-n", strings.Repeat("(", MaxNestingDepth+1)+"0"+strings.Repeat(")", MaxNestingDepth+1))
	assert.Equal(t, uint8(exitCompile), code)
	assert.Contains(t, stderr, "nesting")

	_, stderr, code = runJQ(t, jqRunOptions{}, "-nr", `"a" * 1048576`)
	assert.Equal(t, uint8(exitGeneric), code)
	assert.Contains(t, stderr, "output")

	budget := &inputBudget{used: MaxTotalInputBytes - 1}
	reader := &budgetReader{ctx: context.Background(), reader: strings.NewReader("xx"), budget: budget}
	buf := make([]byte, 2)
	_, err := reader.Read(buf)
	assert.ErrorIs(t, err, errInputLimit)
}

func TestLargeObjectEvaluationUsesBoundedMemory(t *testing.T) {
	const memberCount = MaxFilterNodes - 8
	var filter strings.Builder
	filter.WriteByte('{')
	for i := range memberCount {
		if i > 0 {
			filter.WriteByte(',')
		}
		filter.WriteByte('k')
		filter.WriteString(strconv.Itoa(i))
		filter.WriteString(":0")
	}
	filter.WriteByte('}')

	root, err := parseFilter(filter.String())
	require.NoError(t, err)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	results, err := newEvaluator(context.Background(), nil).evaluate(null(), root)
	runtime.ReadMemStats(&after)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(64<<20))
}

func TestObjectDuplicateKeyAccountsOnlyFinalValue(t *testing.T) {
	large, err := stringValue(strings.Repeat("x", MaxValueBytes-2))
	require.NoError(t, err)
	for _, filter := range []string{`{a:.,a:0}`, `{("a"):.,a:0}`, `{a:.,("a"):0}`} {
		root, err := parseFilter(filter)
		require.NoError(t, err)

		results, err := newEvaluator(context.Background(), nil).evaluate(large, root)
		require.NoError(t, err)
		require.Len(t, results, 1)
		got, ok := results[0].obj.get("a")
		require.True(t, ok)
		assert.Equal(t, int64Value(0), got)
	}

	stdout, stderr, code := runJQ(t, jqRunOptions{}, "-nc", `{a:(1,2),a:0}`)
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Equal(t, "{\"a\":0}\n{\"a\":0}\n", stdout)
}

func TestEvaluationRetentionIsBounded(t *testing.T) {
	large, err := stringValue(strings.Repeat("x", MaxValueBytes-2))
	require.NoError(t, err)
	retained := &evaluationRetention{}
	require.NoError(t, retained.retain(large, large))
	assert.ErrorIs(t, retained.retain(null()), errRetentionLimit)
	retained.release(large, large)
	assert.Zero(t, retained.nodes)
	assert.Zero(t, retained.bytes)
}

func TestOverwrittenObjectValuesAreReleasedBeforeDescent(t *testing.T) {
	const memberCount = 200
	var filter strings.Builder
	filter.WriteByte('{')
	for i := range memberCount {
		if i > 0 {
			filter.WriteByte(',')
		}
		filter.WriteString(`a:("x"*500000)`)
	}
	filter.WriteString(`,a:0}`)
	root, err := parseFilter(filter.String())
	require.NoError(t, err)

	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	ctx := &heapSamplingContext{Context: context.Background(), baseline: baseline.HeapAlloc}
	eval := newEvaluator(ctx, nil)
	results, err := eval.evaluate(null(), root)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Less(t, ctx.maxDelta, uint64(32<<20))
	assert.Zero(t, eval.retention.nodes)
	assert.Zero(t, eval.retention.bytes)
}

func TestNestedObjectsShareOneRetentionBudget(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
	}{
		{name: "values", prefix: `{a:("x"*500000,0),b:`},
		{name: "keys", prefix: `{(("x"*500000)):0,b:`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const levels = 30
			var filter strings.Builder
			for range levels {
				filter.WriteString(tt.prefix)
			}
			filter.WriteByte('0')
			for range levels {
				filter.WriteByte('}')
			}
			root, err := parseFilter(filter.String())
			require.NoError(t, err)

			runtime.GC()
			var baseline runtime.MemStats
			runtime.ReadMemStats(&baseline)
			ctx := &heapSamplingContext{Context: context.Background(), baseline: baseline.HeapAlloc}
			eval := newEvaluator(ctx, nil)
			_, err = eval.evaluate(null(), root)
			assert.ErrorIs(t, err, errRetentionLimit)
			assert.Less(t, ctx.maxDelta, uint64(32<<20))
			assert.Zero(t, eval.retention.nodes)
			assert.Zero(t, eval.retention.bytes)
		})
	}
}

func TestRecursiveEvaluatorSharesOneRetentionBudget(t *testing.T) {
	const levels = 40
	tests := []struct {
		name  string
		build func(*strings.Builder)
	}{
		{
			name: "pipe",
			build: func(filter *strings.Builder) {
				for range levels {
					filter.WriteString(`("x"*500000)|`)
				}
				filter.WriteByte('0')
			},
		},
		{
			name: "index",
			build: func(filter *strings.Builder) {
				filter.WriteByte('.')
				for range levels {
					filter.WriteString(`[("x"*500000)]`)
				}
			},
		},
		{
			name: "boolean",
			build: func(filter *strings.Builder) {
				for range levels {
					filter.WriteString(`("x"*500000) and (`)
				}
				filter.WriteString("true")
				for range levels {
					filter.WriteByte(')')
				}
			},
		},
		{
			name: "binary",
			build: func(filter *strings.Builder) {
				for range levels {
					filter.WriteByte('(')
				}
				filter.WriteByte('0')
				for range levels {
					filter.WriteString(`==("x"*500000))`)
				}
			},
		},
		{
			name: "comma",
			build: func(filter *strings.Builder) {
				for range levels {
					filter.WriteString(`("x"*500000),(`)
				}
				filter.WriteByte('0')
				for range levels {
					filter.WriteByte(')')
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var filter strings.Builder
			tt.build(&filter)
			root, err := parseFilter(filter.String())
			require.NoError(t, err)

			runtime.GC()
			var baseline runtime.MemStats
			runtime.ReadMemStats(&baseline)
			ctx := &heapSamplingContext{Context: context.Background(), baseline: baseline.HeapAlloc}
			eval := newEvaluator(ctx, nil)
			_, err = eval.evaluate(null(), root)
			assert.ErrorIs(t, err, errRetentionLimit)
			assert.Less(t, ctx.maxDelta, uint64(32<<20))
			assert.Zero(t, eval.retention.nodes)
			assert.Zero(t, eval.retention.bytes)
		})
	}
}

func TestNestedMapPartialResultsShareRetentionBudget(t *testing.T) {
	const levels = 20
	var filter strings.Builder
	for range levels {
		filter.WriteString(`map((select(type=="number")|"x"*500000)//(select(type=="array")|`)
	}
	filter.WriteString(`map(.)`)
	for range levels {
		filter.WriteString(`))`)
	}
	root, err := parseFilter(filter.String())
	require.NoError(t, err)

	input, err := arrayValue(nil)
	require.NoError(t, err)
	for range levels {
		input, err = arrayValue([]value{int64Value(0), input})
		require.NoError(t, err)
	}

	runtime.GC()
	var baseline runtime.MemStats
	runtime.ReadMemStats(&baseline)
	ctx := &heapSamplingContext{Context: context.Background(), baseline: baseline.HeapAlloc}
	eval := newEvaluator(ctx, nil)
	_, err = eval.evaluate(input, root)
	assert.ErrorIs(t, err, errRetentionLimit)
	assert.Less(t, ctx.maxDelta, uint64(32<<20))
	assert.Zero(t, eval.retention.nodes)
	assert.Zero(t, eval.retention.bytes)
}

func TestDiagnosticsEscapeFileAndNestedPathErrors(t *testing.T) {
	name := "bad\n\x1bfile"
	opener := func(context.Context, string) (io.ReadWriteCloser, error) {
		return nil, &os.PathError{Op: "open", Path: "nested\nforged", Err: errors.New("denied\nmore")}
	}
	_, stderr, code := runJQ(t, jqRunOptions{opener: opener}, `.`, name)
	assert.Equal(t, uint8(exitSystem), code)
	assert.Equal(t, `jq: bad\n\x1bfile: denied\nmore`+"\n", stderr)
}

func TestCanceledInvocationDoesNotCloseBorrowedStdin(t *testing.T) {
	stdin, err := os.CreateTemp(t.TempDir(), "jq-stdin")
	require.NoError(t, err)
	defer stdin.Close()
	_, err = stdin.WriteString("null\n")
	require.NoError(t, err)
	_, err = stdin.Seek(0, io.SeekStart)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stderr bytes.Buffer
	callCtx := &builtins.CallContext{Stdout: io.Discard, Stderr: &stderr, Stdin: stdin}
	fs := pflag.NewFlagSet("jq", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	handler := Cmd.MakeFlags(fs)
	require.NoError(t, fs.Parse([]string{"."}))
	result := handler(ctx, callCtx, fs.Args())
	assert.Equal(t, uint8(exitGeneric), result.Code)
	_, err = stdin.Stat()
	assert.NoError(t, err)
}

func TestHelp(t *testing.T) {
	stdout, stderr, code := runJQ(t, jqRunOptions{}, "--help")
	assert.Equal(t, uint8(0), code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "Usage: jq")
	assert.Contains(t, stdout, "--arg")
	assert.Contains(t, stdout, "--argjson")
}

func TestParserAssociativity(t *testing.T) {
	root, err := parseFilter(`1 | 2 | 3`)
	require.NoError(t, err)
	assert.Equal(t, nodePipe, root.kind)
	assert.Equal(t, nodePipe, root.right.kind)

	root, err = parseFilter(`false // null // 3`)
	require.NoError(t, err)
	assert.Equal(t, nodeAlternative, root.kind)
	assert.Equal(t, nodeAlternative, root.right.kind)
}
