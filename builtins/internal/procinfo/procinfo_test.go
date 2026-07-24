// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package procinfo

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestTruncateCmdNameSanitizesUnsafeCharacters(t *testing.T) {
	name := "safe\x00\nline\tindent\x1b[31m\x7f" +
		string([]byte{0xff}) +
		"\u2028worker"

	got := truncateCmdName(name)

	require.Equal(t, "safe??line?indent?[31m???worker", got)
	require.NotContains(t, got, "\n")
	require.NotContains(t, got, "\t")
	require.True(t, utf8.ValidString(got))
}

func TestTruncateCmdNameSanitizesEveryASCIIControl(t *testing.T) {
	controls := make([]byte, 0, 33)
	for control := byte(0); control < ' '; control++ {
		controls = append(controls, control)
	}
	controls = append(controls, 0x7f)

	require.Equal(t, strings.Repeat("?", len(controls)), truncateCmdName(string(controls)))
}

func TestTruncateCmdNamePreservesPrintableUnicode(t *testing.T) {
	name := "safe worker 日本語 café"

	require.Equal(t, name, truncateCmdName(name))
}

func TestTruncateCmdNameEnforcesByteLimitAtRuneBoundary(t *testing.T) {
	t.Run("exact limit", func(t *testing.T) {
		name := strings.Repeat("a", MaxCmdLen)

		require.Equal(t, name, truncateCmdName(name))
	})

	t.Run("complete multibyte rune fits", func(t *testing.T) {
		name := strings.Repeat("a", MaxCmdLen-2) + "é" + "z"
		got := truncateCmdName(name)

		require.Equal(t, strings.Repeat("a", MaxCmdLen-2)+"é", got)
		require.Len(t, got, MaxCmdLen)
		require.True(t, utf8.ValidString(got))
	})

	t.Run("multibyte rune would be split", func(t *testing.T) {
		name := strings.Repeat("a", MaxCmdLen-1) + "é"
		got := truncateCmdName(name)

		require.Equal(t, strings.Repeat("a", MaxCmdLen-1), got)
		require.LessOrEqual(t, len(got), MaxCmdLen)
		require.True(t, utf8.ValidString(got))
	})
}
