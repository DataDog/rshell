// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

// Vulnerability-hunt regression tests for campaign 2026-05-20-gpt-5.5-cyber-3.

package ss_test

import (
	"context"
	"fmt"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins/testutil"
)

func TestVulnHuntBuiltinSpecialFiles_PositionalsDoNotBlockOnFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stdout, stderr, code := testutil.RunScriptCtx(ctx, t, fmt.Sprintf(`ss -- %q; echo status:$?`, fifo), "")
	require.Equal(t, 0, code)
	assert.Contains(t, stdout, "status:")
	assert.NotContains(t, stderr, "context deadline exceeded")
	assert.NoError(t, ctx.Err())
}
