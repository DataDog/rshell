// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package traceroutelib wraps the datadog-traceroute library for use by the
// traceroute builtin. It lives under builtins/internal/ so it is exempt from
// the strict symbol allowlist that applies to builtin command implementations.
package traceroutelib

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/DataDog/datadog-traceroute/result"
	"github.com/DataDog/datadog-traceroute/traceroute"
)

// Params holds the user-facing parameters for a traceroute invocation.
type Params struct {
	Hostname              string
	Port                  int
	Protocol              string
	MinTTL                int
	MaxTTL                int
	Delay                 int
	Timeout               time.Duration
	TCPMethod             string
	WantV6                bool
	ReverseDns            bool
	Queries               int
	E2eQueries            int
	SkipPrivateHops       bool
	CollectSourcePublicIP bool
}

// Run executes a traceroute with the given parameters and returns the results.
func Run(ctx context.Context, p Params) (*result.Results, error) {
	tr := traceroute.NewTraceroute()

	params := traceroute.TracerouteParams{
		Hostname:              p.Hostname,
		Port:                  p.Port,
		Protocol:              strings.ToLower(p.Protocol),
		MinTTL:                p.MinTTL,
		MaxTTL:                p.MaxTTL,
		Delay:                 p.Delay,
		Timeout:               p.Timeout,
		TCPMethod:             traceroute.TCPMethod(p.TCPMethod),
		WantV6:                p.WantV6,
		ReverseDns:            p.ReverseDns,
		TracerouteQueries:     p.Queries,
		E2eQueries:            p.E2eQueries,
		SkipPrivateHops:       p.SkipPrivateHops,
		CollectSourcePublicIP: p.CollectSourcePublicIP,
	}

	return tr.RunTraceroute(ctx, params)
}

// FormatJSON returns the results as indented JSON.
func FormatJSON(results *result.Results) (string, error) {
	data, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data) + "\n", nil
}

// FormatText returns the results in traditional traceroute text format.
func FormatText(results *result.Results) string {
	var sb strings.Builder

	// Header
	destIP := ""
	if len(results.Traceroute.Runs) > 0 {
		run := results.Traceroute.Runs[0]
		if run.Destination.IPAddress != nil {
			destIP = run.Destination.IPAddress.String()
		}
	}
	if destIP != "" {
		fmt.Fprintf(&sb, "traceroute to %s (%s), %d hops max, port %d, protocol %s\n",
			results.Destination.Hostname, destIP,
			int(results.Traceroute.HopCount.Max),
			results.Destination.Port,
			strings.ToLower(results.Protocol))
	} else {
		fmt.Fprintf(&sb, "traceroute to %s, %d hops max, port %d, protocol %s\n",
			results.Destination.Hostname,
			int(results.Traceroute.HopCount.Max),
			results.Destination.Port,
			strings.ToLower(results.Protocol))
	}

	if len(results.Traceroute.Runs) == 0 {
		return sb.String()
	}

	// Use the first run for the main output (traditional traceroute shows one run)
	run := results.Traceroute.Runs[0]
	for _, hop := range run.Hops {
		if hop == nil {
			continue
		}
		if hop.IPAddress == nil || hop.IPAddress.Equal(net.IP{}) {
			fmt.Fprintf(&sb, "%2d  * * *\n", hop.TTL)
			continue
		}

		// Format: TTL  hostname (IP)  RTT ms
		hostStr := hop.IPAddress.String()
		if len(hop.ReverseDns) > 0 && hop.ReverseDns[0] != "" {
			hostStr = fmt.Sprintf("%s (%s)", hop.ReverseDns[0], hop.IPAddress.String())
		} else {
			hostStr = fmt.Sprintf("%s (%s)", hop.IPAddress.String(), hop.IPAddress.String())
		}

		fmt.Fprintf(&sb, "%2d  %s  %.3f ms\n", hop.TTL, hostStr, hop.RTT)
	}

	// E2E probe summary if present
	if results.E2eProbe.PacketsSent > 0 {
		fmt.Fprintf(&sb, "\n--- e2e probe statistics ---\n")
		fmt.Fprintf(&sb, "%d packets transmitted, %d received, %.1f%% packet loss\n",
			results.E2eProbe.PacketsSent,
			results.E2eProbe.PacketsReceived,
			float64(results.E2eProbe.PacketLossPercentage)*100)
		if results.E2eProbe.PacketsReceived > 0 {
			fmt.Fprintf(&sb, "rtt min/avg/max/jitter = %.3f/%.3f/%.3f/%.3f ms\n",
				results.E2eProbe.RTT.Min,
				results.E2eProbe.RTT.Avg,
				results.E2eProbe.RTT.Max,
				results.E2eProbe.Jitter)
		}
	}

	return sb.String()
}
