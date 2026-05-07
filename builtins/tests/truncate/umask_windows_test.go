// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package truncate_test

import "testing"

// umaskOrSkip skips the calling test on Windows: the umask concept does
// not exist there, and Windows ACLs make permission-bit assertions
// irrelevant.
func umaskOrSkip(t *testing.T, _ int) int {
	t.Skip("umask is not a Windows concept; permission bits are governed by ACLs")
	return 0
}

func restoreUmask(_ int) {}
