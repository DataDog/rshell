// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package procnetroute

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const routeScanBufInit = 4096

// readRoutes is the Linux implementation of ReadRoutes.
// It opens procPath/net/route, parses each data line, and returns UP entries.
func readRoutes(ctx context.Context, procPath string) ([]Route, error) {
	path := filepath.Join(procPath, "net", "route")
	// Intentional sandbox bypass: os.Open is used directly instead of
	// callCtx.OpenFile because procPath is hardcoded to a kernel pseudo-filesystem
	// (/proc) and is never derived from user input. See package doc for details.
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	buf := make([]byte, routeScanBufInit)
	sc.Buffer(buf, MaxLineBytes)

	var routes []Route
	firstLine := true
	totalLines := 0
	for sc.Scan() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if firstLine {
			firstLine = false
			continue // skip header row
		}
		// MaxRoutes is the memory guard: stop once enough UP routes are held.
		// This check comes first so the loop exits immediately when the memory
		// cap is reached, without consuming any more of the scan-time budget.
		if len(routes) >= MaxRoutes {
			break
		}
		// MaxTotalLines is the CPU/scan-time guard: it bounds the number of
		// lines processed when routes is not yet full. This prevents a
		// pathological file with many non-UP/malformed rows from spinning
		// indefinitely before MaxRoutes UP entries are found.
		// Note: UP routes that triggered the MaxRoutes break above do NOT
		// count toward this budget; only lines processed after that point do.
		totalLines++
		if totalLines > MaxTotalLines {
			break
		}
		r, ok := parseRouteEntry(sc.Text())
		if !ok {
			continue
		}
		if r.Flags&FlagUp == 0 {
			continue // skip routes that are not UP
		}
		routes = append(routes, r)
	}
	return routes, sc.Err()
}

// parseRouteEntry parses a single data line from /proc/net/route.
// Fields are whitespace-separated; IP/flag/mask fields are hex, metric is decimal.
func parseRouteEntry(line string) (Route, bool) {
	fields := strings.Fields(line)
	if len(fields) < 11 {
		return Route{}, false
	}

	dest, err := strconv.ParseUint(fields[1], 16, 32)
	if err != nil {
		return Route{}, false
	}
	gw, err := strconv.ParseUint(fields[2], 16, 32)
	if err != nil {
		return Route{}, false
	}
	flags, err := strconv.ParseUint(fields[3], 16, 32)
	if err != nil {
		return Route{}, false
	}
	metric, err := strconv.ParseUint(fields[6], 10, 32)
	if err != nil {
		return Route{}, false
	}
	mask, err := strconv.ParseUint(fields[7], 16, 32)
	if err != nil {
		return Route{}, false
	}
	if !IsContiguousMask(uint32(mask)) {
		// Non-contiguous masks are not valid CIDR prefixes and are silently
		// skipped. Modern Linux kernels only generate CIDR masks in
		// /proc/net/route, but legacy or manually-crafted routes may differ.
		return Route{}, false
	}

	return Route{
		Iface:  fields[0],
		Dest:   uint32(dest),
		GW:     uint32(gw),
		Flags:  uint32(flags),
		Metric: uint32(metric),
		Mask:   uint32(mask),
	}, true
}
