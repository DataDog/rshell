// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package systemd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const maxTargetSymlinkHops = 40

func (c *Client) lstatTargetPath(path string) (fs.FileInfo, error) {
	if c.target.root == "" {
		return os.Lstat(path)
	}
	root, relative, err := c.openRootedTargetPath(path, false)
	if err != nil {
		return nil, err
	}
	info, operationErr := root.Lstat(relative)
	return info, combineTargetPathErrors(operationErr, c.closeTargetRoot(root))
}

func (c *Client) openTargetFile(path string, followFinalSymlink bool) (*os.File, error) {
	if c.target.root == "" {
		return os.Open(path)
	}
	root, relative, err := c.openRootedTargetPath(path, followFinalSymlink)
	if err != nil {
		return nil, err
	}
	file, operationErr := root.Open(relative)
	closeErr := c.closeTargetRoot(root)
	if operationErr != nil {
		return nil, combineTargetPathErrors(operationErr, closeErr)
	}
	if closeErr != nil {
		return nil, errors.Join(closeErr, file.Close())
	}
	return file, nil
}

func (c *Client) openTargetDirectory(path string) (*os.Root, error) {
	if c.target.root == "" {
		return os.OpenRoot(path)
	}
	root, relative, err := c.openRootedTargetPath(path, true)
	if err != nil {
		return nil, err
	}
	directory, operationErr := root.OpenRoot(relative)
	closeErr := c.closeTargetRoot(root)
	if operationErr != nil {
		return nil, combineTargetPathErrors(operationErr, closeErr)
	}
	if closeErr != nil {
		return nil, errors.Join(closeErr, directory.Close())
	}
	return directory, nil
}

func (c *Client) openRootedTargetPath(path string, followFinalSymlink bool) (*os.Root, string, error) {
	relative, err := filepath.Rel(c.target.root, path)
	if err != nil || !validRootRelativePath(relative) {
		return nil, "", fmt.Errorf("systemd target path %q is outside target root %q", path, c.target.root)
	}

	root, err := os.OpenRoot(c.target.root)
	if err != nil {
		return nil, "", fmt.Errorf("open systemd target root %q: %w", c.target.root, err)
	}
	resolved, resolveErr := resolveRootedPath(root, relative, followFinalSymlink)
	if resolveErr != nil {
		return nil, "", combineTargetPathErrors(resolveErr, c.closeTargetRoot(root))
	}
	return root, resolved, nil
}

func (c *Client) closeTargetRoot(root *os.Root) error {
	if err := root.Close(); err != nil {
		return fmt.Errorf("close systemd target root %q: %w", c.target.root, err)
	}
	return nil
}

func combineTargetPathErrors(operationErr, closeErr error) error {
	if operationErr == nil {
		return closeErr
	}
	if closeErr == nil {
		return operationErr
	}
	return errors.Join(operationErr, closeErr)
}

func resolveRootedPath(root *os.Root, path string, followFinalSymlink bool) (string, error) {
	path = filepath.Clean(path)
	if !validRootRelativePath(path) {
		return "", fmt.Errorf("systemd target path escapes the configured root")
	}

	// Resolve one link per pass so host-absolute targets can be rebased under root.
	for range maxTargetSymlinkHops + 1 {
		components := strings.Split(path, string(filepath.Separator))
		partial := "."
		followed := false
		for index, component := range components {
			if component == "" || component == "." {
				continue
			}
			partial = filepath.Join(partial, component)
			info, err := root.Lstat(partial)
			if err != nil {
				return "", err
			}
			if info.Mode()&fs.ModeSymlink == 0 || (!followFinalSymlink && index == len(components)-1) {
				continue
			}

			target, err := root.Readlink(partial)
			if err != nil {
				return "", err
			}
			if filepath.IsAbs(target) {
				target = rootRelativeAbsolutePath(target)
			} else {
				target = filepath.Join(filepath.Dir(partial), target)
			}
			if index+1 < len(components) {
				target = filepath.Join(target, filepath.Join(components[index+1:]...))
			}
			path = filepath.Clean(target)
			if !validRootRelativePath(path) {
				return "", fmt.Errorf("symbolic link escapes the systemd target root")
			}
			followed = true
			break
		}
		if !followed {
			return path, nil
		}
	}
	return "", fmt.Errorf("systemd target path has too many symbolic links")
}

func rootRelativeAbsolutePath(path string) string {
	path = strings.TrimPrefix(path, filepath.VolumeName(path))
	path = strings.TrimLeft(path, `/\`)
	if path == "" {
		return "."
	}
	return path
}

func validRootRelativePath(path string) bool {
	path = filepath.Clean(path)
	return filepath.VolumeName(path) == "" && !filepath.IsAbs(path) && path != ".." && !strings.HasPrefix(path, ".."+string(filepath.Separator))
}
