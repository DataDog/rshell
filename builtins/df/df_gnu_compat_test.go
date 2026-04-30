// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package df_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/rshell/builtins/testutil"
)

// GNU coreutils df 9.10 reference outputs were captured by running the
// real `gdf` binary on macOS (Homebrew) and `df` on Linux. Because df's
// output is always host-dependent, this test file verifies header
// strings and structural invariants byte-for-byte rather than full row
// content.

// TestGNUCompatHeaderPosix — `gdf -P` always emits this exact header.
//
// Reference: `gdf -P / | head -n 1` →
//
//	"Filesystem 1024-blocks Used Available Capacity Mounted on"
func TestGNUCompatHeaderPosix(t *testing.T) {
	requireSupported(t)
	stdout, _, code := testutil.RunScript(t, "df -P", "")
	assert.Equal(t, 0, code)
	header := firstLine(stdout)
	assert.Equal(t, "Filesystem 1024-blocks Used Available Capacity Mounted on", header)
}

// TestGNUCompatHeaderDefault — `gdf` default header.
//
// Reference: `gdf` (no flags) → header line:
//
//	"Filesystem     1K-blocks      Used Available Use% Mounted on"
//
// Whitespace between columns depends on the longest filesystem name on
// the host so we cannot compare byte-for-byte; instead assert each
// expected header word appears in order.
func TestGNUCompatHeaderDefault(t *testing.T) {
	requireSupported(t)
	stdout, _, code := testutil.RunScript(t, "df", "")
	assert.Equal(t, 0, code)
	header := firstLine(stdout)
	wantOrder := []string{"Filesystem", "1K-blocks", "Used", "Available", "Use%", "Mounted on"}
	prev := -1
	for _, w := range wantOrder {
		idx := strings.Index(header, w)
		assert.GreaterOrEqual(t, idx, 0, "%q missing from header %q", w, header)
		assert.Greater(t, idx, prev, "%q out of order in header %q", w, header)
		prev = idx
	}
}

// TestGNUCompatHeaderHuman — `gdf -h` swaps the block column for "Size".
//
// Reference: `gdf -h` → header has "Size" instead of "1K-blocks".
func TestGNUCompatHeaderHuman(t *testing.T) {
	requireSupported(t)
	stdout, _, _ := testutil.RunScript(t, "df -h", "")
	header := firstLine(stdout)
	assert.Contains(t, header, "Size")
	assert.NotContains(t, header, "1K-blocks")
	assert.NotContains(t, header, "1024-blocks")
}

// TestGNUCompatHeaderInodes — `gdf -i` uses inode column names.
//
// Reference: `gdf -i` →
//
//	"Filesystem     Inodes IUsed IFree IUse% Mounted on"
func TestGNUCompatHeaderInodes(t *testing.T) {
	requireSupported(t)
	stdout, _, _ := testutil.RunScript(t, "df -i", "")
	header := firstLine(stdout)
	wantOrder := []string{"Filesystem", "Inodes", "IUsed", "IFree", "IUse%", "Mounted on"}
	prev := -1
	for _, w := range wantOrder {
		idx := strings.Index(header, w)
		assert.GreaterOrEqual(t, idx, 0, "%q missing from header %q", w, header)
		assert.Greater(t, idx, prev, "%q out of order in header %q", w, header)
		prev = idx
	}
}

// TestGNUCompatHeaderType — `gdf -T` adds the Type column right after
// Filesystem.
//
// Reference: `gdf -T` → "Filesystem     Type 1K-blocks ..."
func TestGNUCompatHeaderType(t *testing.T) {
	requireSupported(t)
	stdout, _, _ := testutil.RunScript(t, "df -T", "")
	header := firstLine(stdout)
	fIdx := strings.Index(header, "Filesystem")
	tIdx := strings.Index(header, "Type")
	bIdx := strings.Index(header, "1K-blocks")
	assert.True(t, fIdx >= 0 && tIdx > fIdx && bIdx > tIdx,
		"Type must be between Filesystem and 1K-blocks: %q", header)
}

// TestGNUCompatPosixSingleSpace — POSIX format uses single-space field
// separators (no tab alignment). Verifies a row's separator byte is
// exactly one space.
//
// Reference: `gdf -P / | sed -n '2p' | od -c | head -1` shows single
// space separators between every field.
func TestGNUCompatPosixSingleSpace(t *testing.T) {
	requireSupported(t)
	stdout, _, _ := testutil.RunScript(t, "df -P", "")
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) < 2 {
		t.Skip("not enough rows to verify spacing")
	}
	for _, l := range lines {
		assert.False(t, strings.Contains(l, "\t"), "POSIX row contains tab: %q", l)
		// No "double space + non-space" sequence (would mean column
		// alignment padding, which POSIX format must not do).
		assert.False(t, strings.Contains(l, "  "),
			"POSIX row %q contains double space (must be single-space separated)", l)
	}
}

// TestGNUCompatTotalRowLabel — `gdf --total` ends with a row whose
// first column is the literal string "total".
//
// Reference: `gdf --total | tail -n 1` → "total ..." or "total\t..."
func TestGNUCompatTotalRowLabel(t *testing.T) {
	requireSupported(t)
	stdout, _, _ := testutil.RunScript(t, "df --total", "")
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	last := lines[len(lines)-1]
	fields := strings.Fields(last)
	assert.Equal(t, "total", fields[0], "total row must start with 'total': %q", last)
}
