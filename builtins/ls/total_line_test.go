// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package ls_test

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

func TestLsLongTotalLineUses1KBlocks(t *testing.T) {
	dir := t.TempDir()

	// Create a file so the directory has a non-zero total.
	require.NoError(t, os.WriteFile(dir+"/a.txt", []byte("hello"), 0o644))

	// Compute expected total: sum of Stat_t.Blocks (512-byte units) / 2 = 1K blocks.
	var st syscall.Stat_t
	require.NoError(t, syscall.Stat(dir+"/a.txt", &st))
	expected1KBlocks := st.Blocks / 2

	stdout, stderr, code := testutil.RunScript(t, "ls -l", dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)

	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	require.True(t, len(lines) >= 1, "expected at least 1 line of output")
	assert.Equal(t, fmt.Sprintf("total %d", expected1KBlocks), lines[0])
}
