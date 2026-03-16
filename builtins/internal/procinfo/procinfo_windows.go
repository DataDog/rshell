// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package procinfo

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func listAll(ctx context.Context) ([]ProcInfo, error) {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("ps: CreateToolhelp32Snapshot: %w", err)
	}
	defer syscall.CloseHandle(snapshot)

	var procs []ProcInfo
	var entry syscall.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))

	if err := syscall.Process32First(snapshot, &entry); err != nil {
		return nil, fmt.Errorf("ps: Process32First: %w", err)
	}

	for {
		if ctx.Err() != nil {
			break
		}
		if len(procs) >= MaxProcesses {
			break
		}
		info := processEntryToProc(&entry)
		procs = append(procs, info)

		if err := syscall.Process32Next(snapshot, &entry); err != nil {
			break // ERROR_NO_MORE_FILES
		}
	}
	return procs, nil
}

func getSession(ctx context.Context) ([]ProcInfo, error) {
	all, err := listAll(ctx)
	if err != nil {
		return nil, err
	}
	// Walk PPID chain upward from current process.
	byPID := make(map[int]ProcInfo, len(all))
	for _, p := range all {
		byPID[p.PID] = p
	}

	selfPID := os.Getpid()
	ancestors := make(map[int]bool)
	cur := selfPID
	for cur > 0 {
		ancestors[cur] = true
		p, ok := byPID[cur]
		if !ok {
			break
		}
		if p.PPID == cur {
			break // avoid infinite loop for PID 0
		}
		cur = p.PPID
	}

	var result []ProcInfo
	for _, p := range all {
		if ctx.Err() != nil {
			break
		}
		if ancestors[p.PID] {
			result = append(result, p)
		}
	}
	return result, nil
}

func getByPIDs(ctx context.Context, pids []int) ([]ProcInfo, error) {
	all, err := listAll(ctx)
	if err != nil {
		return nil, err
	}
	wanted := make(map[int]bool, len(pids))
	for _, pid := range pids {
		wanted[pid] = true
	}
	var result []ProcInfo
	for _, p := range all {
		if ctx.Err() != nil {
			break
		}
		if wanted[p.PID] {
			result = append(result, p)
		}
	}
	return result, nil
}

func processEntryToProc(e *syscall.ProcessEntry32) ProcInfo {
	pid := int(e.ProcessID)
	ppid := int(e.ParentProcessID)

	// Extract executable name from ExeFile ([260]uint16, null-terminated).
	n := 0
	for n < len(e.ExeFile) && e.ExeFile[n] != 0 {
		n++
	}
	cmd := syscall.UTF16ToString(e.ExeFile[:n])
	if len(cmd) > MaxCmdLen {
		cmd = cmd[:MaxCmdLen]
	}

	return ProcInfo{
		PID:   pid,
		PPID:  ppid,
		UID:   "?",
		State: "?",
		TTY:   "?",
		CPU:   0,
		STime: "?",
		Time:  "00:00:00",
		Cmd:   cmd,
	}
}
