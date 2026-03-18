// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package ping implements the ping builtin command.
//
// ping — send ICMP echo requests to a network host
//
// Usage: ping [OPTION]... HOST
//
// Sends ICMP echo requests to HOST and reports round-trip statistics.
// Uses github.com/prometheus-community/pro-bing for ICMP operations.
//
// On Linux, raw ICMP sockets require either root or CAP_NET_RAW, or the
// kernel sysctl net.ipv4.ping_group_range must include the process's GID
// for unprivileged operation. This command attempts unprivileged mode first
// (SetPrivileged(false), UDP-based ICMP) and automatically falls back to
// privileged raw-socket mode if the OS denies the unprivileged attempt.
//
// Accepted flags:
//
//	-c, --count N
//	    Number of ICMP echo requests to send (default 4, clamped to 1–20).
//
//	-W, --wait DURATION
//	    Time to wait for each reply (default 1s, clamped to 100ms–30s).
//
//	-i, --interval DURATION
//	    Interval between sending packets (default 1s, minimum 200ms).
//
//	-q, --quiet
//	    Quiet output: suppress per-packet lines; print only statistics.
//
//	-4
//	    Use IPv4 only.
//
//	-6
//	    Use IPv6 only.
//
//	-h, --help
//	    Print usage to stdout and exit 0.
//
// Dangerous flags NOT implemented (rejected by pflag as unknown):
//
//	-f           Flood ping — sends packets as fast as possible (DoS vector).
//	-b           Allow pinging broadcast addresses (network DoS vector).
//	-s SIZE      Set packet payload size (not needed; default size is used).
//	-I IFACE     Bind to specific network interface.
//	-p PATTERN   Fill packet with pattern.
//	-R           Record route.
//
// Exit codes:
//
//	0  At least one ICMP echo reply was received.
//	1  No replies received, or the host was unreachable, or bad arguments.
//
// Output format:
//
//	PING host (ip): N data bytes
//	N bytes from ip: icmp_seq=S ttl=T time=R ms
//	...
//	--- host ping statistics ---
//	S packets transmitted, R received, X% packet loss
//	round-trip min/avg/max/stddev = min/avg/max/stddev ms
package ping

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	probing "github.com/prometheus-community/pro-bing"

	"github.com/DataDog/rshell/builtins"
)

const (
	defaultCount    = 4
	maxCount        = 20
	defaultInterval = time.Second
	minInterval     = 200 * time.Millisecond
	maxInterval     = 60 * time.Second
	defaultWait     = time.Second
	minWait         = 100 * time.Millisecond
	maxWait         = 30 * time.Second
	// icmpPayloadSize matches the POSIX standard ping payload (56 data bytes).
	icmpPayloadSize = 56
)

// Cmd is the ping builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "ping",
	Description: "send ICMP echo requests to a network host",
	MakeFlags:   registerFlags,
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := fs.BoolP("help", "h", false, "print usage and exit 0")
	count := fs.IntP("count", "c", defaultCount, fmt.Sprintf("number of ICMP packets to send (1–%d)", maxCount))
	wait := fs.DurationP("wait", "W", defaultWait, fmt.Sprintf("time to wait for each reply (%v–%v)", minWait, maxWait))
	interval := fs.DurationP("interval", "i", defaultInterval, fmt.Sprintf("interval between packets (%v–%v)", minInterval, maxInterval))
	quiet := fs.BoolP("quiet", "q", false, "quiet output: suppress per-packet lines")
	ipv4 := fs.BoolP("ipv4", "4", false, "use IPv4")
	ipv6 := fs.BoolP("ipv6", "6", false, "use IPv6")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			printHelp(callCtx, fs)
			return builtins.Result{}
		}

		if len(args) == 0 {
			callCtx.Errf("ping: missing host operand\nTry 'ping --help' for more information.\n")
			return builtins.Result{Code: 1}
		}
		if len(args) > 1 {
			callCtx.Errf("ping: too many arguments\nTry 'ping --help' for more information.\n")
			return builtins.Result{Code: 1}
		}

		// Clamp inputs to safe ranges.
		c := clampInt(*count, 1, maxCount)
		w := clampDuration(*wait, minWait, maxWait)
		iv := clampDuration(*interval, minInterval, maxInterval)

		// Hard total deadline: last-packet deadline + 5s grace.
		// pro-bing's Timeout is a global wall-clock deadline. The last packet
		// is sent at (count-1)*interval after start; we then wait up to one
		// more 'wait' for its reply. So the total is (count-1)*interval + wait.
		total := time.Duration(c-1)*iv + w + 5*time.Second
		if total > 120*time.Second {
			total = 120 * time.Second
		}
		runCtx, cancel := context.WithTimeout(ctx, total)
		defer cancel()

		return execPing(runCtx, callCtx, args[0], c, w, iv, *quiet, *ipv4, *ipv6)
	}
}

// execPing resolves the host, sets up ICMP probing, and prints results.
func execPing(ctx context.Context, callCtx *builtins.CallContext, host string, count int, wait, interval time.Duration, quiet, ipv4, ipv6 bool) builtins.Result {
	pinger, err := buildPinger(ctx, host, count, wait, interval, ipv4, ipv6)
	if err != nil {
		callCtx.Errf("ping: %v\n", err)
		return builtins.Result{Code: 1}
	}

	// Print the header before opening the socket, matching real ping behaviour.
	// If RunWithContext later fails for a non-permission reason, the header on
	// stdout and the error on stderr is intentional — it mirrors what POSIX ping
	// does and all ping scenarios are marked skip_assert_against_bash: true.
	// pinger.Size is the ICMP echo body size (56 bytes); POSIX "data bytes" refers
	// to this same field.  Pro-bing stores its timestamp and UUID within those
	// 56 bytes, so the displayed count matches the on-wire ICMP payload size.
	// Use host (the original argument) for display; pinger.Addr() returns the
	// numeric IP because buildPinger passes a resolved IP to probing.NewPinger.
	callCtx.Outf("PING %s (%s): %d data bytes\n", host, pinger.IPAddr(), pinger.Size)

	onRecv := makeOnRecv(callCtx, quiet)
	pinger.OnRecv = onRecv

	// Attempt unprivileged mode first.
	pinger.SetPrivileged(false)
	err = pinger.RunWithContext(ctx)

	if err != nil && isPermissionErr(err) {
		// EPERM / EACCES / EPROTONOSUPPORT are returned by the internal
		// p.listen() call before any packet is sent, so pinger.Statistics()
		// is all zeros here and the header printed above is still valid.
		// Retry with raw socket privileges. Pass the already-resolved IP so that
		// buildPinger skips the DNS round-trip and returns immediately.
		// pinger.IPAddr().String() uses net.IPAddr.String(), which preserves the
		// zone identifier for link-local IPv6 addresses (e.g. "fe80::1%eth0"),
		// ensuring the OS can route the retry correctly.
		p2, err2 := buildPinger(ctx, pinger.IPAddr().String(), count, wait, interval, ipv4, ipv6)
		if err2 != nil {
			callCtx.Errf("ping: %v\n", err2)
			return builtins.Result{Code: 1}
		}
		p2.OnRecv = onRecv
		p2.SetPrivileged(true)
		err = p2.RunWithContext(ctx)
		pinger = p2
	}

	if err != nil && ctx.Err() == nil {
		callCtx.Errf("ping: %v\n", err)
		return builtins.Result{Code: 1}
	}

	// Print statistics unconditionally — even when the context was cancelled
	// (e.g. script timeout or SIGINT-equivalent). This mirrors POSIX ping
	// which prints partial statistics on SIGINT before exiting.
	stats := pinger.Statistics()
	printStats(callCtx, host, stats)

	if stats.PacketsRecv == 0 {
		return builtins.Result{Code: 1}
	}
	return builtins.Result{}
}

// buildPinger creates and configures a Pinger with the given parameters.
// It uses net.DefaultResolver.LookupIPAddr which is natively context-aware:
// cancellation propagates into the DNS query itself, avoiding goroutine leaks
// that would result from a goroutine wrapping the non-context net.ResolveIPAddr.
func buildPinger(ctx context.Context, host string, count int, wait, interval time.Duration, ipv4, ipv6 bool) (*probing.Pinger, error) {
	if ipv4 && ipv6 {
		return nil, fmt.Errorf("-4 and -6 are mutually exclusive")
	}

	// LookupIPAddr returns both IPv4 and IPv6 addresses; we select below.
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}

	// Select an address matching the requested family.
	// When -4/-6 is given: take the first address of that family or error.
	// When neither is given: prefer IPv4 (traditional ping default) so that
	// AAAA-first DNS results on hosts without working IPv6 do not cause
	// spurious failures; fall back to the first IPv6 address if no IPv4 found.
	var resolved *net.IPAddr
	if ipv4 || ipv6 {
		for i := range addrs {
			a := &addrs[i]
			isV4 := a.IP.To4() != nil
			if (ipv4 && isV4) || (ipv6 && !isV4) {
				resolved = a
				break
			}
		}
	} else {
		// No family flag: prefer IPv4, fall back to first IPv6.
		var ipv6Fallback *net.IPAddr
		for i := range addrs {
			a := &addrs[i]
			if a.IP.To4() != nil {
				resolved = a
				break
			}
			if ipv6Fallback == nil {
				ipv6Fallback = a
			}
		}
		if resolved == nil {
			resolved = ipv6Fallback
		}
	}
	if resolved == nil {
		family := "ip4"
		if ipv6 {
			family = "ip6"
		}
		return nil, fmt.Errorf("no %s address for host %q", family, host)
	}

	// Reject broadcast and multicast destinations to prevent unintended DoS.
	// The -f and -b flags are already rejected by the flag parser; this
	// catches cases where the resolved IP itself is a broadcast or multicast addr.
	//
	// NOTE: pro-bing v0.8.0 automatically retries sends with SO_BROADCAST set
	// on Linux when WriteTo returns EACCES (ping.go sendICMP loop). This means
	// that even without -b, a directed-broadcast address (e.g. 10.0.0.255) would
	// result in actual ICMP broadcast traffic being sent. We therefore reject
	// any IPv4 address whose last octet is 255, which covers both the limited
	// broadcast (255.255.255.255) and all subnet-directed broadcast addresses.
	// An address ending in .255 is only a valid unicast host address on a /31 or
	// /32 subnet; those are extremely rare and sacrificed for safety here.
	ip := resolved.IP
	if ip.IsMulticast() {
		return nil, fmt.Errorf("multicast destination not allowed: %s", ip)
	}
	// Block limited broadcast and subnet-directed broadcast addresses.
	if ip4 := ip.To4(); ip4 != nil && ip4[3] == 255 {
		return nil, fmt.Errorf("broadcast destination not allowed: %s", ip)
	}

	// Pass the numeric IP; pro-bing's internal net.ResolveIPAddr returns
	// immediately for a numeric address, so no second DNS round-trip occurs.
	p, err := probing.NewPinger(resolved.String())
	if err != nil {
		return nil, err
	}
	p.Count = count
	p.Size = icmpPayloadSize
	// pro-bing Timeout is a global wall-clock deadline, not per-packet.
	// The last probe is sent at (count-1)*interval; we then wait up to
	// one 'wait' for its reply.
	p.Timeout = time.Duration(count-1)*interval + wait
	p.Interval = interval
	p.SetLogger(probing.NoopLogger{})
	if ipv4 {
		p.SetNetwork("ip4")
	} else if ipv6 {
		p.SetNetwork("ip6")
	}
	return p, nil
}

// makeOnRecv returns the OnRecv callback. In quiet mode it returns nil so
// no per-packet output is written.
func makeOnRecv(callCtx *builtins.CallContext, quiet bool) func(*probing.Packet) {
	if quiet {
		return nil
	}
	return func(pkt *probing.Packet) {
		// pro-bing sequences start at 0; add 1 to match POSIX/bash ping convention
		// where the first reply carries icmp_seq=1.
		callCtx.Outf("%d bytes from %s: icmp_seq=%d ttl=%d time=%.3f ms\n",
			pkt.Nbytes, pkt.IPAddr, pkt.Seq+1, pkt.TTL, durToMS(pkt.Rtt))
	}
}

// printStats writes the two summary lines that every ping run ends with.
// host is the original argument (hostname or IP) for display in the footer.
func printStats(callCtx *builtins.CallContext, host string, stats *probing.Statistics) {
	callCtx.Outf("\n--- %s ping statistics ---\n", host)
	callCtx.Outf("%d packets transmitted, %d received, %.1f%% packet loss\n",
		stats.PacketsSent, stats.PacketsRecv, stats.PacketLoss)
	if stats.PacketsRecv > 0 {
		callCtx.Outf("round-trip min/avg/max/stddev = %.3f/%.3f/%.3f/%.3f ms\n",
			durToMS(stats.MinRtt), durToMS(stats.AvgRtt),
			durToMS(stats.MaxRtt), durToMS(stats.StdDevRtt))
	}
}

// printHelp writes the usage text to stdout.
func printHelp(callCtx *builtins.CallContext, fs *builtins.FlagSet) {
	callCtx.Out("Usage: ping [OPTION]... HOST\n")
	callCtx.Out("Send ICMP echo requests to HOST and report statistics.\n\n")
	callCtx.Out("Options:\n")
	fs.SetOutput(callCtx.Stdout)
	fs.PrintDefaults()
	callCtx.Out("\nNote: -f (flood) and -b (broadcast) are not supported for safety.\n")
}

// clampInt returns v clamped to [lo, hi].
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// clampDuration returns v clamped to [lo, hi].
func clampDuration(v, lo, hi time.Duration) time.Duration {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// durToMS converts a duration to milliseconds as a float64.
func durToMS(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000.0
}

// isPermissionErr reports whether err indicates that the process lacks the
// privilege to open a raw ICMP socket. When true, the caller should retry
// with privileged raw-socket mode.
//
// This function is only called on errors returned by pinger.RunWithContext,
// which come from the ICMP socket layer — not from DNS. DNS errors are caught
// earlier in buildPinger and returned to the caller before RunWithContext is
// ever invoked, so "permission denied" strings here always originate from
// socket creation, never from a DNS resolver response.
//
// We detect three classes of failure:
//  1. EPERM / EACCES — classic Unix permission denials.
//  2. EPROTONOSUPPORT — returned on Linux when the kernel's
//     net.ipv4.ping_group_range does not cover the process GID and the
//     unprivileged UDP-based ICMP path is unavailable; privileged raw
//     sockets are unaffected and should be tried.
//  3. String-based fallback for Windows and platforms that wrap errors.
func isPermissionErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) ||
		errors.Is(err, syscall.EPROTONOSUPPORT) {
		return true
	}
	// String-based fallback for Windows and other platforms.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "operation not permitted") ||
		strings.Contains(msg, "access is denied") ||
		strings.Contains(msg, "permission denied") ||
		strings.Contains(msg, "protocol not supported") ||
		// Windows: WSAEPROTONOSUPPORT (10043) — returned by pro-bing when an
		// unprivileged raw socket cannot be created; privileged mode should be tried.
		strings.Contains(msg, "the requested protocol has not been configured")
}
