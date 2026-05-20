// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: input_processing (shell-feature)

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/rshell/interp"
)

func TestVulnHuntShellFeatureExpansionChain_CommentAndBlankInputStaysNoop(t *testing.T) {
	script := "# $(echo hacked)\n# > /tmp/out\n\n \t \n"
	code, stdout, stderr := runCLIWithStdin(t, script, "--allow-all-commands")

	assert.Equal(t, 0, code)
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureParserConfusion_UnsupportedLaterSyntaxRejectedBeforeExecution(t *testing.T) {
	code, stdout, stderr := runCLIWithStdin(t, "echo before\necho bg & echo after\n", "--allow-all-commands")

	assert.Equal(t, 2, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "background execution")
}

func TestVulnHuntShellFeatureParserConfusion_CRLFAndNoTrailingNewline(t *testing.T) {
	code, stdout, stderr := runCLIWithStdin(t, "echo one\r\necho two\r\nprintf three", "--allow-all-commands")

	assert.Equal(t, 0, code)
	assert.Equal(t, "one\ntwo\nthree", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureParserConfusion_EmbeddedNULSourceHandledDeterministically(t *testing.T) {
	code, stdout, stderr := runCLIWithStdin(t, "echo before\x00echo after\n", "--allow-all-commands")

	assert.Equal(t, 0, code)
	assert.Equal(t, "beforeecho after\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureSubshellIsolation_SourceStdinAndCommandStdinSeparatedByMode(t *testing.T) {
	t.Run("stdin_script_gets_empty_command_stdin", func(t *testing.T) {
		code, stdout, stderr := runCLIWithStdin(t, "cat\n", "--allow-all-commands")
		assert.Equal(t, 0, code)
		assert.Empty(t, stdout)
		assert.Empty(t, stderr)
	})

	t.Run("command_string_gets_caller_stdin", func(t *testing.T) {
		code, stdout, stderr := runCLIWithStdin(t, "payload\n", "--allow-all-commands", "-c", "cat")
		assert.Equal(t, 0, code)
		assert.Equal(t, "payload\n", stdout)
		assert.Empty(t, stderr)
	})
}

func TestVulnHuntShellFeatureSubshellIsolation_FileScriptsGetFreshStdinReaders(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.sh")
	second := filepath.Join(dir, "second.sh")
	assert.NoError(t, os.WriteFile(first, []byte("cat\n"), 0o644))
	assert.NoError(t, os.WriteFile(second, []byte("cat\n"), 0o644))

	code, stdout, stderr := runCLIWithStdin(t, "payload\n", "--allow-all-commands", first, second)

	assert.Equal(t, 0, code)
	assert.Equal(t, "payload\npayload\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_LongSourceLineWithinScriptLimit(t *testing.T) {
	longComment := "#" + strings.Repeat("x", 1<<20+1) + "\n"
	script := longComment + "echo ok\n"
	assert.LessOrEqual(t, len(script), interp.MaxScriptBytes)

	code, stdout, stderr := runCLIWithStdin(t, script, "--allow-all-commands")

	assert.Equal(t, 0, code)
	assert.Equal(t, "ok\n", stdout)
	assert.Empty(t, stderr)
}
