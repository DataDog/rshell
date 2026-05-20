// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: cat (builtin)

package cat_test

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

	catcmd "github.com/DataDog/rshell/builtins/cat"
	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
)

func TestVulnHuntBuiltinFlagDrivenExploit_DangerousFlagsRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "in.txt", "safe\n")

	for _, script := range []string{
		"cat --output=out.txt in.txt",
		"cat --follow in.txt",
		"cat --files0-from=list.txt",
		"cat -f in.txt",
		`flag="--output=out.txt"; cat $flag in.txt`,
	} {
		t.Run(script, func(t *testing.T) {
			_, stderr, code := cmdRun(t, script, dir)
			assert.Equal(t, 1, code)
			assert.Contains(t, stderr, "cat:")
			assert.NoFileExists(t, filepath.Join(dir, "out.txt"))
		})
	}

	stdout, stderr, code := cmdRun(t, "cat --help --output=out.txt", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage: cat")
	assert.Empty(t, stderr)
	assert.NoFileExists(t, filepath.Join(dir, "out.txt"))
}

func TestVulnHuntBuiltinFlagDrivenExploit_DoubleDashAndDashStdinStayDataOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "-n", "flag-looking-name\n")
	writeFile(t, dir, "stdin.txt", "stdin-once\n")

	stdout, stderr, code := cmdRun(t, "cat -- -n", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "flag-looking-name\n", stdout)
	assert.Empty(t, stderr)

	stdout, stderr, code = cmdRun(t, "cat - - < stdin.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "stdin-once\n", stdout)
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
	stdout, stderr, code := runScript(t, "cat "+secretPath, allowed, interp.AllowedPaths([]string{allowed}))
	assert.Equal(t, 1, code)
	assert.NotContains(t, stdout, "secret")
	assert.Contains(t, stderr, "cat:")

	stdout, stderr, code = runScript(t, "cat link.txt", allowed, interp.AllowedPaths([]string{allowed}))
	assert.Equal(t, 1, code)
	assert.NotContains(t, stdout, "secret")
	assert.Contains(t, stderr, "cat:")
}

func TestVulnHuntBuiltinResourceExhaustion_LineModeLongRecordFailsClosed(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("A", catcmd.MaxLineBytes+1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "huge.txt"), []byte(content), 0o644))

	stdout, stderr, code := cmdRun(t, "cat -n huge.txt", dir)

	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "cat:")
	assert.Contains(t, stderr, "token too long")
}

func TestVulnHuntBuiltinSpecialFiles_DevZeroLineModeFailsAtLineCap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/zero on Windows")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	stdout, stderr, code := runScriptCtx(ctx, t, "cat -n /dev/zero", "", interp.AllowedPaths([]string{"/dev"}))

	require.NoError(t, ctx.Err())
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "token too long")
}

func TestVulnHuntBuiltinSpecialFiles_RawDevZeroHonorsConfiguredTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/zero on Windows")
	}

	prog, err := interp.ParseScript("cat /dev/zero", "cat_devzero_timeout.sh")
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

func TestVulnHuntBuiltinComposition_BrokenPipeTerminatesRawCat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/zero on Windows")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	stdout, stderr, code := runScriptCtx(ctx, t, "cat /dev/zero | head -c 1", "", interp.AllowedPaths([]string{"/dev"}))

	require.NoError(t, ctx.Err())
	assert.Equal(t, 0, code)
	assert.Len(t, stdout, 1)
	assert.Empty(t, stderr)
}
