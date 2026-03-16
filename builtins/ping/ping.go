// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package ping implements the ping builtin command using the
// datadog-traceroute library's ICMP E2E probe mode.
//
// ping — send ICMP ECHO_REQUEST to network hosts
//
// Usage: ping [OPTION]... HOST
//
// Send ICMP echo requests to the specified HOST and report round-trip
// times. Uses the datadog-traceroute library's E2E probe functionality
// internally.
//
// Accepted flags:
//
//	-c, --count N
//	    Stop after sending N probes (default 4). Must be a positive
//	    integer. Clamped to a maximum of 1000 to prevent abuse.
//
//	-W, --timeout N
//	    Time to wait for a response, in seconds (default 2). Must be
//	    a positive integer.
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
//	PING <host>: 56 data bytes
//	64 bytes from <ip>: icmp_seq=0 time=1.234 ms
//	...
//	--- <host> ping statistics ---
//	N packets transmitted, M packets received, X% packet loss
//	round-trip min/avg/max = A/B/C ms
//
// Memory safety:
//
//	The command does not read files or allocate unbounded buffers.
//	All network operations are bounded by the timeout and count
//	parameters. Context cancellation is respected.
package ping

import (
	"context"
	"time"

	"github.com/DataDog/datadog-traceroute/traceroute"

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
	timeout := fs.IntP("timeout", "W", 2, "timeout in seconds per probe")
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

		callCtx.Outf("PING %s: 56 data bytes\n", host)

		// Note: The Delay field is used as SendDelay for traceroute hops only.
		// For E2E probes (TracerouteQueries=0), the inter-probe delay is computed
		// internally by the library as min(MaxTTL*Timeout/E2eQueries, 1s).
		// We still accept and validate -i for forward compatibility with future
		// library versions that may support configurable E2E probe delays.
		params := traceroute.TracerouteParams{
			Hostname:          host,
			Protocol:          "icmp",
			MaxTTL:            64,
			Timeout:           time.Duration(*timeout) * time.Second,
			Delay:             int(*interval * 1000), // milliseconds
			TracerouteQueries: 0,
			E2eQueries:        *count,
		}

		t := traceroute.NewTraceroute()
		results, err := t.RunTraceroute(ctx, params)
		if err != nil {
			callCtx.Errf("ping: %s: %s\n", host, err.Error())
			return builtins.Result{Code: 1}
		}

		// Print per-probe RTTs.
		probe := results.E2eProbe
		for i, rtt := range probe.RTTs {
			if ctx.Err() != nil {
				break
			}
			if rtt <= 0 {
				continue // skip failed probes (library records 0.0 for failures)
			}
			callCtx.Outf("64 bytes from %s: icmp_seq=%d time=%.3f ms\n",
				results.Destination.Hostname, i, rtt)
		}

		// Print summary statistics.
		callCtx.Outf("\n--- %s ping statistics ---\n", host)
		callCtx.Outf("%d packets transmitted, %d packets received, %.1f%% packet loss\n",
			probe.PacketsSent, probe.PacketsReceived, float64(probe.PacketLossPercentage)*100)

		if probe.PacketsReceived > 0 {
			callCtx.Outf("round-trip min/avg/max = %.3f/%.3f/%.3f ms\n",
				probe.RTT.Min, probe.RTT.Avg, probe.RTT.Max)
		}

		// Exit code 1 if any packet loss occurred.
		if probe.PacketsReceived < probe.PacketsSent {
			return builtins.Result{Code: 1}
		}
		return builtins.Result{}
	}
}
