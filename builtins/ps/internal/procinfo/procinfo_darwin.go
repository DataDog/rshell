// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build darwin

package procinfo

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func listAll(ctx context.Context) ([]ProcInfo, error) {
	kprocs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("ps: SysctlKinfoProcSlice: %w", err)
	}

	procs := make([]ProcInfo, 0, min(len(kprocs), MaxProcesses))
	for i := range kprocs {
		if ctx.Err() != nil {
			break
		}
		if len(procs) >= MaxProcesses {
			break
		}
		info := kinfoToProc(&kprocs[i])
		if info.PID == 0 {
			continue
		}
		procs = append(procs, info)
	}
	return procs, nil
}

func getSession(ctx context.Context) ([]ProcInfo, error) {
	all, err := listAll(ctx)
	if err != nil {
		return nil, err
	}

	// Build ancestor chain via PPID.
	byPID := make(map[int]ProcInfo, len(all))
	for _, p := range all {
		byPID[p.PID] = p
	}

	selfPID := os.Getpid()
	ancestors := make(map[int]bool)
	cur := selfPID
	for cur > 1 {
		ancestors[cur] = true
		p, ok := byPID[cur]
		if !ok {
			break
		}
		cur = p.PPID
	}

	// Include all processes in the same session (getsid).
	selfSID, err := syscall.Getsid(0)
	if err != nil {
		selfSID = 0
	}

	var result []ProcInfo
	for _, p := range all {
		if ctx.Err() != nil {
			break
		}
		if ancestors[p.PID] {
			result = append(result, p)
			continue
		}
		if selfSID != 0 {
			// Check session via per-PID sysctl.
			kp, err := unix.SysctlKinfoProc("kern.proc.pid", p.PID)
			if err == nil {
				if int(kp.Eproc.Pgid) != 0 {
					// Approximation: same PGID group as us means same session.
					_ = kp
				}
				// Use SID from getsid per process (requires privileges for other users).
				sid, serr := syscall.Getsid(p.PID)
				if serr == nil && sid == selfSID {
					result = append(result, p)
				}
			}
		}
	}
	return result, nil
}

func getByPIDs(ctx context.Context, pids []int) ([]ProcInfo, error) {
	var result []ProcInfo
	for _, pid := range pids {
		if ctx.Err() != nil {
			break
		}
		kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil {
			continue
		}
		info := kinfoToProc(kp)
		if info.PID == 0 {
			continue
		}
		result = append(result, info)
	}
	return result, nil
}

func kinfoToProc(kp *unix.KinfoProc) ProcInfo {
	pid := int(kp.Proc.P_pid)
	ppid := int(kp.Eproc.Ppid)
	uid := fmt.Sprintf("%d", kp.Eproc.Ucred.Uid)
	state := string([]byte{statByte(kp.Proc.P_stat)})
	tty := resolveTTY(kp.Eproc.Tdev)

	// Start time.
	startSec := kp.Proc.P_starttime.Sec
	startNsec := kp.Proc.P_starttime.Usec * 1000
	startTime := time.Unix(startSec, int64(startNsec))
	var stime string
	now := time.Now()
	if startTime.Day() == now.Day() && startTime.Month() == now.Month() && startTime.Year() == now.Year() {
		stime = startTime.Format("15:04")
	} else {
		stime = startTime.Format("Jan02")
	}

	// Command: prefer full cmdline, fall back to p_comm.
	cmd := readCmdlineForPID(pid)
	if cmd == "" {
		// Trim null bytes from P_comm.
		comm := kp.Proc.P_comm
		n := 0
		for n < len(comm) && comm[n] != 0 {
			n++
		}
		commStr := string(comm[:n])
		cmd = "[" + commStr + "]"
	}

	return ProcInfo{
		PID:   pid,
		PPID:  ppid,
		UID:   uid,
		State: state,
		TTY:   tty,
		CPU:   0,
		STime: stime,
		Time:  "00:00:00",
		Cmd:   cmd,
	}
}

// statByte converts the Darwin p_stat value to a single-character state.
func statByte(stat int8) byte {
	switch stat {
	case 1: // SIDL
		return 'I'
	case 2: // SRUN
		return 'R'
	case 3: // SSLEEP
		return 'S'
	case 4: // SSTOP
		return 'T'
	case 5: // SZOMB
		return 'Z'
	default:
		return '?'
	}
}

// resolveTTY returns a human-readable TTY name from a Darwin dev_t.
func resolveTTY(tdev int32) string {
	if tdev == 0 || tdev == -1 {
		return "?"
	}
	// Major/minor encoding differs on macOS. Return numeric form.
	return fmt.Sprintf("%d", tdev)
}

// readCmdlineForPID reads the argument list for a process using kern.procargs2,
// returning only the argv entries to avoid leaking environment variable values.
//
// Buffer format: [4-byte argc (little-endian int32)][exec_path\0][padding\0...][argv[0]\0...argv[argc-1]\0][env\0...]
func readCmdlineForPID(pid int) string {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(buf) < 4 {
		return ""
	}

	// First 4 bytes: argc as little-endian int32.
	argc := int(int32(buf[0]) | int32(buf[1])<<8 | int32(buf[2])<<16 | int32(buf[3])<<24)
	if argc <= 0 {
		return ""
	}
	rest := buf[4:]

	// Skip exec_path (first null-terminated string) and any padding nulls.
	i := 0
	for i < len(rest) && rest[i] != 0 {
		i++
	}
	for i < len(rest) && rest[i] == 0 {
		i++
	}

	// Collect exactly argc null-separated argv entries; stop before env vars.
	argStart := i
	argsConsumed := 0
	argEnd := i
	for i < len(rest) && argsConsumed < argc {
		if rest[i] == 0 {
			argsConsumed++
			argEnd = i
		}
		i++
	}

	if argEnd <= argStart {
		return ""
	}

	// Copy argv bytes and replace null separators with spaces.
	cmdBytes := make([]byte, argEnd-argStart)
	copy(cmdBytes, rest[argStart:argEnd])
	for j, b := range cmdBytes {
		if b == 0 {
			cmdBytes[j] = ' '
		}
	}
	cmd := strings.TrimSpace(string(cmdBytes))
	if len(cmd) > MaxCmdLen {
		cmd = cmd[:MaxCmdLen]
	}
	return cmd
}
