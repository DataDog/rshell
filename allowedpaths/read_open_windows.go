// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package allowedpaths

import (
	"os"
	"path/filepath"
)

func (s *Sandbox) openReadDenyAware(path string, cwd string, flag int, perm os.FileMode) (*os.File, error) {
	absPath := filepath.Clean(toAbs(path, cwd))
	if s.deniedFor(absPath, denyModeRead) {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
	}
	ar, relPath, ok := s.resolve(absPath)
	if !ok {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
	}

	resolved, resolvedRel, ok := s.resolveRootFollowingSymlinks(absPath, false)
	if ok {
		resolvedAbs := filepath.Join(resolved.absPath, resolvedRel)
		resolvedCanonicalAbs := filepath.Join(resolved.canonicalAbsPath, resolvedRel)
		if s.deniedFor(resolvedAbs, denyModeRead) || s.deniedFor(resolvedCanonicalAbs, denyModeRead) {
			return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
		}
	}

	f, err := ar.root.OpenFile(relPath, flag, perm)
	if err != nil && isPathEscapeError(err) {
		if r, rel, ok := s.resolveFollowingSymlinks(absPath, false); ok {
			f, err = r.OpenFile(rel, flag, perm)
		}
	}
	if err != nil {
		return nil, err
	}
	identity, ok := fileIdentityFromOpenFile(f)
	if ok && s.denyModeForIdentity(identity)&denyModeRead != 0 {
		f.Close()
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
	}
	return f, nil
}
