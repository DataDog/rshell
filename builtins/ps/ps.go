// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package ps implements the ps builtin command.
//
// ps — report process status
//
// Usage: ps [-e|-A] [-f] [-p PIDLIST] [-h]
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
//	-h, --help
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
var Cmd = builtins.Command{Name: "ps", MakeFlags: registerFlags}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	allProcs := fs.BoolP("all", "e", false, "select all processes")
	_ = fs.BoolP("All", "A", false, "select all processes (same as -e)")
	fullFmt := fs.BoolP("full", "f", false, "full-format listing")
	pidList := fs.StringP("pid", "p", "", "select by PID list (comma or space separated)")
	help := fs.BoolP("help", "h", false, "print usage and exit")

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

		switch {
		case *pidList != "":
			// -p: select specific PIDs.
			pids, parseErr := parsePIDs(*pidList)
			if parseErr != nil {
				callCtx.Errf("ps: %v\n", parseErr)
				return builtins.Result{Code: 1}
			}
			procs, err = procinfo.GetByPIDs(ctx, pids)

		case showAll:
			// -e / -A: all processes.
			procs, err = procinfo.ListAll(ctx)

		default:
			// Default: current session processes.
			procs, err = procinfo.GetSession(ctx)
		}

		if err != nil {
			callCtx.Errf("ps: %v\n", err)
			return builtins.Result{Code: 1}
		}

		printProcs(callCtx, procs, full)
		return builtins.Result{}
	}
}

// parsePIDs parses a comma- or whitespace-separated list of PIDs.
func parsePIDs(s string) ([]int, error) {
	// Replace commas with spaces for uniform splitting.
	s = strings.ReplaceAll(s, ",", " ")
	parts := strings.Split(s, " ")
	pids := make([]int, 0, len(parts))
	for _, f := range parts {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		pid, err := strconv.Atoi(f)
		if err != nil || pid < 0 {
			return nil, fmt.Errorf("invalid PID: %s", f)
		}
		pids = append(pids, pid)
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
