// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Tripwire tests added by vuln-hunt campaign 2026-05-20-gpt-5.5-cyber-3 /
// signal-handling.

package main

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestVulnHuntSubsystemSignalHandling_CLITimeoutUsesFixedDiagnostic(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{"--allow-all-commands", "--timeout=25ms", "-c", "while true; do echo x >/dev/null; done"},
		nil,
		&stdout,
		&stderr,
	)

	assert.Equal(t, exitCodeTimeout, code)
	assert.Empty(t, stdout.String())
	assert.Equal(t, "error: execution timed out after 25ms\n", stderr.String())
}

func TestVulnHuntSubsystemSignalHandling_CLIStdinReadTimeoutReturnsPromptly(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() {
		_ = pw.Close()
		_ = pr.Close()
	})

	var stdout, stderr bytes.Buffer
	start := time.Now()
	code := run(
		context.Background(),
		[]string{"--timeout=25ms"},
		pr,
		&stdout,
		&stderr,
	)

	assert.Equal(t, exitCodeTimeout, code)
	assert.Less(t, time.Since(start), time.Second)
	assert.Empty(t, stdout.String())
	assert.Equal(t, "error: execution timed out after 25ms\n", stderr.String())
}
