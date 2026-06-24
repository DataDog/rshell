// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux || darwin

package allowedpaths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

type readRestart struct {
	absPath string
}

func (s *Sandbox) openReadDenyAware(path string, cwd string, flag int, perm os.FileMode) (*os.File, error) {
	absPath := filepath.Clean(toAbs(path, cwd))
	for hops := 0; hops <= maxSymlinkHops; hops++ {
		f, restart, err := s.openReadDenyAwareAbs(path, absPath, flag, perm)
		if err != nil {
			return nil, err
		}
		if restart == nil {
			return f, nil
		}
		absPath = filepath.Clean(restart.absPath)
	}
	return nil, &os.PathError{Op: "openat", Path: path, Err: unix.ELOOP}
}

func (s *Sandbox) openReadDenyAwareAbs(displayPath, absPath string, flag int, perm os.FileMode) (*os.File, *readRestart, error) {
	if s.deniedFor(absPath, denyModeRead) {
		return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: os.ErrPermission}
	}
	ar, relPath, ok := s.resolve(absPath)
	if !ok {
		return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: os.ErrPermission}
	}
	if ar.readRoot == nil {
		return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: os.ErrPermission}
	}

	clean := filepath.Clean(relPath)
	components := []string{"."}
	if clean != "." {
		components = strings.Split(clean, string(filepath.Separator))
	}

	dirFD := int(ar.readRoot.Fd())
	closeDir := false
	currentAbs := ar.absPath
	activeDeny := s.denyModeForPath(currentAbs)
	if activeDeny&denyModeRead != 0 {
		return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: os.ErrPermission}
	}
	for i, component := range components {
		final := i == len(components)-1
		if component == "" || component == "." {
			if final {
				fd, err := unix.Openat(dirFD, ".", flag|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(perm.Perm()))
				if closeDir {
					_ = unix.Close(dirFD)
				}
				if err != nil {
					return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: err}
				}
				if err := s.checkOpenedDeny(fd, displayPath, activeDeny, denyModeRead); err != nil {
					_ = unix.Close(fd)
					return nil, nil, err
				}
				f := os.NewFile(uintptr(fd), displayPath)
				if f == nil {
					_ = unix.Close(fd)
					return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: errors.New("invalid file descriptor")}
				}
				return f, nil, nil
			}
			continue
		}
		if component == ".." {
			if closeDir {
				_ = unix.Close(dirFD)
			}
			return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: os.ErrPermission}
		}

		nextAbs := filepath.Join(currentAbs, component)
		activeDeny |= s.denyModeForPath(nextAbs)
		if activeDeny&denyModeRead != 0 {
			if closeDir {
				_ = unix.Close(dirFD)
			}
			return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: os.ErrPermission}
		}

		if final {
			fd, err := unix.Openat(dirFD, component, flag|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(perm.Perm()))
			if errors.Is(err, unix.ELOOP) {
				target, linkErr := readlinkat(dirFD, component)
				if closeDir {
					_ = unix.Close(dirFD)
				}
				if linkErr != nil {
					return nil, nil, &os.PathError{Op: "readlinkat", Path: displayPath, Err: linkErr}
				}
				return nil, &readRestart{absPath: s.resolveSymlinkTarget(currentAbs, target, nil)}, nil
			}
			if closeDir {
				_ = unix.Close(dirFD)
			}
			if err != nil {
				return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: err}
			}
			if err := s.checkOpenedDeny(fd, displayPath, activeDeny, denyModeRead); err != nil {
				_ = unix.Close(fd)
				return nil, nil, err
			}
			f := os.NewFile(uintptr(fd), displayPath)
			if f == nil {
				_ = unix.Close(fd)
				return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: errors.New("invalid file descriptor")}
			}
			return f, nil, nil
		}

		nextFD, err := unix.Openat(dirFD, component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
		if errors.Is(err, unix.ELOOP) {
			target, linkErr := readlinkat(dirFD, component)
			if closeDir {
				_ = unix.Close(dirFD)
			}
			if linkErr != nil {
				return nil, nil, &os.PathError{Op: "readlinkat", Path: displayPath, Err: linkErr}
			}
			return nil, &readRestart{absPath: s.resolveSymlinkTarget(currentAbs, target, components[i+1:])}, nil
		}
		if closeDir {
			_ = unix.Close(dirFD)
		}
		if err != nil {
			return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: err}
		}
		if err := s.checkOpenedDeny(nextFD, displayPath, activeDeny, denyModeRead); err != nil {
			_ = unix.Close(nextFD)
			return nil, nil, err
		}
		dirFD = nextFD
		closeDir = true
		currentAbs = nextAbs
	}

	if closeDir {
		_ = unix.Close(dirFD)
	}
	return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: os.ErrInvalid}
}

func (s *Sandbox) resolveSymlinkTarget(parentAbs string, target string, remaining []string) string {
	var absPath string
	if filepath.IsAbs(target) {
		absPath = target
		if s.hostPrefix != "" && !strings.HasPrefix(absPath, s.hostPrefix+string(filepath.Separator)) {
			absPath = filepath.Join(s.hostPrefix, absPath)
		}
	} else {
		absPath = filepath.Join(parentAbs, target)
	}
	if len(remaining) > 0 {
		absPath = filepath.Join(absPath, filepath.Join(remaining...))
	}
	return absPath
}

func (s *Sandbox) checkOpenedDeny(fd int, displayPath string, active denyMode, requested denyMode) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return &os.PathError{Op: "fstat", Path: displayPath, Err: err}
	}
	mode := active | s.denyModeForIdentity(fileIdentity{dev: uint64(st.Dev), ino: uint64(st.Ino)})
	if mode&requested != 0 {
		return &os.PathError{Op: "openat", Path: displayPath, Err: os.ErrPermission}
	}
	return nil
}

func readlinkat(dirFD int, name string) (string, error) {
	size := 256
	for {
		buf := make([]byte, size)
		n, err := unix.Readlinkat(dirFD, name, buf)
		if err != nil {
			return "", err
		}
		if n < len(buf) {
			return string(buf[:n]), nil
		}
		size *= 2
	}
}
