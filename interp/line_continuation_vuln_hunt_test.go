// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: line_continuation (shell-feature)

package interp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runLineContinuationVulnHuntScript(t *testing.T, script string, opts ...RunnerOption) (string, string, int, error) {
	t.Helper()

	prog, err := ParseScript(script, "line_continuation_vuln_hunt.sh")
	if err != nil {
		return "", err.Error() + "\n", 2, nil
	}

	var stdout, stderr bytes.Buffer
	allOpts := append([]RunnerOption{
		StdIO(nil, &stdout, &stderr),
		allowAllCommandsOpt(),
	}, opts...)
	r, err := New(allOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = r.Run(ctx, prog)
	exitCode := 0
	if err != nil {
		var status ExitStatus
		if errors.As(err, &status) {
			exitCode = int(status)
			err = nil
		}
	}
	return stdout.String(), stderr.String(), exitCode, err
}

func TestVulnHuntShellFeatureParserConfusion_ContinuationsCannotSmuggleUnsupportedSyntax(t *testing.T) {
	tests := map[string]struct {
		script string
		want   string
	}{
		"background": {
			script: "echo before; echo hidden \\\n& echo after\n",
			want:   "background execution",
		},
		"pipe_all": {
			script: "echo before; echo left |\\\n& cat\n",
			want:   "`|` must be followed by a statement",
		},
		"herestring": {
			script: "echo before; cat <\\\n<\\\n< data\n",
			want:   "`<` must be followed by a word",
		},
		"process_substitution": {
			script: "echo before; cat <\\\n(echo secret)\n",
			want:   "`<` must be followed by a word",
		},
		"function_declaration": {
			script: "echo before; f\\\noo() { echo bad; }\necho after\n",
			want:   "function declarations are not supported",
		},
		"readonly_declaration": {
			script: "echo before; read\\\nonly X=1\necho after\n",
			want:   "readonly is not supported",
		},
		"file_write_redirect": {
			script: "echo before; echo data > ou\\\nt.txt\necho after\n",
			want:   "> file redirection is not supported",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code, err := runLineContinuationVulnHuntScript(t, tc.script)
			require.NoError(t, err)
			assert.Equal(t, 2, code)
			assert.Empty(t, stdout, "whole-file validation must reject before earlier statements execute")
			assert.Contains(t, stderr, tc.want)
		})
	}
}

func TestVulnHuntShellFeatureExpansionChain_ExpansionBackslashNewlineNotReparsed(t *testing.T) {
	script := "PAYLOAD='echo he\\\nllo | cat'\n$PAYLOAD\necho done\n"
	stdout, stderr, code, err := runLineContinuationVulnHuntScript(t, script)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "he\\ llo | cat\ndone\n", stdout)
}

func TestVulnHuntShellFeatureRedirectionChain_ContinuationPreservesRedirectPolicy(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "data.txt"), []byte("from-file\n"), 0o644))
	target := filepath.ToSlash(filepath.Join(dir, "da\\\nta.txt"))

	stdout, stderr, code, err := runLineContinuationVulnHuntScript(t,
		"cat < "+target+"\necho hidden > /dev/nu\\\nll\necho visible\n",
		AllowedPaths([]string{dir}),
	)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "from-file\nvisible\n", stdout)
}

func TestVulnHuntShellFeatureBlockedCommand_ContinuationCommandNameCheckedAfterFolding(t *testing.T) {
	prog, err := ParseScript("ec\\\nho ok\n", "line_continuation_command_policy.sh")
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), AllowedCommands([]string{"rshell:cat"}))
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	err = r.Run(context.Background(), prog)
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	assert.Equal(t, ExitStatus(127), status)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "command not allowed")
}

func TestVulnHuntShellFeatureHeredocChain_QuotedAndUnquotedContinuationRules(t *testing.T) {
	tests := map[string]struct {
		script string
		want   string
	}{
		"unquoted_consumes": {
			script: "cat <<EOF\nhello \\\nworld\nEOF\n",
			want:   "hello world\n",
		},
		"quoted_literal": {
			script: "cat <<'EOF'\nhello \\\nworld\nEOF\n",
			want:   "hello \\\nworld\n",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code, err := runLineContinuationVulnHuntScript(t, tc.script)
			require.NoError(t, err)
			assert.Equal(t, 0, code)
			assert.Empty(t, stderr)
			assert.Equal(t, tc.want, stdout)
		})
	}
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_MaxScriptBytesCountsRawContinuations(t *testing.T) {
	_, err := ParseScript(strings.Repeat("\\\n", MaxScriptBytes/2+1), "oversized_continuations.sh")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestVulnHuntShellFeatureParserConfusion_LineEndingAndEOFCases(t *testing.T) {
	tests := map[string]string{
		"crlf_continuation": "echo hel\\\r\nlo\r\n",
		"trailing_eof":      "echo before\\",
	}

	for name, script := range tests {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code, err := runLineContinuationVulnHuntScript(t, script)
			require.NoError(t, err)
			assert.Equal(t, 0, code)
			assert.Empty(t, stderr)
			switch name {
			case "crlf_continuation":
				assert.Equal(t, "hello\n", stdout)
			case "trailing_eof":
				assert.Equal(t, "before\n", stdout)
			}
		})
	}
}
