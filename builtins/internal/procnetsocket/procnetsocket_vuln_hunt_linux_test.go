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
	"strings"
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

func TestVulnHuntSubsystemSSProcnetReaders_MaxSocketEntriesCap(t *testing.T) {
	dir := t.TempDir()
	netDir := filepath.Join(dir, "net")
	require.NoError(t, os.MkdirAll(netDir, 0o755))

	const header = "  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"
	const row = "   0: 0100007F:0016 00000000:0000 01 00000000:00000000 00:00000000 00000000 1000 0 12345 1 0000000000000000 100 0 0 10 0\n"
	var b strings.Builder
	b.Grow(len(header) + (MaxEntries+1)*len(row))
	b.WriteString(header)
	for range MaxEntries + 1 {
		b.WriteString(row)
	}
	require.NoError(t, os.WriteFile(filepath.Join(netDir, "tcp"), []byte(b.String()), 0o644))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := ReadTCP4(ctx, dir)
	require.ErrorIs(t, err, ErrMaxEntries)
	assert.NotEqual(t, context.DeadlineExceeded, ctx.Err(), "ReadTCP4 hung before enforcing MaxEntries")
}
