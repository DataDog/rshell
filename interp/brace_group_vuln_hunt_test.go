// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-19-gpt-5.5-cyber-2
// Target: brace_group (shell-feature)

package interp

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVulnHuntShellFeatureParserConfusion_MalformedBraceGroups(t *testing.T) {
	tests := []string{
		`{ }`,
		`{ echo unterminated`,
		`{ echo missing-semicolon }`,
	}
	for _, src := range tests {
		t.Run(src, func(t *testing.T) {
			assert.Error(t, parseScriptWantErr(t, src))
		})
	}
}

func TestVulnHuntShellFeatureReadonlyBypass_BraceGroupAssignmentBlocked(t *testing.T) {
	stdout, stderr := runScriptWithReadonly(t,
		"{ RO_VAR=hacked; echo inside=$RO_VAR; }\necho after=$RO_VAR\n")

	assert.Contains(t, stderr, "readonly variable",
		"brace-group assignment to readonly must produce readonly error")
	assert.NotContains(t, stdout, "hacked",
		"brace group must not observe or leak a bypassed readonly value")
	assert.Contains(t, stdout, "inside=original",
		"the failed assignment must leave the readonly value visible inside the block")
	assert.Contains(t, stdout, "after=original",
		"the readonly value must remain intact after the block")
}

func TestVulnHuntShellFeatureSignalContext_BraceGroupInfiniteLoopRespectsCancellation(t *testing.T) {
	r := newTimeoutRunner(t, MaxExecutionTime(100*time.Millisecond))

	start := time.Now()
	err := r.Run(context.Background(), parseScript(t, `{ while true; do :; done; }`))
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 2*time.Second,
		"brace-group loop did not stop promptly after timeout: %s", elapsed)
}

func TestVulnHuntShellFeatureSignalContext_BraceGroupPipelineStageClosesOnCancel(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r := newTimeoutRunner(t,
		StdIO(nil, &stdout, &stderr),
		MaxExecutionTime(500*time.Millisecond),
	)

	start := time.Now()
	err := r.Run(context.Background(), parseScript(t, `{ while true; do echo x; done; } | head -n 1`))
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, "x\n", stdout.String(),
		"head should receive the first line before the pipeline is cancelled")
	assert.Less(t, elapsed, 2*time.Second,
		"brace-group pipeline did not stop promptly after timeout: %s", elapsed)
}
