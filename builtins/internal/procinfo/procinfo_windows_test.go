// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package procinfo

import (
	"context"
	"os"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestProcessEntryToProcUsesExeNameOnly(t *testing.T) {
	var entry windows.ProcessEntry32
	entry.ProcessID = 123
	entry.ParentProcessID = 1
	for i, ch := range utf16.Encode([]rune("safeproc.exe")) {
		entry.ExeFile[i] = ch
	}

	info := processEntryToProc(&entry)

	require.Equal(t, 123, info.PID)
	require.Equal(t, "safeproc.exe", info.Cmd)
	require.NotContains(t, info.Cmd, "--token")
}

func TestGetByPIDsWithMetricsWindowsSelf(t *testing.T) {
	metrics := MetricStartTime | MetricCPUTime | MetricElapsed |
		MetricRSS | MetricVSZ | MetricPMem | MetricPCPU

	procs, err := getByPIDs(context.Background(), "", []int{os.Getpid()}, metrics)

	require.NoError(t, err)
	require.Len(t, procs, 1)
	proc := procs[0]
	require.True(t, proc.Has(metrics))
	require.Positive(t, proc.RSSKiB)
	require.Positive(t, proc.VSZKiB)
	require.GreaterOrEqual(t, proc.PMem, 0.0)
	require.GreaterOrEqual(t, proc.PCPU, 0.0)
	require.False(t, proc.StartTime.IsZero())
	require.Positive(t, proc.Elapsed)
}

func TestParseSystemProcessMemoryUsesWorkingSetAndVirtualSize(t *testing.T) {
	recordSize := int(unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{}))
	buffer := make([]byte, recordSize*2)

	first := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buffer[0]))
	first.NextEntryOffset = uint32(recordSize)
	first.UniqueProcessID = 101
	first.WorkingSetSize = 12 * 1024
	first.VirtualSize = 34 * 1024
	first.PagefileUsage = 999 * 1024

	second := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buffer[recordSize]))
	second.UniqueProcessID = 202
	second.WorkingSetSize = 56 * 1024
	second.VirtualSize = 78 * 1024
	second.PagefileUsage = 888 * 1024

	metrics, ok := parseSystemProcessMemory(buffer)

	require.True(t, ok)
	require.Equal(t, windowsProcessMemory{rssKiB: 12, vszKiB: 34}, metrics[101])
	require.Equal(t, windowsProcessMemory{rssKiB: 56, vszKiB: 78}, metrics[202])
}

func TestParseSystemProcessMemoryRejectsMalformedOffset(t *testing.T) {
	recordSize := int(unsafe.Sizeof(windows.SYSTEM_PROCESS_INFORMATION{}))
	buffer := make([]byte, recordSize)
	record := (*windows.SYSTEM_PROCESS_INFORMATION)(unsafe.Pointer(&buffer[0]))
	record.NextEntryOffset = uint32(recordSize - 1)

	metrics, ok := parseSystemProcessMemory(buffer)

	require.False(t, ok)
	require.Nil(t, metrics)
}

func TestNextSystemProcessInfoSizeUsesFinalBoundedAttempt(t *testing.T) {
	next, ok := nextSystemProcessInfoSize(20<<20, 21<<20)
	require.True(t, ok)
	require.Equal(t, uint32(maxSystemProcessInfoBytes), next)

	_, ok = nextSystemProcessInfoSize(maxSystemProcessInfoBytes, maxSystemProcessInfoBytes+1)
	require.False(t, ok)
}

func TestApplyWindowsMemoryTracksUnavailablePercentage(t *testing.T) {
	var info ProcInfo
	memory := windowsProcessMemory{rssKiB: 256, vszKiB: 1024}

	applyWindowsMemory(
		&info,
		MetricRSS|MetricVSZ|MetricPMem,
		memory,
		0,
		false,
	)

	require.Equal(t, uint64(256), info.RSSKiB)
	require.Equal(t, uint64(1024), info.VSZKiB)
	require.True(t, info.Has(MetricRSS|MetricVSZ))
	require.False(t, info.Has(MetricPMem))

	applyWindowsMemory(&info, MetricPMem, memory, 1024*1024, true)

	require.InDelta(t, 25, info.PMem, 0.001)
	require.True(t, info.Has(MetricPMem))
}

func TestApplyWindowsProcessTimesUsesLifetimeAverageCPU(t *testing.T) {
	start := time.Date(2026, time.July, 24, 12, 30, 0, 0, time.Local)
	now := start.Add(10 * time.Second)
	creation := windowsFiletimeFromTicks(
		filetimeUnixEpochTicks + uint64(start.UnixNano()/100),
	)
	kernel := windowsFiletimeFromTicks(uint64((2 * time.Second) / (100 * time.Nanosecond)))
	user := windowsFiletimeFromTicks(uint64(1 * time.Second / (100 * time.Nanosecond)))
	var info ProcInfo

	applyWindowsProcessTimes(
		&info,
		MetricStartTime|MetricCPUTime|MetricElapsed|MetricPCPU,
		now,
		creation,
		kernel,
		user,
	)

	require.True(t, info.Has(MetricStartTime|MetricCPUTime|MetricElapsed|MetricPCPU))
	require.Equal(t, start.UnixNano(), info.StartTime.UnixNano())
	require.Equal(t, 3*time.Second, info.CPUTime)
	require.Equal(t, "00:00:03", info.Time)
	require.Equal(t, 10*time.Second, info.Elapsed)
	require.InDelta(t, 30, info.PCPU, 0.001)
	require.Equal(t, 30, info.CPU)
}

func TestApplyWindowsProcessTimesLeavesFailedDependenciesUnavailable(t *testing.T) {
	now := time.Date(2026, time.July, 24, 12, 30, 0, 0, time.Local)
	invalidCreation := windowsFiletimeFromTicks(filetimeUnixEpochTicks - 1)
	kernel := windowsFiletimeFromTicks(uint64(time.Second / (100 * time.Nanosecond)))
	var info ProcInfo

	applyWindowsProcessTimes(
		&info,
		MetricCPUTime|MetricElapsed|MetricPCPU,
		now,
		invalidCreation,
		kernel,
		windows.Filetime{},
	)

	require.True(t, info.Has(MetricCPUTime))
	require.False(t, info.Has(MetricElapsed))
	require.False(t, info.Has(MetricPCPU))
	require.Equal(t, time.Second, info.CPUTime)
}

func windowsFiletimeFromTicks(ticks uint64) windows.Filetime {
	return windows.Filetime{
		LowDateTime:  uint32(ticks),
		HighDateTime: uint32(ticks >> 32),
	}
}
