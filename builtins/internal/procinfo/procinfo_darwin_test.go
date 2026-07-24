// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build darwin

package procinfo

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestKinfoToProcUsesCommNameOnly(t *testing.T) {
	var kp unix.KinfoProc
	kp.Proc.P_pid = 123
	kp.Eproc.Ppid = 1
	kp.Eproc.Ucred.Uid = 501
	kp.Proc.P_stat = 3
	copy(kp.Proc.P_comm[:], "safeproc")

	info := kinfoToProc(&kp)

	require.Equal(t, 123, info.PID)
	require.Equal(t, "safeproc", info.Cmd)
	require.NotContains(t, info.Cmd, "[")
	require.NotContains(t, info.Cmd, "]")
}

func TestGetByPIDsWithMetricsDarwinSelf(t *testing.T) {
	metrics := MetricStartTime | MetricCPUTime | MetricElapsed | MetricRSS | MetricVSZ | MetricPMem | MetricPCPU

	procs, err := getByPIDs(context.Background(), "", []int{os.Getpid()}, metrics)

	require.NoError(t, err)
	require.Len(t, procs, 1)
	proc := procs[0]
	require.True(t, proc.Has(MetricStartTime|MetricElapsed))
	require.False(t, proc.StartTime.IsZero())
	require.Positive(t, proc.Elapsed)

	// Sandboxed runners may deny PROC_PIDTASKINFO or the host sysctls used
	// for percentage calculations. Any values that are available must still
	// describe a consistent process sample.
	require.Equal(t, proc.Has(MetricRSS), proc.Has(MetricVSZ))
	if proc.Has(MetricPMem) {
		require.True(t, proc.Has(MetricRSS))
		require.GreaterOrEqual(t, proc.PMem, 0.0)
	}
	if proc.Has(MetricCPUTime) {
		require.Equal(t, formatDarwinCPUTime(proc.CPUTime), proc.Time)
	} else {
		require.Equal(t, "-", proc.Time)
	}
	if proc.Has(MetricPCPU) {
		require.True(t, proc.Has(MetricCPUTime|MetricElapsed))
		require.InDelta(t, float64(proc.CPUTime)*100/float64(proc.Elapsed), proc.PCPU, 0.001)
		require.Equal(t, int(proc.PCPU), proc.CPU)
	}
}

func TestReadDarwinTaskInfoSelf(t *testing.T) {
	taskInfo, err := readDarwinTaskInfo(os.Getpid())
	if errors.Is(err, syscall.EPERM) ||
		errors.Is(err, syscall.EACCES) ||
		errors.Is(err, syscall.ENOTSUP) {
		t.Skipf("process task metrics blocked by host policy: %v", err)
	}
	require.NoError(t, err)
	require.Positive(t, taskInfo.virtualSize)
	require.Positive(t, taskInfo.residentSize)
}

func TestGetByPIDsWithMetricsDarwinPermissionDeniedKeepsBaseRow(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root: PID 1 task metrics are readable")
	}
	if _, err := readDarwinTaskInfo(1); err == nil {
		t.Skip("runner is entitled to read PID 1 task metrics")
	} else if !errors.Is(err, syscall.EPERM) && !errors.Is(err, syscall.EACCES) {
		t.Skipf("PID 1 task metrics unavailable for a non-permission reason: %v", err)
	}
	metrics := MetricStartTime | MetricCPUTime | MetricElapsed | MetricRSS | MetricVSZ | MetricPMem | MetricPCPU

	procs, err := getByPIDs(context.Background(), "", []int{1}, metrics)

	require.NoError(t, err)
	require.Len(t, procs, 1)
	require.Equal(t, 1, procs[0].PID)
	require.NotEmpty(t, procs[0].Cmd)
	require.True(t, procs[0].Has(MetricStartTime|MetricElapsed))
	require.False(t, procs[0].Has(MetricCPUTime))
	require.False(t, procs[0].Has(MetricRSS))
	require.False(t, procs[0].Has(MetricVSZ))
	require.False(t, procs[0].Has(MetricPMem))
	require.False(t, procs[0].Has(MetricPCPU))
	require.Equal(t, "-", procs[0].Time)
}

func TestDecodeDarwinTaskInfo(t *testing.T) {
	buf := make([]byte, procTaskInfoSize)
	binary.LittleEndian.PutUint64(buf[0:8], 8*1024)
	binary.LittleEndian.PutUint64(buf[8:16], 4*1024)
	binary.LittleEndian.PutUint64(buf[16:24], 12_000_000)
	binary.LittleEndian.PutUint64(buf[24:32], 8_000_000)

	info, err := decodeDarwinTaskInfo(buf)

	require.NoError(t, err)
	require.Equal(t, uint64(8*1024), info.virtualSize)
	require.Equal(t, uint64(4*1024), info.residentSize)
	require.Equal(t, uint64(12_000_000), info.totalUser)
	require.Equal(t, uint64(8_000_000), info.totalSystem)
}

func TestDecodeDarwinTaskInfoRejectsShortBuffer(t *testing.T) {
	_, err := decodeDarwinTaskInfo(make([]byte, procTaskInfoSize-1))

	require.Error(t, err)
}

func TestPopulateDarwinMetrics(t *testing.T) {
	now := time.Unix(20, 0)
	info := ProcInfo{
		PID:       123,
		Cmd:       "safeproc",
		StartTime: now.Add(-10 * time.Second),
	}
	taskInfo := &darwinTaskInfo{
		virtualSize:  8*1024 + 511,
		residentSize: 4 * 1024,
		totalUser:    24_000_000,
		totalSystem:  24_000_000,
	}
	metrics := MetricStartTime | MetricCPUTime | MetricElapsed | MetricRSS | MetricVSZ | MetricPMem | MetricPCPU
	metricCtx := darwinMetricContext{
		now:               now,
		totalMemoryBytes:  8 * 1024,
		timebaseFrequency: 24_000_000,
	}

	populateDarwinMetrics(&info, metrics, metricCtx, taskInfo)

	require.True(t, info.Has(metrics))
	require.Equal(t, 10*time.Second, info.Elapsed)
	require.Equal(t, 2*time.Second, info.CPUTime)
	require.Equal(t, uint64(4), info.RSSKiB)
	require.Equal(t, uint64(8), info.VSZKiB)
	require.InDelta(t, 50, info.PMem, 0.001)
	require.InDelta(t, 20, info.PCPU, 0.001)
	require.Equal(t, 20, info.CPU)
	require.Equal(t, "00:00:02", info.Time)
}

func TestPopulateDarwinMetricsKeepsTaskMetricsUnavailable(t *testing.T) {
	now := time.Unix(20, 0)
	info := ProcInfo{
		PID:       123,
		Cmd:       "safeproc",
		StartTime: now.Add(-10 * time.Second),
	}
	metrics := MetricStartTime | MetricCPUTime | MetricElapsed | MetricRSS | MetricVSZ | MetricPMem | MetricPCPU

	// A nil taskInfo represents the common EPERM or process-exited case.
	populateDarwinMetrics(&info, metrics, darwinMetricContext{now: now}, nil)

	require.True(t, info.Has(MetricStartTime|MetricElapsed))
	require.False(t, info.Has(MetricCPUTime))
	require.False(t, info.Has(MetricRSS))
	require.False(t, info.Has(MetricVSZ))
	require.False(t, info.Has(MetricPMem))
	require.False(t, info.Has(MetricPCPU))
	require.Equal(t, 123, info.PID)
	require.Equal(t, "safeproc", info.Cmd)
}

func TestPopulateDarwinMetricsDoesNotFabricateMissingHostCounters(t *testing.T) {
	now := time.Unix(20, 0)
	info := ProcInfo{StartTime: now.Add(-10 * time.Second)}
	taskInfo := &darwinTaskInfo{
		virtualSize:  8 * 1024,
		residentSize: 4 * 1024,
		totalUser:    1,
		totalSystem:  1,
	}
	metrics := MetricCPUTime | MetricRSS | MetricVSZ | MetricPMem | MetricPCPU

	// A zero memory total or timebase frequency means the corresponding
	// sysctl was unavailable. RSS/VSZ are still genuine task measurements.
	populateDarwinMetrics(&info, metrics, darwinMetricContext{now: now}, taskInfo)

	require.True(t, info.Has(MetricRSS|MetricVSZ))
	require.False(t, info.Has(MetricCPUTime))
	require.False(t, info.Has(MetricPMem))
	require.False(t, info.Has(MetricPCPU))
}

func TestDarwinTicksToDuration(t *testing.T) {
	got, ok := darwinTicksToDuration(36_000_000, 24_000_000)

	require.True(t, ok)
	require.Equal(t, 1500*time.Millisecond, got)

	_, ok = darwinTicksToDuration(1, 0)
	require.False(t, ok)

	_, ok = darwinTicksToDuration(^uint64(0), 1)
	require.False(t, ok)
}
