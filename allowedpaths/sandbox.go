// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package allowedpaths implements a filesystem sandbox that restricts access
// to a set of allowed directories using os.Root (Go 1.24+).
package allowedpaths

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// Access mode bits for permission checks.
const (
	modeRead    = 0x04
	modeWrite   = 0x02
	modeExecute = 0x01
)

// MaxGlobEntries is the maximum number of directory entries read per single
// glob expansion step. ReadDirForGlob returns an error for directories that
// exceed this limit to prevent memory exhaustion during pattern matching.
const MaxGlobEntries = 10_000

// root pairs an absolute directory path with its opened os.Root handle.
//
// canonicalAbsPath is the symlink-resolved form of absPath (computed
// via filepath.EvalSymlinks at sandbox-setup time). It equals absPath
// when absPath is not a symlink. Builtins that compute canonical
// paths (e.g. pwd -P) use this to translate the configured-root
// prefix back to its on-disk canonical form, which os.Root has
// already followed implicitly when opening the root.
type root struct {
	absPath          string
	canonicalAbsPath string
	root             *os.Root
}

// Sandbox restricts filesystem access to a set of allowed directories.
// The restriction is enforced using os.Root (Go 1.24+), which uses openat
// syscalls for atomic path validation — immune to symlink and ".." traversal attacks.
type Sandbox struct {
	roots      []root
	hostPrefix string // when non-empty, enables container symlink resolution
	readOnly   bool   // when true (default), Open rejects any write flags
}

// New creates a sandbox from an allowlist of directory paths. Paths that do
// not exist or cannot be opened are silently skipped — the sandbox operates
// with whatever paths are available at construction time.
//
// Diagnostic messages about skipped paths are collected into warnings. The
// caller is responsible for writing them to the appropriate output stream.
func New(paths []string) (sb *Sandbox, warnings []byte, err error) {
	var buf bytes.Buffer
	roots := make([]root, 0, len(paths))
	for _, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			fmt.Fprintf(&buf, "AllowedPaths: skipping %q: %v\n", p, err)
			continue
		}
		r, err := os.OpenRoot(abs)
		if err != nil {
			// AllowedPaths is a suggestion, not a requirement. If we can't
			// open a path (missing, not a directory, no permission, etc.),
			// skip it and work with whatever paths are available.
			fmt.Fprintf(&buf, "AllowedPaths: skipping %q: %v\n", abs, err)
			continue
		}
		// Record the canonical (symlink-resolved) form of the configured
		// root. os.OpenRoot already follows symlinks at the path itself,
		// so the opened handle observes the target directory; we capture
		// that resolution here so builtins like `pwd -P` can translate
		// the configured-root prefix back to its canonical form.
		// EvalSymlinks failure is not fatal — fall back to the configured
		// path, matching prior behavior.
		canonical, evalErr := filepath.EvalSymlinks(abs)
		if evalErr != nil {
			canonical = abs
		}
		roots = append(roots, root{absPath: abs, canonicalAbsPath: canonical, root: r})
	}
	return &Sandbox{roots: roots, readOnly: true}, buf.Bytes(), nil
}

// isPathEscapeError reports whether err is the unexported "path escapes
// from parent" error from os.Root. Stable per Hyrum's Law.
func isPathEscapeError(err error) bool {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err != nil && pe.Err.Error() == "path escapes from parent"
	}
	return false
}

// maxSymlinkHops is the maximum number of symlink resolutions performed
// when following cross-root symlinks. Prevents infinite loops from
// circular symlinks.
const maxSymlinkHops = 10

// resolve returns the matching root entry and the path relative to it for
// the given absolute path. It returns false if no root matches.
func (s *Sandbox) resolve(absPath string) (*root, string, bool) {
	if s == nil {
		return nil, "", false
	}
	for i := range s.roots {
		rel, err := filepath.Rel(s.roots[i].absPath, absPath)
		if err != nil {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return &s.roots[i], rel, true
	}
	return nil, "", false
}

// isAncestorOfRoot reports whether absPath is a directory prefix of any
// configured sandbox root, without being inside that root itself.
func (s *Sandbox) isAncestorOfRoot(absPath string) bool {
	if s == nil {
		return false
	}
	absPath = filepath.Clean(absPath)
	for i := range s.roots {
		rootPath := filepath.Clean(s.roots[i].absPath)
		if absPath == rootPath {
			continue
		}
		rel, err := filepath.Rel(absPath, rootPath)
		if err != nil {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return true
	}
	return false
}

// resolveRootFollowingSymlinks resolves absPath to a (root, relPath) pair,
// following symlinks that cross between allowed roots. It walks the
// relative path component by component; when a component is a symlink,
// its target is resolved to an absolute path and matched against all
// roots, then resolution continues with the remaining components.
//
// When preserveLast is true, the final path component is not resolved
// even if it is a symlink. This preserves lstat/readlink semantics.
//
// This is only called as a fallback when the primary os.Root operation
// fails, so there is no overhead on the happy path.
func (s *Sandbox) resolveRootFollowingSymlinks(absPath string, preserveLast bool) (*root, string, bool) {
	// Clean trailing slashes so filepath.Dir computes the correct parent
	// when resolving relative symlink targets.
	absPath = filepath.Clean(absPath)
	// N+1 iterations: up to N to resolve symlinks, 1 more to confirm
	// the final path has no more symlinks and return success.
	for range maxSymlinkHops + 1 {
		ar, rel, ok := s.resolve(absPath)
		if !ok {
			return nil, "", false
		}

		// Walk rel component by component looking for symlinks.
		components := strings.Split(rel, string(filepath.Separator))
		symlinkFound := false
		for i := range components {
			if components[i] == "." {
				continue
			}
			// When preserveLast is set, skip the final component so that
			// Lstat and Readlink operate on the symlink itself.
			if preserveLast && i == len(components)-1 {
				break
			}
			partial := strings.Join(components[:i+1], string(filepath.Separator))
			info, err := ar.root.Lstat(partial)
			if err != nil {
				// Component doesn't exist or isn't accessible. It can't
				// be a symlink we need to resolve, so return what we have
				// and let the caller get the real error.
				return ar, rel, true
			}
			if info.Mode()&fs.ModeSymlink == 0 {
				continue
			}
			// Found a symlink — read its target.
			target, err := ar.root.Readlink(partial)
			if err != nil {
				return nil, "", false
			}
			// Resolve target to absolute path.
			if !filepath.IsAbs(target) {
				parentAbs := absPath
				for j := len(components) - 1; j >= i; j-- {
					parentAbs = filepath.Dir(parentAbs)
				}
				target = filepath.Join(parentAbs, target)
			}
			// Append remaining components after the symlink.
			if i+1 < len(components) {
				remaining := strings.Join(components[i+1:], string(filepath.Separator))
				target = filepath.Join(target, remaining)
			}
			absPath = filepath.Clean(target)
			// In containers, host symlinks use host-absolute paths
			// (e.g. /var/log/pods/...) that don't include the mount
			// prefix. Prepend it so the path matches our roots. Skip
			// if the path already starts with the prefix (e.g. a
			// relative symlink that resolved within the same root).
			if s.hostPrefix != "" && !strings.HasPrefix(absPath, s.hostPrefix+string(filepath.Separator)) {
				absPath = filepath.Join(s.hostPrefix, absPath)
			}
			symlinkFound = true
			break
		}
		if !symlinkFound {
			return ar, rel, true
		}
	}
	return nil, "", false // too many hops
}

// resolveFollowingSymlinks is like resolveRootFollowingSymlinks but returns
// the *os.Root handle instead of the internal root entry.
func (s *Sandbox) resolveFollowingSymlinks(absPath string, preserveLast bool) (*os.Root, string, bool) {
	ar, rel, ok := s.resolveRootFollowingSymlinks(absPath, preserveLast)
	if !ok {
		return nil, "", false
	}
	return ar.root, rel, true
}

// openWithSymlinkFallback opens relPath through root, falling back to
// cross-root symlink resolution if the open fails with a path escape.
func (s *Sandbox) openWithSymlinkFallback(root *os.Root, relPath, absPath string) (*os.File, error) {
	f, err := root.Open(relPath)
	if err != nil && isPathEscapeError(err) {
		if r, rel, ok := s.resolveFollowingSymlinks(absPath, false); ok {
			f, err = r.Open(rel)
		}
	}
	return f, err
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
		checkRead := mode&modeRead != 0
		checkWrite := mode&modeWrite != 0
		checkExec := mode&modeExecute != 0

		_, err = ar.accessCheck(rel, checkRead, checkWrite, checkExec)
		if err == nil {
			return nil
		}
		if !isPathEscapeError(err) {
			return &os.PathError{Op: "access", Path: path, Err: os.ErrPermission}
		}
		// Symlink escapes this root — resolve across all roots.
		resolved, resolvedRel, ok := s.resolveRootFollowingSymlinks(absPath, false)
		if !ok {
			return &os.PathError{Op: "access", Path: path, Err: os.ErrPermission}
		}
		_, err = resolved.accessCheck(resolvedRel, checkRead, checkWrite, checkExec)
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

// allowedOpenFlags is the set of os.OpenFile flag bits the sandbox admits.
// These are the minimal flags needed for shell redirections (O_RDONLY for <,
// O_WRONLY|O_CREATE|O_TRUNC for >, O_WRONLY|O_APPEND|O_CREATE for >>).
// Anything outside this mask (O_RDWR, O_EXCL, O_DIRECTORY, O_NOFOLLOW,
// O_SYNC, O_NONBLOCK, etc.) is rejected as a defense-in-depth measure.
const allowedOpenFlags = os.O_RDONLY | os.O_WRONLY |
	os.O_APPEND | os.O_CREATE | os.O_TRUNC

// writeOpenFlags is the subset of allowedOpenFlags that implies a write
// operation. Used to enforce the readOnly mode check.
const writeOpenFlags = os.O_WRONLY | os.O_APPEND | os.O_CREATE | os.O_TRUNC

// Open implements the restricted file-open policy. The file is opened through
// os.Root for atomic path validation, which uses openat under the hood and is
// immune to symlink and ".." traversal between path validation and open.
//
// In the default read-only mode only O_RDONLY opens are accepted; any write
// flag returns ErrPermission. Call SetWritable to enable write opens.
//
// In write-permitted mode, flag bits must be within allowedOpenFlags;
// anything else (unknown platform flags, O_RDWR, O_EXCL, etc.) returns
// ErrPermission.
//
// The cross-root symlink fallback (resolveFollowingSymlinks) is read-only
// regardless of sandbox mode. Following a symlink that escapes its os.Root
// and then performing a write (O_CREATE/O_TRUNC/O_APPEND/O_WRONLY) is the
// classic TOCTOU footgun: a malicious symlink could redirect a create or
// truncate to a target that has changed between resolution and open,
// defeating the sandbox. Writes must stay within a single os.Root.
func (s *Sandbox) Open(path string, cwd string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
	if s == nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
	}
	if s.readOnly && flag&writeOpenFlags != 0 {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
	}
	if flag&^allowedOpenFlags != 0 {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
	}

	absPath := toAbs(path, cwd)

	ar, relPath, ok := s.resolve(absPath)
	if !ok {
		return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrPermission}
	}

	f, err := ar.root.OpenFile(relPath, flag, perm)
	if err == nil {
		return f, nil
	}
	if !isPathEscapeError(err) {
		return nil, PortablePathError(err)
	}
	// Symlink escapes this root. Only fall back across roots for
	// read-only opens — cross-root writes are TOCTOU-unsafe (see the
	// function comment above).
	if flag != os.O_RDONLY {
		return nil, PortablePathError(err)
	}
	r, rel, ok := s.resolveFollowingSymlinks(absPath, false)
	if !ok {
		return nil, PortablePathError(err)
	}
	f, err = r.OpenFile(rel, flag, perm)
	if err != nil {
		return nil, PortablePathError(err)
	}
	return f, nil
}

// Truncate sets the size of the file at path to size bytes. When create is
// true, a missing file is created with the open(2) permissive default
// (0666 & ~umask), matching GNU truncate and bash redirect semantics; the
// process umask is what actually decides the mode. When create is false,
// a missing file returns os.ErrNotExist (the caller, e.g. truncate -c,
// decides whether to treat that as an error or a silent skip).
//
// Like Open, the operation goes through os.Root for atomic openat-based path
// validation. The cross-root symlink fallback is intentionally NOT used:
// resolving a symlink that escapes one root and then writing through the
// resolved path is the classic TOCTOU footgun. Writes must stay within a
// single allowed root.
//
// Non-regular targets (FIFO, socket, char/block device) are rejected by an
// atomic open-and-fstat sequence:
//
//  1. The open includes O_NONBLOCK so that an O_WRONLY open of a FIFO with
//     no reader returns ENXIO immediately instead of blocking the shell
//     waiting for a connection. (O_NONBLOCK is benign on regular files —
//     it sets the fd's status flag but does not change open semantics —
//     and is a no-op on platforms where the constant is zero, e.g. Windows.)
//  2. After a successful open, fstat on the returned fd verifies the file
//     is regular before any ftruncate runs. This closes the TOCTOU window
//     that a pre-open Stat would have left open: even if a regular file
//     is swapped for a FIFO between path resolution and the open syscall,
//     the resulting fd is rejected before the size change reaches the
//     kernel.
//
// Negative sizes are rejected with EINVAL. Sizes within int64 range are
// passed through to the kernel; the kernel/filesystem rejects values it
// cannot represent (e.g. exceeding the filesystem's maximum file size).
func (s *Sandbox) Truncate(path string, cwd string, size int64, create bool) error {
	if size < 0 {
		return &os.PathError{Op: "truncate", Path: path, Err: syscall.EINVAL}
	}

	absPath := toAbs(path, cwd)

	ar, relPath, ok := s.resolve(absPath)
	if !ok {
		return &os.PathError{Op: "truncate", Path: path, Err: os.ErrPermission}
	}

	// O_NONBLOCK is hardcoded here (not derived from user input) so it does
	// not need to pass through the allowedOpenFlags mask that Sandbox.Open
	// enforces for caller-supplied flags. The mask exists to prevent users
	// from sneaking in flags like O_NONBLOCK or O_SYNC; here we are the ones
	// setting it intentionally for the FIFO-blocking prevention described
	// in the method doc above.
	flag := os.O_WRONLY | syscall.O_NONBLOCK
	if create {
		flag |= os.O_CREATE
	}
	// 0666 lets the process umask determine the final mode (open(2) applies
	// mode & ~umask). This matches GNU truncate and bash >FILE behaviour.
	f, err := ar.root.OpenFile(relPath, flag, 0666)
	if err != nil {
		// Return the raw error so callers can use errors.Is against
		// fs.ErrNotExist / fs.ErrPermission. Wrapping would hide
		// os.ErrNotExist behind a fresh errors.New value, breaking the
		// truncate -c silent-skip path.
		return err
	}
	// fstat the fd we actually opened (not the path) so a swap between
	// path resolution and open is caught before ftruncate runs.
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return &os.PathError{Op: "truncate", Path: path, Err: errors.New("not a regular file")}
	}
	truncErr := f.Truncate(size)
	// Surface a deferred Close error only when Truncate itself succeeded;
	// a failed Close after a successful ftruncate is the only case where a
	// Close error reflects user-visible data loss (flush failure on write-back).
	closeErr := f.Close()
	if truncErr != nil {
		return truncErr
	}
	return closeErr
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

	ar, relPath, ok := s.resolve(absPath)
	if !ok {
		if maxEntries > 0 && s.isAncestorOfRoot(absPath) {
			// Absolute glob expansion walks non-meta path components with
			// ReadDirForGlob before it reaches the directory containing the
			// metacharacters. Let it traverse harmless ancestors of allowed
			// roots, but do not expose their entries.
			return nil, nil
		}
		return nil, &os.PathError{Op: "readdir", Path: path, Err: os.ErrPermission}
	}

	f, err := s.openWithSymlinkFallback(ar.root, relPath, absPath)
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

	ar, relPath, ok := s.resolve(absPath)
	if !ok {
		return nil, &os.PathError{Op: "opendir", Path: path, Err: os.ErrPermission}
	}

	f, err := s.openWithSymlinkFallback(ar.root, relPath, absPath)
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

	ar, relPath, ok := s.resolve(absPath)
	if !ok {
		return false, &os.PathError{Op: "readdir", Path: path, Err: os.ErrPermission}
	}

	f, err := s.openWithSymlinkFallback(ar.root, relPath, absPath)
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
	ar, relPath, ok := s.resolve(absPath)
	if !ok {
		return nil, false, &os.PathError{Op: "readdir", Path: path, Err: os.ErrPermission}
	}
	f, err := s.openWithSymlinkFallback(ar.root, relPath, absPath)
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

	ar, relPath, ok := s.resolve(absPath)
	if !ok {
		return nil, &os.PathError{Op: "stat", Path: path, Err: os.ErrPermission}
	}

	info, err := ar.root.Stat(relPath)
	if err == nil {
		return info, nil
	}
	if !isPathEscapeError(err) {
		return nil, PortablePathError(err)
	}
	r, rel, ok := s.resolveFollowingSymlinks(absPath, false)
	if !ok {
		return nil, PortablePathError(err)
	}
	info, err = r.Stat(rel)
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

	ar, relPath, ok := s.resolve(absPath)
	if !ok {
		return nil, &os.PathError{Op: "lstat", Path: path, Err: os.ErrPermission}
	}

	info, err := ar.root.Lstat(relPath)
	if err == nil {
		return info, nil
	}
	if !isPathEscapeError(err) {
		return nil, PortablePathError(err)
	}
	r, rel, ok := s.resolveFollowingSymlinks(absPath, true)
	if !ok {
		return nil, PortablePathError(err)
	}
	info, err = r.Lstat(rel)
	if err != nil {
		return nil, PortablePathError(err)
	}
	return info, nil
}

// Readlink returns the destination of a symbolic link within the sandbox.
func (s *Sandbox) Readlink(path string, cwd string) (string, error) {
	absPath := toAbs(path, cwd)

	ar, relPath, ok := s.resolve(absPath)
	if !ok {
		return "", &os.PathError{Op: "readlink", Path: path, Err: os.ErrPermission}
	}

	target, err := ar.root.Readlink(relPath)
	if err == nil {
		return target, nil
	}
	if !isPathEscapeError(err) {
		return "", PortablePathError(err)
	}
	r, rel, ok := s.resolveFollowingSymlinks(absPath, true)
	if !ok {
		return "", PortablePathError(err)
	}
	target, err = r.Readlink(rel)
	if err != nil {
		return "", PortablePathError(err)
	}
	return target, nil
}

// SetHostPrefix overrides the mount prefix used to translate host-absolute
// symlink targets inside containers.
func (s *Sandbox) SetHostPrefix(prefix string) {
	s.hostPrefix = filepath.Clean(prefix)
}

// HostPrefix returns the current host mount prefix.
func (s *Sandbox) HostPrefix() string {
	return s.hostPrefix
}

// SetWritable switches the sandbox from its default read-only mode into
// write-permitted mode. In write-permitted mode, Open accepts write flags
// (O_WRONLY, O_APPEND, O_CREATE, O_TRUNC) in addition to O_RDONLY, as long
// as the target path is within the configured allowlist.
//
// This is called by the interpreter when RemediationMode is active. The
// default (read-only) enforces that even if a code path mistakenly passes
// write flags to Open, the sandbox rejects them — defense-in-depth on top
// of the interpreter-level redirection guard.
func (s *Sandbox) SetWritable() {
	s.readOnly = false
}

// CanonicalizeRootPrefix returns absPath with any matching sandbox-root
// prefix replaced by that root's canonical (symlink-resolved) form. If
// absPath is outside every root, or its containing root is not a
// symlink, the input is returned unchanged.
//
// Use case: builtins like `pwd -P` walk symlinks within the sandbox
// via callCtx.LstatFile/ReadlinkFile, but the *root itself* may be a
// symlink (e.g. AllowedPaths=/tmp/link with /tmp/link -> /tmp/real).
// os.OpenRoot follows the root symlink at open time, so per-component
// LstatFile cannot detect it. This helper applies the missing
// translation by mapping the configured-root prefix to the canonical
// one captured at New() time.
func (s *Sandbox) CanonicalizeRootPrefix(absPath string) string {
	if s == nil {
		return absPath
	}
	for i := range s.roots {
		r := &s.roots[i]
		if r.canonicalAbsPath == "" || r.canonicalAbsPath == r.absPath {
			continue
		}
		rel, err := filepath.Rel(r.absPath, absPath)
		if err != nil {
			continue
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if rel == "." {
			return r.canonicalAbsPath
		}
		return filepath.Join(r.canonicalAbsPath, rel)
	}
	return absPath
}

// Paths returns the resolved absolute paths of all allowed directories.
func (s *Sandbox) Paths() []string {
	if s == nil {
		return nil
	}
	paths := make([]string, len(s.roots))
	for i, r := range s.roots {
		paths[i] = r.absPath
	}
	return paths
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
