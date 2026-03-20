// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tcpdump_test

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/rshell/interp"
)

// cmdRunCtxFuzz is a non-conflicting wrapper for fuzz tests.
func cmdRunCtxFuzz(ctx context.Context, t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return runScriptCtx(ctx, t, script, dir, interp.AllowedPaths([]string{dir}))
}

// fuzzTimeout limits how long each fuzz iteration runs.
const fuzzTimeout = 5 * time.Second

// minimalPcapHeader returns a well-formed pcap global header (little-endian, Ethernet link type).
func minimalPcapHeader() []byte {
	hdr := make([]byte, 24)
	binary.LittleEndian.PutUint32(hdr[0:], 0xa1b2c3d4) // magic LE
	binary.LittleEndian.PutUint16(hdr[4:], 2)          // major version
	binary.LittleEndian.PutUint16(hdr[6:], 4)          // minor version
	binary.LittleEndian.PutUint32(hdr[16:], 65535)     // snaplen
	binary.LittleEndian.PutUint32(hdr[20:], 1)         // link type ETHERNET
	return hdr
}

// appendPcapRecord wraps raw bytes in a pcap packet record.
func appendPcapRecord(buf []byte, ts time.Time, data []byte) []byte {
	rec := make([]byte, 16)
	binary.LittleEndian.PutUint32(rec[0:], uint32(ts.Unix()))
	binary.LittleEndian.PutUint32(rec[8:], uint32(len(data)))
	binary.LittleEndian.PutUint32(rec[12:], uint32(len(data)))
	buf = append(buf, rec...)
	buf = append(buf, data...)
	return buf
}

// FuzzTcpdumpPcapContent fuzzes the raw bytes of a pcap file fed to tcpdump.
// This exercises the pcap parser, packet decoder, and output formatter.
func FuzzTcpdumpPcapContent(f *testing.F) {
	// Source A: valid pcap global header with zero packets.
	f.Add(minimalPcapHeader())

	// Source A: valid pcap with a single TCP SYN packet.
	{
		pkt := buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80)
		pcap := append(minimalPcapHeader(), appendPcapRecord(nil, t0, pkt)...)
		f.Add(pcap)
	}

	// Source A: valid pcap with a single UDP packet.
	{
		pkt := buildUDP(mac1, mac2, ip1, ip2, 1234, 53, []byte("query"))
		pcap := append(minimalPcapHeader(), appendPcapRecord(nil, t0, pkt)...)
		f.Add(pcap)
	}

	// Source A: valid pcap with an ICMP packet.
	{
		pkt := buildICMP(mac1, mac2, ip1, ip2, 1, 1)
		pcap := append(minimalPcapHeader(), appendPcapRecord(nil, t0, pkt)...)
		f.Add(pcap)
	}

	// Source B: security/CVE history — boundary inputs.
	// Only 3 bytes (below magic read threshold)
	f.Add([]byte{0xd4, 0xc3, 0xb2})
	// Exactly 4 bytes (magic only, no further data)
	f.Add([]byte{0xd4, 0xc3, 0xb2, 0xa1})
	// All zeroes (invalid magic)
	f.Add(make([]byte, 64))
	// All 0xFF (invalid magic)
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	// pcapng magic
	f.Add([]byte{0x0a, 0x0d, 0x0d, 0x0a, 0x1c, 0x00, 0x00, 0x00})
	// Integer overflow: packet count field with MaxUint32
	{
		hdr := minimalPcapHeader()
		rec := make([]byte, 16)
		binary.LittleEndian.PutUint32(rec[8:], 0xffffffff) // incl_len = MaxUint32
		binary.LittleEndian.PutUint32(rec[12:], 0xffffffff)
		f.Add(append(hdr, rec...))
	}
	// Zero-length packet record
	{
		pcap := append(minimalPcapHeader(), appendPcapRecord(nil, t0, []byte{})...)
		f.Add(pcap)
	}
	// Truncated packet record (16-byte header but only 4 data bytes, claimed 20)
	{
		hdr := minimalPcapHeader()
		rec := make([]byte, 16)
		binary.LittleEndian.PutUint32(rec[8:], 20) // claimed length 20
		binary.LittleEndian.PutUint32(rec[12:], 20)
		hdr = append(hdr, rec...)
		hdr = append(hdr, 0x45, 0x00, 0x00, 0x14) // only 4 bytes of data
		f.Add(hdr)
	}

	f.Fuzz(func(t *testing.T, content []byte) {
		if len(content) > 1<<20 {
			return // cap at 1 MiB
		}

		dir := t.TempDir()
		if err := writeBytes(dir, "fuzz.pcap", content); err != nil {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), fuzzTimeout)
		defer cancel()

		_, _, code := cmdRunCtxFuzz(ctx, t, "tcpdump -r fuzz.pcap", dir)
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d", code)
		}
	})
}

// FuzzTcpdumpFilter fuzzes the filter expression passed to tcpdump.
func FuzzTcpdumpFilter(f *testing.F) {
	// Source C: existing test filter expressions.
	f.Add("host 192.168.1.1")
	f.Add("src host 192.168.1.1")
	f.Add("dst host 192.168.1.2")
	f.Add("port 80")
	f.Add("src port 443")
	f.Add("dst port 53")
	f.Add("tcp")
	f.Add("udp")
	f.Add("icmp")
	f.Add("ip")
	f.Add("ip6")
	f.Add("tcp and port 80")
	f.Add("tcp or udp")
	f.Add("not icmp")
	f.Add("src host 10.0.0.1 and dst port 80")
	// Note: parenthesised filters like "(tcp or udp)" require shell quoting and are
	// covered by unit tests; they are omitted here to avoid shell parse errors.

	// Source B: injection / overflow attempts.
	f.Add("host 999.999.999.999")
	f.Add("port 0")
	f.Add("port 65536")
	f.Add("port -1")
	f.Add("gobbledygook")
	f.Add("")
	f.Add("not not not tcp")
	f.Add("tcp and and udp")
	f.Add("src")
	f.Add("dst")
	f.Add("host")
	f.Add("port")
	f.Add("src dst tcp")

	f.Fuzz(func(t *testing.T, filterExpr string) {
		if len(filterExpr) > 1024 {
			return
		}

		// Create a tiny but valid pcap to avoid file-open errors interfering.
		dir := t.TempDir()
		pkt := buildTCPSYN(mac1, mac2, ip1, ip2, 1234, 80)
		pcap := append(minimalPcapHeader(), appendPcapRecord(nil, t0, pkt)...)
		if err := writeBytes(dir, "fuzz.pcap", pcap); err != nil {
			return
		}

		// Shell-escape the filter expression to prevent injection.
		// We pass each word as a separate argument via printf.
		// Since we can't shell-escape arbitrary strings in the shell script,
		// we only fuzz expressions that look like safe tokens.
		for _, ch := range filterExpr {
			// Skip characters that would be interpreted by the shell rather than
			// passed as filter arguments (metacharacters, background operator, etc.).
			if ch == '\'' || ch == '"' || ch == '\\' || ch == '\n' || ch == '\r' ||
				ch == '(' || ch == ')' || ch == '&' || ch == '|' || ch == ';' ||
				ch == '<' || ch == '>' || ch == '`' || ch == '$' || ch == 0 {
				return
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), fuzzTimeout)
		defer cancel()

		script := "tcpdump -r fuzz.pcap " + filterExpr
		_, _, code := cmdRunCtxFuzz(ctx, t, script, dir)
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d for filter %q", code, filterExpr)
		}
	})
}

// writeBytes writes content to dir/name, returning any error.
func writeBytes(dir, name string, content []byte) error {
	return os.WriteFile(filepath.Join(dir, name), content, 0644)
}
