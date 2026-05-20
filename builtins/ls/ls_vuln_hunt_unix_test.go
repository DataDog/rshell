// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package ls_test

import (
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVulnHuntBuiltinSpecialFiles_FifoLongFormatMetadataOnly(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o644))

	mustNotHang(t, func() {
		stdout, stderr, code := lsRun(t, "ls -lF fifo", dir)
		assert.Equal(t, 0, code)
		assert.Empty(t, stderr)
		assert.Contains(t, stdout, "fifo|")
	})
}
