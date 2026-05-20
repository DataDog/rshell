// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package sort_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVulnHuntBuiltinSort_DevZeroHitsLineCap(t *testing.T) {
	dir := t.TempDir()

	mustNotHang(t, func() {
		stdout, stderr, code := sortRun(t, "sort /dev/zero", dir, "/dev")
		assert.Equal(t, 1, code)
		assert.Empty(t, stdout)
		assert.Contains(t, stderr, "sort:")
		assert.Contains(t, stderr, "token too long")
	})
}
