// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package kill

import (
	"context"
	"errors"
	"os"
	"syscall"

	"github.com/DataDog/rshell/builtins/internal/procinfo"
)

func signalPID(pid int, force bool) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	return proc.Signal(sig)
}

func pidAlive(ctx context.Context, pid int) (bool, error) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, err
	}
	err = proc.Signal(syscall.Signal(0))
	if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	infos, infoErr := procinfo.GetByPIDs(ctx, "", []int{pid})
	if infoErr == nil {
		if len(infos) == 0 || infos[0].State == "Z" {
			return false, nil
		}
	}
	return true, nil
}

func signalName(force bool) string {
	if force {
		return "SIGKILL"
	}
	return "SIGTERM"
}
