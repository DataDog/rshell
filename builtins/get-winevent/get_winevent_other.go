// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package get_winevent

import (
	"context"

	"github.com/DataDog/rshell/builtins"
)

// run is the non-Windows stub. get-winevent wraps the native Windows event
// log service (wevtapi.dll) which has no equivalent on other platforms, so the
// command is explicitly unsupported off Windows.
func run(_ context.Context, callCtx *builtins.CallContext, _ options) builtins.Result {
	callCtx.Errf("get-winevent: only available on Windows\n")
	return builtins.Result{Code: 1}
}
