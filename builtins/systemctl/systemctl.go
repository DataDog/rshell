// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package systemctl implements a guarded systemctl command.
package systemctl

import (
	"context"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the systemctl builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "systemctl",
	Description: "run a restricted service lifecycle action",
	MakeFlags:   registerFlags,
}

func printUsage(callCtx *builtins.CallContext) {
	callCtx.Out("Usage: systemctl ACTION UNIT\n")
	callCtx.Out("Run start, stop, restart, or reload for UNIT.\n")
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	helpFlag := fs.BoolP("help", "h", false, "print usage and exit")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *helpFlag {
			printUsage(callCtx)
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}
		if len(args) != 2 {
			callCtx.Errf("systemctl: expected ACTION and UNIT\n")
			return builtins.Result{Code: 1}
		}
		switch args[0] {
		case "restart", "start", "stop", "reload":
		default:
			callCtx.Errf("systemctl: unsupported action: %s\n", args[0])
			return builtins.Result{Code: 1}
		}
		return callCtx.InvokeHostCommand(ctx, "systemctl", args)
	}
}
