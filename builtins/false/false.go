// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package falsecmd implements the false builtin command.
//
// false — return an unsuccessful exit status
//
// Usage: false
//
// Do nothing and return an exit status of 1. All arguments are ignored.
//
// Exit codes:
//
//	1  Always fails.
package falsecmd

import (
	"context"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the false builtin command descriptor.
var Cmd = builtins.Command{Name: "false", Description: "return unsuccessful exit status", MakeFlags: builtins.NoFlags(run)}

func run(_ context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	if len(args) > 0 && args[0] == "--help" {
		callCtx.Out("Usage: false\nExit with a status code indicating failure.\n")
		return builtins.Result{Code: 1}
	}
	return builtins.Result{Code: 1}
}
