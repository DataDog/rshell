// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
)

func TestAllowedCommandsNamespaceRequired(t *testing.T) {
	_, err := interp.New(interp.AllowedCommands([]string{"echo"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing namespace prefix")
}

func TestAllowedCommandsUnknownNamespace(t *testing.T) {
	_, err := interp.New(interp.AllowedCommands([]string{"host:echo"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown namespace")
}

func TestAllowedCommandsEmptyCommandName(t *testing.T) {
	_, err := interp.New(interp.AllowedCommands([]string{"rshell:"}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty command name")
}

func TestAllowedCommandsValidPrefix(t *testing.T) {
	_, err := interp.New(interp.AllowedCommands([]string{"rshell:echo"}))
	require.NoError(t, err)
}

func TestAllowedCommandsTrimsWhitespace(t *testing.T) {
	// Verify parse succeeds with whitespace-padded entries.
	var stdout bytes.Buffer
	runner, err := interp.New(
		interp.AllowedCommands([]string{" rshell:echo ", " rshell: cat "}),
		interp.StdIO(nil, &stdout, nil),
	)
	require.NoError(t, err)
	defer runner.Close()

	// Verify the trimmed command is executable at runtime.
	prog, _ := syntax.NewParser().Parse(strings.NewReader("echo hello"), "")
	err = runner.Run(context.Background(), prog)
	require.NoError(t, err)
	assert.Equal(t, "hello\n", stdout.String())
}

func TestAllowedCommandsEmpty(t *testing.T) {
	_, err := interp.New(interp.AllowedCommands([]string{""}))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty command name")
}
