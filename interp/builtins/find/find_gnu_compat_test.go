// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// GNU compatibility tests for the find builtin.
//
// Expected outputs were captured from GNU findutils find 4.10.0
// (Debian bookworm) and are embedded as string literals so the tests
// run without any GNU tooling present on CI.

package find_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestGNUCompatFindDefaultPrintsDot — find with no arguments prints "." and all entries.
//
// GNU command: find  (in a directory with one file)
// Expected:    ".\n./file.txt\n"
func TestGNUCompatFindDefaultPrintsDot(t *testing.T) {
	dir := setupDir(t, map[string]string{"file.txt": "hello\n"})
	stdout, _, code := cmdRun(t, "find", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, ".\n./file.txt\n", stdout)
}

// TestGNUCompatFindNameGlob — -name "*.txt" filters by extension.
//
// GNU command: find . -name "*.txt"  (dir has a.txt, b.log)
// Expected:    "./a.txt\n"
func TestGNUCompatFindNameGlob(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"a.txt": "a",
		"b.log": "b",
	})
	stdout, _, code := cmdRun(t, `find . -name "*.txt"`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./a.txt\n", stdout)
}

// TestGNUCompatFindTypeF — -type f lists only regular files.
//
// GNU command: find . -type f  (dir has a.txt and subdir)
// Expected:    "./a.txt\n"
func TestGNUCompatFindTypeF(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "a"})
	stdout, _, code := cmdRun(t, "find . -type f", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./a.txt\n", stdout)
}

// TestGNUCompatFindTypeD — -type d lists only directories.
//
// GNU command: find . -type d  (dir has a.txt and sub/)
// Expected:    ".\n./sub\n"
func TestGNUCompatFindTypeD(t *testing.T) {
	dir := setupDir(t, map[string]string{"sub/a.txt": "a"})
	stdout, _, code := cmdRun(t, "find . -type d", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, ".\n./sub\n", stdout)
}

// TestGNUCompatFindMaxdepthZero — -maxdepth 0 prints only starting point.
//
// GNU command: find . -maxdepth 0
// Expected:    ".\n"
func TestGNUCompatFindMaxdepthZero(t *testing.T) {
	dir := setupDir(t, map[string]string{"file.txt": "a"})
	stdout, _, code := cmdRun(t, "find . -maxdepth 0", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, ".\n", stdout)
}

// TestGNUCompatFindMaxdepthOne — -maxdepth 1 lists starting point and direct children.
//
// GNU command: find . -maxdepth 1  (with file.txt and sub/deep.txt)
// Expected:    ".\n./file.txt\n./sub\n"
func TestGNUCompatFindMaxdepthOne(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"file.txt":     "a",
		"sub/deep.txt": "b",
	})
	stdout, _, code := cmdRun(t, "find . -maxdepth 1", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, ".\n./file.txt\n./sub\n", stdout)
}

// TestGNUCompatFindPrint0 — -print0 separates with null byte.
//
// GNU command: find . -name "a.txt" -print0
// Expected:    "./a.txt\x00"
func TestGNUCompatFindPrint0(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "a"})
	stdout, _, code := cmdRun(t, `find . -name "a.txt" -print0`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./a.txt\x00", stdout)
}

// TestGNUCompatFindFalse — -false produces no output.
//
// GNU command: find . -false
// Expected:    ""
func TestGNUCompatFindFalse(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "a"})
	stdout, _, code := cmdRun(t, "find . -false", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

// TestGNUCompatFindNotName — ! -name filters out matches.
//
// GNU command: find . -type f ! -name "*.log"
// Expected:    "./a.txt\n"
func TestGNUCompatFindNotName(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"a.txt": "a",
		"b.log": "b",
	})
	stdout, _, code := cmdRun(t, `find . -type f ! -name "*.log"`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./a.txt\n", stdout)
}
