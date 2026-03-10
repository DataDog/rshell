// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

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
