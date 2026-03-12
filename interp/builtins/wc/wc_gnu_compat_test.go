// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package wc_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGNUCompatDefaultEmpty — no flags on empty input.
//
// GNU command: printf ” | gwc
// Expected: "      0       0       0\n"
func TestGNUCompatDefaultEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "empty.txt", "")
	stdout, _, code := cmdRun(t, "wc empty.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "0 0 0 empty.txt\n", stdout)
}

// TestGNUCompatDefaultBasic — default counts on "a b\nc\n".
//
// GNU command: printf 'a b\nc\n' | gwc
// Expected: "      2       3       6\n"
func TestGNUCompatDefaultBasic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a b\nc\n")
	stdout, _, code := cmdRun(t, "wc file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2 3 6 file.txt\n", stdout)
}

// TestGNUCompatLinesCount — -l on input with 2 newlines.
//
// GNU command: printf 'x\ny\n' | gwc -l
// Expected: "2\n"
func TestGNUCompatLinesCount(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\ny\n")
	stdout, _, code := cmdRun(t, "wc -l file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2 file.txt\n", stdout)
}

// TestGNUCompatLinesNoNewline — -l on input with no newline.
//
// GNU command: printf 'x y' | gwc -l
// Expected: "0\n"
func TestGNUCompatLinesNoNewline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x y")
	stdout, _, code := cmdRun(t, "wc -l file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "0 file.txt\n", stdout)
}

// TestGNUCompatWordsEmpty — -w on empty.
//
// GNU command: printf ” | gwc -w
// Expected: "0\n"
func TestGNUCompatWordsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "")
	stdout, _, code := cmdRun(t, "wc -w file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "0 file.txt\n", stdout)
}

// TestGNUCompatWordsMulti — -w on "x y\nz".
//
// GNU command: printf 'x y\nz' | gwc -w
// Expected: "3\n"
func TestGNUCompatWordsMulti(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x y\nz")
	stdout, _, code := cmdRun(t, "wc -w file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "3 file.txt\n", stdout)
}

// TestGNUCompatBytesCount — -c on "x".
//
// GNU command: printf 'x' | gwc -c
// Expected: "0\n"
func TestGNUCompatBytesCount(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x")
	stdout, _, code := cmdRun(t, "wc -c file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1 file.txt\n", stdout)
}

// TestGNUCompatMaxLineLen — -L on "1\n12\n".
//
// GNU command: printf '1\n12\n' | gwc -L
// Expected: "2\n"
func TestGNUCompatMaxLineLen(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "1\n12\n")
	stdout, _, code := cmdRun(t, "wc -L file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2 file.txt\n", stdout)
}

// TestGNUCompatMaxLineLenLastLine — -L on "\n123456" (no trailing newline).
//
// GNU command: printf '\n123456' | gwc -L
// Expected: "6\n"
func TestGNUCompatMaxLineLenLastLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "\n123456")
	stdout, _, code := cmdRun(t, "wc -L file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "6 file.txt\n", stdout)
}

// TestGNUCompatMultipleFiles — two files with total line.
//
// GNU command: gwc a.txt b.txt
// a.txt = "hello\n" (1 line, 1 word, 6 bytes)
// b.txt = "world foo\n" (1 line, 2 words, 10 bytes)
// Expected:
//
//	" 1  1  6 a.txt\n 1  2 10 b.txt\n 2  3 16 total\n"
func TestGNUCompatMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "hello\n")
	writeFile(t, dir, "b.txt", "world foo\n")
	stdout, _, code := cmdRun(t, "wc a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, " 1  1  6 a.txt\n 1  2 10 b.txt\n 2  3 16 total\n", stdout)
}

// TestGNUCompatCharsMultibyte — -m on "café\n".
//
// GNU command: printf 'café\n' | gwc -m
// Expected: "5\n" (5 chars: c, a, f, é, \n)
func TestGNUCompatCharsMultibyte(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "café\n")
	stdout, _, code := cmdRun(t, "wc -m file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "5 file.txt\n", stdout)
}

// TestGNUCompatControlCharIsWord — control byte \x01 does NOT count as a word.
//
// GNU command: printf '\x01\n' | gwc -w
// Expected: "0\n"
func TestGNUCompatControlCharIsWord(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "\x01\n")
	stdout, _, code := cmdRun(t, "wc -w file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "0 file.txt\n", stdout)
}

// TestGNUCompatRejectedFlag — unknown flag exits 1.
//
// GNU command: gwc --follow
// Expected: exit 1, stderr contains "wc:"
func TestGNUCompatRejectedFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "wc --follow", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "wc:")
}
