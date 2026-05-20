// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

// Tripwire tests added by vuln-hunt campaign 2026-05-20-gpt-5.5-cyber-3 /
// uname.

package uname_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/interp"
)

func TestVulnHuntBuiltinUname_UnsupportedFlagSurfaceRejected(t *testing.T) {
	dir := t.TempDir()
	writeFakeProc(t, dir, defaultFakeProc())

	for _, script := range []string{
		"uname --processor",
		"uname --operating-system",
		"uname --version",
		"uname --kernel-name=Linux",
		"uname -s=Linux",
		"uname -- -a",
	} {
		t.Run(script, func(t *testing.T) {
			stdout, stderr, code := cmdRun(t, script, dir)
			assert.Equal(t, 1, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "uname:")
		})
	}
}

func TestVulnHuntBuiltinUname_HelpDoesNotReadProcOrValidateTrailingJunk(t *testing.T) {
	dir := t.TempDir()

	stdout, stderr, code := cmdRun(t, "uname --help foo --kernel-name=/etc/passwd", dir)

	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage: uname")
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinUname_DefaultAndOrderStableAcrossRepeatedCommands(t *testing.T) {
	dir := t.TempDir()
	writeFakeProc(t, dir, defaultFakeProc())

	stdout, stderr, code := cmdRun(t, "uname -a\nuname\nuname -mrvns\n", dir)

	assert.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Equal(t,
		"Linux testhost 5.15.0-test #1 SMP Test x86_64\n"+
			"Linux\n"+
			"Linux testhost 5.15.0-test #1 SMP Test x86_64\n",
		stdout,
	)
}

func TestVulnHuntBuiltinUname_ProcPathScriptAssignmentCannotRedirect(t *testing.T) {
	root := t.TempDir()
	configuredProc := filepath.Join(root, "configured")
	evilProc := filepath.Join(root, "evil")
	writeFakeProc(t, configuredProc, map[string]string{"ostype": "Linux"})
	writeFakeProc(t, evilProc, map[string]string{"ostype": "EVIL"})

	stdout, stderr, code := runScript(t,
		"ProcPath="+evilProc+" uname -s\n",
		root,
		interp.ProcPath(configuredProc),
	)

	assert.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Equal(t, "Linux\n", stdout)
}

func TestVulnHuntBuiltinUname_AllowedPathsDoNotReachProcPath(t *testing.T) {
	procPath := t.TempDir()
	allowed := t.TempDir()
	writeFakeProc(t, procPath, defaultFakeProc())

	stdout, stderr, code := runScript(t,
		"uname -a",
		allowed,
		interp.ProcPath(procPath),
		interp.AllowedPaths([]string{allowed}),
	)

	assert.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Equal(t, "Linux testhost 5.15.0-test #1 SMP Test x86_64\n", stdout)
}

func TestVulnHuntBuiltinUname_ProcPathTraversalRejected(t *testing.T) {
	root := t.TempDir()
	secretProc := filepath.Join(root, "secretproc")
	writeFakeProc(t, secretProc, map[string]string{"ostype": "SECRET"})
	traversalProc := root + "/proc/../secretproc"

	stdout, stderr, code := runScript(t,
		"uname -s",
		root,
		interp.ProcPath(traversalProc),
	)

	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "unsafe procPath")
	assert.NotContains(t, stderr, "SECRET")
}

func TestVulnHuntBuiltinUname_OverlongProcValueIsCapped(t *testing.T) {
	dir := t.TempDir()
	kernelDir := filepath.Join(dir, "sys", "kernel")
	require.NoError(t, os.MkdirAll(kernelDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(kernelDir, "ostype"), []byte(strings.Repeat("A", 1<<20)), 0o644))

	stdout, stderr, code := cmdRun(t, "uname -s", dir)

	assert.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Len(t, stdout, 4097)
	assert.True(t, strings.HasPrefix(stdout, strings.Repeat("A", 4096)))
	assert.Equal(t, byte('\n'), stdout[len(stdout)-1])
}

func TestVulnHuntBuiltinUname_FifoProcEntryRejectedPromptly(t *testing.T) {
	dir := t.TempDir()
	kernelDir := filepath.Join(dir, "sys", "kernel")
	require.NoError(t, os.MkdirAll(kernelDir, 0o755))
	require.NoError(t, syscall.Mkfifo(filepath.Join(kernelDir, "ostype"), 0o600))

	start := time.Now()
	stdout, stderr, code := cmdRun(t, "uname -s", dir)

	assert.Equal(t, 1, code)
	assert.Less(t, time.Since(start), time.Second)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "not a regular file")
}

func TestVulnHuntBuiltinUname_CharDeviceProcEntryIsCapped(t *testing.T) {
	dir := t.TempDir()
	kernelDir := filepath.Join(dir, "sys", "kernel")
	require.NoError(t, os.MkdirAll(kernelDir, 0o755))
	require.NoError(t, os.Symlink("/dev/zero", filepath.Join(kernelDir, "ostype")))

	stdout, stderr, code := cmdRun(t, "uname -s", dir)

	assert.Equal(t, 0, code, "stderr: %s", stderr)
	assert.Empty(t, stderr)
	assert.Len(t, stdout, 4097)
	assert.Equal(t, byte('\n'), stdout[len(stdout)-1])
}

func TestVulnHuntBuiltinUname_PreCanceledContextHasNoPartialOutput(t *testing.T) {
	dir := t.TempDir()
	writeFakeProc(t, dir, defaultFakeProc())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	stdout, _, code := runScriptCtx(ctx, t, "uname -a", dir, interp.ProcPath(dir))

	assert.NotEqual(t, 0, code)
	assert.Empty(t, stdout)
}
