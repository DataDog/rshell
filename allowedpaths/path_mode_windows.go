// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package allowedpaths

import (
	"path/filepath"
	"strings"
)

func resolveAllowedPathMode(path string) (string, pathMode) {
	stripped, mode, ok := splitAllowedPathMode(path)
	if !ok {
		return path, pathModeReadOnly
	}
	// Windows treats terminal ":ro" and ":rw" as rshell access-mode
	// metadata unconditionally. Unlike POSIX, where ":" is ordinary filename
	// text and an existing literal path must be preserved, Windows path
	// components cannot normally end this way without entering NTFS alternate
	// data stream syntax (for example, "file:rw"). We intentionally choose the
	// rshell mode suffix interpretation for this unsupported ambiguity.
	return stripped, mode
}

func resolveDeniedPathMode(path string) (string, denyMode) {
	stripped, mode, ok := splitDeniedPathMode(path)
	if !ok {
		return path, denyModeRead | denyModeWrite
	}
	// Windows treats terminal ":r" and ":w" as rshell deny-mode metadata.
	return stripped, mode
}

func relWithin(rootPath, path string) (string, bool) {
	rootPath = filepath.Clean(rootPath)
	path = filepath.Clean(path)
	if strings.EqualFold(rootPath, path) {
		return ".", true
	}
	prefix := rootPath
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if len(path) < len(prefix) || !strings.EqualFold(path[:len(prefix)], prefix) {
		return "", false
	}
	return path[len(prefix):], true
}

func hasUnsupportedPathSyntax(path string) bool {
	return hasWindowsAlternateDataStream(path)
}
