// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build unix

package read

import "golang.org/x/sys/unix"

// pollInputNonConsuming reports whether reading from the given file
// descriptor would not block — meaning either input is buffered or the
// peer has closed (EOF). It does not consume any data from the
// descriptor, matching bash's `read -t 0` semantics. The supported
// return reports whether the platform implementation could perform the
// check; on platforms without a non-blocking poll syscall the caller
// must fall back to a consume-based probe.
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
	return fds[0].Revents&(unix.POLLIN|unix.POLLHUP) != 0, true
}
