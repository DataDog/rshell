// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package tee implements a guarded tee command.
package tee

import (
	"context"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the tee builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "tee",
	Description: "write stdin to stdout and one allowed file",
	MakeFlags:   registerFlags,
}

func printUsage(callCtx *builtins.CallContext) {
	callCtx.Out("Usage: tee [OPTION] FILE\n")
	callCtx.Out("Copy standard input to standard output and FILE.\n")
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	appendFlag := fs.BoolP("append", "a", false, "append to FILE instead of overwriting")
	helpFlag := fs.BoolP("help", "h", false, "print usage and exit")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *helpFlag {
			printUsage(callCtx)
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}
		if len(args) != 1 {
			callCtx.Errf("tee: expected exactly one file\n")
			return builtins.Result{Code: 1}
		}
		if callCtx.CheckFileWrite == nil {
			callCtx.Errf("tee: file write validation is not available\n")
			return builtins.Result{Code: 1}
		}
		if err := callCtx.CheckFileWrite(ctx, args[0]); err != nil {
			callCtx.Errf("tee: %s: %s\n", args[0], callCtx.PortableErr(err))
			return builtins.Result{Code: 1}
		}
		argv := []string{"--", args[0]}
		if *appendFlag {
			argv = []string{"-a", "--", args[0]}
		}
		return callCtx.InvokeHostCommand(ctx, "tee", argv)
	}
}
