// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package procnetsocket

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVulnHuntSubsystemSSProcnetReaders_DevZeroSocketTableIsBounded(t *testing.T) {
	dir := t.TempDir()
	netDir := filepath.Join(dir, "net")
	require.NoError(t, os.MkdirAll(netDir, 0o755))
	require.NoError(t, os.Symlink("/dev/zero", filepath.Join(netDir, "tcp")))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := ReadTCP4(ctx, dir)
	require.Error(t, err)
	assert.NotEqual(t, context.DeadlineExceeded, ctx.Err(), "ReadTCP4 hung on /dev/zero instead of hitting the scanner cap")
}
