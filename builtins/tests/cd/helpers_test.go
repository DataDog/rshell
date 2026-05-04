// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package cd_test

import (
	"context"
	"testing"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

// cmdRunCtxFuzz runs a script with a context and AllowedPaths anchored at
// dir. Distinct from cmdRunCtx in the unit-test file (which lives in the
// builtins/cd package) to avoid name collisions when the unit and fuzz
// tests share a directory; this helper is owned by the fuzz suite.
func cmdRunCtxFuzz(ctx context.Context, t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	allOpts := append([]interp.RunnerOption{interp.AllowedPaths([]string{dir})}, opts...)
	return testutil.RunScriptCtx(ctx, t, script, dir, allOpts...)
}
