// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package jq_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

// runScript runs a shell script and returns stdout, stderr, and exit code.
func runScript(t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScript(t, script, dir, opts...)
}

// runScriptCtx runs a shell script under a custom context.
func runScriptCtx(ctx context.Context, t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, opts...)
}

// jqRun runs jq with AllowedPaths set to dir.
func jqRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return runScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

// writeFile creates a file in dir with the given content.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
	return name
}

// --- Default (pretty-print) ---

func TestJqIdentityPretty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"a":1,"b":2}`)
	stdout, stderr, code := jqRun(t, "jq . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "{\n  \"a\": 1,\n  \"b\": 2\n}\n", stdout)
}

func TestJqIdentityNested(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"a":{"b":[1,2]}}`)
	stdout, _, code := jqRun(t, "jq . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "{\n  \"a\": {\n    \"b\": [\n      1,\n      2\n    ]\n  }\n}\n", stdout)
}

func TestJqEmptyArrayAndObjectInline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"a":[],"b":{}}`)
	stdout, _, code := jqRun(t, "jq . x.json", dir)
	assert.Equal(t, 0, code)
	// Empty containers stay on one line, matching jq.
	assert.Equal(t, "{\n  \"a\": [],\n  \"b\": {}\n}\n", stdout)
}

// --- -c / --compact-output ---

func TestJqCompact(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"a":1,"b":2}`)
	stdout, _, code := jqRun(t, "jq -c . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "{\"a\":1,\"b\":2}\n", stdout)
}

func TestJqCompactLongForm(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"a":1}`)
	stdout, _, code := jqRun(t, "jq --compact-output . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "{\"a\":1}\n", stdout)
}

// --- -r / --raw-output ---

func TestJqRawString(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"name":"alice"}`)
	stdout, _, code := jqRun(t, "jq -r .name x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "alice\n", stdout)
}

func TestJqRawNonString(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"a":42,"b":true,"c":null}`)
	stdout, _, code := jqRun(t, "jq -r .a x.json && jq -r .b x.json && jq -r .c x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "42\ntrue\nnull\n", stdout)
}

func TestJqRawDecodesEscapes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"s":"a\tb\nc"}`)
	stdout, _, code := jqRun(t, "jq -r .s x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\tb\nc\n", stdout)
}

// --- -j / --join-output ---

func TestJqJoinOutput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"a":"hello","b":"world"}`)
	stdout, _, code := jqRun(t, "jq -j .a,.b x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "helloworld", stdout)
}

func TestJqJoinImpliesRaw(t *testing.T) {
	// -j must imply -r: a string output is decoded, no quotes.
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `"hello"`)
	stdout, _, code := jqRun(t, "jq -j . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello", stdout)
}

// --- -n / --null-input ---

func TestJqNullInput(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := jqRun(t, "jq -n .", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "null\n", stdout)
}

func TestJqNullInputLiteral(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := jqRun(t, `jq -n -c '{x: 1, y: 2}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "{\"x\":1,\"y\":2}\n", stdout)
}

func TestJqNullInputIgnoresFiles(t *testing.T) {
	// -n must not read the file argument, even if it is unreadable.
	dir := t.TempDir()
	stdout, _, code := jqRun(t, "jq -n . nonexistent.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "null\n", stdout)
}

// --- -s / --slurp ---

func TestJqSlurp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "stream.json", "1 2 3")
	stdout, _, code := jqRun(t, "jq -c -s . stream.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "[1,2,3]\n", stdout)
}

func TestJqSlurpMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.json", "1")
	writeFile(t, dir, "b.json", "2 3")
	stdout, _, code := jqRun(t, "jq -c -s . a.json b.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "[1,2,3]\n", stdout)
}

func TestJqSlurpEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "empty.json", "")
	stdout, _, code := jqRun(t, "jq -c -s . empty.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "[]\n", stdout)
}

// --- -R / --raw-input ---

func TestJqRawInput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "lines.txt", "foo\nbar\nbaz\n")
	stdout, _, code := jqRun(t, "jq -R . lines.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\"foo\"\n\"bar\"\n\"baz\"\n", stdout)
}

func TestJqRawInputEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "empty.txt", "")
	stdout, _, code := jqRun(t, "jq -R . empty.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

func TestJqSlurpRawInput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "text.txt", "hello world\n")
	stdout, _, code := jqRun(t, "jq -s -R . text.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\"hello world\\n\"\n", stdout)
}

// --- -S / --sort-keys ---

func TestJqSortKeys(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"banana":2,"apple":1,"cherry":3}`)
	stdout, _, code := jqRun(t, "jq -S -c . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "{\"apple\":1,\"banana\":2,\"cherry\":3}\n", stdout)
}

func TestJqSortKeysNested(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"z":{"b":2,"a":1},"y":{"d":4,"c":3}}`)
	stdout, _, code := jqRun(t, "jq -S -c . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "{\"y\":{\"c\":3,\"d\":4},\"z\":{\"a\":1,\"b\":2}}\n", stdout)
}

func TestJqSortKeysPretty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"b":2,"a":1}`)
	stdout, _, code := jqRun(t, "jq -S . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "{\n  \"a\": 1,\n  \"b\": 2\n}\n", stdout)
}

// --- -a / --ascii-output ---

func TestJqAsciiOutputBMP(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"s":"héllo"}`)
	stdout, _, code := jqRun(t, "jq -a -c .s x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\"h\\u00e9llo\"\n", stdout)
}

func TestJqAsciiOutputSurrogatePair(t *testing.T) {
	// 😀 (U+1F600) requires a surrogate pair under -a.
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"s":"a😀b"}`)
	stdout, _, code := jqRun(t, "jq -a -c .s x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\"a\\ud83d\\ude00b\"\n", stdout)
}

// --- -e / --exit-status ---

func TestJqExitStatusTruthy(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"a":1}`)
	stdout, _, code := jqRun(t, "jq -e .a x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout)
}

func TestJqExitStatusNullOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"a":1}`)
	stdout, _, code := jqRun(t, "jq -e .missing x.json", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "null\n", stdout)
}

func TestJqExitStatusFalseOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"flag":false}`)
	stdout, _, code := jqRun(t, "jq -e .flag x.json", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "false\n", stdout)
}

func TestJqExitStatusNoOutput(t *testing.T) {
	// select() that filters everything out leaves no output → exit 1.
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"k":1}`)
	_, _, code := jqRun(t, `jq -e 'select(.k == 999)' x.json`, dir)
	assert.Equal(t, 1, code)
}

func TestJqExitStatusMixedTruthy(t *testing.T) {
	// At least one truthy output → exit 0 even with leading null.
	dir := t.TempDir()
	writeFile(t, dir, "x.json", "null\n42\n")
	_, _, code := jqRun(t, "jq -c -e . x.json", dir)
	assert.Equal(t, 0, code)
}

// --- stdin ---

func TestJqReadsFromStdin(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := jqRun(t, `printf '{"k":"v"}' | jq -c .k`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\"v\"\n", stdout)
}

func TestJqDashIsStdin(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := jqRun(t, `printf '{"k":"v"}' | jq -c .k -`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\"v\"\n", stdout)
}

// --- multi-file ---

func TestJqMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.json", "1\n")
	writeFile(t, dir, "b.json", "2\n")
	stdout, _, code := jqRun(t, "jq -c . a.json b.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n2\n", stdout)
}

// --- error cases ---

func TestJqMissingFilter(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := jqRun(t, "jq", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "no filter")
}

func TestJqCompileError(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := jqRun(t, "jq -n '...not-a-filter...'", dir)
	assert.Equal(t, 3, code)
	assert.Contains(t, stderr, "compile error")
}

func TestJqMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := jqRun(t, "jq . missing.json", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "missing.json")
}

func TestJqInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bad.json", `{"unterminated":`)
	_, stderr, code := jqRun(t, "jq . bad.json", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "error reading")
}

func TestJqUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := jqRun(t, "jq --no-such-flag .", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "unknown flag")
}

func TestJqRejectsColorFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := jqRun(t, "jq -C -n .", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "unknown shorthand flag")
}

func TestJqRejectsFromFile(t *testing.T) {
	// -f / --from-file is documented as not implemented in v1.
	dir := t.TempDir()
	_, stderr, code := jqRun(t, "jq -f filter.jq .", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "unknown shorthand flag")
}

// --- --help ---

func TestJqHelpFlagPrintsUsage(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := jqRun(t, "jq --help", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage: jq")
	assert.Contains(t, stdout, "--null-input")
}

// --- access control ---

func TestJqAccessDeniedOutsideAllowedPaths(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	writeFile(t, other, "x.json", "1")
	_, stderr, code := runScript(t, "jq . "+filepath.Join(other, "x.json"), dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 1, code)
	assert.NotEqual(t, "", stderr)
}

// --- end-of-flags --- ---

func TestJqEndOfFlagsTerminator(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"a":1}`)
	stdout, _, code := jqRun(t, "jq -- . x.json", dir)
	assert.Equal(t, 0, code)
	assert.True(t, strings.Contains(stdout, `"a"`))
}

// --- context cancellation ---

func TestJqRespectsContextCancel(t *testing.T) {
	// A pre-cancelled context must short-circuit promptly rather than
	// process the entire 1000-document stream.
	dir := t.TempDir()
	var sb strings.Builder
	for i := 0; i < 1000; i++ {
		sb.WriteString(`{"i":0,"v":"abc"}` + "\n")
	}
	writeFile(t, dir, "stream.json", sb.String())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel
	start := time.Now()
	_, _, _ = runScriptCtx(ctx, t, "jq -c . stream.json", dir, interp.AllowedPaths([]string{dir}))
	elapsed := time.Since(start)
	// Pre-cancelled context: should return effectively instantly. We
	// give a generous slack for the runner-bootstrap cost.
	assert.Less(t, elapsed, 2*time.Second, "cancelled context took %s — expected <2s", elapsed)
}
