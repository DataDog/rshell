// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: uniq (builtin)

package uniq_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
)

func TestVulnHuntBuiltinFlagDrivenExploit_OutputFileOperandRejectedBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	secret := t.TempDir()
	writeFile(t, dir, "in.txt", "a\na\n")
	require.NoError(t, os.WriteFile(filepath.Join(secret, "secret.txt"), []byte("secret\n"), 0o644))

	_, stderr, code := cmdRun(t, "uniq in.txt out.txt", dir)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "extra operand")
	assert.NoFileExists(t, filepath.Join(dir, "out.txt"))

	secretPath := strings.ReplaceAll(filepath.Join(secret, "secret.txt"), `\`, `/`)
	stdout, stderr, code := runScript(t, "uniq "+secretPath+" out.txt", dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "extra operand")
	assert.NoFileExists(t, filepath.Join(dir, "out.txt"))
}

func TestVulnHuntBuiltinFlagDrivenExploit_DangerousWriteAndFollowFlagsRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "in.txt", "a\na\n")

	for _, script := range []string{
		"uniq --output=out.txt in.txt",
		"uniq --files0-from=list.txt",
		"uniq --follow in.txt",
		"uniq -x in.txt",
		`flag="--output=out.txt"; uniq $flag in.txt`,
	} {
		t.Run(script, func(t *testing.T) {
			_, stderr, code := cmdRun(t, script, dir)
			assert.Equal(t, 1, code)
			assert.Contains(t, stderr, "uniq:")
			assert.NoFileExists(t, filepath.Join(dir, "out.txt"))
		})
	}

	stdout, stderr, code := cmdRun(t, "uniq --help --output=out.txt", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage: uniq")
	assert.Empty(t, stderr)
	assert.NoFileExists(t, filepath.Join(dir, "out.txt"))
}

func TestVulnHuntBuiltinDeclaredVsImplemented_NumericValuesReportBeforeHelp(t *testing.T) {
	dir := t.TempDir()

	for _, tc := range []struct {
		script string
		want   string
	}{
		{"uniq -f nope --help", "invalid number of fields to skip"},
		{"uniq -s -1 --help", "invalid number of bytes to skip"},
		{"uniq -w '' --help", "invalid number of bytes to compare"},
	} {
		t.Run(tc.script, func(t *testing.T) {
			stdout, stderr, code := cmdRun(t, tc.script, dir)
			assert.Equal(t, 1, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, tc.want)
		})
	}
}

func TestVulnHuntBuiltinIntegerOverflow_MethodPrefixesStayDocumented(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "in.txt", "a\na\nb\n")

	stdout, stderr, code := cmdRun(t, "uniq --group=p in.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\na\na\n\nb\n", stdout)
	assert.Empty(t, stderr)

	stdout, stderr, code = cmdRun(t, "uniq --all-repeated=s in.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\na\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinFileAccessBypass_OutsidePathsAndSymlinkEscapeBlocked(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on many Windows builders")
	}

	allowed := t.TempDir()
	secret := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(secret, "secret.txt"), []byte("secret\n"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(secret, "secret.txt"), filepath.Join(allowed, "link.txt")))

	secretPath := strings.ReplaceAll(filepath.Join(secret, "secret.txt"), `\`, `/`)
	stdout, stderr, code := runScript(t, "uniq "+secretPath, allowed, interp.AllowedPaths([]string{allowed}))
	assert.Equal(t, 1, code)
	assert.NotContains(t, stdout, "secret")
	assert.Contains(t, stderr, "uniq:")

	stdout, stderr, code = runScript(t, "uniq link.txt", allowed, interp.AllowedPaths([]string{allowed}))
	assert.Equal(t, 1, code)
	assert.NotContains(t, stdout, "secret")
	assert.Contains(t, stderr, "path escapes")
}

func TestVulnHuntBuiltinResourceExhaustion_NulDelimitedLongRecordFailsClosed(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "long.bin"), []byte(strings.Repeat("A", 1<<20+1)), 0o644))

	_, stderr, code := cmdRun(t, "uniq -z long.bin", dir)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "token too long")
}

func TestVulnHuntBuiltinSpecialFiles_DevZeroLineModeFailsAtLineCap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/zero on Windows")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, stderr, code := runScriptCtx(ctx, t, "uniq /dev/zero", "", interp.AllowedPaths([]string{"/dev"}))

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "token too long")
}

func TestVulnHuntBuiltinSpecialFiles_ZeroTerminatedDevZeroHonorsConfiguredTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/zero on Windows")
	}

	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader("uniq -z /dev/zero"), "")
	require.NoError(t, err)

	var stderr bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(nil, io.Discard, &stderr),
		interpoption.AllowAllCommands().(interp.RunnerOption),
		interp.AllowedPaths([]string{"/dev"}),
		interp.MaxExecutionTime(25*time.Millisecond),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runner.Close() })

	start := time.Now()
	err = runner.Run(context.Background(), prog)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 2*time.Second)
}
