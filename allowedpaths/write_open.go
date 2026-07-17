// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedpaths

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/rshell/allowedpaths/internal/writeopen"
)

func openWriteRoot(root *os.Root) (*os.File, error) {
	return writeopen.OpenRoot(root)
}

func closeWriteRoot(file *os.File) {
	writeopen.CloseRoot(file)
}

func (r *root) openWriteFile(relPath string, flag int, perm os.FileMode) (*os.File, error) {
	if err := r.rejectSymlinkWriteTarget(relPath); err != nil {
		return nil, err
	}
	return writeopen.OpenFile(r.writeRoot, r.root, relPath, flag, perm)
}

// removeFile unlinks relPath through an atomic, no-follow openat walk (see
// writeopen.Unlink) rather than a separate Lstat precheck followed by a
// path-based os.Root.Remove call, closing the TOCTOU window where an
// intermediate directory component could be swapped for a symlink between
// the check and the removal. The rejectSymlinkPathPrefix precheck is kept as
// defense in depth on top of the atomic walk, mirroring openWriteFile above.
func (r *root) removeFile(relPath string) error {
	if err := r.rejectSymlinkPathPrefix(relPath); err != nil {
		return err
	}
	return writeopen.Unlink(r.writeRoot, r.root, relPath)
}

func (r *root) rejectSymlinkWriteTarget(relPath string) error {
	return r.rejectSymlinkPathComponents(relPath, true)
}

// rejectSymlinkPathPrefix rejects paths whose intermediate directory
// components are symlinks, but allows the final component itself to be a
// symlink. Used by Remove: unlink(2) semantics delete the symlink named by
// the final component without following it, so a symlink is a legitimate rm
// target — only a symlinked *directory* earlier in the path is a sandbox
// escape risk.
func (r *root) rejectSymlinkPathPrefix(relPath string) error {
	return r.rejectSymlinkPathComponents(relPath, false)
}

// rejectSymlinkPathComponents walks relPath component by component and
// rejects the first symlink found. When includeLast is true (write-open
// targets), the final component is checked too, since writing through a
// symlink would redirect the write outside the resolved root. When false
// (remove targets), the final component is skipped since removing a
// symlink is expected to remove the link itself, not its referent.
func (r *root) rejectSymlinkPathComponents(relPath string, includeLast bool) error {
	clean := filepath.Clean(relPath)
	components := strings.Split(clean, string(filepath.Separator))
	var partial string
	for i, component := range components {
		if component == "" || component == "." {
			continue
		}
		if partial == "" {
			partial = component
		} else {
			partial = filepath.Join(partial, component)
		}
		if !includeLast && i == len(components)-1 {
			break
		}
		info, err := r.root.Lstat(partial)
		if err != nil {
			return nil
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return &os.PathError{Op: "open", Path: relPath, Err: writeopen.ErrSymlinkWriteTarget}
		}
	}
	return nil
}
