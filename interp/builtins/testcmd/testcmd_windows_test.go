//go:build windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package testcmd_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/rshell/interp"
)

func TestTestWindowsReservedNames(t *testing.T) {
	dir := t.TempDir()
	reserved := []string{"CON", "PRN", "AUX", "NUL", "COM1", "LPT1"}
	for _, name := range reserved {
		t.Run(name, func(t *testing.T) {
			_, _, code := runScript(t, `test -e `+name, dir, interp.AllowedPaths([]string{dir}))
			assert.Equal(t, 1, code)
		})
	}
}
