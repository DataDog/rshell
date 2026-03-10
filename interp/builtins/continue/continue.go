// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package continuecmd implements the continue builtin command.
//
// continue — resume the next iteration of a for, while, or until loop
//
// Usage: continue [N]
//
// Resume the next iteration of the innermost enclosing loop. If N is
// specified, resume the Nth enclosing loop.
//
// Exit codes:
//
//	0  Iteration resumed successfully.
//	1  Not inside a loop, or invalid argument.
package continuecmd

import (
	"context"

	"github.com/DataDog/rshell/interp/builtins"
	"github.com/DataDog/rshell/interp/builtins/internal/loopctl"
)

// Cmd is the continue builtin command descriptor.
var Cmd = builtins.Command{Name: "continue", Run: run}

func run(_ context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	return loopctl.LoopControl(callCtx, "continue", args)
}
