// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import "context"

func init() {
	register("echo", builtinEcho)
}

func builtinEcho(_ context.Context, callCtx *CallContext, args []string) Result {
	for i, arg := range args {
		if i > 0 {
			callCtx.Out(" ")
		}
		callCtx.Out(arg)
	}
	callCtx.Out("\n")
	return Result{}
}
