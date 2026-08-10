// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package allowedpaths

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenRegularRejectsProcDescriptorPortalOutsideAllowedPaths(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "descriptor-target")
	require.NoError(t, err)
	defer file.Close()

	sb, _, err := New([]string{"/proc"})
	require.NoError(t, err)
	defer sb.Close()

	handle, err := sb.OpenRegular(fmt.Sprintf("/proc/self/fd/%d", file.Fd()), "/")
	assert.Nil(t, handle)
	assert.Error(t, err)
}

func TestOpenRegularRejectsProcRootPortalOutsideAllowedPaths(t *testing.T) {
	sb, _, err := New([]string{"/proc"})
	require.NoError(t, err)
	defer sb.Close()

	handle, err := sb.OpenRegular("/proc/self/root/etc/passwd", "/")
	assert.Nil(t, handle)
	assert.Error(t, err)
}
