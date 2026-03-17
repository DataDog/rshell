// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package traceroute implements the traceroute builtin command.
//
// traceroute — print the route packets take to a network host
//
// Usage: traceroute [OPTION]... HOST
//
// Traces the network path from the local machine to HOST, showing each
// intermediate hop along the route. Uses the datadog-traceroute library
// which supports UDP, TCP, and ICMP protocols.
//
// Accepted flags:
//
//	-m, --max-hops N         Maximum number of hops (default: 30)
//	-f, --first-ttl N        Start from the given TTL (default: 1)
//	-w, --wait TIMEOUT       Probe timeout in seconds (default: 3)
//	-q, --queries N          Number of traceroute queries (default: 3)
//	-p, --port N             Destination port (default: 33434)
//	    --protocol PROTO     Protocol: udp, tcp, icmp (default: udp)
//	-n, --no-dns             Do not resolve IP addresses to hostnames
//	-6, --ipv6               Prefer IPv6 resolution
//	    --json               Output results as JSON
//	    --delay MS            Delay between probes in ms (default: 50)
//	    --tcp-method METHOD  TCP method: syn, sack, prefer_sack (default: syn)
//	    --skip-private-hops  Remove private IP hops from results
//	    --e2e-queries N      Number of end-to-end probes (default: 50)
//	-h, --help               Show usage information
//
// Exit codes:
//
//	0  Traceroute completed successfully.
//	1  An error occurred (invalid arguments, network error, etc.).
package traceroute

import (
	"context"
	"strings"
	"time"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/traceroutelib"
)

// Cmd is the traceroute builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "traceroute",
	Description: "print the route packets take to a network host",
	MakeFlags:   registerFlags,
}

// maxHops is the upper bound for --max-hops to prevent unreasonably long traces.
const maxHops = 255

// maxPort is the maximum valid port number.
const maxPort = 65535

// maxQueries caps the number of traceroute queries to prevent excessive resource use.
const maxQueries = 100

// maxE2eQueries caps the number of e2e probes.
const maxE2eQueries = 1000

// maxDelay caps the delay between probes in milliseconds.
const maxDelay = 60000

// maxTimeout caps the per-probe timeout in seconds.
const maxTimeout = 300

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	maxHopsFlag := fs.IntP("max-hops", "m", 30, "maximum number of hops")
	firstTTL := fs.IntP("first-ttl", "f", 1, "start from the given TTL")
	wait := fs.IntP("wait", "w", 3, "probe timeout in seconds")
	queries := fs.IntP("queries", "q", 3, "number of traceroute queries")
	port := fs.IntP("port", "p", 33434, "destination port")
	protocol := fs.String("protocol", "udp", "protocol: udp, tcp, icmp")
	noDns := fs.BoolP("no-dns", "n", false, "do not resolve IP addresses to hostnames")
	ipv6 := fs.BoolP("ipv6", "6", false, "prefer IPv6 resolution")
	jsonOut := fs.Bool("json", false, "output results as JSON")
	delay := fs.Int("delay", 50, "delay between probes in ms")
	tcpMethod := fs.String("tcp-method", "syn", "TCP method: syn, sack, prefer_sack")
	skipPrivate := fs.Bool("skip-private-hops", false, "remove private IP hops from results")
	e2eQueriesFlag := fs.Int("e2e-queries", 50, "number of end-to-end probes")
	help := fs.BoolP("help", "h", false, "show usage information")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: traceroute [OPTION]... HOST\n\n")
			callCtx.Out("Print the route packets take to a network host.\n\n")
			callCtx.Out("Options:\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}

		if len(args) == 0 {
			callCtx.Errf("traceroute: missing host operand\n")
			return builtins.Result{Code: 1}
		}
		if len(args) > 1 {
			callCtx.Errf("traceroute: too many arguments\n")
			return builtins.Result{Code: 1}
		}

		hostname := args[0]
		if hostname == "" {
			callCtx.Errf("traceroute: empty hostname\n")
			return builtins.Result{Code: 1}
		}

		// Validate numeric parameters
		if *maxHopsFlag < 1 || *maxHopsFlag > maxHops {
			callCtx.Errf("traceroute: --max-hops must be between 1 and %d\n", maxHops)
			return builtins.Result{Code: 1}
		}
		if *firstTTL < 1 || *firstTTL > *maxHopsFlag {
			callCtx.Errf("traceroute: --first-ttl must be between 1 and max-hops (%d)\n", *maxHopsFlag)
			return builtins.Result{Code: 1}
		}
		if *wait < 1 || *wait > maxTimeout {
			callCtx.Errf("traceroute: --wait must be between 1 and %d seconds\n", maxTimeout)
			return builtins.Result{Code: 1}
		}
		if *queries < 1 || *queries > maxQueries {
			callCtx.Errf("traceroute: --queries must be between 1 and %d\n", maxQueries)
			return builtins.Result{Code: 1}
		}
		if *port < 1 || *port > maxPort {
			callCtx.Errf("traceroute: --port must be between 1 and %d\n", maxPort)
			return builtins.Result{Code: 1}
		}
		if *delay < 0 || *delay > maxDelay {
			callCtx.Errf("traceroute: --delay must be between 0 and %d ms\n", maxDelay)
			return builtins.Result{Code: 1}
		}
		if *e2eQueriesFlag < 0 || *e2eQueriesFlag > maxE2eQueries {
			callCtx.Errf("traceroute: --e2e-queries must be between 0 and %d\n", maxE2eQueries)
			return builtins.Result{Code: 1}
		}

		// Validate protocol
		proto := strings.ToLower(*protocol)
		switch proto {
		case "udp", "tcp", "icmp":
			// valid
		default:
			callCtx.Errf("traceroute: unknown protocol %q (must be udp, tcp, or icmp)\n", *protocol)
			return builtins.Result{Code: 1}
		}

		// Validate TCP method
		tcpMeth := strings.ToLower(*tcpMethod)
		switch tcpMeth {
		case "syn", "sack", "prefer_sack":
			// valid
		default:
			callCtx.Errf("traceroute: unknown TCP method %q (must be syn, sack, or prefer_sack)\n", *tcpMethod)
			return builtins.Result{Code: 1}
		}

		// Check context before starting network operation
		if ctx.Err() != nil {
			return builtins.Result{Code: 1}
		}

		params := traceroutelib.Params{
			Hostname:        hostname,
			Port:            *port,
			Protocol:        proto,
			MinTTL:          *firstTTL,
			MaxTTL:          *maxHopsFlag,
			Delay:           *delay,
			Timeout:         time.Duration(*wait) * time.Second,
			TCPMethod:       tcpMeth,
			WantV6:          *ipv6,
			ReverseDns:      !*noDns,
			Queries:         *queries,
			E2eQueries:      *e2eQueriesFlag,
			SkipPrivateHops: *skipPrivate,
		}

		results, err := traceroutelib.Run(ctx, params)
		if err != nil {
			// Check if it was a context cancellation
			if ctx.Err() != nil {
				return builtins.Result{Code: 1}
			}
			callCtx.Errf("traceroute: %v\n", err)
			return builtins.Result{Code: 1}
		}

		if *jsonOut {
			output, err := traceroutelib.FormatJSON(results)
			if err != nil {
				callCtx.Errf("traceroute: failed to format JSON: %v\n", err)
				return builtins.Result{Code: 1}
			}
			callCtx.Out(output)
		} else {
			callCtx.Out(traceroutelib.FormatText(results))
		}

		return builtins.Result{}
	}
}
