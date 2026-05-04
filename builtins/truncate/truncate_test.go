// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package truncate

import (
	"errors"
	"math"
	"testing"
)

// TestParseSizeAccepts covers every accepted suffix and key boundary value.
// Any change to the suffix table or numeric handling should keep these
// outputs byte-identical to GNU truncate's behaviour for the same inputs.
func TestParseSizeAccepts(t *testing.T) {
	cases := []struct {
		input string
		want  int64
	}{
		// Bare digits.
		{"0", 0},
		{"1", 1},
		{"123456", 123456},
		{"9223372036854775807", math.MaxInt64}, // exact int64 ceiling.

		// Binary suffixes (1024-based). Leading letter is case-insensitive;
		// the trailing "iB" must keep exact casing.
		{"1K", 1 << 10},
		{"1k", 1 << 10},
		{"1KiB", 1 << 10},
		{"1kiB", 1 << 10},
		{"2K", 2 << 10},
		{"1M", 1 << 20},
		{"1m", 1 << 20},
		{"1MiB", 1 << 20},
		{"1miB", 1 << 20},
		{"1G", 1 << 30},
		{"1g", 1 << 30},
		{"1GiB", 1 << 30},
		{"1giB", 1 << 30},
		{"1T", 1 << 40},
		{"1t", 1 << 40},
		{"1TiB", 1 << 40},
		{"1tiB", 1 << 40},

		// Decimal suffixes (1000-based). Same rule: leading letter
		// case-insensitive, trailing "B" must be uppercase.
		{"1KB", 1000},
		{"1kB", 1000},
		{"1MB", 1000 * 1000},
		{"1mB", 1000 * 1000},
		{"1GB", 1000 * 1000 * 1000},
		{"1gB", 1000 * 1000 * 1000},
		{"1TB", 1000 * 1000 * 1000 * 1000},
		{"1tB", 1000 * 1000 * 1000 * 1000},

		// Zero with suffix is still zero.
		{"0K", 0},
		{"0MB", 0},
	}
	for _, tc := range cases {
		got, err := parseSize(tc.input)
		if err != nil {
			t.Errorf("parseSize(%q) returned error %v, want %d", tc.input, err, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("parseSize(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

// TestParseSizeRejects covers every malformed-input class. The expected
// error sentinel matters because the handler distinguishes errRelativeSize
// from errInvalidSize when formatting user-facing messages.
func TestParseSizeRejects(t *testing.T) {
	cases := []struct {
		input string
		want  error
	}{
		// Empty, whitespace, garbage.
		{"", errInvalidSize},
		{" ", errInvalidSize},
		{" 10", errInvalidSize}, // leading whitespace.
		{"abc", errInvalidSize},
		{"1.5", errInvalidSize},  // floats not supported.
		{"1KIB", errInvalidSize}, // GNU rejects all-caps "iB".
		{"1Kib", errInvalidSize}, // and lowercase "ib".
		{"1KIb", errInvalidSize}, // and "Ib".
		{"1kIB", errInvalidSize}, // any non-"iB" trailing form is invalid.
		{"1kb", errInvalidSize},  // trailing "b" must be uppercase.
		{"1Kb", errInvalidSize},
		{"1XB", errInvalidSize},
		{"1KB1", errInvalidSize},  // trailing junk.
		{"1Ki", errInvalidSize},   // partial "iB" suffix.
		{"K", errInvalidSize},     // suffix without digits.
		{"+", errRelativeSize},    // bare modifier.
		{"-", errRelativeSize},    // bare modifier.
		{"%5", errRelativeSize},   // GNU shell-percent rounding mode.
		{"/2", errRelativeSize},   // GNU divide-by-N mode.
		{"<10", errRelativeSize},  // GNU shrink-to-at-most mode.
		{">10", errRelativeSize},  // GNU expand-to-at-least mode.
		{"+10", errRelativeSize},  // GNU relative add.
		{"-10", errRelativeSize},  // GNU relative subtract.
		{"+10K", errRelativeSize}, // modifier with suffix.

		// Overflow boundaries.
		{"9223372036854775808", errInvalidSize},  // int64 max + 1.
		{"99999999999999999999", errInvalidSize}, // far past int64.
		{"8388608T", errInvalidSize},             // 2^23 TiB overflows int64 multiplier.
	}
	for _, tc := range cases {
		_, err := parseSize(tc.input)
		if err == nil {
			t.Errorf("parseSize(%q) returned no error, want %v", tc.input, tc.want)
			continue
		}
		if !errors.Is(err, tc.want) {
			t.Errorf("parseSize(%q) returned %v, want %v", tc.input, err, tc.want)
		}
	}
}

// TestParseSizeMaxIntBoundary pins the exact T-suffix ceiling to guard
// against off-by-one regressions in the overflow check.
//
// MaxInt64 / TiB == 8388607, so:
//   - 8388607T must succeed (multiplies to 8388607 * 2^40, just under MaxInt64)
//   - 8388608T must fail (would multiply to MaxInt64+1)
func TestParseSizeMaxIntBoundary(t *testing.T) {
	maxT := int64(math.MaxInt64) / (1 << 40)
	if maxT != 8388607 {
		t.Fatalf("test assumes maxT == 8388607, got %d", maxT)
	}
	largest := strconvFormat(maxT) + "T"
	got, err := parseSize(largest)
	if err != nil {
		t.Errorf("parseSize(%q) should succeed at the ceiling, got %v", largest, err)
	}
	if got != maxT*(1<<40) {
		t.Errorf("parseSize(%q) = %d, want %d", largest, got, maxT*(1<<40))
	}
	overflow := strconvFormat(maxT+1) + "T"
	if _, err := parseSize(overflow); err == nil {
		t.Errorf("parseSize(%q) must reject one above the ceiling", overflow)
	}
}

// strconvFormat is a tiny helper that avoids pulling strconv import
// duplication into the test file's import set.
func strconvFormat(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	n := v
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
