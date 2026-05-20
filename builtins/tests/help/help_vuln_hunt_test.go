// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package help_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
)

// Campaign: 2026-05-20-gpt-5.5-cyber-3

func allowAllCommands() interp.RunnerOption {
	return interpoption.AllowAllCommands().(interp.RunnerOption)
}

func TestVulnHuntBuiltinFlagDrivenExploit_HelpRejectsMalformedAndUnknownFlags(t *testing.T) {
	stdout, stderr, code := runScript(t, `help --all=maybe; echo all=$?
help --help=maybe; echo help=$?
help --verbose; echo verbose=$?
`, "", allowAllCommands())

	assert.Equal(t, 0, code)
	assert.Equal(t, "all=1\nhelp=1\nverbose=1\n", stdout)
	assert.Contains(t, stderr, `help: invalid argument "maybe" for "--all" flag`)
	assert.Contains(t, stderr, `help: invalid argument "maybe" for "--help" flag`)
	assert.Contains(t, stderr, "help: unrecognized option '--verbose'")
}

func TestVulnHuntBuiltinFlagDrivenExploit_TrailingHelpUsesSharedHelpTrim(t *testing.T) {
	stdout, stderr, code := runScript(t, "help missing-topic --help; echo status=$?", "", allowAllCommands())

	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage: help [--all] [feature|command]\n")
	assert.True(t, strings.HasSuffix(stdout, "status=0\n"))
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinResourceExhaustion_ManyAllowedPathsStayBelowOutputLimit(t *testing.T) {
	base := t.TempDir()
	paths := make([]string, 0, 128)
	for i := 0; i < 128; i++ {
		p := filepath.Join(base, fmt.Sprintf("root-%03d", i))
		require.NoError(t, os.Mkdir(p, 0755))
		paths = append(paths, p)
	}

	stdout, stderr, code := runScript(t, "help --all", "",
		allowAllCommands(),
		interp.AllowedPaths(paths),
	)

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Less(t, len(stdout), 1024*1024)
	assert.Contains(t, stdout, "\nAllowed paths:\n")
	assert.Contains(t, stdout, "\n  "+paths[0]+"\n")
	assert.Contains(t, stdout, "\n  "+paths[len(paths)-1]+"\n")
}

func TestVulnHuntBuiltinResourceExhaustion_CommandTopicHelpIsFiniteAndNonExecuting(t *testing.T) {
	_, _ = interp.New(allowAllCommands())

	for _, name := range builtins.Names() {
		stdout, stderr, code := runScript(t, "help "+name, "", allowAllCommands())
		assert.Equalf(t, 0, code, "help %s", name)
		assert.Emptyf(t, stderr, "help %s", name)
		assert.NotEmptyf(t, stdout, "help %s", name)
		assert.Lessf(t, len(stdout), 64*1024, "help %s", name)
	}

	stdout, stderr, code := runScript(t, "help exit; echo still=$?", "", allowAllCommands())
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "exit:")
	assert.True(t, strings.HasSuffix(stdout, "still=0\n"))
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinDeclaredVsImplemented_TopicPolicyAndAllowedPathsHook(t *testing.T) {
	stdout, stderr, code := runScript(t, "help cat", "",
		interp.AllowedCommands([]string{"rshell:help"}),
	)
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "help: no help topics match 'cat'\n")
	assert.NotContains(t, stderr, "concatenate and print files")

	stdout, stderr, code = runScript(t, "help variables", "",
		interp.AllowedCommands([]string{"rshell:help"}),
	)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "variables - Assignments")
	assert.Empty(t, stderr)

	realRoot := filepath.Join(t.TempDir(), "real-root")
	require.NoError(t, os.Mkdir(realRoot, 0755))

	stdout, stderr, code = runScript(t, "ALLOWED_PATHS=/fake help", "",
		allowAllCommands(),
		interp.AllowedPaths([]string{realRoot}),
	)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "\n  "+realRoot+"\n")
	assert.NotContains(t, stdout, "/fake")

	stdout, stderr, code = runScript(t, "ALLOWED_PATHS=/fake help", "", allowAllCommands())
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "Allowed paths:")
	assert.Contains(t, stdout, "(no allowed paths configured")
	assert.NotContains(t, stdout, "/fake")
}

func TestVulnHuntBuiltinDeclaredVsImplemented_StaticSurfaceHasNoFileNetworkExecOrProcParsers(t *testing.T) {
	srcBytes, err := os.ReadFile(filepath.Join("..", "..", "help", "help.go"))
	require.NoError(t, err)
	src := string(srcBytes)

	assert.NotContains(t, src, `"os"`)
	assert.NotContains(t, src, "os.")
	assert.NotContains(t, src, "OpenFile(")
	assert.NotContains(t, src, "ReadFile(")
	assert.NotContains(t, src, "ReadDir(")
	assert.NotContains(t, src, "Stat(")
	assert.NotContains(t, src, "Lstat(")
	assert.NotContains(t, src, "RunCommand")
	assert.NotContains(t, src, "os/exec")
	assert.NotContains(t, src, "net/")
	assert.NotContains(t, src, "builtins/internal/proc")
	assert.NotContains(t, src, "builtins/internal/diskstats")
}
