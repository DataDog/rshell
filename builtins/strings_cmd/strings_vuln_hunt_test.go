// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Blocked-attack regression tests added by the vuln-hunt campaign
// 2026-05-19-gpt-5.5-cyber-2 (target: strings_cmd).
package strings_cmd_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

func runVulnHuntStrings(t *testing.T, script, dir string, allowedPaths ...string) (string, string, int) {
	t.Helper()
	if len(allowedPaths) == 0 {
		allowedPaths = []string{dir}
	}
	return testutil.RunScript(t, script, dir, interp.AllowedPaths(allowedPaths))
}

func writeVulnHuntStringsFile(t *testing.T, dir, name string, content []byte) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), content, 0644))
}

func TestVulnHuntBuiltinFlagDrivenExploit_UnsupportedFlagsRejected(t *testing.T) {
	dir := t.TempDir()
	writeVulnHuntStringsFile(t, dir, "data.bin", []byte("visible\x00"))

	for _, flag := range []string{"--encoding=s", "--target=binary", "--include-all-whitespace"} {
		_, stderr, code := runVulnHuntStrings(t, "strings "+flag+" data.bin", dir)
		assert.Equal(t, 1, code, "flag: %s", flag)
		assert.Contains(t, stderr, "strings:", "flag: %s", flag)
	}
}

func TestVulnHuntBuiltinFlagDrivenExploit_OutputSeparatorControlBytes(t *testing.T) {
	dir := t.TempDir()
	writeVulnHuntStringsFile(t, dir, "data.bin", []byte("alpha\x00bravo\x00"))

	stdout, stderr, code := runVulnHuntStrings(t,
		`strings -s $'\nNEXT\n' data.bin`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "alpha\nNEXT\nbravo\nNEXT\n", stdout)
}

func TestVulnHuntBuiltinFlagDrivenExploit_PrintFileNameWithSpaces(t *testing.T) {
	dir := t.TempDir()
	name := "name with spaces.bin"
	writeVulnHuntStringsFile(t, dir, name, []byte("alpha\x00"))

	stdout, stderr, code := runVulnHuntStrings(t,
		"strings -f "+strconv.Quote(name), dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, name+": alpha\n", stdout)
}

func TestVulnHuntBuiltinFileAccessBypass_SymlinkOutsideSandbox(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks are restricted on Windows")
	}
	allowed := t.TempDir()
	secret := t.TempDir()
	secretPath := filepath.Join(secret, "secret.txt")
	require.NoError(t, os.WriteFile(secretPath, []byte("S3CR3T\n"), 0644))
	require.NoError(t, os.Symlink(secretPath, filepath.Join(allowed, "link")))

	stdout, stderr, code := runVulnHuntStrings(t, "strings link", allowed)
	assert.Equal(t, 1, code)
	assert.NotContains(t, stdout, "S3CR3T")
	assert.Contains(t, stderr, "strings:")
}

func TestVulnHuntBuiltinIntegerOverflow_MinLenBoundsRejected(t *testing.T) {
	dir := t.TempDir()
	writeVulnHuntStringsFile(t, dir, "data.bin", []byte("visible\x00"))

	for _, script := range []string{
		"strings -n -1 data.bin",
		"strings -n 0 data.bin",
		"strings -n 2147483648 data.bin",
		"strings --bytes=999999999999999999999 data.bin",
	} {
		_, stderr, code := runVulnHuntStrings(t, script, dir)
		assert.Equal(t, 1, code, "script: %s", script)
		assert.Contains(t, stderr, "strings:", "script: %s", script)
	}
}

func TestVulnHuntBuiltinIntegerOverflow_MaxIntMinLenDoesNotAllocate(t *testing.T) {
	dir := t.TempDir()
	writeVulnHuntStringsFile(t, dir, "data.bin", []byte("visible\x00"))

	stdout, stderr, code := runVulnHuntStrings(t, "strings -n 2147483647 data.bin", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "", stdout)
}

func TestVulnHuntBuiltinSpecialFiles_DevNullNoOutput(t *testing.T) {
	if os.DevNull == "NUL" {
		t.Skip("platform null device is a reserved filename on Windows")
	}
	dir := t.TempDir()

	stdout, stderr, code := runVulnHuntStrings(t,
		"strings "+os.DevNull, dir, filepath.Dir(os.DevNull))
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "", stdout)
}

func TestVulnHuntBuiltinSpecialFiles_DevZeroHonorsCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/zero on Windows")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, _, _ = testutil.RunScriptCtx(ctx, t,
		"strings /dev/zero", dir, interp.AllowedPaths([]string{"/dev"}))
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
}
