// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt 2026-05-20-gpt-5.5-cyber-3 (target: pwd)

package pwd_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

func TestVulnHuntBuiltinPwdFlagDrivenExploit_ModeFlagValuesRejected(t *testing.T) {
	dir := canonicalTempDir(t)
	for _, script := range []string{
		"pwd --physical=false",
		"pwd --physical=true",
		"pwd --logical=true",
		"pwd --logical=TRUE",
	} {
		t.Run(script, func(t *testing.T) {
			stdout, stderr, code := pwdRun(t, script, dir)
			assert.Equal(t, 1, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "pwd:")
			assert.Contains(t, stderr, "doesn't allow an argument")
		})
	}
}

func TestVulnHuntBuiltinPwdDeclaredVsImplemented_HelpDoesNotPrintCwd(t *testing.T) {
	if filepath.Separator == '\\' {
		t.Skip("newline path component is Unix-specific")
	}
	root := canonicalTempDir(t)
	weird := filepath.Join(root, "cwd\nFORGED_PWD_HELP_ROW=1")
	require.NoError(t, os.Mkdir(weird, 0755))

	stdout, stderr, code := testutil.RunScript(t, "pwd --help ignored", weird, interp.AllowedPaths([]string{root}))
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "Usage: pwd")
	assert.NotContains(t, stdout, "FORGED_PWD_HELP_ROW")
}
