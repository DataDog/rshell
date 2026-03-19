// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package help_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
)

func runScript(t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return runScriptCtx(context.Background(), t, script, dir, opts...)
}

func runScriptCtx(ctx context.Context, t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(script), "")
	require.NoError(t, err)
	var outBuf, errBuf bytes.Buffer
	allOpts := append([]interp.RunnerOption{interp.StdIO(nil, &outBuf, &errBuf)}, opts...)
	runner, err := interp.New(allOpts...)
	require.NoError(t, err)
	defer runner.Close()
	if dir != "" {
		runner.Dir = dir
	}
	err = runner.Run(ctx, prog)
	exitCode := 0
	if err != nil {
		var es interp.ExitStatus
		if errors.As(err, &es) {
			exitCode = int(es)
		} else if ctx.Err() == nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// --- Exit code ---

func TestHelpExitCode(t *testing.T) {
	stdout, stderr, code := runScript(t, "help", "", interpoption.AllowAllCommands().(interp.RunnerOption))
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.NotEmpty(t, stdout)
}

// --- Output content ---

func TestHelpListsAllBuiltins(t *testing.T) {
	stdout, _, code := runScript(t, "help", "", interpoption.AllowAllCommands().(interp.RunnerOption))
	assert.Equal(t, 0, code)

	// Every registered builtin should appear in the output.
	expected := []string{
		"[", "break", "cat", "continue", "cut", "echo", "exit",
		"false", "find", "grep", "head", "help", "ls", "printf",
		"sed", "sort", "strings", "tail", "test", "tr", "true",
		"uniq", "wc",
	}
	for _, cmd := range expected {
		assert.Contains(t, stdout, cmd, "help output should list %q", cmd)
	}
}

func TestHelpListsSorted(t *testing.T) {
	stdout, _, code := runScript(t, "help", "", interpoption.AllowAllCommands().(interp.RunnerOption))
	assert.Equal(t, 0, code)

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	// Last line is the footer hint — exclude it and the blank line before it.
	var cmdLines []string
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "Run '") {
			continue
		}
		cmdLines = append(cmdLines, line)
	}

	// Extract command names from the first column.
	var names []string
	for _, line := range cmdLines {
		fields := strings.Fields(line)
		if len(fields) > 0 {
			names = append(names, fields[0])
		}
	}

	// Verify sorted order.
	for i := 1; i < len(names); i++ {
		assert.True(t, names[i-1] <= names[i],
			"commands should be sorted, but %q > %q", names[i-1], names[i])
	}
}

func TestHelpIncludesDescriptions(t *testing.T) {
	stdout, _, code := runScript(t, "help", "", interpoption.AllowAllCommands().(interp.RunnerOption))
	assert.Equal(t, 0, code)

	// Spot-check a few descriptions.
	assert.Contains(t, stdout, "concatenate and print files")
	assert.Contains(t, stdout, "write arguments to stdout")
	assert.Contains(t, stdout, "display help for commands")
	assert.Contains(t, stdout, "list directory contents")
}

func TestHelpIncludesFooterHint(t *testing.T) {
	stdout, _, code := runScript(t, "help", "", interpoption.AllowAllCommands().(interp.RunnerOption))
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Run 'help <command>' for more information on a specific command.")
}

func TestHelpColumnsAligned(t *testing.T) {
	stdout, _, code := runScript(t, "help", "", interpoption.AllowAllCommands().(interp.RunnerOption))
	assert.Equal(t, 0, code)

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	// The format is "%-*s  %s\n" — name padded to maxLen, two spaces, description.
	// The longest builtin name determines the column width. All command lines
	// should have the same total length for the "name + padding + gap" prefix.
	// We detect the description column by finding the last "  " (double space)
	// followed by a non-space character.
	descCol := -1
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "Run '") {
			continue
		}
		// Walk backwards from the end to find where the description text starts.
		// The description is preceded by exactly "  " (the gap).
		// Find the rightmost "  X" where X is non-space.
		col := -1
		for i := len(line) - 1; i >= 2; i-- {
			if line[i] != ' ' && line[i-1] == ' ' && line[i-2] == ' ' {
				col = i
				break
			}
		}
		if col == -1 {
			continue
		}
		if descCol == -1 {
			descCol = col
		} else {
			assert.Equal(t, descCol, col,
				"description column should be aligned across all lines, mismatch on line: %q", line)
		}
	}
	assert.Greater(t, descCol, 0, "should have found description column")
}

// --- Restricted commands ---

func TestHelpRestrictedShowsOnlyAllowed(t *testing.T) {
	stdout, stderr, code := runScript(t, "help", "",
		interp.AllowedCommands([]string{"rshell:echo"}))
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "echo")
	assert.Contains(t, stdout, "help") // help is always listed
	assert.NotContains(t, stdout, "cat")
	assert.NotContains(t, stdout, "grep")
	assert.NotContains(t, stdout, "ls")
}

func TestHelpRestrictedSingleCommand(t *testing.T) {
	// Only "ls" is explicitly allowed; help should still appear.
	stdout, _, code := runScript(t, "help", "",
		interp.AllowedCommands([]string{"rshell:ls"}))
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "help")
	assert.Contains(t, stdout, "ls")
	assert.NotContains(t, stdout, "echo")
}

func TestHelpRestrictedAlignmentAdjusts(t *testing.T) {
	// With "wc" (2-char) and "strings" (7-char) plus implicit "help" (4-char),
	// the column width should match the longest allowed name.
	stdout, _, code := runScript(t, "help", "",
		interp.AllowedCommands([]string{"rshell:wc", "rshell:strings"}))
	assert.Equal(t, 0, code)

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "Run '") {
			continue
		}
		// "strings" is the longest name (7 chars), so the description should
		// start at the same column for all lines.
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "wc" {
			// "wc" should be padded to the same width as "strings".
			assert.True(t, strings.HasPrefix(line, "wc       "),
				"short name should be padded, got: %q", line)
		}
	}
}

func TestHelpAlwaysAvailable(t *testing.T) {
	// help is not in the allowed list, but should still run.
	stdout, stderr, code := runScript(t, "help", "",
		interp.AllowedCommands([]string{"rshell:echo", "rshell:ls"}))
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "help")
	assert.Contains(t, stdout, "echo")
	assert.Contains(t, stdout, "ls")
	assert.NotContains(t, stdout, "cat")
}

func TestHelpAlwaysAvailableNoCommands(t *testing.T) {
	// Even with an empty allowed list, help should work.
	stdout, stderr, code := runScript(t, "help", "",
		interp.AllowedCommands([]string{}))
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	// Only help itself should be listed.
	assert.Contains(t, stdout, "help")
	assert.NotContains(t, stdout, "echo")
}

// --- Error handling ---

func TestHelpUnknownCommandShowsError(t *testing.T) {
	_, stderr, code := runScript(t, "help foo", "", interpoption.AllowAllCommands().(interp.RunnerOption))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "no help topics match 'foo'")
}

func TestHelpShowsCommandHelp(t *testing.T) {
	stdout, _, code := runScript(t, "help echo", "", interpoption.AllowAllCommands().(interp.RunnerOption))
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "echo: echo [-neE]")
}

func TestHelpFlagPrintsUsage(t *testing.T) {
	stdout, _, code := runScript(t, "help --help", "", interpoption.AllowAllCommands().(interp.RunnerOption))
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout, "Usage: help")
	assert.Contains(t, stdout, "Display help for builtin commands.")
}

func TestHelpUnknownFlagRejected(t *testing.T) {
	_, stderr, code := runScript(t, "help --verbose", "", interpoption.AllowAllCommands().(interp.RunnerOption))
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "help:")
}

// --- Pipeline / composition ---

func TestHelpInPipeline(t *testing.T) {
	stdout, _, code := runScript(t, "help | grep echo", "", interpoption.AllowAllCommands().(interp.RunnerOption))
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "echo")
	assert.Contains(t, stdout, "write arguments to stdout")
}

func TestHelpExitCodeInScript(t *testing.T) {
	stdout, _, code := runScript(t, "help; echo $?", "", interpoption.AllowAllCommands().(interp.RunnerOption))
	assert.Equal(t, 0, code)
	// The last line before the footer should be "0" from echo $?.
	assert.True(t, strings.HasSuffix(strings.TrimSpace(stdout), "0"))
}

func TestHelpFailExitCodeInScript(t *testing.T) {
	stdout, _, code := runScript(t, "help badarg; echo $?", "", interpoption.AllowAllCommands().(interp.RunnerOption))
	assert.Equal(t, 0, code) // overall script exits 0 because echo $? succeeds
	assert.True(t, strings.HasSuffix(strings.TrimSpace(stdout), "1"))
}

// --- Help lists itself ---

func TestHelpListsItself(t *testing.T) {
	stdout, _, code := runScript(t, "help", "", interpoption.AllowAllCommands().(interp.RunnerOption))
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "help")
	assert.Contains(t, stdout, "display help for commands")
}

// --- Empty stderr on success ---

func TestHelpNoStderrOnSuccess(t *testing.T) {
	_, stderr, code := runScript(t, "help", "", interpoption.AllowAllCommands().(interp.RunnerOption))
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
}
