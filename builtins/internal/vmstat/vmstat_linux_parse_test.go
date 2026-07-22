// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package vmstat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleProcStat = `cpu  201313 3153 46753 1049295 3200 0 1465 0 0 0
cpu0 100000 1500 20000 500000 1600 0 700 0 0 0
intr 1234567 0 0 0
ctxt 987654
btime 1600000000
processes 12345
procs_running 2
procs_blocked 1
softirq 555555 0 1 2 3
`

const sampleMeminfo = `MemTotal:        8000000 kB
MemFree:         2000000 kB
MemAvailable:    4000000 kB
Buffers:          100000 kB
Cached:          1500000 kB
SwapCached:            0 kB
Active:          3000000 kB
Inactive:        1000000 kB
SwapTotal:       2000000 kB
SwapFree:        1900000 kB
`

const sampleVmstat = `nr_free_pages 500000
pgpgin 123456
pgpgout 654321
pswpin 10
pswpout 20
pgfault 999
`

const sampleLoadavg = `0.12 0.34 0.56 1/234 5678
`

const sampleUptime = `12345.67 98765.43
`

func writeTemp(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

func TestReadImpl_HappyPath(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "stat", sampleProcStat)
	writeTemp(t, dir, "meminfo", sampleMeminfo)
	writeTemp(t, dir, "vmstat", sampleVmstat)
	writeTemp(t, dir, "loadavg", sampleLoadavg)
	writeTemp(t, dir, "uptime", sampleUptime)

	st, err := readImpl(context.Background(), dir)
	require.NoError(t, err)
	assert.Equal(t, AllFields, st.Partial)
	assert.InDelta(t, 12345.67, st.Uptime, 1e-9)

	assert.EqualValues(t, 2, st.ProcsRunning)
	assert.EqualValues(t, 1, st.ProcsBlocked)
	assert.EqualValues(t, 1234567, st.Interrupts)
	assert.EqualValues(t, 987654, st.ContextSwitches)
	assert.EqualValues(t, 201313, st.CPUUser)
	assert.EqualValues(t, 3153, st.CPUNice)
	assert.EqualValues(t, 46753, st.CPUSystem)
	assert.EqualValues(t, 1049295, st.CPUIdle)
	assert.EqualValues(t, 3200, st.CPUIOWait)
	assert.EqualValues(t, 1465, st.CPUSoftIRQ)

	assert.EqualValues(t, 8000000*1024, st.MemTotal)
	assert.EqualValues(t, 2000000*1024, st.MemFree)
	assert.EqualValues(t, 100000*1024, st.MemBuffers)
	assert.EqualValues(t, 1500000*1024, st.MemCached)
	assert.EqualValues(t, 3000000*1024, st.MemActive)
	assert.EqualValues(t, 1000000*1024, st.MemInactive)
	assert.EqualValues(t, 2000000*1024, st.SwapTotal)
	assert.EqualValues(t, 1900000*1024, st.SwapFree)

	assert.EqualValues(t, 123456, st.PagesInKB)
	assert.EqualValues(t, 654321, st.PagesOutKB)
	assert.EqualValues(t, 10, st.SwapInPages)
	assert.EqualValues(t, 20, st.SwapOutPages)

	assert.InDelta(t, 0.12, st.LoadAvg1, 1e-9)
	assert.InDelta(t, 0.34, st.LoadAvg5, 1e-9)
	assert.InDelta(t, 0.56, st.LoadAvg15, 1e-9)

	assert.EqualValues(t, clockTicksPerSec, st.ClockTicksPerSec)
	assert.Greater(t, st.PageSize, uint64(0))
}

func TestReadImpl_PartialWhenFilesMissing(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "meminfo", sampleMeminfo)
	// stat, vmstat, loadavg are absent.

	st, err := readImpl(context.Background(), dir)
	require.NoError(t, err)
	assert.Equal(t, FieldMemory|FieldMemoryDetail|FieldSwap, st.Partial)
	assert.EqualValues(t, 0, st.ProcsRunning, "no /proc/stat means procs fields stay zero")
}

func TestReadImpl_AllFilesMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := readImpl(context.Background(), dir)
	assert.Error(t, err)
}

func TestReadImpl_ContextCancelled(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "stat", sampleProcStat)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := readImpl(ctx, dir)
	assert.Error(t, err)
}

func TestReadProcStat_MalformedCPULine(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "stat", "cpu  1 2\nprocs_running 3\n")

	var st Stats
	foundProcs, foundSystem, foundCPU, err := readProcStat(context.Background(), filepath.Join(dir, "stat"), &st)
	require.NoError(t, err)
	assert.EqualValues(t, 1, st.CPUUser)
	assert.EqualValues(t, 2, st.CPUNice)
	assert.EqualValues(t, 0, st.CPUSystem, "missing trailing fields default to 0, not a panic")
	assert.EqualValues(t, 3, st.ProcsRunning)
	assert.True(t, foundProcs)
	assert.True(t, foundCPU)
	assert.False(t, foundSystem, "no intr/ctxt line means the system group was not actually read")
}

func TestReadImpl_PartialGroupsAreIndependentPerLine(t *testing.T) {
	dir := t.TempDir()
	// /proc/stat has only an "intr " line: no "cpu " line, no procs_*
	// lines. FieldCPU/FieldProcs must NOT be set, or the CPU/procs
	// columns would render a fabricated "0" instead of "-".
	writeTemp(t, dir, "stat", "intr 42\n")

	st, err := readImpl(context.Background(), dir)
	require.NoError(t, err)
	assert.Equal(t, FieldSystem, st.Partial)
	assert.EqualValues(t, 42, st.Interrupts)
}

func TestParseMeminfoLine(t *testing.T) {
	key, kb, ok := parseMeminfoLine("MemTotal:        8000000 kB")
	assert.True(t, ok)
	assert.Equal(t, "MemTotal", key)
	assert.EqualValues(t, 8000000, kb)

	_, _, ok = parseMeminfoLine("garbage line with no colon")
	assert.False(t, ok)

	_, _, ok = parseMeminfoLine("MemTotal: notanumber kB")
	assert.False(t, ok)
}

func TestScanBounded_RespectsLineCeiling(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("procs_running 1\n", maxLines+10)
	writeTemp(t, dir, "stat", content)

	seen := 0
	err := scanBounded(context.Background(), filepath.Join(dir, "stat"), func(string) { seen++ })
	require.NoError(t, err)
	assert.LessOrEqual(t, seen, maxLines)
}

func TestReadProcUptime(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "uptime", sampleUptime)

	var st Stats
	err := readProcUptime(context.Background(), filepath.Join(dir, "uptime"), &st)
	require.NoError(t, err)
	assert.InDelta(t, 12345.67, st.Uptime, 1e-9)
}

func TestReadProcUptime_NegativeRejected(t *testing.T) {
	dir := t.TempDir()
	writeTemp(t, dir, "uptime", "-5 0\n")

	var st Stats
	err := readProcUptime(context.Background(), filepath.Join(dir, "uptime"), &st)
	assert.Error(t, err)
}

func TestScanBounded_MissingFile(t *testing.T) {
	err := scanBounded(context.Background(), "/nonexistent/path/for/vmstat/test", func(string) {})
	assert.Error(t, err)
}
