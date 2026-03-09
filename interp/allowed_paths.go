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

// newPathSandbox validates paths and creates a pathSandbox without opening
// os.Root handles. Call [pathSandbox.openRoots] to activate the sandbox.
func newPathSandbox(paths []string) (*pathSandbox, error) {
	roots := make([]allowedRoot, len(paths))
	for i, p := range paths {
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, fmt.Errorf("AllowedPaths: cannot resolve %q: %w", p, err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("AllowedPaths: cannot stat %q: %w", abs, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("AllowedPaths: %q is not a directory", abs)
		}
		roots[i] = allowedRoot{absPath: abs}
	}
	return &pathSandbox{roots: roots}, nil
}

// openRoots opens os.Root handles for every allowed path. It is a no-op if
// the handles are already open.
func (s *pathSandbox) openRoots() error {
	if s == nil || len(s.roots) == 0 || s.roots[0].root != nil {
		return nil
	}
	for i := range s.roots {
		root, err := os.OpenRoot(s.roots[i].absPath)
		if err != nil {
			for _, prev := range s.roots[:i] {
				prev.root.Close()
			}
			return fmt.Errorf("AllowedPaths: cannot open root %q: %w", s.roots[i].absPath, err)
		}
		s.roots[i].root = root
	}
	return nil
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

// toAbs resolves path against cwd when it is not already absolute.
func toAbs(path, cwd string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cwd, path)
}

// open implements the restricted file-open policy. The file is opened through
// os.Root for atomic path validation.
func (s *pathSandbox) open(ctx context.Context, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
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
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	// os.Root's ReadDir does not guarantee sorted order like os.ReadDir.
	// Sort to match POSIX glob expansion expectations.
	slices.SortFunc(entries, func(a, b fs.DirEntry) int {
		if a.Name() < b.Name() {
			return -1
		}
		if a.Name() > b.Name() {
			return 1
		}
		return 0
	})
	return entries, nil
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

