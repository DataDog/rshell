// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package sysinfo

import (
	"fmt"
	"syscall"
	"time"
)

var (
	kernel32       = syscall.MustLoadDLL("kernel32.dll")
	getTickCount64 = kernel32.MustFindProc("GetTickCount64")
)

func getImpl() (Info, error) {
	// GetTickCount64 returns milliseconds since last boot as a ULONGLONG.
	// It always succeeds; the third return from Call (GetLastError) is not
	// meaningful here, so it is discarded.
	//
	// On 64-bit Windows, lo holds the full 64-bit value and hi is zero.
	// On 32-bit Windows (386), the Win32 calling convention places the low
	// 32 bits in lo and the high 32 bits in hi — combining them avoids the
	// ~49.7-day rollover that would occur if only lo were used.
	lo, hi, _ := getTickCount64.Call()
	ms := uint64(lo) | uint64(hi)<<32

	uptimeSecs := float64(ms) / 1000.0
	return Info{
		UptimeSeconds: uptimeSecs,
		LoadAvailable: false,
		BootTime:      time.Now().Unix() - int64(uptimeSecs),
	}, nil
}
