// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"
)

// Campaign: 2026-05-20-gpt-5.5-cyber-3

func TestVulnHuntShellFeatureDeclaredVsImplemented_EmptyFileClearsStaleExitOnReusedRunner(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })

	parser := syntax.NewParser()
	falseProg, err := parser.Parse(strings.NewReader("false\n"), "")
	require.NoError(t, err)
	err = r.Run(context.Background(), falseProg)
	require.Error(t, err)
	assert.Equal(t, uint8(1), r.exit.code)

	emptyProg, err := parser.Parse(strings.NewReader(""), "")
	require.NoError(t, err)
	err = r.Run(context.Background(), emptyProg)
	require.NoError(t, err)
	assert.Equal(t, uint8(0), r.exit.code)
	assert.Empty(t, stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_ParseScriptRejectsOversizedWhitespaceOnly(t *testing.T) {
	_, err := ParseScript(strings.Repeat(" ", MaxScriptBytes+1), "empty.sh")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestVulnHuntShellFeatureSignalContext_PreCancelledEmptyScriptReturns(t *testing.T) {
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(""), "")
	require.NoError(t, err)

	r, err := New(allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	err = r.Run(ctx, prog)
	require.NoError(t, err)
	assert.Less(t, time.Since(start), time.Second)
}
