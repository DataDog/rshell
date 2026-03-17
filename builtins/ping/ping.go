// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package ping implements the ping builtin command using the
// pro-bing library for ICMP echo requests.
//
// ping — send ICMP ECHO_REQUEST to network hosts
//
// Usage: ping [OPTION]... HOST
//
// Send ICMP echo requests to the specified HOST and report round-trip
// times. Attempts privileged (raw socket) mode first; falls back to
// unprivileged (UDP-based ICMP) mode on Linux/macOS if the process
// lacks CAP_NET_RAW. On Windows, privileged mode is always used
// (works without elevation on Windows 10+).
//
// Accepted flags:
//
//	-c, --count N
//	    Stop after sending N probes (default 4). Must be a positive
//	    integer. Clamped to a maximum of 1000 to prevent abuse.
//
//	-W, --timeout N
//	    Time to wait for a response, in seconds (default 2). Must be
//	    a positive integer. Clamped to a maximum of 60.
//
//	-i, --interval N
//	    Wait N seconds between sending each probe (default 1). Must be
//	    a positive number (fractional seconds allowed).
//
//	--help
//	    Print usage to stdout and exit 0.
//
// Exit codes:
//
//	0  All probes received responses.
//	1  At least one error occurred (bad arguments, DNS failure, packet
//	   loss, or timeout).
//
// Output format:
//
//	PING <host> (<ip>): 56 data bytes
//	64 bytes from <ip>: icmp_seq=1 ttl=64 time=1.234 ms
//	...
//	--- <host> ping statistics ---
//	N packets transmitted, M packets received, X% packet loss
//	round-trip min/avg/max/stddev = A/B/C/D ms
//
// Memory safety:
//
//	The command does not read files or allocate unbounded buffers.
//	All network operations are bounded by the timeout and count
//	parameters. Context cancellation is respected.
package ping

import (
	"context"
	"runtime"
	"strings"
	"time"

	probing "github.com/prometheus-community/pro-bing"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the ping builtin command descriptor.
var Cmd = builtins.Command{Name: "ping", Description: "send ICMP ECHO_REQUEST to network hosts", MakeFlags: registerFlags}

// MaxCount is the maximum number of pings allowed to prevent abuse.
const MaxCount = 1000

// MaxTimeout is the maximum per-probe timeout in seconds.
const MaxTimeout = 60

// MaxInterval is the maximum interval between probes in seconds.
const MaxInterval = 60

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	count := fs.IntP("count", "c", 4, "stop after sending N probes")
	timeout := fs.IntP("timeout", "W", 2, "time to wait for a response, in seconds")
	interval := fs.Float64P("interval", "i", 1.0, "interval in seconds between probes")
	help := fs.BoolP("help", "h", false, "print usage and exit")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: ping [OPTION]... HOST\n")
			callCtx.Out("Send ICMP ECHO_REQUEST to network hosts.\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}

		if len(args) == 0 {
			callCtx.Errf("ping: missing host operand\n")
			return builtins.Result{Code: 1}
		}
		if len(args) > 1 {
			callCtx.Errf("ping: too many arguments\n")
			return builtins.Result{Code: 1}
		}

		host := args[0]

		// Validate count.
		if *count <= 0 {
			callCtx.Errf("ping: invalid count: %d\n", *count)
			return builtins.Result{Code: 1}
		}
		if *count > MaxCount {
			*count = MaxCount
		}

		// Validate timeout.
		if *timeout <= 0 {
			callCtx.Errf("ping: invalid timeout: %d\n", *timeout)
			return builtins.Result{Code: 1}
		}
		if *timeout > MaxTimeout {
			*timeout = MaxTimeout
		}

		// Validate interval.
		if *interval <= 0 || *interval != *interval { // NaN != NaN
			callCtx.Errf("ping: invalid interval: %g\n", *interval)
			return builtins.Result{Code: 1}
		}
		if *interval > MaxInterval {
			*interval = MaxInterval
		}

		if ctx.Err() != nil {
			return builtins.Result{Code: 1}
		}

		// Resolve hostname before printing the header so DNS errors are
		// reported cleanly without a dangling header line.
		pinger, err := probing.NewPinger(host)
		if err != nil {
			callCtx.Errf("ping: %s: %s\n", host, err)
			return builtins.Result{Code: 1}
		}

		ip := pinger.IPAddr().String()
		callCtx.Outf("PING %s (%s): 56 data bytes\n", host, ip)

		intervalDur := time.Duration(*interval * float64(time.Second))
		// Total timeout accommodates all probes: each probe may wait up to
		// *timeout seconds for a reply, plus *interval seconds before the
		// next send, with a 1-second buffer.
		totalTimeout := time.Duration(*count)*(time.Duration(*timeout)*time.Second+intervalDur) + time.Second

		onRecv := func(pkt *probing.Packet) {
			// icmp_seq is 1-indexed to match standard ping (Linux/macOS).
			callCtx.Outf("%d bytes from %s: icmp_seq=%d ttl=%d time=%.3f ms\n",
				pkt.Nbytes, pkt.IPAddr.String(), pkt.Seq+1, pkt.TTL,
				float64(pkt.Rtt)/float64(time.Millisecond))
		}

		// On Windows, privileged mode is required but works without elevation.
		// On other platforms, try raw-socket (privileged) first and fall back
		// to UDP-based (unprivileged) ICMP on permission errors.
		modes := []bool{true, false}
		if runtime.GOOS == "windows" {
			modes = []bool{true}
		}

		var stats *probing.Statistics
		var runErr error
		for i, privileged := range modes {
			var p *probing.Pinger
			if i == 0 {
				p = pinger
			} else {
				// Re-create pinger to reset internal state for the fallback.
				p, err = probing.NewPinger(host)
				if err != nil {
					runErr = err
					break
				}
			}
			p.Count = *count
			p.Interval = intervalDur
			p.Timeout = totalTimeout
			p.OnRecv = onRecv
			p.SetPrivileged(privileged)

			runErr = p.RunWithContext(ctx)
			if runErr == nil || !isPermissionError(runErr) {
				stats = p.Statistics()
				break
			}
		}

		// If no packets were sent, report the error and exit 1.
		if stats == nil || stats.PacketsSent == 0 {
			if ctx.Err() != nil {
				callCtx.Errf("ping: %s: %s\n", host, ctx.Err())
			} else if runErr != nil {
				callCtx.Errf("ping: %s: %s\n", host, runErr)
			}
			return builtins.Result{Code: 1}
		}

		// Print summary statistics.
		callCtx.Outf("\n--- %s ping statistics ---\n", host)
		callCtx.Outf("%d packets transmitted, %d packets received, %.1f%% packet loss\n",
			stats.PacketsSent, stats.PacketsRecv, stats.PacketLoss)

		if stats.PacketsRecv > 0 {
			callCtx.Outf("round-trip min/avg/max/stddev = %.3f/%.3f/%.3f/%.3f ms\n",
				float64(stats.MinRtt)/float64(time.Millisecond),
				float64(stats.AvgRtt)/float64(time.Millisecond),
				float64(stats.MaxRtt)/float64(time.Millisecond),
				float64(stats.StdDevRtt)/float64(time.Millisecond))
		}

		if stats.PacketsRecv < stats.PacketsSent {
			return builtins.Result{Code: 1}
		}
		return builtins.Result{}
	}
}

// isPermissionError returns true if err indicates a socket permission error,
// which means the process lacks the capability to open a raw ICMP socket.
func isPermissionError(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "operation not permitted") ||
		strings.Contains(s, "permission denied") ||
		strings.Contains(s, "access is denied")
}
