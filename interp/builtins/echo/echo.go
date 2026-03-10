// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package echo

import (
	"context"

	"github.com/DataDog/rshell/interp/builtins"
)

// Cmd is the echo builtin command descriptor.
var Cmd = builtins.Command{Name: "echo", Run: run}

func run(_ context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	for i, arg := range args {
		if i > 0 {
			callCtx.Out(" ")
		}
		callCtx.Out(arg)
	}
	callCtx.Out("\n")
	return builtins.Result{}
}
