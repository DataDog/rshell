// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package wineventlog

import "testing"

func TestUint32ExceedsLimit(t *testing.T) {
	const limit = 64 * 1024
	for _, tc := range []struct {
		used uint32
		want bool
	}{
		{used: limit, want: false},
		{used: limit + 1, want: true},
		{used: 0x80000000, want: true},
		{used: 0xffffffff, want: true},
	} {
		if got := uint32ExceedsLimit(tc.used, limit); got != tc.want {
			t.Errorf("uint32ExceedsLimit(%#x, %d) = %v, want %v", tc.used, limit, got, tc.want)
		}
	}
}
