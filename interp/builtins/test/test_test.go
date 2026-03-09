// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package test_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
)

// runScript runs a shell script and returns stdout, stderr, and the exit code.
func runScript(t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(script), "")
	require.NoError(t, err)

	var outBuf, errBuf bytes.Buffer
	allOpts := append([]interp.RunnerOption{interp.StdIO(nil, &outBuf, &errBuf)}, opts...)
	runner, err := interp.New(allOpts...)
	require.NoError(t, err)
	defer runner.Close()

	if dir != "" {
		runner.Dir = dir
	}

	err = runner.Run(context.Background(), prog)
	exitCode := 0
	if err != nil {
		var es interp.ExitStatus
		if errors.As(err, &es) {
			exitCode = int(es)
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// cmdRun runs a test command with AllowedPaths set to dir.
func cmdRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return runScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

// writeFile creates a file in dir with the given content.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
}

// Tests below require Go-specific setup that cannot be expressed in YAML scenarios.
// All other test builtin tests live in tests/scenarios/cmd/test/.

func TestTestFileNewerThan(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "old.txt", "old")
	past := time.Now().Add(-2 * time.Second)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "old.txt"), past, past))
	writeFile(t, dir, "new.txt", "new")
	stdout, _, code := cmdRun(t, `test new.txt -nt old.txt && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestFileOlderThan(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "old.txt", "old")
	past := time.Now().Add(-2 * time.Second)
	require.NoError(t, os.Chtimes(filepath.Join(dir, "old.txt"), past, past))
	writeFile(t, dir, "new.txt", "new")
	stdout, _, code := cmdRun(t, `test old.txt -ot new.txt && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestFileSameFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello")
	stdout, _, code := cmdRun(t, `test file.txt -ef file.txt && echo yes`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestTestFileDifferentFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "hello")
	writeFile(t, dir, "b.txt", "hello")
	_, _, code := cmdRun(t, `test a.txt -ef b.txt`, dir)
	assert.Equal(t, 1, code)
}

func TestTestOutsideAllowedPaths(t *testing.T) {
	allowed := t.TempDir()
	secret := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(secret, "secret.txt"), []byte("secret"), 0644))
	secretPath := strings.ReplaceAll(filepath.Join(secret, "secret.txt"), `\`, `/`)
	// test -e should return false for files outside allowed paths.
	_, _, code := runScript(t, "test -e "+secretPath, allowed, interp.AllowedPaths([]string{allowed}))
	assert.Equal(t, 1, code)
}
