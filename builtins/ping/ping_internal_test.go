// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// White-box tests for unexported helpers in the ping package.
package ping

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ============================================================================
// isPermissionErr
// ============================================================================

func TestIsPermissionErrNil(t *testing.T) {
	assert.False(t, isPermissionErr(nil))
}

func TestIsPermissionErrEPERM(t *testing.T) {
	assert.True(t, isPermissionErr(syscall.EPERM))
}

func TestIsPermissionErrEACCES(t *testing.T) {
	assert.True(t, isPermissionErr(syscall.EACCES))
}

func TestIsPermissionErrWrappedEPERM(t *testing.T) {
	// net.OpError wrapping an os.SyscallError wrapping EPERM.
	inner := &net.OpError{
		Op:  "socket",
		Err: fmt.Errorf("wrapped: %w", syscall.EPERM),
	}
	assert.True(t, isPermissionErr(inner))
}

func TestIsPermissionErrWrappedEACCES(t *testing.T) {
	inner := &net.OpError{
		Op:  "socket",
		Err: fmt.Errorf("wrapped: %w", syscall.EACCES),
	}
	assert.True(t, isPermissionErr(inner))
}

func TestIsPermissionErrStringFallback(t *testing.T) {
	assert.True(t, isPermissionErr(errors.New("operation not permitted")))
	assert.True(t, isPermissionErr(errors.New("OPERATION NOT PERMITTED"))) // case-insensitive
	assert.True(t, isPermissionErr(errors.New("access is denied")))
	assert.True(t, isPermissionErr(errors.New("permission denied")))
}

func TestIsPermissionErrUnrelated(t *testing.T) {
	assert.False(t, isPermissionErr(errors.New("connection refused")))
	assert.False(t, isPermissionErr(errors.New("no such host")))
	assert.False(t, isPermissionErr(errors.New("i/o timeout")))
}

// ============================================================================
// clampInt / clampDuration
// ============================================================================

func TestClampIntAtMin(t *testing.T) {
	assert.Equal(t, 1, clampInt(-5, 1, 20))
	assert.Equal(t, 1, clampInt(0, 1, 20))
	assert.Equal(t, 1, clampInt(1, 1, 20))
}

func TestClampIntAtMax(t *testing.T) {
	assert.Equal(t, 20, clampInt(21, 1, 20))
	assert.Equal(t, 20, clampInt(1<<30, 1, 20))
}

func TestClampIntMiddle(t *testing.T) {
	assert.Equal(t, 10, clampInt(10, 1, 20))
}

func TestClampDurationAtMin(t *testing.T) {
	assert.Equal(t, minInterval, clampDuration(0, minInterval, 60e9))
	assert.Equal(t, minInterval, clampDuration(1, minInterval, 60e9))
	assert.Equal(t, minInterval, clampDuration(minInterval, minInterval, 60e9))
}

func TestClampDurationAtMax(t *testing.T) {
	assert.Equal(t, maxWait, clampDuration(maxWait+1, 0, maxWait))
}

// ============================================================================
// durToMS
// ============================================================================

func TestDurToMS(t *testing.T) {
	assert.InDelta(t, 1.0, durToMS(1e6), 1e-9)        // 1ms
	assert.InDelta(t, 17.045, durToMS(17045e3), 1e-6) // 17.045ms
	assert.Equal(t, 0.0, durToMS(0))
}
