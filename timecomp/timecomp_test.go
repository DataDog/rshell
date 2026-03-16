// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package timecomp

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestMatchMminCeiling verifies that MatchMmin uses ceiling rounding.
// GNU find rounds up fractional minutes: a file 5 seconds old is in
// minute bucket 1 (not 0). This prevents regression to math.Floor.
func TestMatchMminCeiling(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		age     time.Duration // how old the file is
		n       int64
		cmp     int
		matched bool
	}{
		// Exact match uses ceiling bucketing: ceil(delta_sec / 60)
		// +N/-N use raw second comparison: delta_sec > N*60 / delta_sec < N*60

		// 0 seconds old → ceil(0) = 0 → bucket 0
		{"0s exact 0", 0, 0, CmpExact, true},
		{"0s gt 0", 0, 0, CmpMore, false}, // 0 > 0 = false
		{"0s lt 1", 0, 1, CmpLess, true},  // 0 < 60 = true

		// 1 second old → ceil(1/60) = 1 → bucket 1
		{"1s exact 0", 1 * time.Second, 0, CmpExact, false},
		{"1s exact 1", 1 * time.Second, 1, CmpExact, true},
		{"1s gt 0", 1 * time.Second, 0, CmpMore, true}, // 1 > 0 = true
		{"1s lt 1", 1 * time.Second, 1, CmpLess, true}, // 1 < 60 = true (GNU find matches)

		// 5 seconds old → ceil(5/60) = 1 → bucket 1
		{"5s exact 0", 5 * time.Second, 0, CmpExact, false},
		{"5s exact 1", 5 * time.Second, 1, CmpExact, true},
		{"5s gt 0", 5 * time.Second, 0, CmpMore, true}, // 5 > 0 = true
		{"5s lt 1", 5 * time.Second, 1, CmpLess, true}, // 5 < 60 = true (key regression test)

		// 30 seconds old — the specific case from codex P1
		{"30s lt 1", 30 * time.Second, 1, CmpLess, true}, // 30 < 60 = true

		// 59 seconds old → ceil(59/60) = 1 → bucket 1
		{"59s exact 1", 59 * time.Second, 1, CmpExact, true},
		{"59s exact 0", 59 * time.Second, 0, CmpExact, false},
		{"59s lt 1", 59 * time.Second, 1, CmpLess, true}, // 59 < 60 = true

		// 60 seconds old → ceil(60/60) = 1 → bucket 1
		{"60s exact 1", 60 * time.Second, 1, CmpExact, true},
		{"60s exact 2", 60 * time.Second, 2, CmpExact, false},
		{"60s gt 1", 60 * time.Second, 1, CmpMore, false}, // 60 > 60 = false
		{"60s lt 1", 60 * time.Second, 1, CmpLess, false}, // 60 < 60 = false

		// 61 seconds old → ceil(61/60) = 2 → bucket 2
		{"61s exact 1", 61 * time.Second, 1, CmpExact, false},
		{"61s exact 2", 61 * time.Second, 2, CmpExact, true},
		{"61s gt 1", 61 * time.Second, 1, CmpMore, true}, // 61 > 60 = true
		{"61s lt 2", 61 * time.Second, 2, CmpLess, true}, // 61 < 120 = true

		// 5 minutes old → ceil(300/60) = 5 → bucket 5
		{"5m exact 5", 5 * time.Minute, 5, CmpExact, true},
		{"5m gt 4", 5 * time.Minute, 4, CmpMore, true}, // 300 > 240 = true
		{"5m lt 6", 5 * time.Minute, 6, CmpLess, true}, // 300 < 360 = true

		// 5 minutes 1 second old → ceil(301/60) = 6 → bucket 6
		{"5m1s exact 6", 5*time.Minute + 1*time.Second, 6, CmpExact, true},
		{"5m1s exact 5", 5*time.Minute + 1*time.Second, 5, CmpExact, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modTime := now.Add(-tt.age)
			got := MatchMmin(now, modTime, tt.n, tt.cmp)
			assert.Equal(t, tt.matched, got, "MatchMmin(age=%v, n=%d, cmp=%d)", tt.age, tt.n, tt.cmp)
		})
	}
}

// TestMatchMminOverflow verifies that MatchMmin handles values exceeding
// MaxMminN without integer overflow.
func TestMatchMminOverflow(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	modTime := now.Add(-1 * time.Hour)

	tests := []struct {
		name    string
		n       int64
		cmp     int
		matched bool
	}{
		{"MaxMminN +N", MaxMminN, CmpMore, false},
		{"MaxMminN -N", MaxMminN, CmpLess, true},
		{"MaxMminN exact", MaxMminN, CmpExact, false},
		{"MaxMminN+1 +N", MaxMminN + 1, CmpMore, false},
		{"MaxMminN+1 -N", MaxMminN + 1, CmpLess, true},
		{"huge +N", math.MaxInt64 / 2, CmpMore, false},
		{"huge -N", math.MaxInt64 / 2, CmpLess, true},
		{"maxint64 +N", math.MaxInt64, CmpMore, false},
		{"maxint64 -N", math.MaxInt64, CmpLess, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchMmin(now, modTime, tt.n, tt.cmp)
			assert.Equal(t, tt.matched, got, "MatchMmin(n=%d, cmp=%d)", tt.n, tt.cmp)
		})
	}
}

// TestMatchMtimeFloor verifies that MatchMtime uses floor rounding (NOT ceiling).
// A file 5 hours old should be in day bucket 0 (not 1).
func TestMatchMtimeFloor(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		age     time.Duration
		n       int64
		cmp     int
		matched bool
	}{
		// 0 hours → floor(0/24) = 0
		{"0h exact 0", 0, 0, CmpExact, true},
		{"0h gt 0", 0, 0, CmpMore, false},

		// 5 hours → floor(5/24) = 0
		{"5h exact 0", 5 * time.Hour, 0, CmpExact, true},
		{"5h exact 1", 5 * time.Hour, 1, CmpExact, false},

		// 23 hours → floor(23/24) = 0
		{"23h exact 0", 23 * time.Hour, 0, CmpExact, true},

		// 24 hours → floor(24/24) = 1
		{"24h exact 1", 24 * time.Hour, 1, CmpExact, true},
		{"24h exact 0", 24 * time.Hour, 0, CmpExact, false},

		// 25 hours → floor(25/24) = 1
		{"25h exact 1", 25 * time.Hour, 1, CmpExact, true},

		// 48 hours → floor(48/24) = 2
		{"48h exact 2", 48 * time.Hour, 2, CmpExact, true},
		{"48h gt 1", 48 * time.Hour, 1, CmpMore, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modTime := now.Add(-tt.age)
			got := MatchMtime(now, modTime, tt.n, tt.cmp)
			assert.Equal(t, tt.matched, got, "MatchMtime(age=%v, n=%d, cmp=%d)", tt.age, tt.n, tt.cmp)
		})
	}
}

// TestIsRecentEnough verifies the ls time-format decision logic.
func TestIsRecentEnough(t *testing.T) {
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		mod    time.Time
		months int
		want   bool
	}{
		{"recent file", time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC), 6, true},
		{"exactly 6 months ago", now.AddDate(0, -6, 0), 6, true}, // boundary: not before cutoff
		{"old file", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), 6, false},
		{"future file", time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC), 6, false},
		{"just now", now, 6, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsRecentEnough(now, tt.mod, tt.months)
			assert.Equal(t, tt.want, got)
		})
	}
}
