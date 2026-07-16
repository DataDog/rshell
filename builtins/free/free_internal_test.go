// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package free

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHumanBytes(t *testing.T) {
	cases := []struct {
		v    uint64
		want string
	}{
		{0, "0B"},
		{1, "1B"},
		{1023, "1023B"},
		{1024, "1.0Ki"},
		{1536, "1.5Ki"},
		{10 * 1024, "10Ki"}, // ≥10 drops the decimal
		{1024 * 1024, "1.0Mi"},
		{1 << 30, "1.0Gi"},
		{1 << 40, "1.0Ti"},
		{1 << 50, "1.0Pi"},
		{1 << 60, "1.0Ei"},
		// procps-ng free rounds to nearest (not always up, unlike df's
		// human-readable mode): 1535 bytes is 1.4990...Ki, which rounds
		// to 1.5Ki, and 1536 is exactly 1.5Ki.
		{1535, "1.5Ki"},
		{1583, "1.5Ki"}, // 1.546Ki rounds down to 1.5Ki
		{1587, "1.5Ki"}, // still rounds to 1.5 at one decimal place
		{1638, "1.6Ki"}, // 1.6001...Ki rounds to 1.6Ki
		// Promotion at the boundary: rounding must not emit "1024.0Ki".
		{1024*1024 - 1, "1.0Mi"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, humanBytes(c.v), "humanBytes(%d)", c.v)
	}
}

func TestSaturatingHelpers(t *testing.T) {
	assert.Equal(t, ^uint64(0), saturatingAdd(^uint64(0), 1))
	assert.Equal(t, uint64(3), saturatingAdd(1, 2))
	assert.Equal(t, uint64(0), saturatingSub(1, 2))
	assert.Equal(t, uint64(1), saturatingSub(3, 2))
}
