// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package procnet reads the Linux IPv4 routing table from /proc/net/route.
//
// This package is in builtins/internal/ and is therefore exempt from the
// builtinAllowedSymbols allowlist check. It may use OS-specific APIs freely.
//
// # Sandbox bypass
//
// ReadRoutes intentionally bypasses the AllowedPaths sandbox (callCtx.OpenFile)
// and calls os.Open directly. This is safe because procPath is always a
// kernel-managed pseudo-filesystem root (/proc by default) that is hardcoded
// by the caller — it is never derived from user-supplied input and cannot be
// redirected by a shell script. The caller is responsible for ensuring that
// procPath remains a safe, non-user-controlled path.
//
// /proc/net/route format (tab-separated, one route per line after the header):
//
//	Iface  Destination  Gateway  Flags  RefCnt  Use  Metric  Mask  MTU  Window  IRTT
//	eth0   00000000     0101A8C0 0003   0       0    100     00000000 0  0       0
//
// All IP fields are little-endian uint32 hex: 192.168.1.1 is encoded as
// 0x0101A8C0 (first octet in the least-significant byte).
package procnet

import (
	"context"
	"fmt"
)

// DefaultProcPath is the default proc filesystem root.
// ReadRoutes appends "net/route" to this path to locate the routing table.
const DefaultProcPath = "/proc"

// MaxRoutes caps the number of route entries read to prevent memory exhaustion.
const MaxRoutes = 10_000

// MaxLineBytes is the per-line buffer cap for the route-table scanner.
// Lines longer than this are skipped rather than causing an unbounded allocation.
const MaxLineBytes = 1 << 20 // 1 MiB

// Routing-table flags (from linux/route.h).
const (
	FlagUp      = uint32(0x0001) // RTF_UP
	FlagGateway = uint32(0x0002) // RTF_GATEWAY
)

// Route holds a parsed entry from /proc/net/route.
// IP fields use the same little-endian uint32 encoding as /proc/net/route:
// for 192.168.1.1 the stored value is 0x0101A8C0 and
// HexToIPStr(0x0101A8C0) returns "192.168.1.1".
type Route struct {
	Iface  string
	Dest   uint32
	GW     uint32
	Flags  uint32
	Metric uint32
	Mask   uint32
}

// ReadRoutes opens procPath/net/route and returns all UP route entries.
// procPath is the proc filesystem root (e.g. DefaultProcPath or a test override).
// It is implemented on Linux and returns an error on other platforms.
//
// Sandbox bypass: this function calls os.Open directly, bypassing the
// AllowedPaths sandbox enforced by callCtx.OpenFile. This is intentional —
// procPath must always be a safe, hardcoded kernel pseudo-filesystem path
// (e.g. /proc) that is not controllable from user scripts. Never pass a
// path derived from user input.
func ReadRoutes(ctx context.Context, procPath string) ([]Route, error) {
	return readRoutes(ctx, procPath)
}

// HexToIPStr converts a /proc/net/route little-endian uint32 to dotted-decimal.
// The encoding stores the first octet in the least-significant byte:
// 192.168.1.1 is encoded as 0x0101A8C0, and HexToIPStr(0x0101A8C0) = "192.168.1.1".
func HexToIPStr(val uint32) string {
	return fmt.Sprintf("%d.%d.%d.%d",
		val&0xFF,
		(val>>8)&0xFF,
		(val>>16)&0xFF,
		(val>>24)&0xFF,
	)
}

// Popcount returns the number of set bits in v (used for prefix length).
func Popcount(v uint32) int {
	n := 0
	for v != 0 {
		n += int(v & 1)
		v >>= 1
	}
	return n
}

// LongestPrefixMatch returns the route that best matches addr by
// longest-prefix-match with metric as a tie-breaker (lower metric wins),
// or nil if no route matches.
func LongestPrefixMatch(routes []Route, addr uint32) *Route {
	var best *Route
	bestBits := -1
	for i := range routes {
		r := &routes[i]
		if addr&r.Mask == r.Dest {
			bits := Popcount(r.Mask)
			if bits > bestBits || (bits == bestBits && r.Metric < best.Metric) {
				bestBits = bits
				best = r
			}
		}
	}
	return best
}
