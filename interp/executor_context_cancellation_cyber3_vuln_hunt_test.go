// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt 2026-05-20-gpt-5.5-cyber-3 (target: executor-context-cancellation)

package interp

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVulnHuntSubsystemExecutorContextCancellation_RunAfterTimeoutGetsFreshState(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(
		StdIO(nil, &stdout, &stderr),
		allowAllCommandsOpt(),
		MaxExecutionTime(25*time.Millisecond),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Reset()

	r.execHandler = func(ctx context.Context, _ []string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	err = r.Run(context.Background(), parseScript(t, "slowcmd"))
	require.ErrorIs(t, err, context.DeadlineExceeded)

	r.execHandler = func(context.Context, []string) error { return nil }
	err = r.Run(context.Background(), parseScript(t, "echo after"))
	require.NoError(t, err)

	assert.Equal(t, "after\n", stdout.String())
	assert.Empty(t, stderr.String())
}
