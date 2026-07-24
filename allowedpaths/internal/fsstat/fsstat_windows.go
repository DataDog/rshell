// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package fsstat

import (
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

const fileFsFullSizeInformation = 7

var ntQueryVolumeInformationFile = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQueryVolumeInformationFile")

// fileFsFullSizeInfo mirrors FILE_FS_FULL_SIZE_INFORMATION. Windows reports
// filesystem capacity in allocation units rather than POSIX blocks.
type fileFsFullSizeInfo struct {
	TotalAllocationUnits           int64
	CallerAvailableAllocationUnits int64
	ActualAvailableAllocationUnits int64
	SectorsPerAllocationUnit       uint32
	BytesPerSector                 uint32
}

func read(root *os.Root, relPath string) (Info, error) {
	if !filepath.IsLocal(relPath) {
		return Info{}, &os.PathError{Op: "statfs", Path: relPath, Err: os.ErrInvalid}
	}

	rootFile, err := root.Open(".")
	if err != nil {
		return Info{}, err
	}
	defer rootFile.Close()

	handle := windows.Handle(rootFile.Fd())
	closeHandle := false
	if cleanPath := filepath.Clean(relPath); cleanPath != "." {
		handle, err = openMetadataAt(handle, cleanPath)
		if err != nil {
			if err == windows.STATUS_REPARSE_POINT_ENCOUNTERED {
				return Info{}, ErrPathChanged
			}
			return Info{}, &os.PathError{Op: "statfs", Path: relPath, Err: ntStatusErr(err)}
		}
		closeHandle = true
	}
	if closeHandle {
		defer windows.CloseHandle(handle)
	}

	info, err := readHandle(handle)
	if err != nil {
		return Info{}, &os.PathError{Op: "statfs", Path: relPath, Err: err}
	}
	return info, nil
}

// openMetadataAt opens path relative to rootHandle and rejects reparse points
// in every component. The caller has already resolved legitimate reparse
// points through AllowedPaths; encountering one here means that the path
// changed and must be resolved again.
func openMetadataAt(rootHandle windows.Handle, path string) (windows.Handle, error) {
	name, err := windows.NewNTUnicodeString(path)
	if err != nil {
		return windows.InvalidHandle, err
	}
	attrs := &windows.OBJECT_ATTRIBUTES{
		Length:        uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})),
		RootDirectory: rootHandle,
		ObjectName:    name,
		Attributes:    windows.OBJ_CASE_INSENSITIVE | windows.OBJ_DONT_REPARSE,
	}

	var handle windows.Handle
	err = windows.NtCreateFile(
		&handle,
		windows.FILE_READ_ATTRIBUTES|windows.SYNCHRONIZE,
		attrs,
		&windows.IO_STATUS_BLOCK{},
		nil,
		0,
		windows.FILE_SHARE_DELETE|windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		windows.FILE_OPEN,
		windows.FILE_OPEN_FOR_BACKUP_INTENT|windows.FILE_SYNCHRONOUS_IO_NONALERT,
		0,
		0,
	)
	return handle, err
}

func readHandle(handle windows.Handle) (Info, error) {
	var serialNumber uint32
	var maximumComponentLength uint32
	var fileSystemName [windows.MAX_PATH + 1]uint16
	if err := windows.GetVolumeInformationByHandle(
		handle,
		nil,
		0,
		&serialNumber,
		&maximumComponentLength,
		nil,
		&fileSystemName[0],
		uint32(len(fileSystemName)),
	); err != nil {
		return Info{}, err
	}

	fullSize, err := queryFullSize(handle)
	if err != nil {
		return Info{}, err
	}
	if fullSize.TotalAllocationUnits < 0 ||
		fullSize.CallerAvailableAllocationUnits < 0 ||
		fullSize.ActualAvailableAllocationUnits < 0 ||
		fullSize.SectorsPerAllocationUnit == 0 ||
		fullSize.BytesPerSector == 0 {
		return Info{}, fmt.Errorf("invalid filesystem size information")
	}

	allocationUnitSize := uint64(fullSize.SectorsPerAllocationUnit) * uint64(fullSize.BytesPerSector)
	return Info{
		ID:                   uint64(serialNumber),
		IDAvailable:          true,
		NameMax:              uint64(maximumComponentLength),
		NameMaxAvailable:     true,
		TypeName:             windows.UTF16ToString(fileSystemName[:]),
		IOBlockSize:          allocationUnitSize,
		FundamentalBlockSize: allocationUnitSize,
		Blocks:               uint64(fullSize.TotalAllocationUnits),
		BlocksFree:           uint64(fullSize.ActualAvailableAllocationUnits),
		BlocksAvailable:      uint64(fullSize.CallerAvailableAllocationUnits),
		FilesAvailable:       false,
	}, nil
}

func queryFullSize(handle windows.Handle) (fileFsFullSizeInfo, error) {
	var fullSize fileFsFullSizeInfo
	var ioStatus windows.IO_STATUS_BLOCK
	status, _, _ := ntQueryVolumeInformationFile.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&ioStatus)),
		uintptr(unsafe.Pointer(&fullSize)),
		unsafe.Sizeof(fullSize),
		fileFsFullSizeInformation,
	)
	if ntStatus := windows.NTStatus(uint32(status)); ntStatus != windows.STATUS_SUCCESS {
		return fileFsFullSizeInfo{}, ntStatus.Errno()
	}
	return fullSize, nil
}

func ntStatusErr(err error) error {
	if status, ok := err.(windows.NTStatus); ok {
		return status.Errno()
	}
	return err
}
