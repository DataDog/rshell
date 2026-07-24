// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package procinfo

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReadProcResourceMetrics(t *testing.T) {
	procPath := t.TempDir()
	pid := 42
	pidPath := filepath.Join(procPath, "42")
	require.NoError(t, os.Mkdir(pidPath, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(pidPath, "stat"),
		[]byte("42 (safe worker) S 1 42 42 0 -1 0 0 0 0 0 200 100 0 0 20 0 1 0 100 8388608 256\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(pidPath, "status"),
		[]byte("Name:\tsafe worker\nUid:\t1000 1000 1000 1000\n"),
		0o644,
	))

	requested := MetricStartTime | MetricCPUTime | MetricElapsed |
		MetricRSS | MetricVSZ | MetricPMem | MetricPCPU
	info, err := readProc(procPath, pid, 1_000_000_000, linuxMetricInputs{
		requested:   requested,
		uptime:      1000 * time.Second,
		uptimeValid: true,
		memTotalKiB: 1_048_576,
	})
	require.NoError(t, err)

	require.Equal(t, "safe worker", info.Cmd)
	require.Equal(t, 3*time.Second, info.CPUTime)
	require.Equal(t, "00:00:03", info.Time)
	require.Equal(t, 999*time.Second, info.Elapsed)
	require.Equal(t, uint64(8192), info.VSZKiB)
	require.Equal(t, uint64(256*os.Getpagesize()/1024), info.RSSKiB)
	require.InDelta(t, 100*float64(info.RSSKiB)/1_048_576, info.PMem, 0.0001)
	require.InDelta(t, 100*3.0/999.0, info.PCPU, 0.0001)
	require.True(t, info.Has(requested))
}

func TestReadProcLeavesUnavailableDerivedMetricsUnset(t *testing.T) {
	procPath := t.TempDir()
	pidPath := filepath.Join(procPath, "7")
	require.NoError(t, os.Mkdir(pidPath, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(pidPath, "stat"),
		[]byte("7 (short) S 1 7 7 0 -1 0 0 0 0 0 0 0 0 0 20 0 1 0 100\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(filepath.Join(pidPath, "status"), []byte("Uid:\t1000\n"), 0o644))

	info, err := readProc(procPath, 7, 0, linuxMetricInputs{
		requested: MetricElapsed | MetricRSS | MetricVSZ | MetricPMem | MetricPCPU,
	})
	require.NoError(t, err)
	require.False(t, info.Has(MetricElapsed))
	require.False(t, info.Has(MetricRSS))
	require.False(t, info.Has(MetricVSZ))
	require.False(t, info.Has(MetricPMem))
	require.False(t, info.Has(MetricPCPU))
}

func TestProcAggregateMetricInputs(t *testing.T) {
	procPath := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(procPath, "uptime"), []byte("123.45 0.00\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(procPath, "meminfo"), []byte(
		fmt.Sprintf("MemFree: 1 kB\nMemTotal: %d kB\n", 16_777_216),
	), 0o644))

	uptime, err := procUptime(procPath)
	require.NoError(t, err)
	require.Equal(t, 123*time.Second+450*time.Millisecond, uptime)

	total, err := procMemTotalKiB(procPath)
	require.NoError(t, err)
	require.Equal(t, uint64(16_777_216), total)
}

func TestTicksToDurationRejectsNegativeAndOverflow(t *testing.T) {
	_, ok := ticksToDuration(-1)
	require.False(t, ok)

	_, ok = ticksToDuration(math.MaxInt64)
	require.False(t, ok)

	duration, ok := ticksToDuration(123)
	require.True(t, ok)
	require.Equal(t, time.Second+230*time.Millisecond, duration)
}

func TestProcStartTimePreservesFractionalTicksAndRejectsOverflow(t *testing.T) {
	start, ok := procStartTime(1_000_000_000, 123, nil)
	require.True(t, ok)
	require.Equal(t, time.Unix(1_000_000_001, 230_000_000), start)

	_, ok = procStartTime(math.MaxInt64, 100, nil)
	require.False(t, ok)
}
