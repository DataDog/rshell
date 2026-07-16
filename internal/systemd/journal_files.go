// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package systemd

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxJournalFiles      = 4096
	maxMachineIDFileSize = 64
)

func (c *Client) journalFiles() (string, []string, error) {
	if c.target.MachineIDPath == "" {
		return "", nil, fmt.Errorf("systemd target machine ID path is not configured")
	}
	if len(c.target.JournalDirs) == 0 {
		return "", nil, fmt.Errorf("systemd target journal directories are not configured")
	}

	machineID, err := readMachineID(c.target.MachineIDPath)
	if err != nil {
		return "", nil, err
	}

	files := make([]string, 0)
	for _, baseDir := range c.target.JournalDirs {
		dirPath := filepath.Join(baseDir, machineID)
		dir, err := openJournalMachineDirectory(dirPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", nil, fmt.Errorf("open journal directory %q: %w", dirPath, err)
		}

		entries, readErr := dir.ReadDir(maxJournalFiles + 1)
		closeErr := dir.Close()
		if readErr != nil && readErr != io.EOF {
			return "", nil, fmt.Errorf("read journal directory %q: %w", dirPath, readErr)
		}
		if closeErr != nil {
			return "", nil, fmt.Errorf("close journal directory %q: %w", dirPath, closeErr)
		}
		if len(entries) > maxJournalFiles {
			return "", nil, fmt.Errorf("journal directory %q has too many entries (maximum %d)", dirPath, maxJournalFiles)
		}

		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".journal") || entry.Type()&fs.ModeSymlink != 0 {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				return "", nil, fmt.Errorf("inspect journal file %q: %w", filepath.Join(dirPath, entry.Name()), err)
			}
			if !info.Mode().IsRegular() {
				continue
			}
			files = append(files, filepath.Join(dirPath, entry.Name()))
			if len(files) > maxJournalFiles {
				return "", nil, fmt.Errorf("systemd target has too many journal files (maximum %d)", maxJournalFiles)
			}
		}
	}

	sort.Strings(files)
	return machineID, files, nil
}

func openJournalMachineDirectory(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("journal machine directory %q is not a real directory", path)
	}

	dir, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := dir.Stat()
	if err != nil {
		dir.Close()
		return nil, err
	}
	after, err := os.Lstat(path)
	if err != nil {
		dir.Close()
		return nil, err
	}
	if !opened.IsDir() || !after.IsDir() || after.Mode()&fs.ModeSymlink != 0 || !os.SameFile(before, opened) || !os.SameFile(after, opened) {
		dir.Close()
		return nil, fmt.Errorf("journal machine directory %q changed while opening", path)
	}
	return dir, nil
}

func readMachineID(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open systemd machine ID %q: %w", path, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxMachineIDFileSize+1))
	closeErr := file.Close()
	if readErr != nil {
		return "", fmt.Errorf("read systemd machine ID %q: %w", path, readErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close systemd machine ID %q: %w", path, closeErr)
	}
	if len(data) > maxMachineIDFileSize {
		return "", fmt.Errorf("systemd machine ID file %q is too large", path)
	}

	machineID := strings.TrimSpace(string(data))
	if len(machineID) != 32 {
		return "", fmt.Errorf("systemd machine ID in %q must contain exactly 32 hexadecimal characters", path)
	}
	if !validID128(machineID) {
		return "", fmt.Errorf("systemd machine ID in %q contains a non-hexadecimal character", path)
	}
	return strings.ToLower(machineID), nil
}

func validID128(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f') || (char >= 'A' && char <= 'F')) {
			return false
		}
	}
	return true
}
