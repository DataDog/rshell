// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package falsecmd

import (
	"context"

	"github.com/DataDog/rshell/interp/builtins"
)

func init() {
	builtins.Register("false", run)
}

func run(_ context.Context, _ *builtins.CallContext, _ []string) builtins.Result {
	return builtins.Result{Code: 1}
}
