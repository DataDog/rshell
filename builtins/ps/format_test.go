// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ps

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/procinfo"
)

func TestParseOutputColumnsSupportsRepeatedCommaAndSpaceLists(t *testing.T) {
	columns, err := parseOutputColumns([]string{"pid,ppid", "uid state", "pcpu,%mem,comm"})
	require.NoError(t, err)
	require.Equal(t, []outputColumn{
		{field: fieldPID},
		{field: fieldPPID},
		{field: fieldUID},
		{field: fieldState},
		{field: fieldPCPU},
		{field: fieldPMem},
		{field: fieldComm},
	}, columns)
}

func TestFormatAndSortAliases(t *testing.T) {
	columns, err := parseOutputColumns([]string{"%CPU,%MEM"})
	require.NoError(t, err)
	require.Equal(t, []outputColumn{
		{field: fieldPCPU},
		{field: fieldPMem},
	}, columns)

	keys, err := parseSortKeys("-%CPU +%MEM")
	require.NoError(t, err)
	require.Equal(t, []sortKey{
		{field: fieldPCPU, descending: true},
		{field: fieldPMem},
	}, keys)
}

func TestParseOutputColumnsRejectsUnsafeAndMalformedFields(t *testing.T) {
	tests := []struct {
		name    string
		formats []string
	}{
		{name: "argv alias", formats: []string{"args"}},
		{name: "command alias", formats: []string{"command"}},
		{name: "empty", formats: []string{""}},
		{name: "empty segment", formats: []string{"pid,,comm"}},
		{name: "custom header", formats: []string{"pid=PID"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseOutputColumns(test.formats)
			require.Error(t, err)
		})
	}
}

func TestParseSortKeys(t *testing.T) {
	keys, err := parseSortKeys("-rss,+pid comm")
	require.NoError(t, err)
	require.Equal(t, []sortKey{
		{field: fieldRSS, descending: true},
		{field: fieldPID},
		{field: fieldComm},
	}, keys)
}

func TestParseSortKeysRejectsMalformedSpecs(t *testing.T) {
	for _, spec := range []string{"", "rss,", "rss,,pid", "-", "+", "args"} {
		t.Run(spec, func(t *testing.T) {
			_, err := parseSortKeys(spec)
			require.Error(t, err)
		})
	}
}

func TestRequestedMetricsIncludesColumnsSortAndLegacyFields(t *testing.T) {
	columns := []outputColumn{{field: fieldRSS}, {field: fieldETime}}
	keys := []sortKey{{field: fieldPCPU}}
	require.Equal(
		t,
		procinfo.MetricRSS|procinfo.MetricElapsed|procinfo.MetricPCPU,
		requestedMetrics(columns, keys, false),
	)

	require.Equal(t, procinfo.MetricCPUTime, requestedMetrics(nil, nil, false))
	require.Equal(
		t,
		procinfo.MetricCPUTime|procinfo.MetricPCPU|procinfo.MetricStartTime,
		requestedMetrics(nil, nil, true),
	)
}

func TestSortProcsUsesRawValuesAndPlacesUnavailableLast(t *testing.T) {
	procs := []procinfo.ProcInfo{
		{PID: 3},
		{PID: 2, RSSKiB: 200, Available: procinfo.MetricRSS},
		{PID: 1, RSSKiB: 200, Available: procinfo.MetricRSS},
		{PID: 4, RSSKiB: 100, Available: procinfo.MetricRSS},
	}

	sortProcs(procs, []sortKey{
		{field: fieldRSS, descending: true},
		{field: fieldPID},
	})

	require.Equal(t, []int{1, 2, 4, 3}, []int{
		procs[0].PID,
		procs[1].PID,
		procs[2].PID,
		procs[3].PID,
	})
}

func TestFormatFieldDoesNotFabricateUnavailableMetrics(t *testing.T) {
	proc := procinfo.ProcInfo{
		RSSKiB: 1024,
		PCPU:   12.5,
	}
	require.Equal(t, "-", formatField(proc, fieldRSS))
	require.Equal(t, "-", formatField(proc, fieldPCPU))

	proc.Available = procinfo.MetricRSS | procinfo.MetricPCPU
	require.Equal(t, "1024", formatField(proc, fieldRSS))
	require.Equal(t, "12.5", formatField(proc, fieldPCPU))
}

func TestFullFormatDoesNotFabricateUnavailablePCPU(t *testing.T) {
	var stdout bytes.Buffer
	printProcs(&builtins.CallContext{Stdout: &stdout}, []procinfo.ProcInfo{{
		UID:   "1000",
		PID:   42,
		PPID:  1,
		STime: "-",
		TTY:   "?",
		Time:  "-",
		Cmd:   "worker",
	}}, true)

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	require.Len(t, lines, 2)
	require.Equal(t, "-", strings.Fields(lines[1])[3])
}

func TestFormatElapsed(t *testing.T) {
	require.Equal(t, "00:09", formatElapsed(9*time.Second))
	require.Equal(t, "02:03", formatElapsed(2*time.Minute+3*time.Second))
	require.Equal(t, "04:05:06", formatElapsed(4*time.Hour+5*time.Minute+6*time.Second))
	require.Equal(t, "2-04:05:06", formatElapsed(52*time.Hour+5*time.Minute+6*time.Second))
}
