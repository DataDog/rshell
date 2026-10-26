// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package allowedpaths

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveAllowedPathModeNonWindowsStripsMissingSuffixPath(t *testing.T) {
	base := filepath.Join(t.TempDir(), "policy")

	path, mode := resolveAllowedPathMode(base + ":rw")
	assert.Equal(t, base, path)
	assert.Equal(t, pathModeReadWrite, mode)

	path, mode = resolveAllowedPathMode(base + ":ro")
	assert.Equal(t, base, path)
	assert.Equal(t, pathModeReadOnly, mode)
}

func TestResolveAllowedPathModeNonWindowsPreservesExistingLiteralRWSuffixPath(t *testing.T) {
	literal := filepath.Join(t.TempDir(), "policy:rw")
	require.NoError(t, os.Mkdir(literal, 0755))

	path, mode := resolveAllowedPathMode(literal)
	assert.Equal(t, literal, path)
	assert.Equal(t, pathModeReadOnly, mode)
}

func TestResolveAllowedPathModeNonWindowsAlwaysStripsROSuffix(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "policy")

	for name, create := range map[string]func(string) error{
		"file":    func(p string) error { return os.WriteFile(p, nil, 0644) },
		"dir":     func(p string) error { return os.Mkdir(p, 0755) },
		"symlink": func(p string) error { return os.Symlink(dir, p) },
	} {
		t.Run(name, func(t *testing.T) {
			literal := base + ":ro"
			require.NoError(t, create(literal))
			t.Cleanup(func() { _ = os.RemoveAll(literal) })

			path, mode := resolveAllowedPathMode(literal)
			assert.Equal(t, base, path)
			assert.Equal(t, pathModeReadOnly, mode)
		})
	}
}

// A script with write access to a read-write root must not be able to drop a
// nested read-only root from later sandboxes by creating "<child>:ro" in it.
func TestNestedReadOnlyRootSurvivesLiteralROSuffixEntry(t *testing.T) {
	for name, create := range map[string]func(string) error{
		"file": func(p string) error { return os.WriteFile(p, []byte("x"), 0644) },
		"dir":  func(p string) error { return os.Mkdir(p, 0755) },
	} {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			child := filepath.Join(parent, "protected")
			require.NoError(t, os.Mkdir(child, 0755))
			config := filepath.Join(child, "config")
			key := filepath.Join(child, "key")
			require.NoError(t, os.WriteFile(config, []byte("config"), 0644))
			require.NoError(t, os.WriteFile(key, []byte("key"), 0644))
			require.NoError(t, create(child+":ro"))

			sb, warnings, err := New([]string{parent + ":rw", child + ":ro"})
			require.NoError(t, err)
			defer sb.Close()
			sb.SetWritable()
			assert.Empty(t, string(warnings))
			assert.Equal(t, []string{parent, child}, sb.Paths())

			root, _, ok := sb.resolve(config)
			require.True(t, ok)
			assert.Equal(t, child, root.absPath)
			assert.Equal(t, pathModeReadOnly, root.mode)

			f, err := sb.Open(config, "/", os.O_WRONLY|os.O_TRUNC, 0)
			assert.Nil(t, f)
			assert.ErrorIs(t, err, os.ErrPermission)
			assert.ErrorIs(t, sb.Remove(key, "/"), os.ErrPermission)

			// Reads under the read-only root and writes to the parent still work.
			f, err = sb.Open(config, "/", os.O_RDONLY, 0)
			require.NoError(t, err)
			require.NoError(t, f.Close())
			f, err = sb.Open(filepath.Join(parent, "parent.txt"), "/", os.O_WRONLY|os.O_CREATE, 0644)
			require.NoError(t, err)
			require.NoError(t, f.Close())

			got, err := os.ReadFile(config)
			require.NoError(t, err)
			assert.Equal(t, "config", string(got))
			_, err = os.Stat(key)
			assert.NoError(t, err)
		})
	}
}

func TestResolveAllowedPathModeNonWindowsPreservesPathWhenLstatCannotProveAbsence(t *testing.T) {
	pathWithInvalidName := "policy\x00:rw"

	path, mode := resolveAllowedPathMode(pathWithInvalidName)
	assert.Equal(t, pathWithInvalidName, path)
	assert.Equal(t, pathModeReadOnly, mode)
}
