// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build unix

package truncate_test

import (
	"syscall"
	"testing"
)

// umaskOrSkip sets the process umask to the requested value and returns
// the previous value. Callers MUST defer restoreUmask(old) to put it back.
// syscall.Umask is process-global and not safe to run concurrently with
// other umask-sensitive tests.
func umaskOrSkip(_ *testing.T, mask int) int {
	return syscall.Umask(mask)
}

// restoreUmask puts the umask back to the value returned by umaskOrSkip.
func restoreUmask(prev int) {
	syscall.Umask(prev)
}
