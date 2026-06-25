// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package writeopen

import "os"

func OpenRoot(root *os.Root) (*os.File, error) {
	return root.Open(".")
}

func CloseRoot(file *os.File) {
	if file != nil {
		_ = file.Close()
	}
}

func OpenFile(_ *os.File, root *os.Root, relPath string, flag int, perm os.FileMode) (*os.File, error) {
	// On Windows, os.Root.OpenFile uses the runtime's handle-relative
	// openat implementation with O_NOFOLLOW_ANY, which rejects reparse
	// points anywhere in the path. Keep using it here instead of the Unix
	// openat walker.
	return root.OpenFile(relPath, flag, perm)
}
