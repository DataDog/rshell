// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package ps implements the ps builtin command.
//
// ps — report process status
//
// Usage: ps [-e|-A] [-f] [-p PIDLIST] [--help]
//
// Display information about running processes. By default shows processes in
// the current session (ancestor chain from the current process).
//
// Accepted flags:
//
//	-e, -A
//	    Select all processes.
//
//	-f
//	    Full-format listing: UID PID PPID C STIME TTY TIME CMD
//
//	-p PIDLIST
//	    Select processes by comma- or space-separated PID list.
//
//	--help
//	    Print usage to stdout and exit 0.
//
// Output columns (default):
//
//	PID TTY TIME CMD
//
// Output columns (-f):
//
//	UID PID PPID C STIME TTY TIME CMD
//
// Exit codes:
//
//	0  Success (even if 0 processes match).
//	1  Invalid PID value or OS error fetching process list.
package ps

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/procinfo"
)

// Cmd is the ps builtin command descriptor.
var Cmd = builtins.Command{Name: "ps", Description: "report process status", MakeFlags: registerFlags}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	allProcs := fs.BoolP("all", "e", false, "select all processes")
	_ = fs.BoolP("All", "A", false, "select all processes (same as -e)")
	fullFmt := fs.BoolP("full", "f", false, "full-format listing")
	pidList := fs.StringP("pid", "p", "", "select by PID list (comma or space separated)")
	help := fs.Bool("help", false, "print usage and exit")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: ps [-e|-A] [-f] [-p PIDLIST]\n")
			callCtx.Out("Report process status.\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}

		// -A is an alias for -e.
		showAll := *allProcs
		if f := fs.Lookup("All"); f != nil && f.Changed {
			showAll = true
		}

		full := *fullFmt

		var procs []procinfo.ProcInfo
		var err error

		// Detect whether -p was explicitly set (even to empty string).
		pidFlagChanged := fs.Lookup("pid") != nil && fs.Lookup("pid").Changed

		// Merge remaining positional args as blank-separated PIDs.
		// GNU ps allows blank-separated PID lists: ps -p 123 456 or ps 123 456.
		// Non-numeric values are caught by parsePIDs and cause exit 1.
		// If -p was explicitly set (even to empty), do NOT overwrite it with
		// positional args: ps -p '' 1 should still fail, not silently treat 1
		// as the PID list (GNU ps rejects the empty -p value immediately).
		effectivePIDList := *pidList
		if len(args) > 0 {
			for _, arg := range args {
				if effectivePIDList != "" {
					effectivePIDList += " " + arg
				} else if !pidFlagChanged {
					effectivePIDList = arg
				}
			}
			pidFlagChanged = true
		}

		// Validate and parse -p upfront. GNU ps rejects malformed -p input
		// even when combined with -e/-A (verified against debian:bookworm-slim).
		var parsedPIDs []int
		if pidFlagChanged || effectivePIDList != "" {
			var parseErr error
			parsedPIDs, parseErr = parsePIDs(effectivePIDList)
			if parseErr != nil {
				callCtx.Errf("ps: %v\n", parseErr)
				return builtins.Result{Code: 1}
			}
		}

		pidMode := false
		switch {
		case showAll:
			// -e / -A: all processes. Takes priority over -p because it is a
			// superset; GNU ps treats selection options as additive (union), so
			// -e -p <valid_or_missing> still returns all processes and exits 0.
			procs, err = procinfo.ListAll(ctx)

		case len(parsedPIDs) > 0:
			// -p only (no -e/-A): select specific PIDs.
			pidMode = true
			procs, err = procinfo.GetByPIDs(ctx, parsedPIDs)

		default:
			// Default: current session processes.
			procs, err = procinfo.GetSession(ctx)
		}

		if err != nil {
			callCtx.Errf("ps: %v\n", err)
			return builtins.Result{Code: 1}
		}

		printProcs(callCtx, procs, full)
		// GNU ps exits 1 when -p selects no processes (liveness check idiom).
		if pidMode && len(procs) == 0 {
			return builtins.Result{Code: 1}
		}
		return builtins.Result{}
	}
}

// parsePIDs parses a comma- or whitespace-separated list of PIDs.
// Each PID must be a positive integer (> 0).
func parsePIDs(s string) ([]int, error) {
	// Reject empty comma-separated segments: consecutive commas, leading or
	// trailing comma (e.g. "1,,2", ",1", "1,") are invalid PID lists.
	for _, seg := range strings.Split(s, ",") {
		if strings.TrimSpace(seg) == "" {
			return nil, fmt.Errorf("invalid PID list: %s", s)
		}
	}
	// Replace commas with spaces, then split on all whitespace so that
	// tab-delimited PID lists (e.g. ps -p $'1\t2') also work.
	s = strings.ReplaceAll(s, ",", " ")
	parts := strings.Fields(s)
	pids := make([]int, 0, len(parts))
	seen := make(map[int]bool, len(parts))
	for _, f := range parts {
		pid, err := strconv.Atoi(f)
		if err != nil || pid <= 0 {
			return nil, fmt.Errorf("invalid PID: %s", f)
		}
		if !seen[pid] {
			seen[pid] = true
			pids = append(pids, pid)
		}
	}
	if len(pids) == 0 {
		return nil, fmt.Errorf("invalid PID: %s", s)
	}
	return pids, nil
}

// printProcs writes the process list to stdout.
func printProcs(callCtx *builtins.CallContext, procs []procinfo.ProcInfo, full bool) {
	if full {
		callCtx.Outf("%-12s %6s %6s %2s %-5s %-12s %8s %s\n",
			"UID", "PID", "PPID", "C", "STIME", "TTY", "TIME", "CMD")
		for _, p := range procs {
			callCtx.Outf("%-12s %6d %6d %2d %-5s %-12s %8s %s\n",
				p.UID, p.PID, p.PPID, p.CPU, p.STime, p.TTY, p.Time, p.Cmd)
		}
	} else {
		callCtx.Outf("%6s %-12s %8s %s\n", "PID", "TTY", "TIME", "CMD")
		for _, p := range procs {
			callCtx.Outf("%6d %-12s %8s %s\n", p.PID, p.TTY, p.Time, p.Cmd)
		}
	}
}
