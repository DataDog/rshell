// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedpaths

import (
	"errors"
	"os"

	"github.com/DataDog/rshell/allowedpaths/internal/fsstat"
)

// FileSystemInfo is the normalized filesystem metadata returned by StatFS.
type FileSystemInfo = fsstat.Info

// StatFS returns filesystem metadata for path after resolving it through the
// AllowedPaths sandbox. The platform backend queries the opened path handle,
// so the result cannot be redirected outside the sandbox after validation.
func (s *Sandbox) StatFS(path, cwd string) (FileSystemInfo, error) {
	if path == "" {
		return FileSystemInfo{}, &os.PathError{Op: "statfs", Path: path, Err: os.ErrNotExist}
	}
	absPath := toAbs(path, cwd)

	for range maxSymlinkHops + 1 {
		ar, relPath, ok := s.resolveRootFollowingSymlinks(absPath, false)
		if !ok {
			return FileSystemInfo{}, &os.PathError{Op: "statfs", Path: path, Err: os.ErrPermission}
		}

		info, err := fsstat.Read(ar.root, relPath)
		if errors.Is(err, fsstat.ErrPathChanged) || isPathEscapeError(err) {
			continue
		}
		if err != nil {
			if errors.Is(err, fsstat.ErrNotSupported) {
				return FileSystemInfo{}, err
			}
			return FileSystemInfo{}, rewrapPathError("statfs", path, err)
		}
		return info, nil
	}

	return FileSystemInfo{}, &os.PathError{Op: "statfs", Path: path, Err: fsstat.ErrPathChanged}
}
