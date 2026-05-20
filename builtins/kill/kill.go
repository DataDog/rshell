// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package kill implements a guarded kill command.
package kill

import (
	"context"
	"strconv"
	"time"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the kill builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "kill",
	Description: "terminate an allowed process by pid",
	MakeFlags:   registerFlags,
}

func printUsage(callCtx *builtins.CallContext) {
	callCtx.Out("Usage: kill [-9] [--timeout DURATION] [--json] PID\n")
	callCtx.Out("Send SIGTERM, or SIGKILL with -9, to PID, then poll for exit.\n")
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	forceFlag := fs.BoolP("force", "9", false, "send SIGKILL instead of SIGTERM")
	timeoutFlag := fs.Duration("timeout", 5*time.Second, "maximum time to wait for PID to exit")
	jsonFlag := fs.Bool("json", false, "print a structured remediation receipt")
	helpFlag := fs.BoolP("help", "h", false, "print usage and exit")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *helpFlag {
			printUsage(callCtx)
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}
		if len(args) != 1 {
			callCtx.Errf("kill: expected exactly one pid\n")
			return builtins.Result{Code: 1}
		}
		pid, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil || pid <= 0 {
			callCtx.Errf("kill: invalid pid: %s\n", args[0])
			return builtins.Result{Code: 1}
		}
		if *timeoutFlag < 0 {
			callCtx.Errf("kill: invalid timeout: %s\n", timeoutFlag.String())
			return builtins.Result{Code: 1}
		}
		argv := []string{strconv.FormatInt(pid, 10)}
		if *forceFlag {
			argv = []string{"-9", strconv.FormatInt(pid, 10)}
		}
		if *jsonFlag {
			return runJSON(ctx, callCtx, pid, *forceFlag, *timeoutFlag, argv)
		}
		res := callCtx.InvokeHostCommand(ctx, "kill", argv)
		if res.Code != 0 || res.Exiting {
			return res
		}
		timedOut, waitRes, ok := waitForExit(ctx, callCtx, strconv.FormatInt(pid, 10), *timeoutFlag)
		if !ok {
			return waitRes
		}
		if timedOut {
			callCtx.Errf("kill: timed out waiting for pid %d to exit\n", pid)
		}
		return res
	}
}

type receipt struct {
	PID             int64  `json:"pid"`
	Force           bool   `json:"force"`
	Signal          string `json:"signal"`
	TimedOut        bool   `json:"timed_out"`
	ExitCode        uint8  `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutTruncated bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated bool   `json:"stderr_truncated,omitempty"`
}

func runJSON(ctx context.Context, callCtx *builtins.CallContext, pid int64, force bool, timeout time.Duration, argv []string) builtins.Result {
	host, res, ok := callCtx.CaptureHostCommand(ctx, "kill", argv)
	if !ok {
		return res
	}
	timedOut := false
	if host.Code == 0 {
		var waitRes builtins.Result
		timedOut, waitRes, ok = waitForExit(ctx, callCtx, strconv.FormatInt(pid, 10), timeout)
		if !ok {
			return waitRes
		}
	}
	signal := "SIGTERM"
	if force {
		signal = "SIGKILL"
	}
	outRes := callCtx.OutJSON(receipt{
		PID:             pid,
		Force:           force,
		Signal:          signal,
		TimedOut:        timedOut,
		ExitCode:        host.Code,
		Stdout:          host.Stdout,
		Stderr:          host.Stderr,
		StdoutTruncated: host.StdoutTruncated,
		StderrTruncated: host.StderrTruncated,
	})
	if outRes.Code != 0 || outRes.Exiting {
		return outRes
	}
	return builtins.Result{Code: host.Code}
}

func waitForExit(ctx context.Context, callCtx *builtins.CallContext, pid string, timeout time.Duration) (bool, builtins.Result, bool) {
	if timeout == 0 {
		return false, builtins.Result{}, true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		alive, res, ok := processAlive(ctx, callCtx, pid)
		if !ok {
			return false, res, false
		}
		if !alive {
			return false, builtins.Result{}, true
		}
		select {
		case <-ctx.Done():
			return false, builtins.Result{Code: 1, Exiting: true}, false
		case <-timer.C:
			return true, builtins.Result{}, true
		case <-ticker.C:
		}
	}
}

func processAlive(ctx context.Context, callCtx *builtins.CallContext, pid string) (bool, builtins.Result, bool) {
	host, res, ok := callCtx.CaptureHostCommand(ctx, "kill", []string{"-0", pid})
	if !ok {
		return false, res, false
	}
	return host.Code == 0, builtins.Result{}, true
}
