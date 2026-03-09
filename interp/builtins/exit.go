// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import (
	"context"
	"strconv"
)

func init() {
	register("exit", builtinExit)
}

func builtinExit(_ context.Context, callCtx *CallContext, args []string) Result {
	var r Result
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
