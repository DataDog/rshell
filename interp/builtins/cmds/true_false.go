// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package cmds

import (
	"context"

	"github.com/DataDog/rshell/interp/builtins"
)

func init() {
	builtins.Register("true", builtinTrue)
	builtins.Register("false", builtinFalse)
}

func builtinTrue(_ context.Context, _ *builtins.CallContext, _ []string) builtins.Result {
	return builtins.Result{}
}

func builtinFalse(_ context.Context, _ *builtins.CallContext, _ []string) builtins.Result {
	return builtins.Result{Code: 1}
}
