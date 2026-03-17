// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package breakcmd implements the break builtin command.
//
// break — exit from a for, while, or until loop
//
// Usage: break [N]
//
// Exit from the innermost enclosing loop. If N is specified,
// break out of N enclosing loops.
//
// Exit codes:
//
//	0  Loop exited successfully.
//	1  Not inside a loop, or invalid argument.
package breakcmd

import (
	"context"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/loopctl"
)

// Cmd is the break builtin command descriptor.
var Cmd = builtins.Command{Name: "break", Description: "exit from a loop", MakeFlags: builtins.NoFlags(run)}

func run(_ context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	return loopctl.LoopControl(callCtx, "break", args)
}
