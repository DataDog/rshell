// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import "context"

func init() {
	register("true", builtinTrue)
	register("false", builtinFalse)
}

func builtinTrue(_ context.Context, _ *CallContext, _ []string) Result {
	return Result{}
}

func builtinFalse(_ context.Context, _ *CallContext, _ []string) Result {
	return Result{Code: 1}
}
