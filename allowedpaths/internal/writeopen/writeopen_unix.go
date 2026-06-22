// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || darwin

package writeopen

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const InvalidFD = -1

func OpenRoot(path string) (int, error) {
	return unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
}

func CloseRoot(fd int) {
	if fd != InvalidFD {
		_ = unix.Close(fd)
	}
}

func OpenFile(rootFD int, _ *os.Root, relPath string, flag int, perm os.FileMode) (*os.File, error) {
	if rootFD == InvalidFD {
		return nil, &os.PathError{Op: "openat", Path: relPath, Err: os.ErrPermission}
	}

	clean := filepath.Clean(relPath)
	if clean == "." {
		return nil, &os.PathError{Op: "openat", Path: relPath, Err: unix.EISDIR}
	}
	components := strings.Split(clean, string(filepath.Separator))
	dirFD := rootFD
	closeDir := false
	for _, component := range components[:len(components)-1] {
		if component == "" || component == "." {
			continue
		}
		if component == ".." {
			if closeDir {
				_ = unix.Close(dirFD)
			}
			return nil, &os.PathError{Op: "openat", Path: relPath, Err: os.ErrPermission}
		}
		nextFD, err := unix.Openat(dirFD, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if closeDir {
			_ = unix.Close(dirFD)
		}
		if err != nil {
			return nil, &os.PathError{Op: "openat", Path: relPath, Err: err}
		}
		dirFD = nextFD
		closeDir = true
	}

	base := components[len(components)-1]
	if base == "" || base == "." || base == ".." {
		if closeDir {
			_ = unix.Close(dirFD)
		}
		return nil, &os.PathError{Op: "openat", Path: relPath, Err: unix.EISDIR}
	}
	fd, err := unix.Openat(dirFD, base, flag|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(perm.Perm()))
	if closeDir {
		_ = unix.Close(dirFD)
	}
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: relPath, Err: err}
	}
	f := os.NewFile(uintptr(fd), relPath)
	if f == nil {
		_ = unix.Close(fd)
		return nil, &os.PathError{Op: "openat", Path: relPath, Err: errors.New("invalid file descriptor")}
	}
	return f, nil
}
