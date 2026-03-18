// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package procinfo

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// clkTck is the number of clock ticks per second. On modern Linux this is
// almost always 100, but we default to 100 and let procBootTime handle errors.
const clkTck = 100

func listAll(ctx context.Context) ([]ProcInfo, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("ps: cannot read /proc: %w", err)
	}

	btime, _ := procBootTime()
	var procs []ProcInfo
	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		if len(procs) >= MaxProcesses {
			break
		}
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		info, err := readProc(pid, btime)
		if err != nil {
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
	// Build a map for quick lookup.
	byPID := make(map[int]ProcInfo, len(all))
	for _, p := range all {
		byPID[p.PID] = p
	}

	// Walk PPID chain from current process upward; collect session ancestors.
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

	// Also include all processes that share our SID (best-effort; fall back to
	// ancestor chain only).
	var selfSID int
	if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", selfPID)); err == nil {
		selfSID = parseSID(data)
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
			if data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", p.PID)); err == nil {
				if parseSID(data) == selfSID {
					result = append(result, p)
				}
			}
		}
	}
	return result, nil
}

func getByPIDs(ctx context.Context, pids []int) ([]ProcInfo, error) {
	btime, _ := procBootTime()
	var result []ProcInfo
	for _, pid := range pids {
		if ctx.Err() != nil {
			break
		}
		info, err := readProc(pid, btime)
		if err != nil {
			continue
		}
		result = append(result, info)
	}
	return result, nil
}

// readProc reads process info for a single PID from /proc.
func readProc(pid int, btime int64) (ProcInfo, error) {
	statData, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return ProcInfo{}, err
	}

	var info ProcInfo
	info.PID = pid

	// Parse /proc/stat. The format is:
	//   pid (comm) state ppid pgroup session tty_nr ...
	// The comm field may contain spaces and is delimited by parentheses.
	statStr := strings.TrimSpace(string(statData))
	openParen := strings.Index(statStr, "(")
	closeParen := strings.LastIndex(statStr, ")")
	if openParen < 0 || closeParen < 0 || closeParen <= openParen {
		return ProcInfo{}, fmt.Errorf("ps: malformed stat for pid %d", pid)
	}
	comm := statStr[openParen+1 : closeParen]
	rest := strings.Fields(statStr[closeParen+1:])
	// rest[0]=state, rest[1]=ppid, rest[2]=pgroup, rest[3]=session, rest[4]=tty_nr
	// rest[11]=utime, rest[12]=stime (1-indexed from after closeParen+1, so offset by 1)
	// Indices: state=0 ppid=1 pgroup=2 session=3 tty_nr=4 ... utime=11 stime=12
	//          cutime=13 cstime=14 ... starttime=19
	if len(rest) < 20 {
		return ProcInfo{}, fmt.Errorf("ps: short stat for pid %d", pid)
	}

	info.State = string(rest[0])
	info.PPID, _ = strconv.Atoi(rest[1])
	ttyNr, _ := strconv.ParseInt(rest[4], 10, 64)
	utime, _ := strconv.ParseInt(rest[11], 10, 64)
	stime, _ := strconv.ParseInt(rest[12], 10, 64)
	starttime, _ := strconv.ParseInt(rest[19], 10, 64)

	// TTY: try to resolve from /proc/pid/fd/0, fall back to device number.
	info.TTY = resolveTTY(pid, ttyNr)

	// CPU time: (utime + stime) in clock ticks → HH:MM:SS.
	totalSecs := (utime + stime) / clkTck
	info.Time = fmt.Sprintf("%02d:%02d:%02d", totalSecs/3600, (totalSecs%3600)/60, totalSecs%60)

	// Start time.
	if btime > 0 {
		startUnix := btime + starttime/clkTck
		t := time.Unix(startUnix, 0)
		now := time.Now()
		if t.Day() == now.Day() && t.Month() == now.Month() && t.Year() == now.Year() {
			info.STime = t.Format("15:04")
		} else {
			info.STime = t.Format("Jan02")
		}
	} else {
		info.STime = "?"
	}

	// UID from /proc/pid/status.
	info.UID = readUID(pid)

	// Full cmdline from /proc/pid/cmdline (null-separated).
	cmdline := readCmdline(pid)
	if cmdline == "" {
		// Kernel thread: show [comm].
		info.Cmd = "[" + comm + "]"
	} else {
		info.Cmd = cmdline
	}

	return info, nil
}

// resolveTTY maps tty_nr (from /proc/pid/stat) to a human-readable name.
// tty_nr encodes the controlling terminal's device number:
//
//	major = bits [15:8]
//	minor = bits [7:0] | (bits [31:20] << 8)
//
// We decode this directly instead of reading /proc/pid/fd/0 (which is stdin
// and may point to a redirected file rather than the controlling terminal).
func resolveTTY(_ int, ttyNr int64) string {
	if ttyNr == 0 {
		return "?"
	}
	major := (ttyNr >> 8) & 0xff
	minor := (ttyNr & 0xff) | ((ttyNr >> 20) << 8)
	switch {
	case major == 4 && minor < 64:
		// Virtual consoles: /dev/ttyN
		return fmt.Sprintf("tty%d", minor)
	case major == 4:
		// Serial terminals: /dev/ttySN
		return fmt.Sprintf("ttyS%d", minor-64)
	case major >= 136 && major <= 143:
		// Pseudo-terminal slaves: /dev/pts/N
		pts := (major-136)*256 + minor
		return fmt.Sprintf("pts/%d", pts)
	default:
		return "?"
	}
}

// readUID reads the real UID from /proc/pid/status.
func readUID(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return "?"
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "Uid:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return fields[1] // real UID
			}
		}
	}
	return "?"
}

// readCmdline reads /proc/pid/cmdline and returns the command line string.
func readCmdline(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil || len(data) == 0 {
		return ""
	}
	// Replace null bytes with spaces.
	for i, b := range data {
		if b == 0 {
			data[i] = ' '
		}
	}
	cmd := strings.TrimRight(string(data), " ")
	if len(cmd) > MaxCmdLen {
		cmd = cmd[:MaxCmdLen]
	}
	return cmd
}

// procBootTime reads the boot time (seconds since epoch) from /proc/stat.
func procBootTime() (int64, error) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "btime ") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return strconv.ParseInt(fields[1], 10, 64)
			}
		}
	}
	return 0, fmt.Errorf("ps: btime not found in /proc/stat")
}

// parseSID extracts the session ID (field 6 after comm) from /proc/pid/stat data.
func parseSID(data []byte) int {
	s := strings.TrimSpace(string(data))
	closeParen := strings.LastIndex(s, ")")
	if closeParen < 0 {
		return 0
	}
	rest := strings.Fields(s[closeParen+1:])
	// rest[3] = session
	if len(rest) >= 4 {
		sid, _ := strconv.Atoi(rest[3])
		return sid
	}
	return 0
}
