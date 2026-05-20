// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt 2026-05-20-gpt-5.5-cyber-3 (target: sort)

package sort_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rsort "github.com/DataDog/rshell/builtins/sort"
	"github.com/DataDog/rshell/interp"
)

func TestVulnHuntBuiltinSort_CumulativeByteCapAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	line := []byte(strings.Repeat("a", 1000) + "\n")
	perFileLines := rsort.MaxTotalBytes/2000 + 8
	content := bytes.Repeat(line, perFileLines)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), content, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), content, 0o644))

	mustNotHang(t, func() {
		stdout, stderr, code := sortRun(t, "sort a.txt b.txt", dir)
		assert.Equal(t, 1, code)
		assert.Empty(t, stdout)
		assert.Contains(t, stderr, "sort:")
		assert.Contains(t, stderr, "exceeds maximum")
		assert.Contains(t, stderr, "5 MiB")
	})
}

func TestVulnHuntBuiltinSort_KeyNumericOverflowRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "b 2\na 1\n")

	for _, tc := range []struct {
		name       string
		script     string
		wantStderr string
	}{
		{
			name:       "field overflow",
			script:     "sort -k 999999999999999999999999999999 f.txt",
			wantStderr: "invalid field number in key",
		},
		{
			name:       "character overflow",
			script:     "sort -k 1.999999999999999999999999999999 f.txt",
			wantStderr: "invalid character position in key",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := sortRun(t, tc.script, dir)
			assert.Equal(t, 2, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "sort:")
			assert.Contains(t, stderr, tc.wantStderr)
		})
	}
}

func TestVulnHuntBuiltinSort_UnsafeFlagsHelpShortCircuitNoSideEffects(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "b\na\n")

	for _, script := range []string{
		"sort --help --output=out.txt f.txt",
		"sort --help --temporary-directory=. f.txt",
		"sort --help --compress-program=sh f.txt",
	} {
		stdout, stderr, code := sortRun(t, script, dir)
		assert.Equal(t, 0, code, script)
		assert.Contains(t, stdout, "Usage: sort")
		assert.Empty(t, stderr)
	}

	_, err := os.Stat(filepath.Join(dir, "out.txt"))
	assert.True(t, os.IsNotExist(err))
}

func TestVulnHuntBuiltinSort_TempDirShortFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "b\na\n")

	stdout, stderr, code := sortRun(t, "sort -T . f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "sort:")
}

func TestVulnHuntBuiltinSort_CheckSilentSuppressesRawDisorderLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "z\nA\rFORGED_SORT_ROW=1\n")

	stdout, stderr, code := sortRun(t, "sort -C f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinSort_PreCanceledContextProducesNoOutput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "b\na\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stdout, stderr, _ := runScriptCtx(ctx, t, "sort f.txt", dir, interp.AllowedPaths([]string{dir}))
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}
