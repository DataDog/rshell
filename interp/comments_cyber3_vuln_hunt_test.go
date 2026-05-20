// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: comments (shell-feature)

package interp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runCommentsCyber3(t *testing.T, script string, opts ...RunnerOption) (string, string, int, error) {
	t.Helper()

	prog, err := ParseScript(script, "comments_cyber3_vuln_hunt.sh")
	if err != nil {
		return "", err.Error() + "\n", 2, nil
	}

	var stdout, stderr bytes.Buffer
	allOpts := append([]RunnerOption{StdIO(nil, &stdout, &stderr), allowAllCommandsOpt()}, opts...)
	r, err := New(allOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	err = r.Run(context.Background(), prog)
	code := 0
	if err != nil {
		var status ExitStatus
		if errors.As(err, &status) {
			code = int(status)
			err = nil
		}
	}
	return stdout.String(), stderr.String(), code, err
}

func TestVulnHuntShellFeatureExpansionChain_CommentTextIsNeverExecuted(t *testing.T) {
	stdout, stderr, code, err := runCommentsCyber3(t, `# $(echo HACKED)
# `+"`echo BACKTICK`"+`
# readonly X=1
# echo hidden > out.txt
echo visible
`)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "visible\n", stdout)
	assert.Empty(t, stderr)
	assert.NotContains(t, stdout+stderr, "HACKED")
	assert.NotContains(t, stdout+stderr, "BACKTICK")
	assert.NotContains(t, stdout+stderr, "readonly")
}

func TestVulnHuntShellFeatureExpansionChain_HashLiteralContexts(t *testing.T) {
	stdout, stderr, code, err := runCommentsCyber3(t, `echo "quoted # data"
echo 'single # data'
echo escaped \# data
V=mid#word
echo "var=$V"
`)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "quoted # data\nsingle # data\nescaped # data\nvar=mid#word\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureParserConfusion_CommentsAfterOperatorsPreserveStructure(t *testing.T) {
	stdout, stderr, code, err := runCommentsCyber3(t, `echo one; # semicolon comment
echo two &&# and comment
echo three ||# or comment
echo skipped
echo pipe |# pipe comment
cat
for v # for variable comment
in a b # item comment
do # do comment
  echo "loop=$v" # body comment
done # done comment
`)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "one\ntwo\nthree\npipe\nloop=a\nloop=b\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureParserConfusion_CommentOnlyAndCRLFInputsStayNoop(t *testing.T) {
	tests := map[string]string{
		"comment_only": "# $(echo HACKED)\n# > out.txt\n###\n",
		"indented":     "  # comment\n\t# tab comment\n",
		"crlf":         "echo one # ignored\r\necho two\r\n",
	}

	for name, script := range tests {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code, err := runCommentsCyber3(t, script)

			require.NoError(t, err)
			assert.Equal(t, 0, code)
			assert.Empty(t, stderr)
			if name == "crlf" {
				assert.Equal(t, "one\ntwo\n", stdout)
			} else {
				assert.Empty(t, stdout)
			}
		})
	}
}

func TestVulnHuntShellFeatureSubshellIsolation_CommentsDoNotMoveCompoundBoundaries(t *testing.T) {
	stdout, stderr, code, err := runCommentsCyber3(t, `( X=child # subshell comment
echo "sub=$X" )
echo "parent=$X"
{ Y=block # brace comment
echo "block=$Y"; }
echo "after_block=$Y"
`)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "sub=child\nparent=\nblock=block\nafter_block=block\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_MaxScriptBytesRejectsHugeComments(t *testing.T) {
	line := "# comment with ignored payload $(echo HACKED) > out.txt\n"
	script := strings.Repeat(line, MaxScriptBytes/len(line)+1)
	require.Greater(t, len(script), MaxScriptBytes)

	_, err := ParseScript(script, "comments_oversized.sh")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
	assert.Contains(t, err.Error(), "5 MiB")
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_CommentPreservesPreviousStatus(t *testing.T) {
	stdout, stderr, code, err := runCommentsCyber3(t, `false # ignored comment
echo "status=$?"
`)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "status=1\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureCompositionAttack_CommentBackslashDoesNotContinueLine(t *testing.T) {
	stdout, stderr, code, err := runCommentsCyber3(t, `# comment ending with backslash \
echo visible
`)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "visible\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureCompositionAttack_HeredocHashContentIsLiteral(t *testing.T) {
	stdout, stderr, code, err := runCommentsCyber3(t, `cat <<EOF
# not a comment
value # also literal
EOF
echo after # comment after heredoc
`)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "# not a comment\nvalue # also literal\nafter\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureRedirectionChain_CommentsDoNotCreateOrSuppressRedirects(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code, err := runCommentsCyber3(t, `echo visible # > out.txt
`, AllowedPaths([]string{dir}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "visible\n", stdout)
	assert.Empty(t, stderr)
	assert.NoFileExists(t, filepath.Join(dir, "out.txt"))

	stdout, stderr, code, err = runCommentsCyber3(t, `echo hidden > out.txt # comment
echo after
`, AllowedPaths([]string{dir}))

	require.NoError(t, err)
	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "> file redirection is not supported")
	assert.NoFileExists(t, filepath.Join(dir, "out.txt"))
}

func TestVulnHuntShellFeatureRedirectionChain_InputRedirectCommentRestoresStdin(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("from-file\n"), 0o644))

	stdout, stderr, code, err := runCommentsCyber3(t, `cat < input.txt # comment after input redirect
cat <<EOF
restored
EOF
`, AllowedPaths([]string{dir}))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "from-file\nrestored\n", stdout)
	assert.Empty(t, stderr)
}
