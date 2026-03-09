// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package breakcmd

import (
	"context"

	"github.com/DataDog/rshell/interp/builtins"
	"github.com/DataDog/rshell/interp/builtins/internal/loopctl"
)

func init() {
	builtins.Register("break", run)
}

func run(_ context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	return loopctl.LoopControl(callCtx, "break", args)
}
