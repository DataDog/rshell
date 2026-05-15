// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package kill implements a guarded kill command.
package kill

import (
	"context"
	"strconv"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the kill builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "kill",
	Description: "terminate an allowed process by pid",
	MakeFlags:   registerFlags,
}

func printUsage(callCtx *builtins.CallContext) {
	callCtx.Out("Usage: kill [-9] PID\n")
	callCtx.Out("Send SIGTERM, or SIGKILL with -9, to PID.\n")
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	forceFlag := fs.BoolP("force", "9", false, "send SIGKILL instead of SIGTERM")
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
		argv := []string{strconv.FormatInt(pid, 10)}
		if *forceFlag {
			argv = []string{"-9", strconv.FormatInt(pid, 10)}
		}
		return callCtx.InvokeHostCommand(ctx, "kill", argv)
	}
}
