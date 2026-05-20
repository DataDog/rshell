// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: globbing (shell-feature)

package interp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/expand"

	"github.com/DataDog/rshell/allowedpaths"
)

func runGlobbingCyber3Script(t *testing.T, script, dir string, opts ...RunnerOption) (string, string, int, error) {
	t.Helper()
	prog := parseScript(t, script)

	var stdout, stderr bytes.Buffer
	allOpts := append([]RunnerOption{StdIO(nil, &stdout, &stderr), allowAllCommandsOpt()}, opts...)
	r, err := New(allOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	if dir != "" {
		r.Dir = dir
	}

	err = r.Run(context.Background(), prog)
	code := 0
	var status ExitStatus
	if errors.As(err, &status) {
		code = int(status)
		err = nil
	}
	return stdout.String(), stderr.String(), code, err
}

func TestVulnHuntShellFeatureGlobbing_ExpansionChain_FilenamesAreData(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"$(echo PWNED).txt",
		"redir>out.txt",
		"semi;echo PWNED.txt",
		"space name.txt",
	}
	for _, name := range names {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
	}

	stdout, stderr, code, err := runGlobbingCyber3Script(t, "echo *\n", dir, AllowedPaths([]string{dir}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "$(echo PWNED).txt redir>out.txt semi;echo PWNED.txt space name.txt\n", stdout)
	assert.Empty(t, stderr)
	_, statErr := os.Stat(filepath.Join(dir, "out.txt"))
	assert.True(t, os.IsNotExist(statErr), "glob result containing > must not become a redirect")
}

func TestVulnHuntShellFeatureGlobbing_ExpansionChain_OutsidePatternStaysSandboxed(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.MkdirAll(allowed, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("SECRET\n"), 0o644))

	stdout, stderr, code, err := runGlobbingCyber3Script(t,
		"PATTERN='../outside/*'\necho $PATTERN\necho after\n",
		allowed,
		AllowedPaths([]string{allowed}),
	)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "../outside/*")
	assert.Contains(t, stdout, "after\n")
	assert.NotContains(t, stdout+stderr, "SECRET")
}

func TestVulnHuntShellFeatureGlobbing_ParserConfusion_ExtglobRejectedAndSlashPatternsLiteral(t *testing.T) {
	stdout, stderr, code, err := runGlobbingCyber3Script(t, "echo @(safe)\necho after\n", "")
	require.NoError(t, err)
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "extended globbing is not supported")

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "foo", "dir"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "foo", "dir", "file"), []byte("x"), 0o644))
	stdout, stderr, code, err = runGlobbingCyber3Script(t,
		"echo foo[/]dir[/]file\necho foo?dir?file\n",
		dir,
		AllowedPaths([]string{dir}),
	)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "foo[/]dir[/]file\nfoo?dir?file\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureGlobbing_RedirectionChain_DynamicOrGlobRedirectsDoNotWidenPolicy(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("SECRET\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "dev"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "dev", "null"), []byte("not a device"), 0o644))

	for _, script := range []string{
		"TARGET=/dev/null\necho hi > $TARGET\necho after\n",
		"echo hi > dev/nul*\necho after\n",
	} {
		stdout, stderr, code, err := runGlobbingCyber3Script(t, script, dir, AllowedPaths([]string{dir}))
		require.NoError(t, err)
		assert.Equal(t, 2, code)
		assert.Empty(t, stdout)
		assert.Contains(t, stderr, "file redirection is not supported")
	}

	stdout, stderr, code, err := runGlobbingCyber3Script(t,
		"cat < *.txt\necho status=$?\n",
		dir,
		AllowedPaths([]string{dir}),
	)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "status=1\n", stdout)
	assert.Contains(t, stderr, "*.txt")
	assert.NotContains(t, stdout+stderr, "SECRET")
}

func TestVulnHuntShellFeatureGlobbing_CompositionAttack_ForLoopFilenamesRemainData(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"$(echo hacked).txt", "semi;echo hacked.txt", "space name.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
	}

	stdout, stderr, code, err := runGlobbingCyber3Script(t,
		"for f in *; do echo \"[$f]\"; done\n",
		dir,
		AllowedPaths([]string{dir}),
	)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "[$(echo hacked).txt]\n[semi;echo hacked.txt]\n[space name.txt]\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureGlobbing_ReadonlyBypass_ForLoopVariableRespectsReadonly(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644))

	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt(), AllowedPaths([]string{dir}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Dir = dir
	r.Reset()
	require.NoError(t, r.writeEnv.Set("RO", expand.Variable{
		Set:      true,
		Kind:     expand.String,
		Str:      "original",
		ReadOnly: true,
	}))

	err = r.Run(context.Background(), parseScript(t, "for RO in *; do echo \"loop=$RO\"; done\necho after=$RO\n"))

	require.NoError(t, err)
	assert.Contains(t, stderr.String(), "readonly variable")
	assert.NotContains(t, stdout.String(), "loop=a.txt")
	assert.NotContains(t, stdout.String(), "loop=b.txt")
	assert.Contains(t, stdout.String(), "after=original")
}

func TestVulnHuntShellFeatureGlobbing_SignalContext_ForGlobLoopCancels(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))

	var stdout, stderr bytes.Buffer
	r, err := New(
		StdIO(nil, &stdout, &stderr),
		allowAllCommandsOpt(),
		AllowedPaths([]string{dir}),
		MaxExecutionTime(25*time.Millisecond),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Dir = dir

	start := time.Now()
	err = r.Run(context.Background(), parseScript(t, "while true; do for f in *.txt; do :; done; done\n"))

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Empty(t, stdout.String())
}

func TestVulnHuntShellFeatureGlobbing_DeclaredVsImplemented_DirectoryEntryCapRejectsBeforeMatch(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < allowedpaths.MaxGlobEntries+1; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%05d", i)), []byte("x"), 0o644))
	}

	stdout, stderr, code, err := runGlobbingCyber3Script(t, "echo *\n", dir, AllowedPaths([]string{dir}))

	require.NoError(t, err)
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "directory has too many entries")
}
