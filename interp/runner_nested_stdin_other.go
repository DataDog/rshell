// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package interp

import (
	"context"
	"io"
	"os"
)

func nestedStdinFile(ctx context.Context, stdin io.Reader) (*os.File, bool, bool, error) {
	f, err := stdinFile(ctx, stdin)
	if err != nil {
		return nil, false, false, err
	}
	original, callerOwned := stdin.(*os.File)
	return f, f != nil && (!callerOwned || original != f), false, nil
}
