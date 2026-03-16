// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package help implements the help builtin command.
//
// help — display available commands
//
// Usage: help
//
// List all available builtin commands with a brief description.
// For detailed information on a specific command, run '<command> --help'.
//
// Exit codes:
//
//	0  Success.
//	1  Arguments were provided.
package help

import (
	"context"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the help builtin command descriptor.
var Cmd = builtins.Command{Name: "help", Description: "display available commands", MakeFlags: builtins.NoFlags(run)}

func run(_ context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	if len(args) > 0 {
		callCtx.Errf("help: too many arguments\n")
		return builtins.Result{Code: 1}
	}

	names := builtins.Names()

	// Find the longest command name for alignment.
	maxLen := 0
	for _, name := range names {
		if len(name) > maxLen {
			maxLen = len(name)
		}
	}

	for _, name := range names {
		meta, ok := builtins.Meta(name)
		if !ok {
			continue
		}
		callCtx.Outf("%-*s  %s\n", maxLen, name, meta.Description)
	}

	callCtx.Out("\nRun '<command> --help' for more information on a specific command.\n")
	return builtins.Result{}
}
