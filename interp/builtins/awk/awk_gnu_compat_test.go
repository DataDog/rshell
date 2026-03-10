// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// GNU compatibility tests for the awk builtin.
//
// Expected outputs were captured from GNU awk (gawk) 5.3.1 and are embedded
// as string literals so the tests run without any GNU tooling present on CI.
// To reproduce a reference output, run:
//
//	gawk [program] [file]

package awk_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGNUCompatPrintAll — print all lines.
//
// GNU command: gawk '{print}' file.txt   (file.txt = "alpha\nbeta\ngamma\n")
// Expected:    "alpha\nbeta\ngamma\n"
func TestGNUCompatPrintAll(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "alpha\nbeta\ngamma\n")
	stdout, _, code := cmdRun(t, `awk '{print}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "alpha\nbeta\ngamma\n", stdout)
}

// TestGNUCompatFieldPrint — print specific fields.
//
// GNU command: gawk '{print $1, $3}' file.txt   (file.txt = "one two three\nfour five six\n")
// Expected:    "one three\nfour six\n"
func TestGNUCompatFieldPrint(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "one two three\nfour five six\n")
	stdout, _, code := cmdRun(t, `awk '{print $1, $3}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "one three\nfour six\n", stdout)
}

// TestGNUCompatFieldSeparator — -F flag.
//
// GNU command: gawk -F: '{print $2}' file.csv   (file.csv = "a:b:c\nd:e:f\n")
// Expected:    "b\ne\n"
func TestGNUCompatFieldSeparator(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.csv", "a:b:c\nd:e:f\n")
	stdout, _, code := cmdRun(t, `awk -F: '{print $2}' file.csv`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "b\ne\n", stdout)
}

// TestGNUCompatNR — NR variable.
//
// GNU command: gawk '{print NR, $0}' file.txt   (file.txt = "a\nb\nc\n")
// Expected:    "1 a\n2 b\n3 c\n"
func TestGNUCompatNR(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a\nb\nc\n")
	stdout, _, code := cmdRun(t, `awk '{print NR, $0}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1 a\n2 b\n3 c\n", stdout)
}

// TestGNUCompatBeginEnd — BEGIN and END blocks.
//
// GNU command: gawk 'BEGIN {print "S"} {c++} END {print c}' file.txt   (3-line file)
// Expected:    "S\n3\n"
func TestGNUCompatBeginEnd(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a\nb\nc\n")
	stdout, _, code := cmdRun(t, `awk 'BEGIN {print "S"} {c++} END {print c}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "S\n3\n", stdout)
}

// TestGNUCompatPatternMatch — regex pattern match.
//
// GNU command: gawk '/hello/' file.txt   (file.txt = "hello world\nfoo bar\nhello again\n")
// Expected:    "hello world\nhello again\n"
func TestGNUCompatPatternMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello world\nfoo bar\nhello again\n")
	stdout, _, code := cmdRun(t, `awk '/hello/' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\nhello again\n", stdout)
}

// TestGNUCompatEmptyFile — no output on empty file.
//
// GNU command: gawk '{print}' empty.txt
// Expected:    ""
func TestGNUCompatEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "empty.txt", "")
	stdout, _, code := cmdRun(t, `awk '{print}' empty.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

// TestGNUCompatPrintf — printf formatting.
//
// GNU command: gawk '{printf "%03d %s\n", NR, $0}' file.txt   (file.txt = "a\nb\n")
// Expected:    "001 a\n002 b\n"
func TestGNUCompatPrintf(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a\nb\n")
	stdout, _, code := cmdRun(t, `awk '{printf "%03d %s\n", NR, $0}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "001 a\n002 b\n", stdout)
}

// TestGNUCompatVFlag — -v variable assignment.
//
// GNU command: gawk -v x=42 'BEGIN {print x}'
// Expected:    "42\n"
func TestGNUCompatVFlag(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk -v x=42 'BEGIN {print x}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "42\n", stdout)
}

// TestGNUCompatComparisonPattern — comparison selects lines.
//
// GNU command: gawk '$1 > 3' file.txt   (file.txt = "1\n2\n3\n4\n5\n")
// Expected:    "4\n5\n"
func TestGNUCompatComparisonPattern(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "1\n2\n3\n4\n5\n")
	stdout, _, code := cmdRun(t, `awk '$1 > 3' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "4\n5\n", stdout)
}

// TestGNUCompatOFS — output field separator.
//
// GNU command: gawk -v OFS="," '{$1=$1; print}' file.txt   (file.txt = "a b c\n")
// Expected:    "a,b,c\n"
func TestGNUCompatOFS(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a b c\n")
	stdout, _, code := cmdRun(t, `awk -v OFS="," '{$1=$1; print}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a,b,c\n", stdout)
}

// TestGNUCompatArrayCounting — word counting with arrays.
//
// GNU command: gawk '{count[$1]++} END {for (k in count) print k, count[k]}' file.txt
// (file.txt = "a\nb\na\nc\nb\na\n")
// Expected (sorted): "a 3\nb 2\nc 1\n"
func TestGNUCompatArrayCounting(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a\nb\na\nc\nb\na\n")
	stdout, _, code := cmdRun(t, `awk '{count[$1]++} END {for (k in count) print k, count[k]}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a 3\nb 2\nc 1\n", stdout)
}

// TestGNUCompatFNR — per-file record number.
//
// GNU command: gawk '{print FNR, $0}' a.txt b.txt
// Expected:    "1 x\n2 y\n1 z\n"
func TestGNUCompatFNR(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "x\ny\n")
	writeFile(t, dir, "b.txt", "z\n")
	stdout, _, code := cmdRun(t, `awk '{print FNR, $0}' a.txt b.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1 x\n2 y\n1 z\n", stdout)
}

// TestGNUCompatNoProgram — no program text.
//
// GNU command: gawk
// Expected:    exit 1, stderr message
func TestGNUCompatNoProgram(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `awk`, dir)
	assert.Equal(t, 1, code)
	assert.NotEmpty(t, stderr)
}

// TestGNUCompatRejectedFlag — unknown flag.
//
// GNU command: gawk --garbage
// Expected:    exit 1, stderr message
func TestGNUCompatRejectedFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `awk --garbage '{print}'`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "awk:")
}
