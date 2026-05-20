// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package kill

import (
	"errors"
	"os"
	"syscall"
)

func signalPID(pid int64, force bool) error {
	proc, err := os.FindProcess(int(pid))
	if err != nil {
		return err
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	return proc.Signal(sig)
}

func pidAlive(pid int64) (bool, error) {
	proc, err := os.FindProcess(int(pid))
	if err != nil {
		return false, err
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	return false, err
}

func signalName(force bool) string {
	if force {
		return "SIGKILL"
	}
	return "SIGTERM"
}
