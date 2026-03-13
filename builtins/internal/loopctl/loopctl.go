// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package loopctl

import (
	"strconv"

	"github.com/DataDog/rshell/builtins"
)

// LoopControl implements the shared logic for the break and continue builtins.
func LoopControl(callCtx *builtins.CallContext, name string, args []string) builtins.Result {
	if !callCtx.InLoop {
		callCtx.Errf("%s is only useful in a loop\n", name)
		return builtins.Result{}
	}

	n := 1
	switch len(args) {
	case 0:
	case 1:
		parsed, err := strconv.Atoi(args[0])
		if err != nil {
			callCtx.Errf("%s: %s: numeric argument required\n", name, args[0])
			return builtins.Result{Code: 128, Exiting: true}
		}
		if parsed < 1 {
			callCtx.Errf("%s: %s: loop count out of range\n", name, args[0])
			return builtins.Result{Code: 1, BreakN: 1}
		}
		n = parsed
	default:
		callCtx.Errf("%s: too many arguments\n", name)
		return builtins.Result{Code: 1, BreakN: 1}
	}

	var r builtins.Result
	if name == "break" {
		r.BreakN = n
	} else {
		r.ContinueN = n
	}
	return r
}
