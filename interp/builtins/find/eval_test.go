// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package find

import (
	iofs "io/fs"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestEvalMminCeiling verifies that -mmin uses ceiling rounding.
// GNU find rounds up fractional minutes: a file 5 seconds old is in
// minute bucket 1 (not 0). This prevents regression to math.Floor.
func TestEvalMminCeiling(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		age     time.Duration // how old the file is
		n       int64
		cmp     cmpOp
		matched bool
	}{
		// Exact match uses ceiling bucketing: ceil(delta_sec / 60)
		// +N/-N use raw second comparison: delta_sec > N*60 / delta_sec < N*60

		// 0 seconds old → ceil(0) = 0 → bucket 0
		{"0s exact 0", 0, 0, cmpExact, true},
		{"0s gt 0", 0, 0, cmpMore, false}, // 0 > 0 = false
		{"0s lt 1", 0, 1, cmpLess, true},  // 0 < 60 = true

		// 1 second old → ceil(1/60) = 1 → bucket 1
		{"1s exact 0", 1 * time.Second, 0, cmpExact, false},
		{"1s exact 1", 1 * time.Second, 1, cmpExact, true},
		{"1s gt 0", 1 * time.Second, 0, cmpMore, true}, // 1 > 0 = true
		{"1s lt 1", 1 * time.Second, 1, cmpLess, true}, // 1 < 60 = true (GNU find matches)

		// 5 seconds old → ceil(5/60) = 1 → bucket 1
		{"5s exact 0", 5 * time.Second, 0, cmpExact, false},
		{"5s exact 1", 5 * time.Second, 1, cmpExact, true},
		{"5s gt 0", 5 * time.Second, 0, cmpMore, true}, // 5 > 0 = true
		{"5s lt 1", 5 * time.Second, 1, cmpLess, true}, // 5 < 60 = true (key regression test)

		// 30 seconds old — the specific case from codex P1
		{"30s lt 1", 30 * time.Second, 1, cmpLess, true}, // 30 < 60 = true

		// 59 seconds old → ceil(59/60) = 1 → bucket 1
		{"59s exact 1", 59 * time.Second, 1, cmpExact, true},
		{"59s exact 0", 59 * time.Second, 0, cmpExact, false},
		{"59s lt 1", 59 * time.Second, 1, cmpLess, true}, // 59 < 60 = true

		// 60 seconds old → ceil(60/60) = 1 → bucket 1
		{"60s exact 1", 60 * time.Second, 1, cmpExact, true},
		{"60s exact 2", 60 * time.Second, 2, cmpExact, false},
		{"60s gt 1", 60 * time.Second, 1, cmpMore, false}, // 60 > 60 = false
		{"60s lt 1", 60 * time.Second, 1, cmpLess, false}, // 60 < 60 = false

		// 61 seconds old → ceil(61/60) = 2 → bucket 2
		{"61s exact 1", 61 * time.Second, 1, cmpExact, false},
		{"61s exact 2", 61 * time.Second, 2, cmpExact, true},
		{"61s gt 1", 61 * time.Second, 1, cmpMore, true}, // 61 > 60 = true
		{"61s lt 2", 61 * time.Second, 2, cmpLess, true}, // 61 < 120 = true

		// 5 minutes old → ceil(300/60) = 5 → bucket 5
		{"5m exact 5", 5 * time.Minute, 5, cmpExact, true},
		{"5m gt 4", 5 * time.Minute, 4, cmpMore, true}, // 300 > 240 = true
		{"5m lt 6", 5 * time.Minute, 6, cmpLess, true}, // 300 < 360 = true

		// 5 minutes 1 second old → ceil(301/60) = 6 → bucket 6
		{"5m1s exact 6", 5*time.Minute + 1*time.Second, 6, cmpExact, true},
		{"5m1s exact 5", 5*time.Minute + 1*time.Second, 5, cmpExact, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modTime := now.Add(-tt.age)
			ec := &evalContext{
				now:  now,
				info: &fakeFileInfo{modTime: modTime},
			}
			got := evalMmin(ec, tt.n, tt.cmp)
			assert.Equal(t, tt.matched, got, "evalMmin(age=%v, n=%d, cmp=%s)", tt.age, tt.n, tt.cmp)
		})
	}
}

// TestEvalMtimeFloor verifies that -mtime uses floor rounding (NOT ceiling).
// A file 5 hours old should be in day bucket 0 (not 1).
func TestEvalMtimeFloor(t *testing.T) {
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		age     time.Duration
		n       int64
		cmp     cmpOp
		matched bool
	}{
		// 0 hours → floor(0/24) = 0
		{"0h exact 0", 0, 0, cmpExact, true},
		{"0h gt 0", 0, 0, cmpMore, false},

		// 5 hours → floor(5/24) = 0
		{"5h exact 0", 5 * time.Hour, 0, cmpExact, true},
		{"5h exact 1", 5 * time.Hour, 1, cmpExact, false},

		// 23 hours → floor(23/24) = 0
		{"23h exact 0", 23 * time.Hour, 0, cmpExact, true},

		// 24 hours → floor(24/24) = 1
		{"24h exact 1", 24 * time.Hour, 1, cmpExact, true},
		{"24h exact 0", 24 * time.Hour, 0, cmpExact, false},

		// 25 hours → floor(25/24) = 1
		{"25h exact 1", 25 * time.Hour, 1, cmpExact, true},

		// 48 hours → floor(48/24) = 2
		{"48h exact 2", 48 * time.Hour, 2, cmpExact, true},
		{"48h gt 1", 48 * time.Hour, 1, cmpMore, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modTime := now.Add(-tt.age)
			ec := &evalContext{
				now:  now,
				info: &fakeFileInfo{modTime: modTime},
			}
			got := evalMtime(ec, tt.n, tt.cmp)
			assert.Equal(t, tt.matched, got, "evalMtime(age=%v, n=%d, cmp=%s)", tt.age, tt.n, tt.cmp)
		})
	}
}

// TestCompareSizeOverflow verifies overflow-safe ceiling division.
func TestCompareSizeOverflow(t *testing.T) {
	tests := []struct {
		name     string
		fileSize int64
		su       sizeUnit
		matched  bool
	}{
		// Normal cases
		{"0 bytes exact 0c", 0, sizeUnit{n: 0, cmp: cmpExact, unit: 'c'}, true},
		{"1 byte exact 1c", 1, sizeUnit{n: 1, cmp: cmpExact, unit: 'c'}, true},
		{"512 bytes exact 1b", 512, sizeUnit{n: 1, cmp: cmpExact, unit: 'b'}, true},
		{"1 byte rounds up to 1 block", 1, sizeUnit{n: 1, cmp: cmpExact, unit: 'b'}, true},
		{"513 bytes rounds up to 2 blocks", 513, sizeUnit{n: 2, cmp: cmpExact, unit: 'b'}, true},

		// Edge: zero-byte file
		{"0 bytes +0c", 0, sizeUnit{n: 0, cmp: cmpMore, unit: 'c'}, false},
		{"0 bytes -1c", 0, sizeUnit{n: 1, cmp: cmpLess, unit: 'c'}, true},

		// Large files near MaxInt64 (overflow protection)
		{"MaxInt64 bytes +0c", 1<<63 - 1, sizeUnit{n: 0, cmp: cmpMore, unit: 'c'}, true},
		{"MaxInt64 bytes exact in blocks", 1<<63 - 1, sizeUnit{n: (1<<63 - 1) / 512, cmp: cmpMore, unit: 'b'}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareSize(tt.fileSize, tt.su)
			assert.Equal(t, tt.matched, got)
		})
	}
}

// fakeFileInfo implements the minimal fs.FileInfo interface for testing.
type fakeFileInfo struct {
	modTime time.Time
	size    int64
	isDir   bool
}

func (f *fakeFileInfo) Name() string       { return "fake" }
func (f *fakeFileInfo) Size() int64        { return f.size }
func (f *fakeFileInfo) ModTime() time.Time { return f.modTime }
func (f *fakeFileInfo) IsDir() bool        { return f.isDir }
func (f *fakeFileInfo) Sys() any           { return nil }

// Mode returns a basic file mode for testing.
func (f *fakeFileInfo) Mode() iofs.FileMode {
	if f.isDir {
		return iofs.ModeDir | 0o755
	}
	return 0o644
}
