// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNowSafePanicsOnZero(t *testing.T) {
	cc := &CallContext{}
	require.Panics(t, func() { cc.NowSafe() })
}

func TestNowSafeReturnsSetValue(t *testing.T) {
	ts := time.Now()
	cc := &CallContext{Now: ts}
	assert.Equal(t, ts, cc.NowSafe())
}
