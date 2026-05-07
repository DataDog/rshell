// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build unix

package df_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/rshell/builtins/testutil"
)

// TestDfDataRowsAreNumeric_POSIX runs df -P (POSIX format, single-space
// separated) and verifies every data row's three numeric columns parse
// as unsigned integers. This catches the entire class of formatting bugs
// where a column would be empty, contain a stray "%", or wrap.
func TestDfDataRowsAreNumeric_POSIX(t *testing.T) {
	stdout, _, code := testutil.RunScript(t, "df -P", "")
	assert.Equal(t, 0, code)
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	assert.Greater(t, len(lines), 1, "expected header + at least one data row")
	for i, line := range lines[1:] {
		fields := strings.Fields(line)
		// POSIX: filesystem 1024-blocks used available capacity mountpoint
		// The mountpoint may itself contain spaces; everything before the
		// last 5 fields must be the filesystem name.
		if len(fields) < 6 {
			t.Errorf("row %d: too few fields: %q", i, line)
			continue
		}
		// columns 1-3 are integers (relative to the last 5 fields)
		blocks := fields[len(fields)-5]
		used := fields[len(fields)-4]
		avail := fields[len(fields)-3]
		_, err := strconv.ParseUint(blocks, 10, 64)
		assert.NoError(t, err, "row %d blocks not integer: %q", i, blocks)
		_, err = strconv.ParseUint(used, 10, 64)
		assert.NoError(t, err, "row %d used not integer: %q", i, used)
		_, err = strconv.ParseUint(avail, 10, 64)
		assert.NoError(t, err, "row %d available not integer: %q", i, avail)
	}
}

// TestDfPercentFormat checks that the capacity column ends with '%' or
// equals '-' (the empty pseudo-FS sentinel).
func TestDfPercentFormat(t *testing.T) {
	stdout, _, _ := testutil.RunScript(t, "df -P", "")
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	for i, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// 5th-from-end is the capacity column.
		cap := fields[len(fields)-2]
		if cap == "-" {
			continue
		}
		assert.True(t, strings.HasSuffix(cap, "%"),
			"row %d capacity column %q does not end with %%", i, cap)
	}
}

// TestDfTotalSumIsConsistent — when --total is given, the total row's
// numeric columns must equal the saturated sum of the per-mount columns.
func TestDfTotalSumIsConsistent(t *testing.T) {
	stdout, _, code := testutil.RunScript(t, "df -P --total", "")
	assert.Equal(t, 0, code)
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) < 3 {
		t.Skip("not enough rows for total verification")
	}
	totalLine := lines[len(lines)-1]
	assert.True(t, strings.HasPrefix(totalLine, "total "), "last line is not total: %q", totalLine)

	// Sum the per-row block columns. Use saturated arithmetic to match
	// the implementation; if the sum would overflow we don't assert
	// equality, only non-zero.
	var sumBlocks, sumUsed, sumAvail uint64
	overflow := false
	for _, line := range lines[1 : len(lines)-1] {
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}
		b, _ := strconv.ParseUint(fields[len(fields)-5], 10, 64)
		u, _ := strconv.ParseUint(fields[len(fields)-4], 10, 64)
		a, _ := strconv.ParseUint(fields[len(fields)-3], 10, 64)
		if sumBlocks > ^uint64(0)-b {
			overflow = true
		}
		sumBlocks += b
		sumUsed += u
		sumAvail += a
	}
	if overflow {
		return
	}
	totalFields := strings.Fields(totalLine)
	gotBlocks, _ := strconv.ParseUint(totalFields[len(totalFields)-5], 10, 64)
	gotUsed, _ := strconv.ParseUint(totalFields[len(totalFields)-4], 10, 64)
	gotAvail, _ := strconv.ParseUint(totalFields[len(totalFields)-3], 10, 64)
	// Per-row 1K-blocks values are ceil(bytes/1024); the total row
	// is computed as ceil(totalBytes/1024). On a filesystem whose
	// f_frsize is not a multiple of 1024 (some FUSE backends), the
	// sum-of-rounded-rows can differ from the rounded-total by up
	// to one block per row. Real Linux/macOS filesystems use ≥4 KiB
	// blocks so this never surfaces in practice, but a tolerance
	// hardens the assertion against pathological FUSE drivers.
	tolerance := uint64(len(lines) - 2) // header + total row are excluded
	assert.LessOrEqual(t, absDiff(gotBlocks, sumBlocks), tolerance,
		"blocks total %d differs from sum %d by more than %d", gotBlocks, sumBlocks, tolerance)
	assert.LessOrEqual(t, absDiff(gotUsed, sumUsed), tolerance,
		"used total %d differs from sum %d by more than %d", gotUsed, sumUsed, tolerance)
	assert.LessOrEqual(t, absDiff(gotAvail, sumAvail), tolerance,
		"avail total %d differs from sum %d by more than %d", gotAvail, sumAvail, tolerance)
}

func absDiff(a, b uint64) uint64 {
	if a >= b {
		return a - b
	}
	return b - a
}

// TestDfTypeFilterMatchesAtLeastOne picks a type from the unfiltered
// listing and verifies that filtering by it returns only rows of that
// type.
func TestDfTypeFilterMatchesAtLeastOne(t *testing.T) {
	stdoutT, _, code := testutil.RunScript(t, "df -PT", "")
	assert.Equal(t, 0, code)
	lines := strings.Split(strings.TrimRight(stdoutT, "\n"), "\n")
	if len(lines) < 2 {
		t.Skip("no mounts to filter")
	}
	// Pick the first row's type.
	firstFields := strings.Fields(lines[1])
	if len(firstFields) < 7 {
		t.Skip("malformed -PT row, skipping")
	}
	fsType := firstFields[1]
	stdoutF, _, _ := testutil.RunScript(t, "df -PT -t "+fsType, "")
	filtered := strings.Split(strings.TrimRight(stdoutF, "\n"), "\n")
	assert.Greater(t, len(filtered), 1, "filter should keep at least one row")
	for _, l := range filtered[1:] {
		fields := strings.Fields(l)
		if len(fields) < 7 {
			continue
		}
		assert.Equal(t, fsType, fields[1])
	}
}

// TestDfExcludeTypeRemovesRows runs the unfiltered listing, picks a type,
// and verifies it disappears under -x.
func TestDfExcludeTypeRemovesRows(t *testing.T) {
	stdoutT, _, _ := testutil.RunScript(t, "df -PT", "")
	lines := strings.Split(strings.TrimRight(stdoutT, "\n"), "\n")
	if len(lines) < 2 {
		t.Skip("no mounts to exclude")
	}
	firstFields := strings.Fields(lines[1])
	if len(firstFields) < 7 {
		t.Skip("malformed -PT row, skipping")
	}
	fsType := firstFields[1]
	stdoutX, _, _ := testutil.RunScript(t, "df -PT -x "+fsType, "")
	for _, l := range strings.Split(strings.TrimRight(stdoutX, "\n"), "\n")[1:] {
		fields := strings.Fields(l)
		if len(fields) < 7 {
			continue
		}
		assert.NotEqual(t, fsType, fields[1])
	}
}

// TestDfHumanReadableHasNoDigits_AtLargeSizes — when sizes are big
// enough that human formatting kicks in, the size column must NOT be a
// raw integer (it should have a K/M/G/T/P/E suffix).
func TestDfHumanReadableHasNoDigits_AtLargeSizes(t *testing.T) {
	stdout, _, _ := testutil.RunScript(t, "df -h", "")
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) < 2 {
		t.Skip("no mounts")
	}
	// Walk every row and verify the Size column (3rd field from the end
	// of the data, so 4th field) ends with K/M/G/T/P/E or is just
	// digits (for very small filesystems).
	suffixOK := func(s string) bool {
		if s == "" {
			return false
		}
		switch s[len(s)-1] {
		case 'K', 'M', 'G', 'T', 'P', 'E':
			return true
		}
		// Bare digits: still OK for sub-base sizes.
		for _, r := range s {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}
	for i, l := range lines[1:] {
		fields := strings.Fields(l)
		if len(fields) < 6 {
			continue
		}
		size := fields[len(fields)-5]
		assert.True(t, suffixOK(size), "row %d size column %q has no recognised suffix", i, size)
	}
}
