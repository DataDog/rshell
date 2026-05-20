// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt 2026-05-20-gpt-5.5-cyber-3 (target: allowed_paths)

package interp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runAllowedPathsVulnHunt(t *testing.T, script, dir string, opts ...RunnerOption) (string, string, int) {
	t.Helper()

	var stdout, stderr bytes.Buffer
	allOpts := append([]RunnerOption{StdIO(nil, &stdout, &stderr), allowAllCommandsOpt()}, opts...)
	r, err := New(allOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	if dir != "" {
		r.Dir = dir
	}

	err = r.Run(context.Background(), parseScript(t, script))
	exitCode := 0
	if err != nil {
		var es ExitStatus
		if errors.As(err, &es) {
			exitCode = int(es)
		} else {
			t.Fatalf("unexpected Run error: %v", err)
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

func TestVulnHuntShellFeatureExpansionChain_AllowedPathsEnvCannotBroadenSandbox(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "safe.txt"), []byte("safe\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o644))

	script := strings.Join([]string{
		"ALLOWED_PATHS=" + shellQuoteForAllowedPathsVH(outside),
		"PWD=" + shellQuoteForAllowedPathsVH(outside),
		"cat ../outside/secret.txt",
		"echo status=$?",
		"cat safe.txt",
	}, "\n") + "\n"
	stdout, stderr, code := runAllowedPathsVulnHunt(t, script, allowed, AllowedPaths([]string{allowed}))

	assert.Equal(t, 0, code)
	assert.Equal(t, "status=1\nsafe\n", stdout)
	assert.Contains(t, stderr, "secret.txt")
	assert.NotContains(t, stdout, "secret")
}

func TestVulnHuntShellFeatureParserConfusion_DotSegmentsAndDotDotNames(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.MkdirAll(filepath.Join(allowed, "sub"), 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "sub", "file.txt"), []byte("nested\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "..data"), []byte("dotdot-name\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o644))

	script := strings.Join([]string{
		"cat sub/../sub/file.txt",
		"cat ..data",
		"cat ../outside/secret.txt",
		"echo outside=$?",
	}, "\n") + "\n"
	stdout, stderr, code := runAllowedPathsVulnHunt(t, script, allowed, AllowedPaths([]string{allowed}))

	assert.Equal(t, 0, code)
	assert.Equal(t, "nested\ndotdot-name\noutside=1\n", stdout)
	assert.Contains(t, stderr, "secret.txt")
	assert.NotContains(t, stdout, "secret")
}

func TestVulnHuntShellFeatureSubshellIsolation_ChildEnvMutationDoesNotBroadenParent(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "safe.txt"), []byte("safe\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o644))

	script := strings.Join([]string{
		"( ALLOWED_PATHS=" + shellQuoteForAllowedPathsVH(outside) + "; PWD=" + shellQuoteForAllowedPathsVH(outside) + "; cat ../outside/secret.txt; echo child=$? )",
		"cat safe.txt",
		"echo parent=$ALLOWED_PATHS",
	}, "\n") + "\n"
	stdout, stderr, code := runAllowedPathsVulnHunt(t, script, allowed, AllowedPaths([]string{allowed}))

	assert.Equal(t, 0, code)
	assert.Equal(t, "child=1\nsafe\nparent="+allowed+"\n", stdout)
	assert.Contains(t, stderr, "secret.txt")
	assert.NotContains(t, stdout, "secret")
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_AllSkippedRootsStillBlockFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "blocked.txt"), []byte("blocked\n"), 0o644))
	var stdout, stderr, warnings bytes.Buffer

	r, err := New(
		StdIO(nil, &stdout, &stderr),
		WarningsWriter(&warnings),
		allowAllCommandsOpt(),
		AllowedPaths([]string{filepath.Join(dir, "missing")}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Dir = dir

	err = r.Run(context.Background(), parseScript(t, "cat blocked.txt\necho read=$?\necho paths=$ALLOWED_PATHS\n"))
	require.NoError(t, err)

	assert.Equal(t, "read=1\npaths=\n", stdout.String())
	assert.Contains(t, stderr.String(), "permission denied")
	assert.NotContains(t, stdout.String(), "blocked")
	assert.Contains(t, warnings.String(), "AllowedPaths: skipping")
	assert.NotContains(t, stdout.String(), "AllowedPaths: skipping")
}

func TestVulnHuntShellFeatureCompositionAttack_RedirectFailureRestoresStdin(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "safe.txt"), []byte("safe\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret\n"), 0o644))

	script := "cat < ../outside/secret.txt\necho status=$?\ncat < safe.txt\ncat <<'EOF'\nheredoc\nEOF\n"
	stdout, stderr, code := runAllowedPathsVulnHunt(t, script, allowed, AllowedPaths([]string{allowed}))

	assert.Equal(t, 0, code)
	assert.Equal(t, "status=1\nsafe\nheredoc\n", stdout)
	assert.Contains(t, stderr, "secret.txt")
	assert.NotContains(t, stdout, "secret")
}

func TestVulnHuntShellFeatureSignalContext_GlobLoopRespectsCancellation(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644))
	r, err := New(
		StdIO(nil, io.Discard, io.Discard),
		allowAllCommandsOpt(),
		AllowedPaths([]string{dir}),
		MaxExecutionTime(100*time.Millisecond),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Dir = dir
	start := time.Now()

	err = r.Run(context.Background(), parseScript(t, "while true; do for f in *.txt; do :; done; done\n"))

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func shellQuoteForAllowedPathsVH(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
