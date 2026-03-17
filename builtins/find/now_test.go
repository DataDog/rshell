// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package find

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins"
)

// TestNowConsistentAcrossRoots verifies that find uses a single consistent
// timestamp across all root paths when evaluating time predicates like
// -mmin, matching GNU find behaviour.
func TestNowConsistentAcrossRoots(t *testing.T) {
	// Create two directories with one file each.
	tmp := t.TempDir()
	dir1 := filepath.Join(tmp, "a")
	dir2 := filepath.Join(tmp, "b")
	require.NoError(t, os.MkdirAll(dir1, 0755))
	require.NoError(t, os.MkdirAll(dir2, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "f1.txt"), []byte("x"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "f2.txt"), []byte("y"), 0644))

	var stdout, stderr bytes.Buffer
	callCtx := &builtins.CallContext{
		Stdout: &stdout,
		Stderr: &stderr,
		Now:    time.Now(),
		LstatFile: func(_ context.Context, path string) (fs.FileInfo, error) {
			return os.Lstat(filepath.Join(tmp, path))
		},
		StatFile: func(_ context.Context, path string) (fs.FileInfo, error) {
			return os.Stat(filepath.Join(tmp, path))
		},
		OpenDir: func(_ context.Context, path string) (fs.ReadDirFile, error) {
			return os.Open(filepath.Join(tmp, path))
		},
		IsDirEmpty: func(_ context.Context, path string) (bool, error) {
			entries, err := os.ReadDir(filepath.Join(tmp, path))
			if err != nil {
				return false, err
			}
			return len(entries) == 0, nil
		},
		PortableErr: func(err error) string {
			return err.Error()
		},
	}

	// Run find with two root paths and a time predicate.
	result := run(context.Background(), callCtx, []string{"a", "b", "-mmin", "-60"})

	assert.Equal(t, uint8(0), result.Code, "find should succeed")
	assert.Contains(t, stdout.String(), "f1.txt")
	assert.Contains(t, stdout.String(), "f2.txt")
}
