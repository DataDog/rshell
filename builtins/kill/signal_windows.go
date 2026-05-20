// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package kill

import "os"

func signalPID(pid int64, _ bool) error {
	proc, err := os.FindProcess(int(pid))
	if err != nil {
		return err
	}
	defer proc.Release()
	return proc.Kill()
}

func pidAlive(pid int64) (bool, error) {
	proc, err := os.FindProcess(int(pid))
	if err != nil {
		return false, nil
	}
	defer proc.Release()
	return true, nil
}

func signalName(force bool) string {
	if force {
		return "SIGKILL"
	}
	return "SIGTERM"
}
