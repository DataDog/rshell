// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// jq_gnu_compat_test.go asserts byte-for-byte output equivalence between
// our jq builtin and the upstream jq CLI for the cases most sensitive to
// formatting (pretty-print, sort-keys, ascii escapes, raw output, slurp,
// raw-input). The reference output strings were captured once from the
// real jq 1.7.1 binary and are embedded as literals so the test runs
// without any host jq present on CI.
package jq_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Reference outputs were captured by piping the documented input through
// jq 1.7.1 and `od -c`-inspecting the result. Each test repeats the exact
// input shape and asserts the captured bytes.

// TestGNUCompatJqIdentityPretty — default pretty-print of a small object.
//
// jq invocation:    printf '{"a":1,"b":2}' | jq .
// Expected output:  "{\n  \"a\": 1,\n  \"b\": 2\n}\n"
func TestGNUCompatJqIdentityPretty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"a":1,"b":2}`)
	stdout, _, code := jqRun(t, "jq . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "{\n  \"a\": 1,\n  \"b\": 2\n}\n", stdout)
}

// TestGNUCompatJqCompactSimple — -c emits one line per output.
//
// jq invocation:    printf '{"a":1,"b":2}' | jq -c .
// Expected output:  "{\"a\":1,\"b\":2}\n"
func TestGNUCompatJqCompactSimple(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"a":1,"b":2}`)
	stdout, _, code := jqRun(t, "jq -c . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "{\"a\":1,\"b\":2}\n", stdout)
}

// TestGNUCompatJqSortKeysCompact — -S sorts object keys lexicographically.
//
// jq invocation:    printf '{"banana":2,"apple":1}' | jq -S -c .
// Expected output:  "{\"apple\":1,\"banana\":2}\n"
func TestGNUCompatJqSortKeysCompact(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"banana":2,"apple":1}`)
	stdout, _, code := jqRun(t, "jq -S -c . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "{\"apple\":1,\"banana\":2}\n", stdout)
}

// TestGNUCompatJqSortKeysPretty — -S in pretty mode sorts and indents.
//
// jq invocation:    printf '{"banana":2,"apple":1}' | jq -S .
// Expected output:  "{\n  \"apple\": 1,\n  \"banana\": 2\n}\n"
func TestGNUCompatJqSortKeysPretty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"banana":2,"apple":1}`)
	stdout, _, code := jqRun(t, "jq -S . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "{\n  \"apple\": 1,\n  \"banana\": 2\n}\n", stdout)
}

// TestGNUCompatJqRawStringEscapes — -r decodes JSON-string escape sequences.
//
// jq invocation:    printf '"hi\\nthere"' | jq -r .
// Expected output:  "hi\nthere\n"
func TestGNUCompatJqRawStringEscapes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `"hi\nthere"`)
	stdout, _, code := jqRun(t, "jq -r . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hi\nthere\n", stdout)
}

// TestGNUCompatJqSlurp — -s collects multiple values into one array.
//
// jq invocation:    printf '1 2 3' | jq -s -c .
// Expected output:  "[1,2,3]\n"
func TestGNUCompatJqSlurp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", "1 2 3")
	stdout, _, code := jqRun(t, "jq -s -c . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "[1,2,3]\n", stdout)
}

// TestGNUCompatJqRawInput — -R wraps each line as a JSON string.
//
// jq invocation:    printf 'foo\nbar\n' | jq -R .
// Expected output:  "\"foo\"\n\"bar\"\n"
func TestGNUCompatJqRawInput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.txt", "foo\nbar\n")
	stdout, _, code := jqRun(t, "jq -R . x.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\"foo\"\n\"bar\"\n", stdout)
}

// TestGNUCompatJqAsciiOutputBMP — -a escapes a non-ASCII BMP character.
//
// jq invocation:    printf '{"s":"héllo"}' | jq -a -c .s
// Expected output:  "\"h\\u00e9llo\"\n"
func TestGNUCompatJqAsciiOutputBMP(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"s":"héllo"}`)
	stdout, _, code := jqRun(t, "jq -a -c .s x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\"h\\u00e9llo\"\n", stdout)
}

// TestGNUCompatJqAsciiOutputSurrogate — -a emits surrogate pairs for
// supplementary-plane characters (U+1F600 = 😀).
//
// jq invocation:    printf '{"s":"a😀b"}' | jq -a -c .s
// Expected output:  "\"a\\ud83d\\ude00b\"\n"
func TestGNUCompatJqAsciiOutputSurrogate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"s":"a😀b"}`)
	stdout, _, code := jqRun(t, "jq -a -c .s x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\"a\\ud83d\\ude00b\"\n", stdout)
}

// TestGNUCompatJqEmptyContainersInline — empty [] and {} stay on one line
// even in pretty mode.
//
// jq invocation:    printf '{"x":[]}' | jq .
// Expected output:  "{\n  \"x\": []\n}\n"
func TestGNUCompatJqEmptyContainersInline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"x":[]}`)
	stdout, _, code := jqRun(t, "jq . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "{\n  \"x\": []\n}\n", stdout)
}

// TestGNUCompatJqNestedPretty — multi-level pretty-print indentation.
//
// jq invocation:    printf '{"a":{"b":[1,2]}}' | jq .
// Expected output:
//
//	{
//	  "a": {
//	    "b": [
//	      1,
//	      2
//	    ]
//	  }
//	}
func TestGNUCompatJqNestedPretty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"a":{"b":[1,2]}}`)
	stdout, _, code := jqRun(t, "jq . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "{\n  \"a\": {\n    \"b\": [\n      1,\n      2\n    ]\n  }\n}\n", stdout)
}

// TestGNUCompatJqNullInput — -n prints null when the filter is identity.
//
// jq invocation:    jq -n .
// Expected output:  "null\n"
func TestGNUCompatJqNullInput(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := jqRun(t, "jq -n .", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "null\n", stdout)
}

// TestGNUCompatJqNullInputConstruct — -n with object construction.
//
// jq invocation:    jq -n -c '{x: 1, y: 2}'
// Expected output:  "{\"x\":1,\"y\":2}\n"
func TestGNUCompatJqNullInputConstruct(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := jqRun(t, "jq -n -c '{x: 1, y: 2}'", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "{\"x\":1,\"y\":2}\n", stdout)
}

// TestGNUCompatJqMultiDoc — multiple JSON values are processed individually.
//
// jq invocation:    printf '1\n2\n3\n' | jq -c .
// Expected output:  "1\n2\n3\n"
func TestGNUCompatJqMultiDoc(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", "1\n2\n3\n")
	stdout, _, code := jqRun(t, "jq -c . x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n2\n3\n", stdout)
}

// TestGNUCompatJqExitStatusNullExit1 — -e returns 1 on null-only output.
//
// jq invocation:    printf '{}' | jq -e .missing
// Expected output:  "null\n", exit code 1
func TestGNUCompatJqExitStatusNullExit1(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", "{}")
	stdout, _, code := jqRun(t, "jq -e .missing x.json", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "null\n", stdout)
}

// TestGNUCompatJqRawNumber — -r leaves a number unchanged (no quotes, no decode).
//
// jq invocation:    printf '{"a":42}' | jq -r .a
// Expected output:  "42\n"
func TestGNUCompatJqRawNumber(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"a":42}`)
	stdout, _, code := jqRun(t, "jq -r .a x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "42\n", stdout)
}

// TestGNUCompatJqRawBool — -r leaves a boolean unchanged.
//
// jq invocation:    printf '{"b":true}' | jq -r .b
// Expected output:  "true\n"
func TestGNUCompatJqRawBool(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"b":true}`)
	stdout, _, code := jqRun(t, "jq -r .b x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "true\n", stdout)
}

// TestGNUCompatJqRawNull — -r leaves null unchanged.
//
// jq invocation:    printf '{"c":null}' | jq -r .c
// Expected output:  "null\n"
func TestGNUCompatJqRawNull(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.json", `{"c":null}`)
	stdout, _, code := jqRun(t, "jq -r .c x.json", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "null\n", stdout)
}
