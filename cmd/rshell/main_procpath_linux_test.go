// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestProcPathFlagNonexistentDir ensures --proc-path with a nonexistent
// directory causes ps -e to fail with a non-zero exit code.
func TestProcPathFlagNonexistentDir(t *testing.T) {
	code, _, stderr := runCLI(t,
		"--allow-all-commands",
		"--proc-path", "/nonexistent/proc/path",
		"-c", "ps -e",
	)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "ps:")
}
