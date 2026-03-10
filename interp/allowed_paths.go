// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// allowedRoot pairs an absolute directory path with its opened os.Root handle.
type allowedRoot struct {
	absPath string
	root    *os.Root
}

// pathSandbox restricts filesystem access to a set of allowed directories.
// The restriction is enforced using os.Root (Go 1.24+), which uses openat
// syscalls for atomic path validation — immune to symlink and ".." traversal attacks.
type pathSandbox struct {
	roots []allowedRoot
}

// newPathSandbox validates paths and eagerly opens os.Root handles so the
// allowed directories are pinned before the caller can modify them between
// construction and the first run.
func newPathSandbox(paths []string) (*pathSandbox, error) {
	roots := make([]allowedRoot, len(paths))
	for i, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("AllowedPaths: cannot resolve %q: %w", p, err)
		}
		root, err := os.OpenRoot(abs)
		if err != nil {
			for _, prev := range roots[:i] {
				if prev.root != nil {
					prev.root.Close()
				}
			}

			info, statErr := os.Stat(abs)
			if statErr == nil && !info.IsDir() {
				return nil, fmt.Errorf("AllowedPaths: %q is not a directory", abs)
			}
			return nil, fmt.Errorf("AllowedPaths: cannot open root %q: %w", abs, err)
		}
		roots[i] = allowedRoot{absPath: abs, root: root}
	}
	return &pathSandbox{roots: roots}, nil
}

// resolve returns the matching os.Root and the path relative to it for the
// given absolute path. It returns false if no root matches.
func (s *pathSandbox) resolve(absPath string) (*os.Root, string, bool) {
	if s == nil {
		return nil, "", false
	}
	for _, ar := range s.roots {
		rel, err := filepath.Rel(ar.absPath, absPath)
		if err != nil {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return ar.root, rel, true
	}
	return nil, "", false
}

// access checks whether the resolved path is accessible with the given mode.
// All operations go through os.Root to stay within the sandbox.
// Mode: 0x04 = read, 0x02 = write, 0x01 = execute.
func (s *pathSandbox) access(ctx context.Context, path string, mode uint32) error {
	absPath := toAbs(path, HandlerCtx(ctx).Dir)

	if s == nil {
		return &os.PathError{Op: "access", Path: path, Err: os.ErrPermission}
	}
	for _, ar := range s.roots {
		rel, err := filepath.Rel(ar.absPath, absPath)
		if err != nil {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}

		// Open through os.Root once. This checks read access and gives
		// us a file descriptor for an atomic Stat (no TOCTOU window).
		f, err := ar.root.Open(rel)
		if err != nil {
			if mode&0x04 != 0 && !isErrIsDirectory(err) {
				return portablePathError(err)
			}
			// Read not requested, or target is a directory; fall back to Stat.
			info, serr := ar.root.Stat(rel)
			if serr != nil {
				return portablePathError(serr)
			}
			if !effectiveHasPerm(info, 0222, 0111, mode&0x02 != 0, mode&0x01 != 0) {
				return &os.PathError{Op: "access", Path: path, Err: os.ErrPermission}
			}
			return nil
		}

		// For write and execute, use mode bits from f.Stat() on the
		// open fd — atomic, no TOCTOU window.
		// The sandbox is read-only so -w is informational only.
		// effectiveHasPerm checks the permission class (owner/group/other)
		// that applies to the current process's effective UID/GID on Unix,
		// rather than the union of all classes.
		if mode&0x03 != 0 {
			info, err := f.Stat()
			if err != nil {
				f.Close()
				return portablePathError(err)
			}
			if !effectiveHasPerm(info, 0222, 0111, mode&0x02 != 0, mode&0x01 != 0) {
				f.Close()
				return &os.PathError{Op: "access", Path: path, Err: os.ErrPermission}
			}
		}
		f.Close()
		return nil
	}
	return &os.PathError{Op: "access", Path: path, Err: os.ErrPermission}
}

// toAbs resolves path against cwd when it is not already absolute.
func toAbs(path, cwd string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cwd, path)
}

// open implements the restricted file-open policy. The file is opened through
// os.Root for atomic path validation. Only read-only access is permitted;
// any write flags are rejected as a defense-in-depth measure.
func (s *pathSandbox) open(ctx context.Context, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	if flag != os.O_RDONLY {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
	}

	absPath := toAbs(path, HandlerCtx(ctx).Dir)

	root, relPath, ok := s.resolve(absPath)
	if !ok {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
	}

	f, err := root.OpenFile(relPath, flag, perm)
	if err != nil {
		return nil, portablePathError(err)
	}
	return f, nil
}

// readDir implements the restricted directory-read policy.
func (s *pathSandbox) readDir(ctx context.Context, path string) ([]fs.DirEntry, error) {
	absPath := toAbs(path, HandlerCtx(ctx).Dir)

	root, relPath, ok := s.resolve(absPath)
	if !ok {
		return nil, &os.PathError{Op: "readdir", Path: path, Err: os.ErrPermission}
	}

	f, err := root.Open(relPath)
	if err != nil {
		return nil, portablePathError(err)
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, portablePathError(err)
	}
	// os.Root's ReadDir does not guarantee sorted order like os.ReadDir.
	// Sort to match POSIX glob expansion expectations.
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})
	return entries, nil
}

// stat implements the restricted stat policy. It uses os.Root.Stat for
// metadata-only access — no file descriptor is opened, so it works on
// unreadable files and does not block on special files (e.g. FIFOs).
func (s *pathSandbox) stat(ctx context.Context, path string) (fs.FileInfo, error) {
	absPath := toAbs(path, HandlerCtx(ctx).Dir)

	root, relPath, ok := s.resolve(absPath)
	if !ok {
		return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrPermission}
	}

	info, err := root.Stat(relPath)
	if err != nil {
		return nil, portablePathError(err)
	}
	return info, nil
}

// lstat implements the restricted lstat policy. Like stat, it uses a
// metadata-only call, but does not follow symbolic links — the returned
// FileInfo describes the link itself rather than its target.
func (s *pathSandbox) lstat(ctx context.Context, path string) (fs.FileInfo, error) {
	absPath := toAbs(path, HandlerCtx(ctx).Dir)

	root, relPath, ok := s.resolve(absPath)
	if !ok {
		return nil, &os.PathError{Op: "lstat", Path: path, Err: os.ErrPermission}
	}

	info, err := root.Lstat(relPath)
	if err != nil {
		return nil, portablePathError(err)
	}
	return info, nil
}

// Close releases all os.Root file descriptors. It is safe to call multiple times.
func (s *pathSandbox) Close() error {
	if s == nil {
		return nil
	}
	for i := range s.roots {
		if s.roots[i].root != nil {
			s.roots[i].root.Close()
			s.roots[i].root = nil
		}
	}
	return nil
}

// AllowedPaths restricts file and directory access to the specified directories.
// Paths must be absolute directories that exist. When set, only files within
// these directories can be opened, read, or executed.
//
// When not set (default), all file access is blocked.
// An empty slice also blocks all file access.
func AllowedPaths(paths []string) RunnerOption {
	return func(r *Runner) error {
		sb, err := newPathSandbox(paths)
		if err != nil {
			return err
		}
		r.sandbox = sb
		return nil
	}
}
