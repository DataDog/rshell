// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Tripwire tests added by vuln-hunt campaign 2026-05-20-gpt-5.5-cyber-3 /
// signal-handling.

package interp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVulnHuntSubsystemSignalHandling_TrapRedirectionCannotBypassSandbox(t *testing.T) {
	allowed := t.TempDir()
	blocked := t.TempDir()
	blockedFile := filepath.Join(blocked, "secret.txt")
	require.NoError(t, os.WriteFile(blockedFile, []byte("secret"), 0o600))

	var stdout, stderr bytes.Buffer
	r, err := New(
		StdIO(nil, &stdout, &stderr),
		AllowedPaths([]string{allowed}),
		AllowedCommands([]string{"rshell:echo"}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	script := fmt.Sprintf("trap 'echo pwned' INT < %q\necho after\n", blockedFile)
	err = r.Run(context.Background(), parseScript(t, script))
	require.NoError(t, err)

	assert.Equal(t, "after\n", stdout.String())
	assert.Contains(t, stderr.String(), "permission denied")
	assert.NotContains(t, stderr.String(), "pwned")
}

func TestVulnHuntSubsystemSignalHandling_CommandSubstitutionCancelIsFatal(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(
		StdIO(nil, &stdout, &stderr),
		allowAllCommandsOpt(),
		MaxExecutionTime(50*time.Millisecond),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Reset()
	r.execHandler = func(ctx context.Context, _ []string) error {
		<-ctx.Done()
		return ctx.Err()
	}

	err = r.Run(context.Background(), parseScript(t, "for x in $(slowcmd); do echo BAD; done"))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.NotContains(t, stdout.String(), "BAD")
}

func TestVulnHuntSubsystemSignalHandling_BuiltinCtxCancelIsFatal(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() {
		_ = pw.Close()
		_ = pr.Close()
	})

	var stderr bytes.Buffer
	r, err := New(
		StdIO(pr, io.Discard, &stderr),
		allowAllCommandsOpt(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	err = r.Run(ctx, parseScript(t, "read x"))
	require.ErrorIs(t, err, context.Canceled)
	assert.Less(t, time.Since(start), time.Second)
}

func TestVulnHuntSubsystemSignalHandling_ConcurrentPipelineStderrSerializesRows(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(
		StdIO(nil, &stdout, &stderr),
		allowAllCommandsOpt(),
		MaxExecutionTime(time.Second),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Reset()
	r.execHandler = func(ctx context.Context, args []string) error {
		hc := HandlerCtx(ctx)
		for i := range 50 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			_, _ = fmt.Fprintf(hc.Stderr, "%s:%02d\n", args[0], i)
		}
		return nil
	}

	err = r.Run(context.Background(), parseScript(t, "leftcmd | rightcmd"))
	require.NoError(t, err)
	assert.Empty(t, stdout.String())

	for _, line := range strings.Split(strings.TrimRight(stderr.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		assert.Regexp(t, `^(leftcmd|rightcmd):[0-9]{2}$`, line)
	}
}

func TestVulnHuntSubsystemSignalHandling_SimpleCommandPanicReturnsInternalError(t *testing.T) {
	var stderr bytes.Buffer
	r, err := New(
		StdIO(nil, io.Discard, &stderr),
		allowAllCommandsOpt(),
		MaxExecutionTime(time.Second),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Reset()
	r.execHandler = func(context.Context, []string) error {
		panic("controlled panic")
	}

	err = r.Run(context.Background(), parseScript(t, "paniccmd"))
	require.EqualError(t, err, "internal error")
	assert.Contains(t, stderr.String(), "rshell: internal panic: controlled panic")
}
