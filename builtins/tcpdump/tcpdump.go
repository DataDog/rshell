// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package tcpdump implements the tcpdump builtin command.
//
// tcpdump — capture and display network packets
//
// Usage: tcpdump (-r file | -i interface) [OPTION]... [FILTER EXPRESSION]
//
// Read packets from a pcap/pcapng file (-r) or capture live packets from a
// network interface (-i). Live capture requires CAP_NET_RAW (Linux) or root
// (macOS). File writing, command execution, and privilege escalation are not
// supported. The dangerous flags -z, -Z, -w, -C, -W, -G are rejected with
// exit code 1.
//
// Source selection (exactly one required):
//
//	-r file, --read-file=file
//	    Read packets from the given pcap or pcapng file.
//
//	-i interface
//	    Capture live packets from the named network interface.
//	    Requires CAP_NET_RAW on Linux or root on macOS.
//	    -c N is strongly recommended to bound the capture; if omitted the
//	    builtin captures up to MaxPacketCount packets before stopping.
//
// Options:
//
//	-c N, --count=N
//	    Stop after processing N packets (must be > 0; clamped to MaxPacketCount).
//
//	-n
//	    Do not convert addresses to names. In this builtin address resolution
//	    is never performed, so -n is accepted but has no additional effect.
//
//	-nn
//	    Do not convert addresses or port numbers to names. Same note as -n:
//	    this is always the effective behaviour.
//
//	-v
//	    Verbose output: show TTL, protocol number, and IP length.
//
//	-vv
//	    More verbose: also show IP flags, ID, and checksum.
//
//	-vvv
//	    Maximum verbosity (currently same as -vv).
//
//	-q
//	    Quiet output: suppress TCP flags and options; show only proto+length.
//
//	-e
//	    Print the link-layer header (Ethernet source/destination MAC) on each
//	    dump line.
//
//	-A
//	    Print packet payload as ASCII (non-printable bytes rendered as '.').
//
//	-x
//	    Print each packet (without link-layer header) in hexadecimal.
//
//	-xx
//	    Print each packet (including link-layer header) in hexadecimal.
//
//	-X
//	    Print each packet (without link-layer header) in hex+ASCII.
//
//	-XX
//	    Print each packet (including link-layer header) in hex+ASCII.
//
//	-t
//	    Do not print a timestamp on each dump line.
//
//	-tt
//	    Print the timestamp as seconds and fractions of a second since the
//	    Unix epoch.
//
//	-ttt
//	    Print a delta (in microseconds) between the current and previous line.
//
//	-tttt
//	    Print the timestamp as a date and time (YYYY-MM-DD HH:MM:SS.ffffff).
//
//	-s N, --snaplen=N
//	    Cap per-packet display at N bytes. N=0 means show all captured bytes
//	    (default). Clamped to MaxSnaplen.
//
//	--help
//	    Print this usage message to stdout and exit 0.
//
// Filter expression:
//
//	Positional arguments after flags form a BPF-style filter expression.
//	Supported primitives:
//	    host <addr>          match src or dst IP
//	    src host <addr>      match src IP
//	    dst host <addr>      match dst IP
//	    port <port>          match src or dst TCP/UDP port
//	    src port <port>      match src port
//	    dst port <port>      match dst port
//	    tcp / udp / icmp     match protocol
//	    ip / ip6             match IPv4 or IPv6
//	Combinators: and (&&), or (||), not (!), and grouping with parentheses.
//
// Rejected flags (all exit 1 with an error message):
//
//	-w       write captured packets to a file
//	-z       execute a postrotate command — arbitrary code execution vector
//	-Z       run as a different user — privilege escalation
//	-C/-W/-G file rotation flags — write operations
//	-D       list network interfaces
//
// Exit codes:
//
//	0  All packets processed successfully (or -c limit reached).
//	1  Error opening file/interface, unrecognised flags, invalid filter, or bad args.
//
// Memory safety:
//
//	Packets are processed one at a time. Each packet's allocation is bounded
//	by the file's global snaplen (file mode) or MaxPacketBytes (live mode).
//	After reading, each packet's display is further capped at MaxPacketBytes
//	(64 KiB). The main loop checks ctx.Err() before every read to honour the
//	execution timeout. In live mode, reads time out every 100 ms so context
//	cancellation is checked frequently even on quiet interfaces.
package tcpdump

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/DataDog/rshell/builtins"
	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
)

// Cmd is the tcpdump builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "tcpdump",
	Description: "capture and display network packets",
	MakeFlags:   registerFlags,
}

const (
	// MaxPacketBytes is the maximum number of bytes displayed per packet.
	MaxPacketBytes = 64 * 1024 // 64 KiB

	// MaxPacketCount is the upper bound on -c (clamped to prevent huge loops).
	MaxPacketCount = 1_000_000

	// MaxSnaplen is the maximum value accepted for -s.
	MaxSnaplen = 65535
)

// errReadTimeout is returned by ReadPacketData when no packet arrived within
// the per-read deadline. The caller should check ctx.Err() and retry.
var errReadTimeout = errors.New("read timeout")

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := fs.BoolP("help", "h", false, "print usage and exit")
	readFile := fs.StringP("read-file", "r", "", "read packets from pcap/pcapng `file`")
	iface := fs.StringP("interface", "i", "", "live capture on network `interface` (requires CAP_NET_RAW or root)")
	count := fs.IntP("count", "c", 0, "stop after processing N packets")
	noResolve := fs.CountP("no-resolve", "n", "do not resolve addresses (-n) or ports (-nn)")
	verbose := fs.CountP("verbose", "v", "increase verbosity (-v, -vv, -vvv)")
	quiet := fs.BoolP("quiet", "q", false, "quiet output (less protocol info)")
	linkLayer := fs.BoolP("link-layer", "e", false, "print link-layer header")
	ascii := fs.BoolP("ascii", "A", false, "print packet payload as ASCII")
	hexCount := fs.CountP("hex", "x", "print packet in hex (-x no link hdr, -xx with link hdr)")
	hexAscii := fs.CountP("hex-ascii", "X", "print packet in hex+ascii (-X no link hdr, -XX with link hdr)")
	tCount := fs.CountP("timestamp", "t", "timestamp mode: -t=none, -tt=unix, -ttt=delta, -tttt=date+time")
	snaplen := fs.IntP("snaplen", "s", 0, "snap each packet at N bytes (0 = unlimited)")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: tcpdump (-r file | -i interface) [OPTION]... [FILTER EXPRESSION]\n")
			callCtx.Out("Capture and display network packets.\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}

		// Exactly one of -r / -i must be given.
		hasR := fs.Changed("read-file")
		hasI := fs.Changed("interface")
		switch {
		case hasR && hasI:
			callCtx.Errf("tcpdump: -r and -i are mutually exclusive\n")
			return builtins.Result{Code: 1}
		case !hasR && !hasI:
			callCtx.Errf("tcpdump: must specify -r file or -i interface\n")
			return builtins.Result{Code: 1}
		}

		if *count < 0 {
			callCtx.Errf("tcpdump: -c count must be >= 0\n")
			return builtins.Result{Code: 1}
		}
		if *count == 0 || *count > MaxPacketCount {
			*count = MaxPacketCount
		}

		if *snaplen < 0 {
			callCtx.Errf("tcpdump: -s snaplen must be >= 0\n")
			return builtins.Result{Code: 1}
		}
		if *snaplen > MaxSnaplen {
			*snaplen = MaxSnaplen
		}

		filterStr := strings.Join(args, " ")

		opts := displayOpts{
			verbose:   *verbose,
			quiet:     *quiet,
			linkLayer: *linkLayer,
			ascii:     *ascii,
			hexCount:  *hexCount,
			hexAscii:  *hexAscii,
			tCount:    *tCount,
			snaplen:   *snaplen,
			noResolve: *noResolve,
		}

		if hasR {
			return runFromFile(ctx, callCtx, *readFile, filterStr, *count, opts)
		}
		return runFromInterface(ctx, callCtx, *iface, filterStr, *count, opts)
	}
}

// runFromFile opens a pcap/pcapng file and runs the capture loop.
func runFromFile(
	ctx context.Context,
	callCtx *builtins.CallContext,
	filename string,
	filterStr string,
	maxCount int,
	opts displayOpts,
) builtins.Result {
	rc, err := callCtx.OpenFile(ctx, filename, os.O_RDONLY, 0)
	if err != nil {
		callCtx.Errf("tcpdump: %s: %s\n", filename, callCtx.PortableErr(err))
		return builtins.Result{Code: 1}
	}
	defer rc.Close()

	reader, openErr := openPcapReader(rc)
	if openErr != nil {
		callCtx.Errf("tcpdump: %s: %s\n", filename, openErr)
		return builtins.Result{Code: 1}
	}

	return runCapture(ctx, callCtx, reader, filterStr, maxCount, opts)
}

// runFromInterface opens a live capture handle and runs the capture loop.
func runFromInterface(
	ctx context.Context,
	callCtx *builtins.CallContext,
	iface string,
	filterStr string,
	maxCount int,
	opts displayOpts,
) builtins.Result {
	reader, err := openLiveInterface(ctx, iface, opts.snaplen)
	if err != nil {
		callCtx.Errf("tcpdump: %s\n", err)
		return builtins.Result{Code: 1}
	}
	if c, ok := reader.(io.Closer); ok {
		defer c.Close()
	}

	return runCapture(ctx, callCtx, reader, filterStr, maxCount, opts)
}

// displayOpts holds the parsed display flags.
type displayOpts struct {
	verbose   int
	quiet     bool
	linkLayer bool
	ascii     bool
	hexCount  int // 1 = -x (no link hdr), 2 = -xx (with link hdr)
	hexAscii  int // 1 = -X (no link hdr), 2 = -XX (with link hdr)
	tCount    int // 0=default, 1=-t none, 2=-tt unix, 3=-ttt delta, 4=-tttt date+time
	snaplen   int // 0 = unlimited
	noResolve int // 0=resolve, 1=-n addr only, 2=-nn both
}

// packetReader abstracts pcap vs pcapng vs live readers.
type packetReader interface {
	ReadPacketData() (data []byte, ci gopacket.CaptureInfo, err error)
	LinkType() layers.LinkType
}

func runCapture(
	ctx context.Context,
	callCtx *builtins.CallContext,
	reader packetReader,
	filterStr string,
	maxCount int,
	opts displayOpts,
) builtins.Result {
	var filter *Filter
	if filterStr != "" {
		var compileErr error
		filter, compileErr = compileFilter(filterStr)
		if compileErr != nil {
			callCtx.Errf("tcpdump: invalid filter expression: %s\n", compileErr)
			return builtins.Result{Code: 1}
		}
	}

	var (
		processed int
		prevTS    time.Time
	)

	for processed < maxCount {
		if ctx.Err() != nil {
			break
		}

		data, ci, readErr := reader.ReadPacketData()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if errors.Is(readErr, errReadTimeout) {
			// No packet arrived within the read window; loop to re-check ctx.
			continue
		}
		if readErr != nil {
			callCtx.Errf("tcpdump: read error: %s\n", readErr)
			return builtins.Result{Code: 1}
		}

		// Cap packet data to MaxPacketBytes then to snaplen (if set).
		if len(data) > MaxPacketBytes {
			data = data[:MaxPacketBytes]
		}
		if opts.snaplen > 0 && len(data) > opts.snaplen {
			data = data[:opts.snaplen]
		}

		pkt := gopacket.NewPacket(data, reader.LinkType(), gopacket.Default)

		if filter != nil && !filter.Matches(pkt) {
			continue
		}

		processed++

		ts := formatTimestamp(ci.Timestamp, prevTS, opts.tCount)
		prevTS = ci.Timestamp

		summary := formatPacket(pkt, ci, opts)

		if ts != "" {
			callCtx.Outf("%s %s\n", ts, summary)
		} else {
			callCtx.Outf("%s\n", summary)
		}

		if opts.hexAscii > 0 || opts.hexCount > 0 || opts.ascii {
			dumpData := selectDumpData(pkt, opts)
			if len(dumpData) > 0 {
				callCtx.Out(formatDump(dumpData, opts))
			}
		}
	}

	return builtins.Result{}
}

// openPcapReader opens a pcap or pcapng file. It reads the first 4 bytes to
// detect the file format, then prepends them back via io.MultiReader.
func openPcapReader(rc io.ReadCloser) (packetReader, error) {
	var magic [4]byte
	n := 0
	for n < 4 {
		m, readErr := rc.Read(magic[n:])
		n += m
		if readErr != nil {
			break
		}
	}
	if n < 4 {
		return nil, errors.New("file too short to be a valid pcap/pcapng capture")
	}

	full := io.MultiReader(bytes.NewReader(magic[:n]), rc)

	// pcapng Section Header Block magic: 0x0A 0x0D 0x0D 0x0A
	if magic[0] == 0x0a && magic[1] == 0x0d && magic[2] == 0x0d && magic[3] == 0x0a {
		ng, err := pcapgo.NewNgReader(full, pcapgo.DefaultNgReaderOptions)
		if err != nil {
			return nil, fmt.Errorf("invalid pcapng file: %w", err)
		}
		return ng, nil
	}

	// Fall back to pcap (handles both little-endian and big-endian magic bytes).
	r, err := pcapgo.NewReader(full)
	if err != nil {
		return nil, fmt.Errorf("invalid pcap/pcapng file: %w", err)
	}
	return r, nil
}

// formatTimestamp formats a packet timestamp according to the -t count.
func formatTimestamp(ts, prev time.Time, tCount int) string {
	switch tCount {
	case 1: // -t: no timestamp
		return ""
	case 2: // -tt: Unix timestamp
		usec := ts.UnixMicro()
		return fmt.Sprintf("%d.%06d", usec/1_000_000, usec%1_000_000)
	case 3: // -ttt: delta from previous packet
		if prev.IsZero() {
			return " 0.000000"
		}
		usec := ts.Sub(prev).Microseconds()
		if usec < 0 {
			usec = -usec
		}
		return fmt.Sprintf(" %d.%06d", usec/1_000_000, usec%1_000_000)
	case 4: // -tttt: date + time
		return ts.Local().Format("2006-01-02 15:04:05.000000")
	default: // 0: HH:MM:SS.ffffff
		return ts.Local().Format("15:04:05.000000")
	}
}

// formatPacket produces the single-line packet summary.
func formatPacket(pkt gopacket.Packet, ci gopacket.CaptureInfo, opts displayOpts) string {
	var sb strings.Builder

	if opts.linkLayer {
		if ethLayer := pkt.Layer(layers.LayerTypeEthernet); ethLayer != nil {
			eth := ethLayer.(*layers.Ethernet)
			sb.WriteString(eth.SrcMAC.String())
			sb.WriteString(" > ")
			sb.WriteString(eth.DstMAC.String())
			sb.WriteString(", ethertype ")
			sb.WriteString(fmt.Sprintf("%s (0x%04x)", eth.EthernetType.String(), uint16(eth.EthernetType)))
			sb.WriteString(", length ")
			sb.WriteString(fmt.Sprintf("%d", ci.Length))
			sb.WriteString(": ")
		}
	}

	if ipv4Layer := pkt.Layer(layers.LayerTypeIPv4); ipv4Layer != nil {
		ip := ipv4Layer.(*layers.IPv4)
		sb.WriteString("IP ")
		if opts.verbose >= 1 {
			sb.WriteString(fmt.Sprintf("(tos 0x%x, ttl %d, id %d, ", ip.TOS, ip.TTL, ip.Id))
			if opts.verbose >= 2 {
				sb.WriteString(fmt.Sprintf("offset %d, ", ip.FragOffset))
				flagStr := ipv4FlagsString(ip)
				if flagStr != "" {
					sb.WriteString(fmt.Sprintf("flags [%s], ", flagStr))
				}
			}
			sb.WriteString(fmt.Sprintf("proto %s (%d), length %d)\n    ", ip.Protocol.String(), uint8(ip.Protocol), ip.Length))
		}
		sb.WriteString(formatTransport(ip.SrcIP.String(), ip.DstIP.String(), pkt, opts))
	} else if ipv6Layer := pkt.Layer(layers.LayerTypeIPv6); ipv6Layer != nil {
		ip := ipv6Layer.(*layers.IPv6)
		sb.WriteString("IP6 ")
		if opts.verbose >= 1 {
			sb.WriteString(fmt.Sprintf("(hlim %d, next-header %s (%d), length %d)\n    ", ip.HopLimit, ip.NextHeader.String(), uint8(ip.NextHeader), ip.Length))
		}
		sb.WriteString(formatTransport(ip.SrcIP.String(), ip.DstIP.String(), pkt, opts))
	} else {
		sb.WriteString(fmt.Sprintf("unknown, length %d", ci.Length))
	}

	return sb.String()
}

func ipv4FlagsString(ip *layers.IPv4) string {
	var flags []string
	if ip.Flags&layers.IPv4DontFragment != 0 {
		flags = append(flags, "DF")
	}
	if ip.Flags&layers.IPv4MoreFragments != 0 {
		flags = append(flags, "MF")
	}
	return strings.Join(flags, ",")
}

func formatTransport(src, dst string, pkt gopacket.Packet, opts displayOpts) string {
	if tcpLayer := pkt.Layer(layers.LayerTypeTCP); tcpLayer != nil {
		tcp := tcpLayer.(*layers.TCP)
		srcAddr := fmt.Sprintf("%s.%d", src, tcp.SrcPort)
		dstAddr := fmt.Sprintf("%s.%d", dst, tcp.DstPort)
		return formatTCP(srcAddr, dstAddr, tcp, opts)
	}
	if udpLayer := pkt.Layer(layers.LayerTypeUDP); udpLayer != nil {
		udp := udpLayer.(*layers.UDP)
		return fmt.Sprintf("%s.%d > %s.%d: UDP, length %d", src, udp.SrcPort, dst, udp.DstPort, len(udp.Payload))
	}
	if icmpLayer := pkt.Layer(layers.LayerTypeICMPv4); icmpLayer != nil {
		icmp := icmpLayer.(*layers.ICMPv4)
		return fmt.Sprintf("%s > %s: ICMP %s, id %d, seq %d, length %d",
			src, dst, icmp.TypeCode.String(), icmp.Id, icmp.Seq, len(icmp.Payload)+8)
	}
	if icmp6Layer := pkt.Layer(layers.LayerTypeICMPv6); icmp6Layer != nil {
		icmp := icmp6Layer.(*layers.ICMPv6)
		return fmt.Sprintf("%s > %s: ICMP6 %s, length %d",
			src, dst, icmp.TypeCode.String(), len(icmp.Payload)+8)
	}
	return fmt.Sprintf("%s > %s: proto unknown", src, dst)
}

func formatTCP(srcAddr, dstAddr string, tcp *layers.TCP, opts displayOpts) string {
	flags := tcpFlagsString(tcp)
	if opts.quiet {
		return fmt.Sprintf("%s > %s: Flags [%s], length %d", srcAddr, dstAddr, flags, len(tcp.Payload))
	}
	s := fmt.Sprintf("%s > %s: Flags [%s]", srcAddr, dstAddr, flags)
	// Emit seq for connection-setup flags and for segments carrying data, matching
	// real tcpdump behaviour (PSH-only segments need seq too).
	if tcp.SYN || tcp.FIN || tcp.ACK || len(tcp.Payload) > 0 {
		s += fmt.Sprintf(", seq %d", tcp.Seq)
	}
	if tcp.ACK {
		s += fmt.Sprintf(", ack %d", tcp.Ack)
	}
	s += fmt.Sprintf(", win %d", tcp.Window)
	s += fmt.Sprintf(", length %d", len(tcp.Payload))
	return s
}

func tcpFlagsString(tcp *layers.TCP) string {
	var f []byte
	if tcp.SYN {
		f = append(f, 'S')
	}
	if tcp.FIN {
		f = append(f, 'F')
	}
	if tcp.RST {
		f = append(f, 'R')
	}
	if tcp.PSH {
		f = append(f, 'P')
	}
	if tcp.ACK {
		f = append(f, '.')
	}
	if tcp.URG {
		f = append(f, 'U')
	}
	if len(f) == 0 {
		return "none"
	}
	return string(f)
}

// selectDumpData returns the bytes to hex-dump.
// hexCount >= 2 or hexAscii >= 2 → include link-layer header (full packet).
// Otherwise, start from the network layer (header + payload).
func selectDumpData(pkt gopacket.Packet, opts displayOpts) []byte {
	withLinkHdr := opts.hexCount >= 2 || opts.hexAscii >= 2
	if withLinkHdr {
		return pkt.Data()
	}
	if nl := pkt.NetworkLayer(); nl != nil {
		return append(nl.LayerContents(), nl.LayerPayload()...)
	}
	return pkt.Data()
}

// formatDump produces the hex / hex+ASCII / ASCII dump lines.
func formatDump(data []byte, opts displayOpts) string {
	showAsciiOnly := opts.ascii && opts.hexCount == 0 && opts.hexAscii == 0
	showHexAscii := opts.hexAscii > 0 || (opts.ascii && (opts.hexCount > 0))
	showHex := opts.hexCount > 0

	var sb strings.Builder
	const lineWidth = 16

	if showAsciiOnly && !showHex {
		for i := 0; i < len(data); i += lineWidth {
			end := min(i+lineWidth, len(data))
			sb.WriteByte('\t')
			for _, b := range data[i:end] {
				if b >= 0x20 && b < 0x7f {
					sb.WriteByte(b)
				} else {
					sb.WriteByte('.')
				}
			}
			sb.WriteByte('\n')
		}
		return sb.String()
	}

	for i := 0; i < len(data); i += lineWidth {
		end := min(i+lineWidth, len(data))
		chunk := data[i:end]

		sb.WriteString(fmt.Sprintf("\t0x%04x:  ", i))

		for j := 0; j < lineWidth; j += 2 {
			switch {
			case j+1 < len(chunk):
				sb.WriteString(fmt.Sprintf("%02x%02x ", chunk[j], chunk[j+1]))
			case j < len(chunk):
				sb.WriteString(fmt.Sprintf("%02x   ", chunk[j]))
			default:
				sb.WriteString("     ")
			}
		}

		if showHexAscii {
			sb.WriteByte(' ')
			for _, b := range chunk {
				if b >= 0x20 && b < 0x7f {
					sb.WriteByte(b)
				} else {
					sb.WriteByte('.')
				}
			}
		}

		sb.WriteByte('\n')
	}
	return sb.String()
}
