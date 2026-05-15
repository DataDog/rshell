// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package truncate implements a guarded truncate command.
package truncate

import (
	"context"
	"strconv"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the truncate builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "truncate",
	Description: "shrink an existing regular file to a byte size",
	MakeFlags:   registerFlags,
}

func printUsage(callCtx *builtins.CallContext) {
	callCtx.Out("Usage: truncate -s SIZE FILE\n")
	callCtx.Out("Shrink FILE to SIZE bytes.\n")
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	sizeFlag := fs.StringP("size", "s", "", "target byte size")
	helpFlag := fs.BoolP("help", "h", false, "print usage and exit")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *helpFlag {
			printUsage(callCtx)
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}
		if *sizeFlag == "" {
			callCtx.Errf("truncate: missing -s SIZE\n")
			return builtins.Result{Code: 1}
		}
		if !isDecimalSize(*sizeFlag) {
			callCtx.Errf("truncate: invalid size: %s\n", *sizeFlag)
			return builtins.Result{Code: 1}
		}
		size, err := strconv.ParseInt(*sizeFlag, 10, 64)
		if err != nil {
			callCtx.Errf("truncate: invalid size: %s\n", *sizeFlag)
			return builtins.Result{Code: 1}
		}
		if len(args) != 1 {
			callCtx.Errf("truncate: expected exactly one file\n")
			return builtins.Result{Code: 1}
		}
		info, err := callCtx.StatFile(ctx, args[0])
		if err != nil {
			callCtx.Errf("truncate: %s: %s\n", args[0], callCtx.PortableErr(err))
			return builtins.Result{Code: 1}
		}
		if !info.Mode().IsRegular() {
			callCtx.Errf("truncate: %s: not a regular file\n", args[0])
			return builtins.Result{Code: 1}
		}
		if size > info.Size() {
			callCtx.Errf("truncate: cannot grow file\n")
			return builtins.Result{Code: 1}
		}
		return callCtx.InvokeHostCommand(ctx, "truncate", []string{"-s", strconv.FormatInt(size, 10), args[0]})
	}
}

func isDecimalSize(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
