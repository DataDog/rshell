// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package systemctl implements a guarded systemctl command.
package systemctl

import (
	"context"
	"strings"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the systemctl builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "systemctl",
	Description: "run a restricted service lifecycle or status action",
	MakeFlags:   registerFlags,
}

func printUsage(callCtx *builtins.CallContext) {
	callCtx.Out("Usage: systemctl [--json] ACTION UNIT\n")
	callCtx.Out("       systemctl show --property=ActiveState --value UNIT\n")
	callCtx.Out("Run start, stop, restart, reload, or status for UNIT.\n")
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	propertyFlag := fs.StringP("property", "p", "", "show a supported unit property")
	valueFlag := fs.Bool("value", false, "print only the property value for show")
	jsonFlag := fs.Bool("json", false, "print a structured remediation receipt")
	helpFlag := fs.BoolP("help", "h", false, "print usage and exit")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *helpFlag {
			printUsage(callCtx)
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}
		if len(args) == 0 {
			callCtx.Errf("systemctl: expected ACTION and UNIT\n")
			return builtins.Result{Code: 1}
		}
		switch args[0] {
		case "restart", "start", "stop", "reload", "status":
			if len(args) != 2 {
				callCtx.Errf("systemctl: expected ACTION and UNIT\n")
				return builtins.Result{Code: 1}
			}
			if *propertyFlag != "" || *valueFlag {
				callCtx.Errf("systemctl: --property and --value are only supported with show\n")
				return builtins.Result{Code: 1}
			}
			hostArgs := []string{args[0], "--", args[1]}
			if *jsonFlag {
				return runJSON(ctx, callCtx, args[0], args[1], hostArgs)
			}
			return callCtx.InvokeHostCommand(ctx, "systemctl", hostArgs)
		case "show":
			if len(args) != 2 {
				callCtx.Errf("systemctl: expected show UNIT\n")
				return builtins.Result{Code: 1}
			}
			if *propertyFlag != "ActiveState" {
				callCtx.Errf("systemctl: show requires --property=ActiveState\n")
				return builtins.Result{Code: 1}
			}
			if !*valueFlag {
				callCtx.Errf("systemctl: show requires --value\n")
				return builtins.Result{Code: 1}
			}
			hostArgs := activeStateArgs(args[1])
			if *jsonFlag {
				return runJSON(ctx, callCtx, "show", args[1], hostArgs)
			}
			return callCtx.InvokeHostCommand(ctx, "systemctl", hostArgs)
		default:
			callCtx.Errf("systemctl: unsupported action: %s\n", args[0])
			return builtins.Result{Code: 1}
		}
	}
}

type receipt struct {
	Unit        string `json:"unit"`
	Action      string `json:"action"`
	ActiveState string `json:"active_state"`
	ExitCode    uint8  `json:"exit_code"`
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
}

func runJSON(ctx context.Context, callCtx *builtins.CallContext, action, unit string, hostArgs []string) builtins.Result {
	actionHost, res, ok := callCtx.CaptureHostCommand(ctx, "systemctl", hostArgs)
	if !ok {
		return res
	}
	stateHost := actionHost
	if action != "show" {
		stateHost, res, ok = callCtx.CaptureHostCommand(ctx, "systemctl", activeStateArgs(unit))
		if !ok {
			return res
		}
	}
	activeState := strings.TrimSpace(stateHost.Stdout)
	exitCode := actionHost.Code
	stderr := actionHost.Stderr
	if actionHost.Code == 0 && stateHost.Code != 0 {
		exitCode = stateHost.Code
		stderr += stateHost.Stderr
	}
	outRes := callCtx.OutJSON(receipt{
		Unit:        unit,
		Action:      action,
		ActiveState: activeState,
		ExitCode:    exitCode,
		Stdout:      actionHost.Stdout,
		Stderr:      stderr,
	})
	if outRes.Code != 0 || outRes.Exiting {
		return outRes
	}
	return builtins.Result{Code: exitCode}
}

func activeStateArgs(unit string) []string {
	return []string{"show", "--property=ActiveState", "--value", "--", unit}
}
