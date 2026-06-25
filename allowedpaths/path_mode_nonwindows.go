// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package allowedpaths

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func resolveAllowedPathMode(path string) (string, pathMode) {
	stripped, mode, ok := splitAllowedPathMode(path)
	if !ok {
		return path, pathModeReadOnly
	}
	// On POSIX filesystems, paths may literally end in ":ro" or ":rw".
	// Preserve an existing literal path, or any path we cannot prove is absent,
	// so a config suffix never widens access by stripping real filename text.
	if _, err := os.Lstat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
		return path, pathModeReadOnly
	}
	return stripped, mode
}

func resolveDeniedPathMode(path string) (string, denyMode) {
	stripped, mode, ok := splitDeniedPathMode(path)
	if !ok {
		return path, denyModeRead | denyModeWrite
	}
	// On POSIX filesystems, paths may literally end in ":r" or ":w".
	// Preserve an existing literal path, or any path we cannot prove is absent,
	// so a config suffix never widens access by stripping real filename text.
	if _, err := os.Lstat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
		return path, denyModeRead | denyModeWrite
	}
	return stripped, mode
}

func relWithin(rootPath, path string) (string, bool) {
	rel, err := filepath.Rel(filepath.Clean(rootPath), filepath.Clean(path))
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

func hasUnsupportedPathSyntax(string) bool {
	return false
}
