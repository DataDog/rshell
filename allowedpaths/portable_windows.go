// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedpaths

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// IsErrIsDirectory checks if the error is the Windows equivalent of EISDIR.
// On Windows, reading a directory handle returns ERROR_INVALID_FUNCTION (errno 1).
func IsErrIsDirectory(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.Errno(1) // ERROR_INVALID_FUNCTION
	}
	return false
}

// FileIdentity extracts canonical file identity on Windows using
// GetFileInformationByHandle (volume serial + file index).
// The path and sandbox are needed to open the file through the sandbox.
func FileIdentity(absPath string, _ fs.FileInfo, sandbox *Sandbox) (uint64, uint64, bool) {
	ar, relPath, ok := sandbox.resolve(absPath)
	if !ok {
		return 0, 0, false
	}
	f, err := ar.root.OpenFile(relPath, os.O_RDONLY, 0)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()

	h := syscall.Handle(f.Fd())
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &d); err != nil {
		return 0, 0, false
	}
	return uint64(d.VolumeSerialNumber), uint64(d.FileIndexHigh)<<32 | uint64(d.FileIndexLow), true
}

// accessCheck verifies the path is inside the sandbox via os.Root.Stat,
// then checks read permission by attempting to open the file through
// os.Root. This respects NTFS ACLs — the kernel denies the open if
// the current user lacks read permission. Named pipes cannot appear in
// regular directories on Windows, so this cannot block.
//
//   - Read: verified by opening through os.Root (respects NTFS ACLs).
//   - Write: checked via mode bits from Stat. On Windows,
//     FILE_ATTRIBUTE_READONLY clears the write permission bits in
//     Mode().Perm(), so mode-bit inspection is reliable.
//   - Execute: Windows has no POSIX execute bits. The check always
//     returns ErrPermission so that test -x behaves like a POSIX shell.
func (r *root) accessCheck(rel string, checkRead, checkWrite, checkExec bool) (fs.FileInfo, error) {
	info, err := r.root.Stat(rel)
	if err != nil {
		return nil, err
	}

	// Windows has no POSIX execute bits — always deny execute checks.
	if checkExec {
		return info, os.ErrPermission
	}

	// On Windows, FILE_ATTRIBUTE_READONLY clears the write permission
	// bits in Mode().Perm(). Check them for write access.
	if checkWrite && info.Mode().Perm()&0200 == 0 {
		return info, os.ErrPermission
	}

	if checkRead && !info.IsDir() {
		f, err := r.root.OpenFile(rel, os.O_RDONLY, 0)
		if err != nil {
			return info, os.ErrPermission
		}
		if err := f.Close(); err != nil {
			return info, err
		}
	}

	return info, nil
}

func (r *root) openFileNoFollow(rel string, flag int, perm os.FileMode) (*os.File, error) {
	rel = filepath.Clean(rel)
	if rel == "." {
		return nil, &os.PathError{Op: "open", Path: rel, Err: errors.New("is a directory")}
	}

	rootDir, err := r.root.Open(".")
	if err != nil {
		return nil, err
	}
	defer rootDir.Close()

	rootHandle := windows.Handle(rootDir.Fd())
	dirHandle := rootHandle
	closeDirHandle := func() {
		if dirHandle != rootHandle {
			_ = windows.CloseHandle(dirHandle)
			dirHandle = rootHandle
		}
	}
	defer closeDirHandle()

	components := strings.Split(rel, string(filepath.Separator))
	for _, component := range components[:len(components)-1] {
		if component == "" || component == "." {
			continue
		}
		handle, err := openDirectoryComponentNoFollow(dirHandle, component)
		if err != nil {
			return nil, noFollowOpenPathError(rel, err)
		}
		closeDirHandle()
		dirHandle = handle
	}

	leaf := components[len(components)-1]
	if leaf == "" || leaf == "." {
		return nil, &os.PathError{Op: "open", Path: rel, Err: errors.New("is a directory")}
	}

	handle, err := openFileComponentNoFollow(dirHandle, leaf, flag, perm)
	if err != nil {
		return nil, noFollowOpenPathError(rel, err)
	}
	f := os.NewFile(uintptr(handle), rel)
	if f == nil {
		_ = windows.CloseHandle(handle)
		return nil, &os.PathError{Op: "open", Path: rel, Err: syscall.EINVAL}
	}
	return f, nil
}

func (r *root) openFileValidatedNoFollow(rel string, flag int, perm os.FileMode, _ bool) (*os.File, error) {
	rel = filepath.Clean(rel)
	if rel == "." {
		return nil, &os.PathError{Op: "open", Path: rel, Err: errors.New("is a directory")}
	}
	f, err := r.openFileNoFollow(rel, flag, perm)
	if err != nil {
		return nil, PortablePathError(err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if info.IsDir() {
		_ = f.Close()
		return nil, &os.PathError{Op: "open", Path: rel, Err: errors.New("is a directory")}
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, &os.PathError{Op: "open", Path: rel, Err: os.ErrPermission}
	}
	return f, nil
}

func openDirectoryComponentNoFollow(dir windows.Handle, name string) (windows.Handle, error) {
	return ntCreateFileNoFollow(
		dir,
		name,
		windows.FILE_GENERIC_READ|windows.FILE_LIST_DIRECTORY,
		windows.FILE_ATTRIBUTE_NORMAL,
		windows.FILE_OPEN,
		windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
	)
}

func openFileComponentNoFollow(dir windows.Handle, name string, flag int, perm os.FileMode) (windows.Handle, error) {
	access := uint32(0)
	options := uint32(windows.FILE_NON_DIRECTORY_FILE | windows.FILE_OPEN_FOR_BACKUP_INTENT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	switch flag & (os.O_RDONLY | os.O_WRONLY | os.O_RDWR) {
	case os.O_WRONLY:
		access |= windows.FILE_GENERIC_WRITE
	case os.O_RDWR:
		access |= windows.FILE_GENERIC_READ | windows.FILE_GENERIC_WRITE
	default:
		access |= windows.FILE_GENERIC_READ
	}
	if flag&os.O_CREATE != 0 {
		access |= windows.FILE_GENERIC_WRITE
	}
	if flag&os.O_APPEND != 0 {
		access |= windows.FILE_APPEND_DATA
		if flag&os.O_TRUNC == 0 {
			access &^= windows.FILE_WRITE_DATA
		}
	}
	access |= windows.STANDARD_RIGHTS_READ | windows.FILE_READ_ATTRIBUTES | windows.FILE_READ_EA

	disposition := uint32(windows.FILE_OPEN)
	switch {
	case flag&(os.O_CREATE|os.O_EXCL) == os.O_CREATE|os.O_EXCL:
		disposition = windows.FILE_CREATE
	case flag&os.O_CREATE != 0:
		disposition = windows.FILE_OPEN_IF
	}

	attrs := uint32(windows.FILE_ATTRIBUTE_NORMAL)
	if uint32(perm)&syscall.S_IWRITE == 0 {
		attrs = windows.FILE_ATTRIBUTE_READONLY
	}

	handle, err := ntCreateFileNoFollow(dir, name, access, attrs, disposition, options)
	if err != nil {
		return windows.InvalidHandle, err
	}
	if flag&os.O_TRUNC != 0 {
		err = syscall.Ftruncate(syscall.Handle(handle), 0)
		if err == windows.ERROR_INVALID_PARAMETER {
			if t, err1 := syscall.GetFileType(syscall.Handle(handle)); err1 == nil && (t == syscall.FILE_TYPE_PIPE || t == syscall.FILE_TYPE_CHAR) {
				err = nil
			}
		}
		if err != nil {
			_ = windows.CloseHandle(handle)
			return windows.InvalidHandle, err
		}
	}
	return handle, nil
}

func ntCreateFileNoFollow(dir windows.Handle, name string, access, attrs, disposition, options uint32) (windows.Handle, error) {
	if name == "" {
		return windows.InvalidHandle, syscall.ERROR_FILE_NOT_FOUND
	}
	objectName, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return windows.InvalidHandle, err
	}
	objectAttrs := &windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: dir,
		ObjectName:    objectName,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}
	var handle windows.Handle
	err = windows.NtCreateFile(
		&handle,
		windows.SYNCHRONIZE|access,
		objectAttrs,
		&windows.IO_STATUS_BLOCK{},
		nil,
		attrs,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		disposition,
		options,
		0,
		0,
	)
	if err != nil {
		return windows.InvalidHandle, ntCreateFileError(err)
	}
	return handle, nil
}

func ntCreateFileError(err error) error {
	status, ok := err.(windows.NTStatus)
	if !ok {
		return err
	}
	switch status {
	case windows.STATUS_REPARSE_POINT_ENCOUNTERED:
		return syscall.ELOOP
	case windows.STATUS_NOT_A_DIRECTORY:
		return syscall.ENOTDIR
	case windows.STATUS_FILE_IS_A_DIRECTORY:
		return syscall.EISDIR
	case windows.STATUS_OBJECT_NAME_COLLISION:
		return syscall.EEXIST
	}
	return status.Errno()
}

func noFollowOpenPathError(rel string, err error) error {
	if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.ENOTDIR) {
		return &os.PathError{Op: "open", Path: rel, Err: os.ErrPermission}
	}
	return &os.PathError{Op: "open", Path: rel, Err: err}
}
