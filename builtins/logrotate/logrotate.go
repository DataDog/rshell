// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package logrotate implements a guarded logrotate command.
package logrotate

import (
	"context"
	"os"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the logrotate builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "logrotate",
	Description: "rotate one allowed log path",
	MakeFlags:   registerFlags,
}

func printUsage(callCtx *builtins.CallContext) {
	callCtx.Out("Usage: logrotate PATH\n")
	callCtx.Out("Rotate PATH using the scenario-provided logrotate wrapper.\n")
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	helpFlag := fs.BoolP("help", "h", false, "print usage and exit")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *helpFlag {
			printUsage(callCtx)
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}
		if len(args) != 1 {
			callCtx.Errf("logrotate: expected exactly one path\n")
			return builtins.Result{Code: 1}
		}
		if callCtx.OpenExistingFileForWrite == nil {
			callCtx.Errf("logrotate: file write is not available\n")
			return builtins.Result{Code: 1}
		}
		f, err := callCtx.OpenExistingFileForWrite(ctx, args[0])
		if err != nil {
			callCtx.Errf("logrotate: %s: %s\n", args[0], callCtx.PortableErr(err))
			return builtins.Result{Code: 1}
		}
		return callCtx.InvokeHostCommandWithFiles(ctx, "logrotate", []string{"--", builtins.HostExtraFilePath(0)}, []*os.File{f})
	}
}
