// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package allowedpaths

import (
	"errors"
	"os"
)

func resolveAllowedPathMode(path string) (string, pathMode) {
	stripped, mode, ok := splitAllowedPathMode(path)
	if !ok {
		return path, pathModeReadOnly
	}
	// A ":ro" suffix is always access-mode metadata. Its meaning must not
	// depend on filesystem state: a script with write access to the parent
	// directory could otherwise create a literal "<root>:ro" entry and make
	// later sandboxes drop a nested read-only root, exposing it to the
	// enclosing read-write root. Stripping ":ro" can only add a read-only
	// root, so it never widens write access.
	if mode == pathModeReadOnly {
		return stripped, mode
	}
	// On POSIX filesystems, paths may literally end in ":rw". Preserve an
	// existing literal path, or any path we cannot prove is absent, so the
	// suffix never widens access by stripping real filename text.
	if _, err := os.Lstat(path); err == nil || !errors.Is(err, os.ErrNotExist) {
		return path, pathModeReadOnly
	}
	return stripped, mode
}
