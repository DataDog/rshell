// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build unix

package read

import "golang.org/x/sys/unix"

// pollInputNonConsuming reports whether reading from the given file
// descriptor would yield input data without blocking — i.e. there are
// bytes buffered to be read. It does NOT consider a drained closed
// pipe (POLLHUP without POLLIN) as available, matching bash 5.2's
// `read -t 0` semantics: `printf ” | { read -t 0 v; echo $?; }`
// returns 1 because the peer closed without producing data, not 0.
//
// Linux poll(2) sets revents bits independently:
//   - POLLIN — data is buffered for reading (or EOF on a regular
//     file, which is always immediately readable).
//   - POLLHUP — peer closed the channel; subsequent reads will see
//     buffered data first, then EOF. POLLHUP can be set with or
//     without POLLIN.
//
// When a pipe has buffered data AND the peer has closed, both
// POLLIN and POLLHUP are set — POLLIN alone is enough to recognise
// this as available. When a pipe is fully drained and closed, only
// POLLHUP is set; treating that as available would lie to scripts
// that use `read -t 0` to guard a subsequent read.
//
// The supported return reports whether the platform implementation
// could perform the check; on platforms without a non-blocking poll
// syscall the caller must fall back to a consume-based probe.
func pollInputNonConsuming(fd uintptr) (available, supported bool) {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, 0)
	if err != nil {
		// EINTR or similar: report not-available rather than guessing.
		return false, true
	}
	if n == 0 {
		return false, true
	}
	return fds[0].Revents&unix.POLLIN != 0, true
}
