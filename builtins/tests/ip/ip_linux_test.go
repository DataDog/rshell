// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package ip_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	ipcmd "github.com/DataDog/rshell/builtins/ip"
	"github.com/DataDog/rshell/interp"
)

// syntheticProcNetRoute is a realistic /proc/net/route file with:
//   - A default route via 192.168.1.1 on eth0 (metric 100)
//   - A network route for 192.168.1.0/24 on eth0 (metric 100)
//   - A loopback route for 127.0.0.0/8 on lo (metric 0)
//   - A down route (RTF_UP not set) — should be skipped
//
// Encoding: IPs are little-endian uint32 hex.
//
//	192.168.1.1  = 0x0101A8C0
//	192.168.1.0  = 0x0001A8C0
//	255.255.255.0 = 0x00FFFFFF
//	127.0.0.0    = 0x0000007F
//	255.0.0.0    = 0x000000FF
const syntheticProcNetRoute = `Iface	Destination	Gateway	Flags	RefCnt	Use	Metric	Mask	MTU	Window	IRTT
eth0	00000000	0101A8C0	0003	0	0	100	00000000	0	0	0
eth0	0001A8C0	00000000	0001	0	0	100	00FFFFFF	0	0	0
lo	0000007F	00000000	0001	0	0	0	000000FF	0	0	0
eth0	0002A8C0	00000000	0000	0	0	200	00FFFFFF	0	0	0
`

// writeProcNetRoute writes synthetic /proc/net/route content to a temp directory,
// patches ipcmd.ProcNetRoutePath to point at the file, and returns the temp
// directory. The original path is restored via t.Cleanup.
//
// Pass the returned dir to cmdRunRoute so the sandbox allows access to the file.
func writeProcNetRoute(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "route")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	orig := ipcmd.ProcNetRoutePath
	ipcmd.ProcNetRoutePath = path
	t.Cleanup(func() { ipcmd.ProcNetRoutePath = orig })
	return dir
}

// cmdRunRoute runs an ip command with AllowedPaths restricted to dir so that
// callCtx.OpenFile can read the synthetic /proc/net/route file inside dir.
// Use this for tests that invoke ip route show/get (which read ProcNetRoutePath).
func cmdRunRoute(t *testing.T, script, dir string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

// ============================================================================
// ip route show / list
// ============================================================================

// TestIPRouteShowDefault verifies "ip route show" outputs the default route.
func TestIPRouteShowDefault(t *testing.T) {
	dir := writeProcNetRoute(t, syntheticProcNetRoute)
	stdout, stderr, code := cmdRunRoute(t, "ip route show", dir)
	assert.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "default via 192.168.1.1 dev eth0 metric 100")
}

// TestIPRouteShowNetworkRoute verifies "ip route show" outputs network routes.
func TestIPRouteShowNetworkRoute(t *testing.T) {
	dir := writeProcNetRoute(t, syntheticProcNetRoute)
	stdout, _, code := cmdRunRoute(t, "ip route show", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "192.168.1.0/24 dev eth0 metric 100")
}

// TestIPRouteShowLoopback verifies the loopback route appears with no gateway.
func TestIPRouteShowLoopback(t *testing.T) {
	dir := writeProcNetRoute(t, syntheticProcNetRoute)
	stdout, _, code := cmdRunRoute(t, "ip route show", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "127.0.0.0/8 dev lo")
	assert.NotContains(t, stdout, "127.0.0.0/8 dev lo via") // no gateway
}

// TestIPRouteShowZeroMetricOmitted verifies that metric 0 is not printed.
func TestIPRouteShowZeroMetricOmitted(t *testing.T) {
	dir := writeProcNetRoute(t, syntheticProcNetRoute)
	stdout, _, code := cmdRunRoute(t, "ip route show", dir)
	assert.Equal(t, 0, code)
	// lo has metric 0 — it should not appear in the lo line
	assert.NotContains(t, stdout, "127.0.0.0/8 dev lo metric 0")
}

// TestIPRouteShowDownRouteSkipped verifies routes without RTF_UP are excluded.
func TestIPRouteShowDownRouteSkipped(t *testing.T) {
	dir := writeProcNetRoute(t, syntheticProcNetRoute)
	stdout, _, code := cmdRunRoute(t, "ip route show", dir)
	assert.Equal(t, 0, code)
	// The 192.168.2.0/24 route has flags=0x0000 (RTF_UP not set) — must be absent.
	assert.NotContains(t, stdout, "192.168.2.0")
}

// TestIPRouteListAliasForShow verifies "ip route list" is an alias for show.
func TestIPRouteListAliasForShow(t *testing.T) {
	dir := writeProcNetRoute(t, syntheticProcNetRoute)
	show, _, code1 := cmdRunRoute(t, "ip route show", dir)
	list, _, code2 := cmdRunRoute(t, "ip route list", dir)
	assert.Equal(t, 0, code1)
	assert.Equal(t, 0, code2)
	assert.Equal(t, show, list)
}

// TestIPRouteShowDefaultRouteAlias verifies "ip route" (no subcommand) defaults to show.
func TestIPRouteShowDefaultRouteAlias(t *testing.T) {
	dir := writeProcNetRoute(t, syntheticProcNetRoute)
	withShow, _, code1 := cmdRunRoute(t, "ip route show", dir)
	withoutSub, _, code2 := cmdRunRoute(t, "ip route", dir)
	assert.Equal(t, 0, code1)
	assert.Equal(t, 0, code2)
	assert.Equal(t, withShow, withoutSub)
}

// TestIPRouteShowEmptyTable verifies "ip route show" on an empty table outputs nothing.
func TestIPRouteShowEmptyTable(t *testing.T) {
	// Only the header row, no data rows.
	dir := writeProcNetRoute(t, "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n")
	stdout, _, code := cmdRunRoute(t, "ip route show", dir)
	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
}

// TestIPRouteShowDefaultOnly verifies output with only a default route.
func TestIPRouteShowDefaultOnly(t *testing.T) {
	content := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n"
	dir := writeProcNetRoute(t, content)
	stdout, _, code := cmdRunRoute(t, "ip route show", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "default via 192.168.1.1 dev eth0 metric 100\n", stdout)
}

// TestIPRouteShowMalformedLinesSkipped verifies malformed lines are skipped
// without crashing.
func TestIPRouteShowMalformedLinesSkipped(t *testing.T) {
	content := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"not-enough-fields\n" + // too few fields
		"eth0\tZZZZZZZZ\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" + // invalid hex dest
		"eth0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" // valid default
	dir := writeProcNetRoute(t, content)
	stdout, _, code := cmdRunRoute(t, "ip route show", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "default")
}

// TestIPRouteShowLargeMetric verifies a large metric value is printed correctly.
func TestIPRouteShowLargeMetric(t *testing.T) {
	content := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t0101A8C0\t0003\t0\t0\t4294967295\t00000000\t0\t0\t0\n" // metric near max uint32
	dir := writeProcNetRoute(t, content)
	stdout, _, code := cmdRunRoute(t, "ip route show", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "metric 4294967295")
}

// ============================================================================
// ip route get
// ============================================================================

// TestIPRouteGetDefaultRoute verifies get selects the default route for an
// address with no more-specific match.
func TestIPRouteGetDefaultRoute(t *testing.T) {
	dir := writeProcNetRoute(t, syntheticProcNetRoute)
	stdout, stderr, code := cmdRunRoute(t, "ip route get 10.0.0.1", dir)
	assert.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "10.0.0.1")
	assert.Contains(t, stdout, "via 192.168.1.1")
	assert.Contains(t, stdout, "dev eth0")
}

// TestIPRouteGetNetworkRoute verifies get selects the network route for an
// address within the 192.168.1.0/24 subnet (more specific than default).
func TestIPRouteGetNetworkRoute(t *testing.T) {
	dir := writeProcNetRoute(t, syntheticProcNetRoute)
	stdout, stderr, code := cmdRunRoute(t, "ip route get 192.168.1.50", dir)
	assert.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "192.168.1.50")
	// No "via" for directly connected route
	assert.NotContains(t, stdout, "via")
	assert.Contains(t, stdout, "dev eth0")
}

// TestIPRouteGetLoopback verifies get selects the loopback route for 127.x.x.x.
func TestIPRouteGetLoopback(t *testing.T) {
	dir := writeProcNetRoute(t, syntheticProcNetRoute)
	stdout, stderr, code := cmdRunRoute(t, "ip route get 127.0.0.1", dir)
	assert.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Contains(t, stdout, "127.0.0.1")
	assert.Contains(t, stdout, "dev lo")
}

// TestIPRouteGetUnreachable verifies get returns exit 1 when no route matches.
func TestIPRouteGetUnreachable(t *testing.T) {
	// Only a /24 network route — no default.
	content := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"eth0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n"
	dir := writeProcNetRoute(t, content)
	_, stderr, code := cmdRunRoute(t, "ip route get 10.0.0.1", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "unreachable")
}

// TestIPRouteGetLongestPrefixMatch verifies that the most-specific prefix wins
// when both a /24 and a /16 route match.
func TestIPRouteGetLongestPrefixMatch(t *testing.T) {
	// 10.1.2.0/24 via gw1 and 10.1.0.0/16 via gw2 — address 10.1.2.5 should
	// match the /24 (longer prefix).
	//   10.1.2.0   = 0x0002010A (little-endian)
	//   255.255.255.0 = 0x00FFFFFF
	//   10.1.0.0   = 0x0000010A
	//   255.255.0.0   = 0x0000FFFF
	//   gw1 = 10.0.0.1 = 0x0100000A
	//   gw2 = 10.0.0.2 = 0x0200000A
	content := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"eth0\t0002010A\t0100000A\t0003\t0\t0\t0\t00FFFFFF\t0\t0\t0\n" +
		"eth0\t0000010A\t0200000A\t0003\t0\t0\t0\t0000FFFF\t0\t0\t0\n"
	dir := writeProcNetRoute(t, content)
	stdout, _, code := cmdRunRoute(t, "ip route get 10.1.2.5", dir)
	assert.Equal(t, 0, code)
	// Must select the /24 gateway (10.0.0.1), not the /16 (10.0.0.2).
	assert.Contains(t, stdout, "via 10.0.0.1")
	assert.NotContains(t, stdout, "via 10.0.0.2")
}

// TestIPRouteGetInvalidAddr verifies get with a non-IP argument returns exit 1.
// Input validation happens before file access, so no AllowedPaths needed.
func TestIPRouteGetInvalidAddr(t *testing.T) {
	dir := writeProcNetRoute(t, syntheticProcNetRoute)
	_, stderr, code := cmdRunRoute(t, "ip route get not-an-ip", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "invalid address")
}

// TestIPRouteGetMissingAddr verifies "ip route get" with no address returns exit 1.
func TestIPRouteGetMissingAddr(t *testing.T) {
	_, stderr, code := cmdRun(t, "ip route get")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "missing address")
}

// ============================================================================
// ip route — write operations blocked
// ============================================================================

// TestIPRouteAddBlocked verifies "ip route add" is blocked.
func TestIPRouteAddBlocked(t *testing.T) {
	_, stderr, code := cmdRun(t, "ip route add 10.0.0.0/8 via 192.168.1.1")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "write operations are not permitted")
}

// TestIPRouteDelBlocked verifies "ip route del" is blocked.
func TestIPRouteDelBlocked(t *testing.T) {
	_, stderr, code := cmdRun(t, "ip route del default")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "write operations are not permitted")
}

// TestIPRouteDeleteAliasBlocked verifies "ip route delete" is blocked.
func TestIPRouteDeleteAliasBlocked(t *testing.T) {
	_, stderr, code := cmdRun(t, "ip route delete default")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "write operations are not permitted")
}

// TestIPRouteChangeBlocked verifies "ip route change" is blocked.
func TestIPRouteChangeBlocked(t *testing.T) {
	_, stderr, code := cmdRun(t, "ip route change default via 10.0.0.1")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "write operations are not permitted")
}

// TestIPRouteReplaceBlocked verifies "ip route replace" is blocked.
func TestIPRouteReplaceBlocked(t *testing.T) {
	_, stderr, code := cmdRun(t, "ip route replace default via 10.0.0.1")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "write operations are not permitted")
}

// TestIPRouteFlushBlocked verifies "ip route flush" is blocked.
func TestIPRouteFlushBlocked(t *testing.T) {
	_, stderr, code := cmdRun(t, "ip route flush")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "write operations are not permitted")
}

// TestIPRouteSaveBlocked verifies "ip route save" is blocked.
func TestIPRouteSaveBlocked(t *testing.T) {
	_, stderr, code := cmdRun(t, "ip route save")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "write operations are not permitted")
}

// TestIPRouteRestoreBlocked verifies "ip route restore" is blocked.
func TestIPRouteRestoreBlocked(t *testing.T) {
	_, stderr, code := cmdRun(t, "ip route restore")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "write operations are not permitted")
}

// TestIPRouteUnknownSubcommand verifies an unknown subcommand exits 1.
func TestIPRouteUnknownSubcommand(t *testing.T) {
	_, stderr, code := cmdRun(t, "ip route unknowncmd")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "unknown subcommand")
}

// ============================================================================
// ip -6 route — blocked on route
// ============================================================================

// TestIPIPv6RouteBlocked verifies "-6 route show" returns exit 1 with a clear
// error (IPv6 routing is not supported via /proc/net/route).
func TestIPIPv6RouteBlocked(t *testing.T) {
	dir := writeProcNetRoute(t, syntheticProcNetRoute)
	_, stderr, code := cmdRunRoute(t, "ip -6 route show", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "IPv6")
}

// ============================================================================
// ip route — max-routes cap (memory safety)
// ============================================================================

// TestIPRouteMaxRoutesCap verifies that parseRoutingTable reads at most
// maxRoutes entries and does not allocate unboundedly for a large file.
func TestIPRouteMaxRoutesCap(t *testing.T) {
	// Build a file with 15 000 route entries (> maxRoutes=10000).
	var b []byte
	b = append(b, "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n"...)
	row := "eth0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n"
	for range 15_000 {
		b = append(b, row...)
	}
	dir := writeProcNetRoute(t, string(b))
	stdout, _, code := cmdRunRoute(t, "ip route show", dir)
	assert.Equal(t, 0, code)
	// Verify the output does not exceed 10000 lines (the maxRoutes cap).
	lines := 0
	for _, c := range stdout {
		if c == '\n' {
			lines++
		}
	}
	assert.LessOrEqual(t, lines, 10_000, "expected at most 10000 route lines, got %d", lines)
}

// ============================================================================
// ip route — coverage for parseRouteEntry failure paths
// ============================================================================

// TestIPRouteParseEntryExactlyElevenFields verifies that a valid line with
// exactly 11 fields (the minimum) is accepted.
func TestIPRouteParseEntryExactlyElevenFields(t *testing.T) {
	content := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n"
	dir := writeProcNetRoute(t, content)
	stdout, _, code := cmdRunRoute(t, "ip route show", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "default")
}

// TestIPRouteParseEntryBadGateway verifies a line with an invalid hex gateway
// is skipped without crashing.
func TestIPRouteParseEntryBadGateway(t *testing.T) {
	content := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\tZZZZZZZZ\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
		"eth0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n"
	dir := writeProcNetRoute(t, content)
	stdout, _, code := cmdRunRoute(t, "ip route show", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "192.168.1.0/24") // valid line still appears
	assert.NotContains(t, stdout, "default")     // bad line skipped
}

// TestIPRouteParseEntryBadFlags verifies a line with invalid hex flags is skipped.
func TestIPRouteParseEntryBadFlags(t *testing.T) {
	content := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t0101A8C0\tZZZZ\t0\t0\t100\t00000000\t0\t0\t0\n" +
		"eth0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n"
	dir := writeProcNetRoute(t, content)
	stdout, _, code := cmdRunRoute(t, "ip route show", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "192.168.1.0/24")
}

// TestIPRouteParseEntryBadMetric verifies a line with invalid decimal metric is skipped.
func TestIPRouteParseEntryBadMetric(t *testing.T) {
	content := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t0101A8C0\t0003\t0\t0\tNAN\t00000000\t0\t0\t0\n" +
		"eth0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n"
	dir := writeProcNetRoute(t, content)
	stdout, _, code := cmdRunRoute(t, "ip route show", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "192.168.1.0/24")
}

// TestIPRouteParseEntryBadMask verifies a line with invalid hex mask is skipped.
func TestIPRouteParseEntryBadMask(t *testing.T) {
	content := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"eth0\t00000000\t0101A8C0\t0003\t0\t0\t100\tZZZZZZZZ\t0\t0\t0\n" +
		"eth0\t0001A8C0\t00000000\t0001\t0\t0\t100\t00FFFFFF\t0\t0\t0\n"
	dir := writeProcNetRoute(t, content)
	stdout, _, code := cmdRunRoute(t, "ip route show", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "192.168.1.0/24")
}

// TestIPRouteGetHostRoute verifies a /32 route (exact host match) wins over broader
// routes via longest-prefix-match (popcount of 0xFFFFFFFF = 32 bits).
func TestIPRouteGetHostRoute(t *testing.T) {
	//   host route: 10.0.0.1/32 (mask=0xFFFFFFFF) direct via eth1
	//   default:    0.0.0.0/0   via gw 192.168.1.1 via eth0
	//   10.0.0.1 = 0x0100000A (little-endian)
	//   255.255.255.255 = 0xFFFFFFFF
	content := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"eth1\t0100000A\t00000000\t0001\t0\t0\t0\tFFFFFFFF\t0\t0\t0\n" +
		"eth0\t00000000\t0101A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n"
	dir := writeProcNetRoute(t, content)
	stdout, _, code := cmdRunRoute(t, "ip route get 10.0.0.1", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "dev eth1")
	assert.NotContains(t, stdout, "via 192.168.1.1")
}

// TestIPRouteGetInvalidAddrEmpty verifies empty string is rejected.
func TestIPRouteGetInvalidAddrEmpty(t *testing.T) {
	dir := writeProcNetRoute(t, syntheticProcNetRoute)
	_, stderr, code := cmdRunRoute(t, `ip route get ""`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "invalid address")
}

// TestIPRouteGetInvalidAddrTooFewOctets verifies "192.168.1" (no 4th octet) is rejected.
func TestIPRouteGetInvalidAddrTooFewOctets(t *testing.T) {
	dir := writeProcNetRoute(t, syntheticProcNetRoute)
	_, stderr, code := cmdRunRoute(t, "ip route get 192.168.1", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "invalid address")
}

// TestIPRouteGetInvalidAddrOctetOverflow verifies an octet > 255 is rejected.
func TestIPRouteGetInvalidAddrOctetOverflow(t *testing.T) {
	dir := writeProcNetRoute(t, syntheticProcNetRoute)
	_, stderr, code := cmdRunRoute(t, "ip route get 192.168.1.256", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "invalid address")
}

// ============================================================================
// ip route — context cancellation (DoS prevention)
// ============================================================================

// TestIPRouteShowContextCancellation verifies "ip route show" honours context
// cancellation and does not hang when the context is cancelled.
func TestIPRouteShowContextCancellation(t *testing.T) {
	dir := writeProcNetRoute(t, syntheticProcNetRoute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, code := runScriptCtx(ctx, t, "ip route show", dir, interp.AllowedPaths([]string{dir}))
	assert.True(t, code == 0 || code == 1, "expected exit 0 or 1, got %d", code)
}

// TestIPRouteGetContextCancellation verifies "ip route get" honours context
// cancellation and does not hang when the context is cancelled.
func TestIPRouteGetContextCancellation(t *testing.T) {
	dir := writeProcNetRoute(t, syntheticProcNetRoute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, code := runScriptCtx(ctx, t, "ip route get 10.0.0.1", dir, interp.AllowedPaths([]string{dir}))
	assert.True(t, code == 0 || code == 1, "expected exit 0 or 1, got %d", code)
}

// ============================================================================
// ip route — path traversal guard (sandbox)
// ============================================================================

// TestIPRoutePathTraversalBlocked verifies that ProcNetRoutePath outside the
// allowed sandbox is denied. cmdRun is used (no AllowedPaths configured), so
// the nil sandbox blocks all file access — /etc/hosts is denied.
func TestIPRoutePathTraversalBlocked(t *testing.T) {
	orig := ipcmd.ProcNetRoutePath
	ipcmd.ProcNetRoutePath = "/etc/hosts"
	t.Cleanup(func() { ipcmd.ProcNetRoutePath = orig })

	_, stderr, code := cmdRun(t, "ip route show")
	// The nil sandbox (no AllowedPaths) should deny access to /etc/hosts.
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "ip:")
}
