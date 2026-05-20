// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt 2026-05-20-gpt-5.5-cyber-3 (target: while_clause)

package tests_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
)

func TestVulnHuntWhileClause_RunnerReuseClearsLoopControl(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := interp.New(
		interp.StdIO(nil, &stdout, &stderr),
		interpoption.AllowAllCommands().(interp.RunnerOption),
	)
	require.NoError(t, err)
	defer r.Close()

	parser := syntax.NewParser()
	first, err := parser.Parse(bytes.NewReader([]byte("while true; do break; done\n")), "")
	require.NoError(t, err)
	require.NoError(t, r.Run(context.Background(), first))

	second, err := parser.Parse(bytes.NewReader([]byte("echo after\n")), "")
	require.NoError(t, err)
	require.NoError(t, r.Run(context.Background(), second))

	assert.Equal(t, "after\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntWhileClause_FailedInputRedirectRestoresStdin(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("ok\n"), 0o644))

	stdout, stderr, code := testutil.RunScript(t, `while read line; do echo bad; done < missing.txt
cat input.txt
`, dir, interp.AllowedPaths([]string{dir}))

	assert.Equal(t, 0, code)
	assert.Equal(t, "ok\n", stdout)
	assert.Contains(t, stderr, "missing.txt")
	assert.NotContains(t, stdout, "bad")
}

func TestVulnHuntWhileClause_ConditionCmdSubstDeniedBeforeBody(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "allowed"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "secret"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "secret", "forbidden.txt"), []byte("secret\n"), 0o644))

	stdout, stderr, code := testutil.RunScript(t, `while [ -n "$(cat secret/forbidden.txt)" ]; do echo body; break; done
echo done
`, root, interp.AllowedPaths([]string{filepath.Join(root, "allowed")}))

	assert.Equal(t, 0, code)
	assert.Equal(t, "done\n", stdout)
	assert.Contains(t, stderr, "permission denied")
	assert.NotContains(t, stdout, "body")
}

func TestVulnHuntWhileClause_ReadonlyConditionAssignmentBlocked(t *testing.T) {
	stdout, stderr, code := whileRun(t, `readonly i
while i=a; do echo body; break; done
echo "i=$i"
`)

	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "readonly")
	assert.NotContains(t, stdout, "body")
}
