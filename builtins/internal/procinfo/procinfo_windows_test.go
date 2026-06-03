// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package procinfo

import (
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func TestProcessEntryToProcUsesExeNameOnly(t *testing.T) {
	var entry windows.ProcessEntry32
	entry.ProcessID = 123
	entry.ParentProcessID = 1
	for i, ch := range utf16.Encode([]rune("safeproc.exe")) {
		entry.ExeFile[i] = ch
	}

	info := processEntryToProc(&entry)

	require.Equal(t, 123, info.PID)
	require.Equal(t, "safeproc.exe", info.Cmd)
	require.NotContains(t, info.Cmd, "--token")
}
