// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package kill implements a guarded kill command.
package kill

import (
	"context"
	"fmt"
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
		pid, err := strconv.Atoi(args[0])
		if err != nil || pid <= 0 {
			callCtx.Errf("kill: invalid pid: %s\n", args[0])
			return builtins.Result{Code: 1}
		}
		if *timeoutFlag < 0 {
			callCtx.Errf("kill: invalid timeout: %s\n", timeoutFlag.String())
			return builtins.Result{Code: 1}
		}
		if *jsonFlag {
			return runJSON(ctx, callCtx, pid, *forceFlag, *timeoutFlag)
		}
		if err := signalPID(pid, *forceFlag); err != nil {
			callCtx.Errf("kill: %s\n", err)
			return builtins.Result{Code: 1}
		}
		timedOut, waitErr, waitRes, ok := waitForExit(ctx, pid, *timeoutFlag)
		if !ok {
			return waitRes
		}
		if waitErr != nil {
			callCtx.Errf("kill: %s\n", waitErr)
			return builtins.Result{Code: 1}
		}
		if timedOut {
			callCtx.Errf("kill: timed out waiting for pid %d to exit\n", pid)
			return builtins.Result{Code: 1}
		}
		return builtins.Result{}
	}
}

type receipt struct {
	PID             int    `json:"pid"`
	Force           bool   `json:"force"`
	Signal          string `json:"signal"`
	TimedOut        bool   `json:"timed_out"`
	ExitCode        uint8  `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutTruncated bool   `json:"stdout_truncated,omitempty"`
	StderrTruncated bool   `json:"stderr_truncated,omitempty"`
}

func runJSON(ctx context.Context, callCtx *builtins.CallContext, pid int, force bool, timeout time.Duration) builtins.Result {
	exitCode := uint8(0)
	stderr := ""
	timedOut := false
	if err := signalPID(pid, force); err != nil {
		exitCode = 1
		stderr = fmt.Sprintf("kill: %s\n", err)
	} else {
		var waitRes builtins.Result
		var ok bool
		var waitErr error
		timedOut, waitErr, waitRes, ok = waitForExit(ctx, pid, timeout)
		if !ok {
			return waitRes
		}
		if waitErr != nil {
			exitCode = 1
			stderr = fmt.Sprintf("kill: %s\n", waitErr)
		} else if timedOut {
			exitCode = 1
			stderr = fmt.Sprintf("kill: timed out waiting for pid %d to exit\n", pid)
		}
	}
	outRes := callCtx.OutJSON(receipt{
		PID:      pid,
		Force:    force,
		Signal:   signalName(force),
		TimedOut: timedOut,
		ExitCode: exitCode,
		Stdout:   "",
		Stderr:   stderr,
	})
	if outRes.Code != 0 || outRes.Exiting {
		return outRes
	}
	return builtins.Result{Code: exitCode}
}

func waitForExit(ctx context.Context, pid int, timeout time.Duration) (bool, error, builtins.Result, bool) {
	if timeout == 0 {
		return false, nil, builtins.Result{}, true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		alive, err := pidAlive(pid)
		if err != nil {
			return false, err, builtins.Result{}, true
		}
		if !alive {
			return false, nil, builtins.Result{}, true
		}
		select {
		case <-ctx.Done():
			return false, nil, builtins.Result{Code: 1, Exiting: true}, false
		case <-timer.C:
			return true, nil, builtins.Result{}, true
		case <-ticker.C:
		}
	}
}
