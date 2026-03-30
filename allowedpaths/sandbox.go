// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package allowedpaths implements a filesystem sandbox that restricts access
// to a set of allowed directories using os.Root (Go 1.24+).
package allowedpaths

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// MaxGlobEntries is the maximum number of directory entries read per single
// glob expansion step. ReadDirForGlob returns an error for directories that
// exceed this limit to prevent memory exhaustion during pattern matching.
const MaxGlobEntries = 10_000

// root pairs an absolute directory path with its opened os.Root handle.
type root struct {
	absPath string
	root    *os.Root
}

// Sandbox restricts filesystem access to a set of allowed directories.
// The restriction is enforced using os.Root (Go 1.24+), which uses openat
// syscalls for atomic path validation — immune to symlink and ".." traversal attacks.
type Sandbox struct {
	roots []root
}

// New creates a sandbox from an allowlist of directory paths. Paths that do
// not exist or cannot be opened are silently skipped — the sandbox operates
// with whatever paths are available at construction time.
func New(paths []string) (*Sandbox, error) {
	roots := make([]root, 0, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			continue
		}
		r, err := os.OpenRoot(abs)
		if err != nil {
			// Path does not exist or is not a directory — skip.
			continue
		}
		roots = append(roots, root{absPath: abs, root: r})
	}
	return &Sandbox{roots: roots}, nil
}

// resolve returns the matching os.Root and the path relative to it for the
// given absolute path. It returns false if no root matches.
func (s *Sandbox) resolve(absPath string) (*os.Root, string, bool) {
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

// Access checks whether the resolved path is accessible with the given mode.
// All operations go through os.Root to stay within the sandbox.
// Mode: 0x04 = read, 0x02 = write, 0x01 = execute.
//
// On Unix, read permission for regular files is verified by attempting
// to open through os.Root with O_NONBLOCK (fd-relative openat, respects
// POSIX ACLs, never blocks on FIFOs). Metadata is obtained from the
// opened fd via fstat to eliminate TOCTOU between open and stat.
// For special files where open fails (e.g. sockets), and for write and
// execute checks, mode-bit inspection is used on the fd-relative Stat
// result. On Windows, the same OpenFile approach is used for read
// checks; write and execute checks are not performed.
//
// All operations are fd-relative through os.Root — no filesystem path is
// re-resolved through the mutable namespace after initial validation.
func (s *Sandbox) Access(path string, cwd string, mode uint32) error {
	absPath := toAbs(path, cwd)

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

		// accessCheck opens or stats the path through os.Root and
		// performs the permission check (fd-relative OpenFile with
		// O_NONBLOCK for reads on Unix, mode-bit inspection for
		// everything else).
		_, err = ar.accessCheck(rel, mode&0x04 != 0, mode&0x02 != 0, mode&0x01 != 0)
		if err != nil {
			return &os.PathError{Op: "access", Path: path, Err: os.ErrPermission}
		}
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

// IsDevNull reports whether path refers to the platform's null device.
func IsDevNull(path string) bool {
	if path == "/dev/null" {
		return true
	}
	// On Windows, os.DevNull is "NUL". Accept it case-insensitively.
	if os.DevNull != "/dev/null" && strings.EqualFold(path, os.DevNull) {
		return true
	}
	return false
}

// Open implements the restricted file-open policy. The file is opened through
// os.Root for atomic path validation. Only read-only access is permitted;
// any write flags are rejected as a defense-in-depth measure.
func (s *Sandbox) Open(path string, cwd string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	if flag != os.O_RDONLY {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
	}

	absPath := toAbs(path, cwd)

	root, relPath, ok := s.resolve(absPath)
	if !ok {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
	}

	f, err := root.OpenFile(relPath, flag, perm)
	if err != nil {
		return nil, PortablePathError(err)
	}
	return f, nil
}

// ReadDir implements the restricted directory-read policy.
func (s *Sandbox) ReadDir(path string, cwd string) ([]fs.DirEntry, error) {
	return s.readDirN(path, cwd, -1)
}

// ReadDirForGlob reads directory entries for glob expansion, capped at
// MaxGlobEntries. The underlying ReadDir call is limited to MaxGlobEntries+1
// so the kernel never materialises more entries than needed. If the directory
// exceeds the limit an error is returned before any pattern matching or
// sorting can occur, making the failure explicit rather than silently returning
// a partial listing that could miss valid matches.
func (s *Sandbox) ReadDirForGlob(path string, cwd string) ([]fs.DirEntry, error) {
	return s.readDirN(path, cwd, MaxGlobEntries)
}

// readDirN is the shared implementation for ReadDir and ReadDirForGlob.
// maxEntries <= 0 means unlimited. Otherwise f.ReadDir is called with
// maxEntries+1 to cap the read at the OS level; if the directory has more
// entries than the limit an error is returned.
func (s *Sandbox) readDirN(path string, cwd string, maxEntries int) ([]fs.DirEntry, error) {
	absPath := toAbs(path, cwd)

	root, relPath, ok := s.resolve(absPath)
	if !ok {
		return nil, &os.PathError{Op: "readdir", Path: path, Err: os.ErrPermission}
	}

	f, err := root.Open(relPath)
	if err != nil {
		return nil, PortablePathError(err)
	}
	defer f.Close()

	var entries []fs.DirEntry
	if maxEntries <= 0 {
		entries, err = f.ReadDir(-1)
	} else {
		entries, err = f.ReadDir(maxEntries + 1)
	}
	if err != nil && err != io.EOF {
		return nil, PortablePathError(err)
	}
	if maxEntries > 0 && len(entries) > maxEntries {
		return nil, &os.PathError{
			Op:   "readdir",
			Path: path,
			Err:  fmt.Errorf("directory has too many entries (cap: %d)", maxEntries),
		}
	}
	// os.Root's ReadDir does not guarantee sorted order like os.ReadDir.
	// Sort to match POSIX glob expansion expectations.
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})
	return entries, nil
}

// OpenDir opens a directory within the sandbox for incremental reading
// via ReadDir(n). The caller must close the returned handle when done.
// Returns fs.ReadDirFile to expose only read-only directory methods.
func (s *Sandbox) OpenDir(path string, cwd string) (fs.ReadDirFile, error) {
	absPath := toAbs(path, cwd)

	root, relPath, ok := s.resolve(absPath)
	if !ok {
		return nil, &os.PathError{Op: "opendir", Path: path, Err: os.ErrPermission}
	}

	f, err := root.Open(relPath)
	if err != nil {
		return nil, PortablePathError(err)
	}
	return f, nil
}

// IsDirEmpty checks whether a directory is empty by reading at most one
// entry. More efficient than reading all entries when only emptiness
// needs to be determined.
func (s *Sandbox) IsDirEmpty(path string, cwd string) (bool, error) {
	absPath := toAbs(path, cwd)

	root, relPath, ok := s.resolve(absPath)
	if !ok {
		return false, &os.PathError{Op: "readdir", Path: path, Err: os.ErrPermission}
	}

	f, err := root.Open(relPath)
	if err != nil {
		return false, PortablePathError(err)
	}
	defer f.Close()
	entries, err := f.ReadDir(1)
	if err != nil && err != io.EOF {
		return false, PortablePathError(err)
	}
	return len(entries) == 0, nil
}

// ReadDirLimited reads directory entries, skipping the first offset entries
// and returning up to maxRead entries sorted by name within the read window.
// Returns (entries, truncated, error). When truncated is true, the directory
// contained more entries beyond the returned set.
//
// The offset skips raw directory entries during reading (before sorting).
// This means offset does NOT correspond to positions in a sorted listing —
// pages may overlap or miss entries. This is an acceptable tradeoff to achieve
// O(n) memory regardless of offset value, where n = min(maxRead, entries).
func (s *Sandbox) ReadDirLimited(path string, cwd string, offset, maxRead int) ([]fs.DirEntry, bool, error) {
	absPath := toAbs(path, cwd)
	root, relPath, ok := s.resolve(absPath)
	if !ok {
		return nil, false, &os.PathError{Op: "readdir", Path: path, Err: os.ErrPermission}
	}
	f, err := root.Open(relPath)
	if err != nil {
		return nil, false, PortablePathError(err)
	}
	defer f.Close()

	// Defense-in-depth: clamp non-positive values.
	if offset < 0 {
		offset = 0
	}
	if maxRead <= 0 {
		return nil, false, nil
	}

	const batchSize = 256
	entries, truncated, lastErr := CollectDirEntries(func(n int) ([]fs.DirEntry, error) {
		return f.ReadDir(n)
	}, batchSize, offset, maxRead)

	if lastErr != nil {
		return entries, truncated, PortablePathError(lastErr)
	}
	return entries, truncated, nil
}

// CollectDirEntries reads directory entries in batches using readBatch,
// skipping the first offset entries and collecting up to maxRead entries.
// Returns (entries, truncated, lastErr). Entries are sorted by name.
//
// NOTE: We intentionally truncate before reading all entries. For directories
// larger than maxRead, the returned entries are sorted within the read window
// but may not be the globally-smallest names. Reading all entries to get
// globally-correct sorting would defeat the DoS protection — a directory with
// millions of files would OOM or stall. The truncation warning communicates
// that output is incomplete.
func CollectDirEntries(readBatch func(n int) ([]fs.DirEntry, error), batchSize, offset, maxRead int) ([]fs.DirEntry, bool, error) {
	entries := make([]fs.DirEntry, 0, maxRead)
	truncated := false
	skipped := 0
	var lastErr error

	for {
		batch, err := readBatch(batchSize)
		for _, e := range batch {
			if skipped < offset {
				skipped++
				continue
			}
			entries = append(entries, e)
		}
		// Capture non-EOF errors before checking truncation, since
		// ReadDir can return partial entries alongside an error.
		if err != nil && !errors.Is(err, io.EOF) {
			lastErr = err
		}
		if len(entries) > maxRead {
			truncated = true
			break
		}
		if err != nil {
			break
		}
	}

	// Sort collected entries by name.
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		return strings.Compare(a.Name(), b.Name())
	})

	// Trim to exactly maxRead if we overshot.
	if truncated && len(entries) > maxRead {
		entries = entries[:maxRead]
	}

	return entries, truncated, lastErr
}

// Stat implements the restricted stat policy. It uses os.Root.Stat for
// metadata-only access — no file descriptor is opened, so it works on
// unreadable files and does not block on special files (e.g. FIFOs).
func (s *Sandbox) Stat(path string, cwd string) (fs.FileInfo, error) {
	// The null device (/dev/null on Unix, NUL on Windows) is always
	// allowed and must be stat-ed directly because os.Root.Stat cannot
	// resolve platform device names (e.g. NUL on Windows).
	if IsDevNull(path) {
		return os.Stat(os.DevNull)
	}

	absPath := toAbs(path, cwd)

	root, relPath, ok := s.resolve(absPath)
	if !ok {
		return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrPermission}
	}

	info, err := root.Stat(relPath)
	if err != nil {
		return nil, PortablePathError(err)
	}
	return info, nil
}

// Lstat implements the restricted lstat policy. Like stat, it uses a
// metadata-only call, but does not follow symbolic links — the returned
// FileInfo describes the link itself rather than its target.
func (s *Sandbox) Lstat(path string, cwd string) (fs.FileInfo, error) {
	// The null device is never a symlink, so lstat behaves like stat.
	if IsDevNull(path) {
		return os.Stat(os.DevNull)
	}

	absPath := toAbs(path, cwd)

	root, relPath, ok := s.resolve(absPath)
	if !ok {
		return nil, &os.PathError{Op: "lstat", Path: path, Err: os.ErrPermission}
	}

	info, err := root.Lstat(relPath)
	if err != nil {
		return nil, PortablePathError(err)
	}
	return info, nil
}

// Close releases all os.Root file descriptors. It is safe to call multiple times.
func (s *Sandbox) Close() error {
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
