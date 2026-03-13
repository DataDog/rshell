// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedpaths

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSandboxOpenRejectsWriteFlags(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test.txt"), []byte("data"), 0644))

	sb, err := New([]string{dir})
	require.NoError(t, err)
	defer sb.Close()

	writeFlags := []int{
		os.O_WRONLY,
		os.O_RDWR,
		os.O_APPEND,
		os.O_CREATE,
		os.O_TRUNC,
		os.O_WRONLY | os.O_CREATE | os.O_TRUNC,
	}
	for _, flag := range writeFlags {
		f, err := sb.Open("test.txt", dir, flag, 0644)
		assert.Nil(t, f, "open with flag %d should return nil", flag)
		assert.ErrorIs(t, err, os.ErrPermission, "open with flag %d should be denied", flag)
	}

	// Read-only should still work.
	f, err := sb.Open("test.txt", dir, os.O_RDONLY, 0)
	require.NoError(t, err)
	f.Close()
}
