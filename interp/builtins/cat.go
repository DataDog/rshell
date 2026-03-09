// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import (
	"context"
	"io"
	"os"
)

func init() {
	register("cat", builtinCat)
}

func builtinCat(ctx context.Context, callCtx *CallContext, args []string) Result {
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
		return Result{Code: 1}
	}
	return Result{}
}

func catFile(ctx context.Context, callCtx *CallContext, path string) error {
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
