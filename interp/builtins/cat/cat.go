// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package cat implements the cat builtin command.
//
// cat — concatenate and print files
//
// Usage: cat [FILE]...
//
// Concatenate FILE(s) to standard output.
// With no FILE, or when FILE is -, read standard input.
//
// Exit codes:
//
//	0  All files processed successfully.
//	1  At least one error occurred (missing file, permission denied, etc.).
package cat

import (
	"context"
	"io"
	"os"

	"github.com/DataDog/rshell/interp/builtins"
)

// Cmd is the cat builtin command descriptor.
var Cmd = builtins.Command{Name: "cat", MakeFlags: builtins.NoFlags(run)}

func run(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	if len(args) == 0 {
		args = []string{"-"}
	}
	var failed bool
	for _, arg := range args {
		if err := catFile(ctx, callCtx, arg); err != nil {
			callCtx.Errf("cat: %s: %s\n", arg, callCtx.PortableErr(err))
			failed = true
		}
	}
	if failed {
		return builtins.Result{Code: 1}
	}
	return builtins.Result{}
}

func catFile(ctx context.Context, callCtx *builtins.CallContext, path string) error {
	var rc io.ReadCloser
	if path == "-" {
		if callCtx.Stdin == nil {
			return nil
		}
		rc = io.NopCloser(callCtx.Stdin)
	} else {
		f, err := callCtx.OpenFile(ctx, path, os.O_RDONLY, 0)
		if err != nil {
			return err
		}
		rc = f
	}
	defer rc.Close()
	_, err := io.Copy(callCtx.Stdout, rc)
	return err
}
