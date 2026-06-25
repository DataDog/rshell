// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package allowedpaths

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

const windowsSymlinkFlagRelative = 1

type readRestart struct {
	absPath string
}

func (s *Sandbox) openReadDenyAware(path string, cwd string, flag int, perm os.FileMode) (*os.File, error) {
	if flag != os.O_RDONLY {
		return nil, &os.PathError{Op: "openat", Path: path, Err: os.ErrPermission}
	}
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
	return nil, &os.PathError{Op: "openat", Path: path, Err: syscall.ELOOP}
}

func (s *Sandbox) openReadDenyAwareAbs(displayPath, absPath string, _ int, _ os.FileMode) (*os.File, *readRestart, error) {
	if hasWindowsAlternateDataStream(absPath) || s.deniedFor(absPath, denyModeRead) {
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

	dirHandle := windows.Handle(ar.readRoot.Fd())
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
				f, err := s.openWindowsFinalRead(dirHandle, closeDir, ".", displayPath, activeDeny)
				return f, nil, err
			}
			continue
		}
		if component == ".." {
			closeWindowsHandle(dirHandle, closeDir)
			return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: os.ErrPermission}
		}

		nextAbs := filepath.Join(currentAbs, component)
		activeDeny |= s.denyModeForPath(nextAbs)
		if activeDeny&denyModeRead != 0 {
			closeWindowsHandle(dirHandle, closeDir)
			return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: os.ErrPermission}
		}

		nextHandle, err := openWindowsReadHandleAt(dirHandle, component)
		closeWindowsHandle(dirHandle, closeDir)
		if err != nil {
			return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: err}
		}

		info, err := windowsHandleInfo(nextHandle)
		if err != nil {
			_ = syscall.CloseHandle(syscall.Handle(nextHandle))
			return nil, nil, &os.PathError{Op: "fstat", Path: displayPath, Err: err}
		}
		if info.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			target, relative, err := readWindowsSupportedReparseTarget(nextHandle)
			_ = syscall.CloseHandle(syscall.Handle(nextHandle))
			if err != nil {
				if errors.Is(err, os.ErrPermission) {
					return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: os.ErrPermission}
				}
				return nil, nil, &os.PathError{Op: "readlinkat", Path: displayPath, Err: err}
			}
			remaining := components[i+1:]
			restart, ok := s.resolveWindowsReparseTarget(currentAbs, target, relative, remaining)
			if !ok {
				return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: os.ErrPermission}
			}
			return nil, &readRestart{absPath: restart}, nil
		}

		if final {
			if err := s.checkOpenedDenyWindows(nextHandle, displayPath, activeDeny, denyModeRead); err != nil {
				_ = syscall.CloseHandle(syscall.Handle(nextHandle))
				return nil, nil, err
			}
			f := os.NewFile(uintptr(nextHandle), displayPath)
			if f == nil {
				_ = syscall.CloseHandle(syscall.Handle(nextHandle))
				return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: os.ErrInvalid}
			}
			return f, nil, nil
		}

		if info.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY == 0 {
			_ = syscall.CloseHandle(syscall.Handle(nextHandle))
			return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: syscall.ENOTDIR}
		}
		if err := s.checkOpenedDenyWindows(nextHandle, displayPath, activeDeny, denyModeRead); err != nil {
			_ = syscall.CloseHandle(syscall.Handle(nextHandle))
			return nil, nil, err
		}
		dirHandle = nextHandle
		closeDir = true
		currentAbs = nextAbs
	}

	closeWindowsHandle(dirHandle, closeDir)
	return nil, nil, &os.PathError{Op: "openat", Path: displayPath, Err: os.ErrInvalid}
}

func (s *Sandbox) openWindowsFinalRead(parent windows.Handle, closeParent bool, name, displayPath string, active denyMode) (*os.File, error) {
	h, err := openWindowsReadHandleAt(parent, name)
	closeWindowsHandle(parent, closeParent)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: displayPath, Err: err}
	}
	if err := s.checkOpenedDenyWindows(h, displayPath, active, denyModeRead); err != nil {
		_ = syscall.CloseHandle(syscall.Handle(h))
		return nil, err
	}
	f := os.NewFile(uintptr(h), displayPath)
	if f == nil {
		_ = syscall.CloseHandle(syscall.Handle(h))
		return nil, &os.PathError{Op: "openat", Path: displayPath, Err: os.ErrInvalid}
	}
	return f, nil
}

func openWindowsReadHandleAt(parent windows.Handle, name string) (windows.Handle, error) {
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return windows.InvalidHandle, err
	}
	attrs := &windows.OBJECT_ATTRIBUTES{
		Length:        windowsObjectAttributesLength(),
		RootDirectory: parent,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE,
	}

	var h windows.Handle
	err = windows.NtCreateFile(
		&h,
		windows.FILE_GENERIC_READ,
		attrs,
		&windows.IO_STATUS_BLOCK{},
		nil,
		uint32(syscall.FILE_ATTRIBUTE_NORMAL),
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		windows.FILE_OPEN,
		windows.FILE_SYNCHRONOUS_IO_NONALERT|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_OPEN_REPARSE_POINT,
		0,
		0,
	)
	if err != nil {
		return windows.InvalidHandle, windowsOpenError(err)
	}
	return h, nil
}

func windowsObjectAttributesLength() uint32 {
	if strconv.IntSize == 64 {
		return 48
	}
	return 24
}

func windowsOpenError(err error) error {
	if status, ok := err.(windows.NTStatus); ok {
		switch status {
		case windows.STATUS_REPARSE_POINT_ENCOUNTERED:
			return syscall.ELOOP
		case windows.STATUS_NOT_A_DIRECTORY:
			return syscall.ENOTDIR
		case windows.STATUS_FILE_IS_A_DIRECTORY:
			return syscall.EISDIR
		case windows.STATUS_OBJECT_NAME_COLLISION:
			return syscall.EEXIST
		default:
			return status.Errno()
		}
	}
	return err
}

func windowsHandleInfo(h windows.Handle) (syscall.ByHandleFileInformation, error) {
	var info syscall.ByHandleFileInformation
	err := syscall.GetFileInformationByHandle(syscall.Handle(h), &info)
	return info, err
}

func (s *Sandbox) checkOpenedDenyWindows(h windows.Handle, displayPath string, active denyMode, requested denyMode) error {
	info, err := windowsHandleInfo(h)
	if err != nil {
		return &os.PathError{Op: "fstat", Path: displayPath, Err: err}
	}
	mode := active | s.denyModeForIdentity(fileIdentity{
		dev: uint64(info.VolumeSerialNumber),
		ino: uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow),
	})
	if mode&requested != 0 {
		return &os.PathError{Op: "openat", Path: displayPath, Err: os.ErrPermission}
	}
	return nil
}

func closeWindowsHandle(h windows.Handle, close bool) {
	if close {
		_ = syscall.CloseHandle(syscall.Handle(h))
	}
}

func readWindowsSupportedReparseTarget(h windows.Handle) (string, bool, error) {
	buf := make([]byte, windows.MAXIMUM_REPARSE_DATA_BUFFER_SIZE)
	var returned uint32
	err := windows.DeviceIoControl(
		h,
		windows.FSCTL_GET_REPARSE_POINT,
		nil,
		0,
		&buf[0],
		uint32(len(buf)),
		&returned,
		nil,
	)
	if err != nil {
		return "", false, err
	}
	if returned < 8 {
		return "", false, os.ErrInvalid
	}
	buf = buf[:returned]
	tag := windowsReparseUint32(buf, 0)
	switch tag {
	case windows.IO_REPARSE_TAG_SYMLINK:
		if len(buf) < 20 {
			return "", false, os.ErrInvalid
		}
		target, err := windowsReparseString(buf, 20, windowsReparseUint16(buf, 8), windowsReparseUint16(buf, 10))
		return target, windowsReparseUint32(buf, 16)&windowsSymlinkFlagRelative != 0, err
	case windows.IO_REPARSE_TAG_MOUNT_POINT:
		if len(buf) < 16 {
			return "", false, os.ErrInvalid
		}
		target, err := windowsReparseString(buf, 16, windowsReparseUint16(buf, 8), windowsReparseUint16(buf, 10))
		return target, false, err
	default:
		return "", false, os.ErrPermission
	}
}

func windowsReparseString(buf []byte, pathStart int, offset uint16, length uint16) (string, error) {
	start := pathStart + int(offset)
	end := start + int(length)
	if start < pathStart || end < start || end > len(buf) || length%2 != 0 {
		return "", os.ErrInvalid
	}
	words := make([]uint16, int(length)/2)
	for i := range words {
		j := start + i*2
		words[i] = uint16(buf[j]) | uint16(buf[j+1])<<8
	}
	return syscall.UTF16ToString(words), nil
}

func windowsReparseUint16(buf []byte, offset int) uint16 {
	return uint16(buf[offset]) | uint16(buf[offset+1])<<8
}

func windowsReparseUint32(buf []byte, offset int) uint32 {
	return uint32(windowsReparseUint16(buf, offset)) | uint32(windowsReparseUint16(buf, offset+2))<<16
}

func (s *Sandbox) resolveWindowsReparseTarget(parentAbs string, target string, relative bool, remaining []string) (string, bool) {
	var absPath string
	if relative {
		absPath = filepath.Join(parentAbs, target)
	} else {
		var ok bool
		absPath, ok = normalizeWindowsAbsoluteReparseTarget(target)
		if !ok {
			return "", false
		}
	}
	if len(remaining) > 0 {
		absPath = filepath.Join(absPath, filepath.Join(remaining...))
	}
	if hasWindowsAlternateDataStream(absPath) {
		return "", false
	}
	return absPath, true
}

func normalizeWindowsAbsoluteReparseTarget(target string) (string, bool) {
	target = filepath.Clean(target)
	const (
		ntPrefix       = `\??\`
		ntUNCPrefix    = `\??\UNC\`
		win32Prefix    = `\\?\`
		win32UNCPrefix = `\\?\UNC\`
		uncPrefix      = `\\`
		volumePrefix   = `Volume{`
	)
	switch {
	case strings.HasPrefix(target, ntUNCPrefix):
		target = uncPrefix + strings.TrimPrefix(target, ntUNCPrefix)
	case strings.HasPrefix(target, ntPrefix):
		target = strings.TrimPrefix(target, ntPrefix)
		if strings.HasPrefix(target, volumePrefix) {
			target = win32Prefix + target
		}
	case strings.HasPrefix(target, win32UNCPrefix):
		target = uncPrefix + strings.TrimPrefix(target, win32UNCPrefix)
	case strings.HasPrefix(target, win32Prefix):
		target = strings.TrimPrefix(target, win32Prefix)
		if strings.HasPrefix(target, volumePrefix) {
			target = win32Prefix + target
		}
	}
	target = filepath.Clean(target)
	if !filepath.IsAbs(target) {
		return "", false
	}
	return target, true
}

func hasWindowsAlternateDataStream(path string) bool {
	volume := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path, volume)
	return strings.Contains(rest, ":")
}
