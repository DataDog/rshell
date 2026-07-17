// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package writeopen

import "os"

func OpenRoot(*os.Root) (*os.File, error) {
	return nil, nil
}

func CloseRoot(*os.File) {}

func OpenFile(_ *os.File, root *os.Root, relPath string, flag int, perm os.FileMode) (*os.File, error) {
	// On Windows, os.Root.OpenFile uses the runtime's handle-relative
	// openat implementation with O_NOFOLLOW_ANY, which rejects reparse
	// points anywhere in the path. Keep using it here instead of the Unix
	// openat walker.
	return root.OpenFile(relPath, flag, perm)
}

// Unlink removes relPath. On Windows, os.Root.Remove is implemented on top
// of the same O_NOFOLLOW_ANY handle-relative primitives as OpenFile, so it
// already rejects reparse points anywhere in the path atomically; no custom
// walker is needed here. Directories are rejected via Lstat, matching the
// Unix implementation's fstatat check.
func Unlink(_ *os.File, root *os.Root, relPath string) error {
	info, err := root.Lstat(relPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return &os.PathError{Op: "remove", Path: relPath, Err: ErrIsDirectory}
	}
	return root.Remove(relPath)
}
