// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package procnetroute

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVulnHuntSubsystemSSProcnetReaders_RejectsTraversalProcPath(t *testing.T) {
	_, err := ReadRoutes(context.Background(), "/proc/../tmp")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsafe procPath")
}
