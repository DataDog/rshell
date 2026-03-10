// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package exit implements the exit builtin command.
//
// exit — cause the shell to exit
//
// Usage: exit [N]
//
// Exit the shell with status N. If N is omitted, the exit status is
// that of the last command executed. If N is not a valid integer, the
// shell prints an error and exits with status 2.
//
// Exit codes:
//
//	N    The supplied exit status (truncated to uint8).
//	2    Invalid (non-numeric) argument.
//	1    Too many arguments.
package exit

import (
	"context"
	"strconv"

	"github.com/DataDog/rshell/interp/builtins"
)

// Cmd is the exit builtin command descriptor.
var Cmd = builtins.Command{Name: "exit", MakeFlags: builtins.NoFlags(run)}

func run(_ context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	var r builtins.Result
	if len(args) > 0 && args[0] == "--" {
		args = args[1:]
	}
	switch len(args) {
	case 0:
		r.Code = callCtx.LastExitCode
	case 1:
		n, err := strconv.Atoi(args[0])
		if err != nil {
			callCtx.Errf("invalid exit status code: %q\n", args[0])
			r.Code = 2
			// In bash, exit with invalid args still terminates the shell.
			r.Exiting = true
			return r
		}
		r.Code = uint8(n)
	default:
		callCtx.Errf("exit cannot take multiple arguments\n")
		r.Code = 1
		// In bash, exit with too many args still terminates the shell.
		r.Exiting = true
		return r
	}
	r.Exiting = true
	return r
}
