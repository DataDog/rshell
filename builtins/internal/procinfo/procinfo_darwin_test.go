// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build darwin

package procinfo

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestKinfoToProcUsesCommNameOnly(t *testing.T) {
	var kp unix.KinfoProc
	kp.Proc.P_pid = 123
	kp.Eproc.Ppid = 1
	kp.Eproc.Ucred.Uid = 501
	kp.Proc.P_stat = 3
	copy(kp.Proc.P_comm[:], "safeproc")

	info := kinfoToProc(&kp)

	require.Equal(t, 123, info.PID)
	require.Equal(t, "safeproc", info.Cmd)
	require.NotContains(t, info.Cmd, "[")
	require.NotContains(t, info.Cmd, "]")
}
