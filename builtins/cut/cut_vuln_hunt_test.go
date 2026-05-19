// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Blocked-attack regression tests added by the vuln-hunt campaign
// 2026-05-18-initial-audit (target: cut).
// Additional coverage: 2026-05-19-gpt-5.5-cyber-2.
package cut_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

// H2: a symlink inside AllowedPaths that points outside the sandbox must not
// reveal the target's content. allowedpaths uses os.Root which refuses to
// follow such symlinks.
func TestVulnHuntBuiltinFileAccessBypass_SymlinkOutsideSandbox(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks are restricted on Windows")
	}
	allowed := t.TempDir()
	parent := filepath.Dir(allowed)
	require.NoError(t, os.WriteFile(filepath.Join(parent, "secret-out.txt"),
		[]byte("S3CR3T\n"), 0644))
	t.Cleanup(func() { _ = os.Remove(filepath.Join(parent, "secret-out.txt")) })
	require.NoError(t, os.Symlink(filepath.Join(parent, "secret-out.txt"),
		filepath.Join(allowed, "link")))

	stdout, stderr, code := testutil.RunScript(t, "cut -c1- link", allowed,
		interp.AllowedPaths([]string{allowed}))
	assert.NotEqual(t, 0, code, "symlink-out attack must fail")
	assert.NotContains(t, stdout, "S3CR3T")
	assert.NotEmpty(t, stderr)
}

// H13: direct absolute file operands outside AllowedPaths must not be readable
// through cut's normal file-open path.
func TestVulnHuntBuiltinFileAccessBypass_AbsolutePathOutsideSandbox(t *testing.T) {
	allowed := t.TempDir()
	secret := t.TempDir()
	secretPath := filepath.Join(secret, "secret.txt")
	require.NoError(t, os.WriteFile(secretPath, []byte("S3CR3T\n"), 0644))

	stdout, stderr, code := testutil.RunScript(t,
		"cut -b1- "+strconv.Quote(secretPath), allowed,
		interp.AllowedPaths([]string{allowed}))
	assert.NotEqual(t, 0, code, "absolute outside path must fail")
	assert.NotContains(t, stdout, "S3CR3T")
	assert.Contains(t, stderr, "cut:")
}

// H3: --output-delimiter accepts arbitrary strings. Newlines / NUL bytes /
// ANSI escapes inside the delimiter pass through to stdout verbatim, but this
// stays inside the 1MB output cap and therefore inside the sandbox.
// Regression: confirm the value is forwarded as-is (so callers can't smuggle
// metacharacters back into a re-parsing shell — but the rshell child never
// re-parses cut output, so the boundary is observational only).
func TestVulnHuntBuiltinFlagDrivenExploit_OutputDelimiterControlBytes(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "in.txt"),
		[]byte("a:b:c\n"), 0644))
	// Embed a newline in the delimiter via $'\n'. The interpreter should
	// honour the ANSI-C quoting, and the bytes should appear in stdout
	// between fields.
	stdout, _, code := testutil.RunScript(t,
		"cut -d: --output-delimiter=$'\\n' -f1,3 in.txt", dir,
		interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nc\n", stdout)
}

// H5: a line larger than MaxLineBytes (1 MiB) is rejected by the line scanner
// with a non-zero exit and an explanatory stderr message.
func TestVulnHuntBuiltinResourceExhaustion_LineExceedsMaxLineBytes(t *testing.T) {
	dir := t.TempDir()
	body := make([]byte, (1<<20)+128) // > 1 MiB, no newline yet
	for i := range body {
		body[i] = 'a'
	}
	body = append(body, '\n')
	require.NoError(t, os.WriteFile(filepath.Join(dir, "huge.txt"), body, 0644))

	_, stderr, code := testutil.RunScript(t, "cut -c1-5 huge.txt > /dev/null", dir,
		interp.AllowedPaths([]string{dir}))
	assert.NotEqual(t, 0, code, "over-cap line must produce an error")
	assert.True(t,
		strings.Contains(stderr, "cut:") || strings.Contains(stderr, "too long"),
		"stderr should report the cap, got %q", stderr)
}

// H8: -b range with a value that overflows strconv.Atoi must be rejected.
func TestVulnHuntBuiltinIntegerOverflow_RangeAtoiOverflow(t *testing.T) {
	dir := t.TempDir()
	cases := []string{
		"99999999999999999999",
		"99999999999999999999999",
		"-b99999999999999999999",
	}
	for _, val := range cases {
		t.Run(val, func(t *testing.T) {
			_, stderr, code := testutil.RunScript(t,
				"echo abc | cut -b"+val, dir, interp.AllowedPaths([]string{dir}))
			assert.Equal(t, 1, code, "overflow value must exit 1")
			assert.Contains(t, stderr, "cut:")
		})
	}
}

// H9: a very large unbounded end like -b1-99999999999 stores end as an int
// (int64 on 64-bit). The processing loop is bounded by line length, so no
// overflow path is reachable. Confirm the command succeeds and emits the
// expected line content (echo's output, no truncation).
func TestVulnHuntBuiltinIntegerOverflow_LargeUnboundedEnd(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := testutil.RunScript(t,
		"echo hello | cut -b1-99999999999", dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
}

// H18: /dev/zero is an infinite stream without newlines. The scanner's
// MaxLineBytes cap must terminate cut promptly instead of waiting for EOF.
func TestVulnHuntBuiltinSpecialFiles_DevZeroTerminatesAtLineCap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/zero on Windows")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, stderr, code := testutil.RunScriptCtx(ctx, t,
		"cut -b1 /dev/zero", dir, interp.AllowedPaths([]string{"/dev"}))
	require.NoError(t, ctx.Err(), "cut /dev/zero hung or timed out")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "cut:")
}

// H19: non-regular file operands must fail safely rather than being treated as
// normal input streams.
func TestVulnHuntBuiltinSpecialFiles_DirectoryAsFileErrors(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "subdir"), 0755))

	_, stderr, code := testutil.RunScript(t, "cut -b1 subdir", dir,
		interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "cut:")
}
