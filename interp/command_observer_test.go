// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newObserverRunner builds a Runner with stdout/stderr discarded so that
// tests can focus on CommandObserver events without asserting on shell
// output.
func newObserverRunner(t *testing.T, opts ...RunnerOption) (*Runner, func()) {
	t.Helper()
	allOpts := append([]RunnerOption{StdIO(nil, io.Discard, io.Discard)}, opts...)
	r, err := New(allOpts...)
	require.NoError(t, err)
	return r, func() { _ = r.Close() }
}

func TestCommandObserverOkStatusForBuiltin(t *testing.T) {
	var events []CommandEvent
	r, cleanup := newObserverRunner(t,
		allowAllCommandsOpt(),
		CommandObserver(func(_ context.Context, e CommandEvent) {
			events = append(events, e)
		}),
	)
	defer cleanup()

	require.NoError(t, r.Run(context.Background(), parseScript(t, "true")))

	require.Len(t, events, 1)
	assert.Equal(t, "true", events[0].Name)
	assert.Equal(t, CommandStatusOk, events[0].Status)
	assert.Equal(t, 0, events[0].ExitCode)
}

func TestCommandObserverOkCarriesNonZeroExitCode(t *testing.T) {
	var events []CommandEvent
	r, cleanup := newObserverRunner(t,
		allowAllCommandsOpt(),
		CommandObserver(func(_ context.Context, e CommandEvent) {
			events = append(events, e)
		}),
	)
	defer cleanup()

	// `false` exits non-zero; the || keeps Run from returning an error
	// so we only observe the single dispatch.
	require.NoError(t, r.Run(context.Background(), parseScript(t, "false || true")))

	require.GreaterOrEqual(t, len(events), 1)
	var seenFalse bool
	for _, e := range events {
		if e.Name == "false" {
			seenFalse = true
			assert.Equal(t, CommandStatusOk, e.Status)
			assert.Equal(t, 1, e.ExitCode)
		}
	}
	assert.True(t, seenFalse, "expected a CommandEvent for 'false'")
}

func TestCommandObserverNotAllowedStatus(t *testing.T) {
	var events []CommandEvent
	// AllowedCommands restricts dispatch to just `true`; invoking anything else
	// should fire an observation with status=not_allowed and ExitCode=-1.
	r, cleanup := newObserverRunner(t,
		AllowedCommands([]string{"rshell:true"}),
		CommandObserver(func(_ context.Context, e CommandEvent) {
			events = append(events, e)
		}),
	)
	defer cleanup()

	// `false` is a real rshell builtin but is not in the allowlist; the
	// Runner rejects it before dispatch.
	_ = r.Run(context.Background(), parseScript(t, "false"))

	require.Len(t, events, 1)
	assert.Equal(t, "false", events[0].Name)
	assert.Equal(t, CommandStatusNotAllowed, events[0].Status)
	assert.Equal(t, -1, events[0].ExitCode)
}

func TestCommandObserverUnknownStatusForNonBuiltin(t *testing.T) {
	var events []CommandEvent
	// Allowlisted but no rshell builtin exists for "definitely-not-a-builtin",
	// so the default noExecHandler refuses it and we observe status=unknown.
	r, cleanup := newObserverRunner(t,
		AllowedCommands([]string{"rshell:definitely-not-a-builtin"}),
		CommandObserver(func(_ context.Context, e CommandEvent) {
			events = append(events, e)
		}),
	)
	defer cleanup()

	_ = r.Run(context.Background(), parseScript(t, "definitely-not-a-builtin"))

	require.Len(t, events, 1)
	assert.Equal(t, "definitely-not-a-builtin", events[0].Name)
	assert.Equal(t, CommandStatusUnknown, events[0].Status)
	assert.Equal(t, -1, events[0].ExitCode)
}

func TestCommandObserverCapturesArgs(t *testing.T) {
	var events []CommandEvent
	r, cleanup := newObserverRunner(t,
		allowAllCommandsOpt(),
		CommandObserver(func(_ context.Context, e CommandEvent) {
			events = append(events, e)
		}),
	)
	defer cleanup()

	require.NoError(t, r.Run(context.Background(), parseScript(t, "echo hello world")))

	require.Len(t, events, 1)
	assert.Equal(t, []string{"echo", "hello", "world"}, events[0].Args)
}

func TestCommandObserverNilIsNoOp(t *testing.T) {
	// Passing nil clears any previously set observer; Run must still succeed
	// and not dereference a nil callback.
	r, cleanup := newObserverRunner(t,
		allowAllCommandsOpt(),
		CommandObserver(nil),
	)
	defer cleanup()

	require.NoError(t, r.Run(context.Background(), parseScript(t, "true")))
}

func TestCommandObserverMultipleCommandsInScript(t *testing.T) {
	var names []string
	r, cleanup := newObserverRunner(t,
		allowAllCommandsOpt(),
		CommandObserver(func(_ context.Context, e CommandEvent) {
			names = append(names, e.Name)
		}),
	)
	defer cleanup()

	require.NoError(t, r.Run(context.Background(), parseScript(t, "true; true; false || true")))

	// One event per dispatched command; order follows execution order.
	assert.Equal(t, []string{"true", "true", "false", "true"}, names)
}
