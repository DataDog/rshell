// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tcpdump_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func runScript(t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScript(t, script, dir, opts...)
}

func runScriptCtx(ctx context.Context, t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, opts...)
}

func cmdRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return runScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

// writePcap writes a minimal pcap file (little-endian) to path with
// the given raw Ethernet packets (including Ethernet header).
// Timestamps start at t0 and advance by 1 second per packet.
func writePcap(t *testing.T, path string, t0 time.Time, packets [][]byte) {
	t.Helper()
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	// Global header (24 bytes).
	// pcap little-endian magic: write 0xa1b2c3d4 as LE → bytes d4 c3 b2 a1 on disk.
	const (
		magicLE    uint32 = 0xa1b2c3d4
		versionMaj        = 2
		versionMin        = 4
		snaplen           = 65535
		linkEth           = 1 // LINKTYPE_ETHERNET
	)
	hdr := make([]byte, 24)
	binary.LittleEndian.PutUint32(hdr[0:], magicLE)
	binary.LittleEndian.PutUint16(hdr[4:], versionMaj)
	binary.LittleEndian.PutUint16(hdr[6:], versionMin)
	binary.LittleEndian.PutUint32(hdr[8:], 0)  // thiszone
	binary.LittleEndian.PutUint32(hdr[12:], 0) // sigfigs
	binary.LittleEndian.PutUint32(hdr[16:], snaplen)
	binary.LittleEndian.PutUint32(hdr[20:], linkEth)
	_, err = f.Write(hdr)
	require.NoError(t, err)

	// Write each packet record.
	for i, pkt := range packets {
		ts := t0.Add(time.Duration(i) * time.Second)
		rec := make([]byte, 16+len(pkt))
		binary.LittleEndian.PutUint32(rec[0:], uint32(ts.Unix()))
		usec := uint32(ts.Nanosecond() / 1000)
		binary.LittleEndian.PutUint32(rec[4:], usec)
		binary.LittleEndian.PutUint32(rec[8:], uint32(len(pkt)))
		binary.LittleEndian.PutUint32(rec[12:], uint32(len(pkt)))
		copy(rec[16:], pkt)
		_, err = f.Write(rec)
		require.NoError(t, err)
	}
}

// buildTCPSYN builds a minimal Ethernet + IPv4 + TCP SYN frame.
// srcIP and dstIP are 4-byte big-endian IPv4 addresses.
// srcPort and dstPort are port numbers.
func buildTCPSYN(srcMAC, dstMAC [6]byte, srcIP, dstIP [4]byte, srcPort, dstPort uint16) []byte {
	var buf bytes.Buffer

	// Ethernet header (14 bytes).
	buf.Write(dstMAC[:])
	buf.Write(srcMAC[:])
	// EtherType 0x0800 = IPv4
	buf.Write([]byte{0x08, 0x00})

	// IPv4 header (20 bytes, no options).
	ipStart := buf.Len()
	ipHdr := make([]byte, 20)
	ipHdr[0] = 0x45                               // Version=4, IHL=5
	ipHdr[1] = 0                                  // DSCP/ECN
	binary.BigEndian.PutUint16(ipHdr[2:], 40)     // Total length = 20 (IP) + 20 (TCP)
	binary.BigEndian.PutUint16(ipHdr[4:], 0x1234) // ID
	binary.BigEndian.PutUint16(ipHdr[6:], 0x4000) // DF flag
	ipHdr[8] = 64                                 // TTL
	ipHdr[9] = 6                                  // Protocol = TCP
	copy(ipHdr[12:], srcIP[:])
	copy(ipHdr[16:], dstIP[:])
	// Compute IP checksum.
	binary.BigEndian.PutUint16(ipHdr[10:], checksum(ipHdr))
	buf.Write(ipHdr)
	_ = ipStart

	// TCP header (20 bytes, no options).
	tcpHdr := make([]byte, 20)
	binary.BigEndian.PutUint16(tcpHdr[0:], srcPort)
	binary.BigEndian.PutUint16(tcpHdr[2:], dstPort)
	binary.BigEndian.PutUint32(tcpHdr[4:], 1000)   // seq
	binary.BigEndian.PutUint32(tcpHdr[8:], 0)      // ack
	tcpHdr[12] = 0x50                              // data offset = 5 (20 bytes)
	tcpHdr[13] = 0x02                              // SYN flag
	binary.BigEndian.PutUint16(tcpHdr[14:], 65535) // window
	buf.Write(tcpHdr)

	return buf.Bytes()
}

// buildUDP builds a minimal Ethernet + IPv4 + UDP frame.
func buildUDP(srcMAC, dstMAC [6]byte, srcIP, dstIP [4]byte, srcPort, dstPort uint16, payload []byte) []byte {
	var buf bytes.Buffer

	buf.Write(dstMAC[:])
	buf.Write(srcMAC[:])
	buf.Write([]byte{0x08, 0x00})

	udpLen := 8 + len(payload)
	totalLen := 20 + udpLen

	ipHdr := make([]byte, 20)
	ipHdr[0] = 0x45
	binary.BigEndian.PutUint16(ipHdr[2:], uint16(totalLen))
	binary.BigEndian.PutUint16(ipHdr[4:], 0x5678)
	ipHdr[8] = 64
	ipHdr[9] = 17 // UDP
	copy(ipHdr[12:], srcIP[:])
	copy(ipHdr[16:], dstIP[:])
	binary.BigEndian.PutUint16(ipHdr[10:], checksum(ipHdr))
	buf.Write(ipHdr)

	udpHdr := make([]byte, 8)
	binary.BigEndian.PutUint16(udpHdr[0:], srcPort)
	binary.BigEndian.PutUint16(udpHdr[2:], dstPort)
	binary.BigEndian.PutUint16(udpHdr[4:], uint16(udpLen))
	buf.Write(udpHdr)
	buf.Write(payload)

	return buf.Bytes()
}

// buildICMP builds a minimal Ethernet + IPv4 + ICMP Echo Request frame.
func buildICMP(srcMAC, dstMAC [6]byte, srcIP, dstIP [4]byte, id, seq uint16) []byte {
	var buf bytes.Buffer

	buf.Write(dstMAC[:])
	buf.Write(srcMAC[:])
	buf.Write([]byte{0x08, 0x00})

	icmpPayload := []byte("hello")
	icmpLen := 8 + len(icmpPayload)
	totalLen := 20 + icmpLen

	ipHdr := make([]byte, 20)
	ipHdr[0] = 0x45
	binary.BigEndian.PutUint16(ipHdr[2:], uint16(totalLen))
	binary.BigEndian.PutUint16(ipHdr[4:], 0xabcd)
	ipHdr[8] = 64
	ipHdr[9] = 1 // ICMP
	copy(ipHdr[12:], srcIP[:])
	copy(ipHdr[16:], dstIP[:])
	binary.BigEndian.PutUint16(ipHdr[10:], checksum(ipHdr))
	buf.Write(ipHdr)

	icmpHdr := make([]byte, 8)
	icmpHdr[0] = 8 // Echo Request
	icmpHdr[1] = 0
	binary.BigEndian.PutUint16(icmpHdr[4:], id)
	binary.BigEndian.PutUint16(icmpHdr[6:], seq)
	// Compute ICMP checksum over header + payload.
	icmpFull := append(icmpHdr, icmpPayload...)
	binary.BigEndian.PutUint16(icmpFull[2:], checksum(icmpFull))
	buf.Write(icmpFull)

	return buf.Bytes()
}

// checksum computes the Internet checksum for a byte slice.
func checksum(data []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(data); i += 2 {
		sum += uint32(data[i])<<8 | uint32(data[i+1])
	}
	if len(data)%2 != 0 {
		sum += uint32(data[len(data)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

var (
	mac1 = [6]byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55}
	mac2 = [6]byte{0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	ip1  = [4]byte{192, 168, 1, 1}
	ip2  = [4]byte{192, 168, 1, 2}
)

// t0 is a fixed reference time used for deterministic timestamps in tests.
var t0 = time.Date(2024, 1, 15, 10, 30, 00, 0, time.UTC)

// setupDir creates a temp directory with a pcap file "capture.pcap"
// containing the given packets.
func setupDir(t *testing.T, packets [][]byte) string {
	t.Helper()
	dir := t.TempDir()
	writePcap(t, filepath.Join(dir, "capture.pcap"), t0, packets)
	return dir
}

// ---------------------------------------------------------------------------
// Basic output tests
// ---------------------------------------------------------------------------

func TestTcpdumpRequiresRFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "tcpdump", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tcpdump:")
	assert.Contains(t, stderr, "-r")
}

func TestTcpdumpMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "tcpdump -r nonexistent.pcap", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tcpdump:")
}

func TestTcpdumpEmptyFile(t *testing.T) {
	dir := setupDir(t, nil)
	stdout, stderr, code := cmdRun(t, "tcpdump -r capture.pcap", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "", stdout)
}

func TestTcpdumpSingleTCPPacket(t *testing.T) {
	pkt := buildTCPSYN(mac1, mac2, ip1, ip2, 12345, 80)
	dir := setupDir(t, [][]byte{pkt})
	stdout, stderr, code := cmdRun(t, "tcpdump -r capture.pcap", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Contains(t, stdout, "IP")
	assert.Contains(t, stdout, "192.168.1.1")
	assert.Contains(t, stdout, "192.168.1.2")
	assert.Contains(t, stdout, "Flags [S]")
}

func TestTcpdumpUDPPacket(t *testing.T) {
	pkt := buildUDP(mac1, mac2, ip1, ip2, 1234, 53, []byte("query"))
	dir := setupDir(t, [][]byte{pkt})
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "UDP")
	assert.Contains(t, stdout, "192.168.1.1.1234")
	assert.Contains(t, stdout, "192.168.1.2.53")
}

func TestTcpdumpICMPPacket(t *testing.T) {
	pkt := buildICMP(mac1, mac2, ip1, ip2, 1, 1)
	dir := setupDir(t, [][]byte{pkt})
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "ICMP")
	assert.Contains(t, stdout, "192.168.1.1")
	assert.Contains(t, stdout, "192.168.1.2")
}

// ---------------------------------------------------------------------------
// Count flag (-c)
// ---------------------------------------------------------------------------

func TestTcpdumpCountFlag(t *testing.T) {
	pkts := [][]byte{
		buildTCPSYN(mac1, mac2, ip1, ip2, 1, 80),
		buildTCPSYN(mac1, mac2, ip1, ip2, 2, 80),
		buildTCPSYN(mac1, mac2, ip1, ip2, 3, 80),
	}
	dir := setupDir(t, pkts)
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap -c 2", dir)
	assert.Equal(t, 0, code)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	assert.Equal(t, 2, len(lines))
}

func TestTcpdumpCountZeroMeansAll(t *testing.T) {
	pkts := [][]byte{
		buildTCPSYN(mac1, mac2, ip1, ip2, 1, 80),
		buildTCPSYN(mac1, mac2, ip1, ip2, 2, 80),
	}
	dir := setupDir(t, pkts)
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap -c 0", dir)
	assert.Equal(t, 0, code)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	assert.Equal(t, 2, len(lines))
}

// ---------------------------------------------------------------------------
// Timestamp flags (-t, -tt, -ttt, -tttt)
// ---------------------------------------------------------------------------

func TestTcpdumpNoTimestamp(t *testing.T) {
	pkt := buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80)
	dir := setupDir(t, [][]byte{pkt})
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap -t", dir)
	assert.Equal(t, 0, code)
	assert.True(t, strings.HasPrefix(stdout, "IP "), "expected no timestamp, got: %q", stdout)
}

func TestTcpdumpUnixTimestamp(t *testing.T) {
	pkt := buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80)
	dir := setupDir(t, [][]byte{pkt})
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap -tt", dir)
	assert.Equal(t, 0, code)
	// Should start with Unix timestamp digits
	assert.Regexp(t, `^\d+\.\d{6} `, stdout)
}

func TestTcpdumpDeltaTimestamp(t *testing.T) {
	pkts := [][]byte{
		buildTCPSYN(mac1, mac2, ip1, ip2, 1, 80),
		buildTCPSYN(mac1, mac2, ip1, ip2, 2, 80),
	}
	dir := setupDir(t, pkts)
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap -ttt", dir)
	assert.Equal(t, 0, code)
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	require.Len(t, lines, 2)
	// First line starts with " 0." (zero delta) — do not TrimSpace as it strips the leading space
	assert.True(t, strings.HasPrefix(strings.TrimLeft(lines[0], " "), "0."), "expected zero delta, got: %q", lines[0])
	// Second line delta should be ~1 second
	assert.True(t, strings.HasPrefix(strings.TrimLeft(lines[1], " "), "1."), "expected ~1s delta, got: %q", lines[1])
}

func TestTcpdumpDateTimestamp(t *testing.T) {
	pkt := buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80)
	dir := setupDir(t, [][]byte{pkt})
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap -tttt", dir)
	assert.Equal(t, 0, code)
	// Should match YYYY-MM-DD HH:MM:SS.ffffff format
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{6} `, stdout)
}

// ---------------------------------------------------------------------------
// Verbosity flag (-v, -vv)
// ---------------------------------------------------------------------------

func TestTcpdumpVerboseShowsTTL(t *testing.T) {
	pkt := buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80)
	dir := setupDir(t, [][]byte{pkt})
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap -v", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "ttl 64")
	assert.Contains(t, stdout, "proto TCP")
}

func TestTcpdumpVVShowsFlags(t *testing.T) {
	pkt := buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80)
	dir := setupDir(t, [][]byte{pkt})
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap -vv", dir)
	assert.Equal(t, 0, code)
	// -vv includes offset and flags in IP header
	assert.Contains(t, stdout, "DF") // DF flag set in our test packet
}

// ---------------------------------------------------------------------------
// Quiet flag (-q)
// ---------------------------------------------------------------------------

func TestTcpdumpQuietSuppressesDetail(t *testing.T) {
	pkt := buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80)
	dir := setupDir(t, [][]byte{pkt})
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap -q", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Flags [S]")
	// Quiet should show length but not detailed options
	assert.Contains(t, stdout, "length")
}

// ---------------------------------------------------------------------------
// Link-layer header (-e)
// ---------------------------------------------------------------------------

func TestTcpdumpLinkLayer(t *testing.T) {
	pkt := buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80)
	dir := setupDir(t, [][]byte{pkt})
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap -e", dir)
	assert.Equal(t, 0, code)
	// Should show MAC addresses
	assert.Contains(t, stdout, "00:11:22:33:44:55")
	assert.Contains(t, stdout, "aa:bb:cc:dd:ee:ff")
}

// ---------------------------------------------------------------------------
// Hex dump flags (-x, -xx, -X, -XX, -A)
// ---------------------------------------------------------------------------

func TestTcpdumpHexDump(t *testing.T) {
	pkt := buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80)
	dir := setupDir(t, [][]byte{pkt})
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap -x", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "0x0000:")
}

func TestTcpdumpHexAsciiDump(t *testing.T) {
	pkt := buildUDP(mac1, mac2, ip1, ip2, 1234, 53, []byte("HELLO"))
	dir := setupDir(t, [][]byte{pkt})
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap -X", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "0x0000:")
	// "HELLO" payload is shown in ASCII column; it may span two hex lines so check partial.
	assert.Contains(t, stdout, "HELL")
}

func TestTcpdumpASCIIDump(t *testing.T) {
	pkt := buildUDP(mac1, mac2, ip1, ip2, 1234, 53, []byte("HELLO"))
	dir := setupDir(t, [][]byte{pkt})
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap -A", dir)
	assert.Equal(t, 0, code)
	// "HELLO" payload spans two ASCII lines; check partial.
	assert.Contains(t, stdout, "HELL")
}

func TestTcpdumpHexWithLinkHdr(t *testing.T) {
	pkt := buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80)
	dir := setupDir(t, [][]byte{pkt})
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap -xx", dir)
	assert.Equal(t, 0, code)
	// -xx includes link-layer header, so MAC bytes should appear in hex
	assert.Contains(t, stdout, "0x0000:")
	// First line should start with destination MAC (aabbccddeeff)
	assert.Contains(t, stdout, "aabb")
}

// ---------------------------------------------------------------------------
// Snaplen flag (-s)
// ---------------------------------------------------------------------------

func TestTcpdumpSnaplen(t *testing.T) {
	pkt := buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80)
	dir := setupDir(t, [][]byte{pkt})
	// With -s 10 and -x, we should only show 10 bytes of hex
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap -x -s 10", dir)
	assert.Equal(t, 0, code)
	// Only 1 line of hex (10 bytes < 16 bytes per line)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	hexLines := 0
	for _, l := range lines {
		if strings.Contains(l, "0x0000:") {
			hexLines++
		}
	}
	assert.Equal(t, 1, hexLines)
}

// ---------------------------------------------------------------------------
// Filter expression tests
// ---------------------------------------------------------------------------

func TestTcpdumpFilterHost(t *testing.T) {
	pkts := [][]byte{
		buildTCPSYN(mac1, mac2, ip1, ip2, 1, 80), // 192.168.1.1 → 192.168.1.2
		buildTCPSYN(mac2, mac1, ip2, ip1, 2, 80), // 192.168.1.2 → 192.168.1.1
	}
	dir := setupDir(t, pkts)
	// Match only packets involving 192.168.1.1
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap host 192.168.1.1", dir)
	assert.Equal(t, 0, code)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	assert.Len(t, lines, 2, "both packets involve 192.168.1.1")
}

func TestTcpdumpFilterSrcHost(t *testing.T) {
	pkts := [][]byte{
		buildTCPSYN(mac1, mac2, ip1, ip2, 1, 80), // src=192.168.1.1
		buildTCPSYN(mac2, mac1, ip2, ip1, 2, 80), // src=192.168.1.2
	}
	dir := setupDir(t, pkts)
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap src host 192.168.1.1", dir)
	assert.Equal(t, 0, code)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	assert.Len(t, lines, 1)
	assert.Contains(t, stdout, "192.168.1.1")
}

func TestTcpdumpFilterPort(t *testing.T) {
	pkts := [][]byte{
		buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80),  // port 80
		buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 443), // port 443
	}
	dir := setupDir(t, pkts)
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap port 80", dir)
	assert.Equal(t, 0, code)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	assert.Len(t, lines, 1)
	assert.Contains(t, stdout, ".80:")
}

func TestTcpdumpFilterTCP(t *testing.T) {
	pkts := [][]byte{
		buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80),
		buildUDP(mac1, mac2, ip1, ip2, 1234, 53, []byte("q")),
	}
	dir := setupDir(t, pkts)
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap tcp", dir)
	assert.Equal(t, 0, code)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	assert.Len(t, lines, 1)
	assert.NotContains(t, stdout, "UDP")
}

func TestTcpdumpFilterAndOr(t *testing.T) {
	pkts := [][]byte{
		buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80),           // port 80
		buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 443),          // port 443
		buildUDP(mac1, mac2, ip1, ip2, 1234, 53, []byte("q")), // port 53
	}
	dir := setupDir(t, pkts)
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap port 80 or port 443", dir)
	assert.Equal(t, 0, code)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	assert.Len(t, lines, 2)
}

func TestTcpdumpFilterNot(t *testing.T) {
	pkts := [][]byte{
		buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80),
		buildUDP(mac1, mac2, ip1, ip2, 1234, 53, []byte("q")),
	}
	dir := setupDir(t, pkts)
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap not tcp", dir)
	assert.Equal(t, 0, code)
	assert.NotContains(t, stdout, "Flags")
	assert.Contains(t, stdout, "UDP")
}

func TestTcpdumpFilterInvalidExpr(t *testing.T) {
	dir := t.TempDir()
	writePcap(t, filepath.Join(dir, "capture.pcap"), t0, nil)
	_, stderr, code := cmdRun(t, "tcpdump -r capture.pcap gobbledygook", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tcpdump:")
}

func TestTcpdumpFilterInvalidHost(t *testing.T) {
	dir := t.TempDir()
	writePcap(t, filepath.Join(dir, "capture.pcap"), t0, nil)
	_, stderr, code := cmdRun(t, "tcpdump -r capture.pcap host not-an-ip", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tcpdump:")
}

func TestTcpdumpFilterInvalidPort(t *testing.T) {
	dir := t.TempDir()
	writePcap(t, filepath.Join(dir, "capture.pcap"), t0, nil)
	_, stderr, code := cmdRun(t, "tcpdump -r capture.pcap port 99999", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tcpdump:")
}

// ---------------------------------------------------------------------------
// Rejected flags
// ---------------------------------------------------------------------------

func TestTcpdumpRejectsLiveCapture(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "tcpdump -i eth0", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tcpdump:")
}

func TestTcpdumpRejectsWriteFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "tcpdump -w output.pcap", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tcpdump:")
}

func TestTcpdumpRejectsExecFlag(t *testing.T) {
	dir := t.TempDir()
	_, _, code := cmdRun(t, "tcpdump -z /bin/sh", dir)
	assert.Equal(t, 1, code)
}

func TestTcpdumpRejectsPrivEscFlag(t *testing.T) {
	dir := t.TempDir()
	_, _, code := cmdRun(t, "tcpdump -Z root", dir)
	assert.Equal(t, 1, code)
}

func TestTcpdumpRejectsUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "tcpdump --follow", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tcpdump:")
}

// ---------------------------------------------------------------------------
// Help flag
// ---------------------------------------------------------------------------

func TestTcpdumpHelp(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, "tcpdump --help", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage:")
	assert.Contains(t, stdout, "-r")
}

func TestTcpdumpHelpShort(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, "tcpdump -h", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage:")
}

// ---------------------------------------------------------------------------
// Invalid file
// ---------------------------------------------------------------------------

func TestTcpdumpInvalidPcapFile(t *testing.T) {
	dir := t.TempDir()
	// Write a file with invalid content (not pcap magic)
	err := os.WriteFile(filepath.Join(dir, "bad.pcap"), []byte("this is not a pcap file"), 0644)
	require.NoError(t, err)
	_, stderr, code := cmdRun(t, "tcpdump -r bad.pcap", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tcpdump:")
}

func TestTcpdumpTooShortFile(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "tiny.pcap"), []byte{0x01, 0x02}, 0644)
	require.NoError(t, err)
	_, stderr, code := cmdRun(t, "tcpdump -r tiny.pcap", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tcpdump:")
}

// ---------------------------------------------------------------------------
// Security: bounded operation
// ---------------------------------------------------------------------------

func TestTcpdumpCountNegativeRejected(t *testing.T) {
	dir := t.TempDir()
	writePcap(t, filepath.Join(dir, "capture.pcap"), t0, nil)
	_, stderr, code := cmdRun(t, "tcpdump -r capture.pcap -c -1", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tcpdump:")
}

func TestTcpdumpSnaplenNegativeRejected(t *testing.T) {
	dir := t.TempDir()
	writePcap(t, filepath.Join(dir, "capture.pcap"), t0, nil)
	_, stderr, code := cmdRun(t, "tcpdump -r capture.pcap -s -1", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tcpdump:")
}

func TestTcpdumpContextCancellation(t *testing.T) {
	// Generate a large pcap with many packets to ensure the loop runs.
	pkts := make([][]byte, 100)
	for i := range pkts {
		pkts[i] = buildTCPSYN(mac1, mac2, ip1, ip2, uint16(i+1), 80)
	}
	dir := setupDir(t, pkts)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	// Should complete without hanging.
	_, _, _ = runScriptCtx(ctx, t, "tcpdump -r capture.pcap", dir, interp.AllowedPaths([]string{dir}))
}

func TestTcpdumpMultiplePackets(t *testing.T) {
	pkts := make([][]byte, 10)
	for i := range pkts {
		pkts[i] = buildTCPSYN(mac1, mac2, ip1, ip2, uint16(i+1), 80)
	}
	dir := setupDir(t, pkts)
	stdout, _, code := cmdRun(t, "tcpdump -r capture.pcap", dir)
	assert.Equal(t, 0, code)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	assert.Len(t, lines, 10)
}

// ---------------------------------------------------------------------------
// No-resolve flags (-n, -nn) — accepted, no DNS so no visible difference
// ---------------------------------------------------------------------------

func TestTcpdumpNFlagAccepted(t *testing.T) {
	pkt := buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80)
	dir := setupDir(t, [][]byte{pkt})
	stdout, stderr, code := cmdRun(t, "tcpdump -r capture.pcap -n", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Contains(t, stdout, "IP")
}

func TestTcpdumpNNFlagAccepted(t *testing.T) {
	pkt := buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80)
	dir := setupDir(t, [][]byte{pkt})
	stdout, stderr, code := cmdRun(t, "tcpdump -r capture.pcap -nn", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Contains(t, stdout, "IP")
}
