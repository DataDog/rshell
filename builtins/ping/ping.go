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
	defaultWait     = time.Second
	maxWait         = 30 * time.Second
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
	wait := fs.DurationP("wait", "W", defaultWait, "time to wait for each reply (100ms–30s)")
	interval := fs.DurationP("interval", "i", defaultInterval, "interval between packets (min 200ms)")
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
		w := clampDuration(*wait, 100*time.Millisecond, maxWait)
		iv := clampDuration(*interval, minInterval, 60*time.Second)

		// Hard total deadline = count × (interval + wait-per-reply) + 5s grace.
		total := time.Duration(c)*(iv+w) + 5*time.Second
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

	// Use host (the original argument) for display; pinger.Addr() returns the
	// numeric IP because buildPinger passes a resolved IP to probing.NewPinger.
	callCtx.Outf("PING %s (%s): %d data bytes\n", host, pinger.IPAddr(), pinger.Size)

	onRecv := makeOnRecv(callCtx, quiet)
	pinger.OnRecv = onRecv

	// Attempt unprivileged mode first.
	pinger.SetPrivileged(false)
	err = pinger.RunWithContext(ctx)

	if err != nil && isPermissionErr(err) {
		// Retry with raw socket privileges. Pass the already-resolved IP so that
		// buildPinger skips the DNS goroutine and returns immediately.
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

	stats := pinger.Statistics()
	printStats(callCtx, host, stats)

	if stats.PacketsRecv == 0 {
		return builtins.Result{Code: 1}
	}
	return builtins.Result{}
}

// buildPinger creates and configures a Pinger with the given parameters.
// It resolves host using the requested address family (ip4/ip6/ip) in a
// goroutine so that ctx cancellation respects the deadline even if the
// resolver hangs. Passing an already-resolved IP skips DNS entirely.
func buildPinger(ctx context.Context, host string, count int, wait, interval time.Duration, ipv4, ipv6 bool) (*probing.Pinger, error) {
	if ipv4 && ipv6 {
		return nil, fmt.Errorf("-4 and -6 are mutually exclusive")
	}

	// Use the requested family so dual-stack hosts resolve to the right address.
	resolveNet := "ip"
	if ipv4 {
		resolveNet = "ip4"
	} else if ipv6 {
		resolveNet = "ip6"
	}

	type result struct {
		addr *net.IPAddr
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		addr, err := net.ResolveIPAddr(resolveNet, host)
		ch <- result{addr, err}
	}()

	var resolved *net.IPAddr
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-ch:
		if r.err != nil {
			return nil, r.err
		}
		resolved = r.addr
	}

	// Pass the numeric IP; pro-bing's internal ResolveIPAddr returns immediately
	// for a numeric address, so no second DNS round-trip occurs.
	p, err := probing.NewPinger(resolved.String())
	if err != nil {
		return nil, err
	}
	p.Count = count
	p.Timeout = time.Duration(count) * (interval + wait)
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
		callCtx.Outf("%d bytes from %s: icmp_seq=%d ttl=%d time=%.3f ms\n",
			pkt.Nbytes, pkt.IPAddr, pkt.Seq, pkt.TTL, durToMS(pkt.Rtt))
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

// isPermissionErr reports whether err is a socket permission error (EPERM or
// EACCES), indicating that the process lacks the privilege to open a raw
// ICMP socket. The check traverses the error chain and falls back to a
// case-insensitive string match for platforms that wrap errors differently.
func isPermissionErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		return true
	}
	// String-based fallback for Windows and other platforms.
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "operation not permitted") ||
		strings.Contains(msg, "access is denied") ||
		strings.Contains(msg, "permission denied")
}
