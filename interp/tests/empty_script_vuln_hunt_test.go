// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tests_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

// Campaign: 2026-05-20-gpt-5.5-cyber-3

func TestVulnHuntShellFeatureExpansionChain_EmptySubstitutionAndCommandStayNoop(t *testing.T) {
	stdout, stderr, code := testutil.RunScript(t, `false
echo "empty=[$()] status=$?"
EMPTY=
$EMPTY
echo "after_empty_cmd=$?"
`, "")

	assert.Equal(t, 0, code)
	assert.Equal(t, "empty=[] status=1\nafter_empty_cmd=0\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureParserConfusion_EmptyAndCommentInputsStayNoop(t *testing.T) {
	cases := []string{
		"",
		" \t \n\n",
		"# comment with $(echo hacked) and `backticks`\n# > /tmp/out\n",
		"# windows comment\r\n# another\r\n",
	}
	for _, script := range cases {
		t.Run("", func(t *testing.T) {
			stdout, stderr, code := testutil.RunScript(t, script, "")
			assert.Equal(t, 0, code)
			assert.Empty(t, stdout)
			assert.Empty(t, stderr)
		})
	}
}

func TestVulnHuntShellFeatureParserConfusion_NULInputFailsSafely(t *testing.T) {
	stdout, stderr, code := testutil.RunScript(t, "\x00", "")
	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureSubshellIsolation_EmptySubshellsFailAtParseTime(t *testing.T) {
	parser := syntax.NewParser()
	_, err := parser.Parse(strings.NewReader("()"), "")
	require.Error(t, err)

	_, err = parser.Parse(strings.NewReader("( # comment\n )"), "")
	require.Error(t, err)
}

func TestVulnHuntShellFeatureCompositionAttack_CommentLineContinuationsDoNotCreateCommands(t *testing.T) {
	stdout, stderr, code := testutil.RunScript(t, `# comment ending with backslash \
echo visible
# another comment
`, "")

	assert.Equal(t, 0, code)
	assert.Equal(t, "visible\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureRedirectionChain_RedirectionOnlyStatementRestoresStdio(t *testing.T) {
	stdout, stderr, code := testutil.RunScript(t, `>/dev/null
echo visible
2>/dev/null
echo err-visible >&2
`, "")

	assert.Equal(t, 0, code)
	assert.Equal(t, "visible\n", stdout)
	assert.Equal(t, "err-visible\n", stderr)
}

func TestVulnHuntShellFeatureRedirectionChain_InputRedirectOnlyDoesNotLeakToNextCommand(t *testing.T) {
	dir := t.TempDir()
	assert.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("secret\n"), 0644))

	stdout, stderr, code := testutil.RunScript(t, `< input.txt
cat
`, dir, interp.AllowedPaths([]string{dir}))

	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureRedirectionChain_InputRedirectOnlyOutsideSandboxBlocked(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	secret := filepath.Join(root, "secret")
	assert.NoError(t, os.Mkdir(allowed, 0755))
	assert.NoError(t, os.Mkdir(secret, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(secret, "hidden.txt"), []byte("secret\n"), 0644))

	stdout, stderr, code := testutil.RunScript(t, `< ../secret/hidden.txt
cat
`, allowed, interp.AllowedPaths([]string{allowed}))

	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "permission denied")
	assert.NotContains(t, stdout, "secret")
}
