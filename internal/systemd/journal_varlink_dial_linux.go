// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package systemd

import (
	"context"
	"fmt"
	"net"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

const journalControlFDDir = "/proc/self/fd"

func dialJournalControl(ctx context.Context, path string) (net.Conn, error) {
	fd, err := openJournalControlSocket(path)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	return dialPinnedJournalControl(ctx, fd)
}

func openJournalControlSocket(path string) (int, error) {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("inspect journal control socket: %w", err)
	}

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("inspect journal control socket: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFSOCK {
		_ = unix.Close(fd)
		return -1, fmt.Errorf("journal control endpoint is not a Unix socket")
	}
	return fd, nil
}

func dialPinnedJournalControl(ctx context.Context, fd int) (net.Conn, error) {
	endpoint := filepath.Join(journalControlFDDir, strconv.Itoa(fd))
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", endpoint)
	if err != nil {
		return nil, contextIOError(ctx, "connect to pinned journal control socket", err)
	}
	return conn, nil
}
