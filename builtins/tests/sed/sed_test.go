// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package sed_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/rshell/interp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"
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
	allOpts := append([]interp.RunnerOption{interp.StdIO(nil, &outBuf, &errBuf), interp.AllowAllCommands()}, opts...)
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

func cmdRun(t *testing.T, script, dir string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644)
	require.NoError(t, err)
}

func setupDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		writeFile(t, dir, name, content)
	}
	return dir
}

// --- Basic Substitution ---

func TestSubstituteBasic(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello world\n",
	})
	stdout, _, code := cmdRun(t, `sed 's/world/earth/' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello earth\n", stdout)
}

func TestSubstituteGlobal(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "aaa bbb aaa\n",
	})
	stdout, _, code := cmdRun(t, `sed 's/aaa/zzz/g' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "zzz bbb zzz\n", stdout)
}

func TestSubstituteNth(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "ab ab ab ab\n",
	})
	stdout, _, code := cmdRun(t, `sed 's/ab/XY/2' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "ab XY ab ab\n", stdout)
}

func TestSubstituteCaseInsensitive(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "Hello HELLO hello\n",
	})
	stdout, _, code := cmdRun(t, `sed 's/hello/bye/i' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "bye HELLO hello\n", stdout)
}

func TestSubstituteAlternateDelimiter(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "/usr/local/bin\n",
	})
	stdout, _, code := cmdRun(t, `sed 's|/usr/local|/opt|' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "/opt/bin\n", stdout)
}

func TestSubstituteAmpersand(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello\n",
	})
	stdout, _, code := cmdRun(t, `sed 's/hello/[&]/' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "[hello]\n", stdout)
}

func TestSubstituteEmptyPattern(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello\n",
	})
	stdout, _, code := cmdRun(t, `sed 's/^/prefix: /' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "prefix: hello\n", stdout)
}

func TestSubstituteWithPrint(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "aaa\nbbb\naaa\n",
	})
	stdout, _, code := cmdRun(t, `sed -n 's/aaa/zzz/p' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "zzz\nzzz\n", stdout)
}

// --- Print and Output ---

func TestPrint(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "line1\nline2\n",
	})
	stdout, _, code := cmdRun(t, `sed 'p' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line1\nline1\nline2\nline2\n", stdout)
}

func TestSuppressAutoPrint(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "line1\nline2\nline3\n",
	})
	stdout, _, code := cmdRun(t, `sed -n 'p' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line1\nline2\nline3\n", stdout)
}

func TestLineNumber(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "aaa\nbbb\nccc\n",
	})
	stdout, _, code := cmdRun(t, `sed '=' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\naaa\n2\nbbb\n3\nccc\n", stdout)
}

func TestPrintUnambiguous(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello\tworld\n",
	})
	stdout, _, code := cmdRun(t, `sed -n 'l' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\\tworld$\n", stdout)
}

// --- Delete ---

func TestDeleteBasic(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "line1\nline2\nline3\n",
	})
	stdout, _, code := cmdRun(t, `sed '2d' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line1\nline3\n", stdout)
}

func TestDeleteRange(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "line1\nline2\nline3\nline4\nline5\n",
	})
	stdout, _, code := cmdRun(t, `sed '2,4d' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line1\nline5\n", stdout)
}

// --- Addressing ---

func TestAddressLine(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "first\nsecond\nthird\n",
	})
	stdout, _, code := cmdRun(t, `sed '2s/second/SECOND/' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "first\nSECOND\nthird\n", stdout)
}

func TestAddressLastLine(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "first\nsecond\nthird\n",
	})
	stdout, _, code := cmdRun(t, `sed '$s/third/THIRD/' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "first\nsecond\nTHIRD\n", stdout)
}

func TestAddressRegex(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "apple\nbanana\ncherry\n",
	})
	stdout, _, code := cmdRun(t, `sed '/banana/d' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "apple\ncherry\n", stdout)
}

func TestAddressRange(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "1\n2\n3\n4\n5\n",
	})
	stdout, _, code := cmdRun(t, `sed -n '2,4p' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2\n3\n4\n", stdout)
}

func TestAddressRegexRange(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "start\nmiddle1\nmiddle2\nend\nafter\n",
	})
	stdout, _, code := cmdRun(t, `sed -n '/start/,/end/p' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "start\nmiddle1\nmiddle2\nend\n", stdout)
}

func TestAddressNegation(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "keep\ndelete\nkeep\n",
	})
	stdout, _, code := cmdRun(t, `sed '/keep/!d' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "keep\nkeep\n", stdout)
}

func TestAddressStep(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "1\n2\n3\n4\n5\n6\n",
	})
	stdout, _, code := cmdRun(t, `sed -n '1~2p' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n3\n5\n", stdout)
}

// --- Text Commands ---

func TestAppend(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "line1\nline2\n",
	})
	stdout, _, code := cmdRun(t, `sed '1a\appended' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line1\nappended\nline2\n", stdout)
}

func TestInsert(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "line1\nline2\n",
	})
	stdout, _, code := cmdRun(t, `sed '2i\inserted' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line1\ninserted\nline2\n", stdout)
}

func TestChange(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "line1\nline2\nline3\n",
	})
	stdout, _, code := cmdRun(t, `sed '2c\changed' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line1\nchanged\nline3\n", stdout)
}

// --- Hold Space ---

func TestHoldCopy(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "first\nsecond\n",
	})
	// Copy first line to hold space, on second line replace pattern with hold
	stdout, _, code := cmdRun(t, `sed -n '1h;2{g;p}' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "first\n", stdout)
}

func TestHoldAppend(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "a\nb\nc\n",
	})
	// Accumulate all lines in hold space, print at end
	stdout, _, code := cmdRun(t, `sed -n 'H;${g;p}' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\na\nb\nc\n", stdout)
}

func TestExchange(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "pattern\n",
	})
	// Exchange swaps pattern space (content) with hold space (initially empty)
	stdout, _, code := cmdRun(t, `sed -n 'x;p' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\n", stdout)
}

// --- Branching ---

func TestBranch(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello\n",
	})
	// b with no label branches to end of script, skipping subsequent commands
	stdout, _, code := cmdRun(t, `sed 'b;s/hello/bye/' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
}

func TestBranchLabel(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello\n",
	})
	stdout, _, code := cmdRun(t, "sed 'b skip;s/hello/bye/;:skip' input.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
}

func TestBranchConditional(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "aXb\n",
	})
	// t branches if substitution was made
	stdout, _, code := cmdRun(t, `sed 's/X/Y/;t done;s/a/Z/;:done' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "aYb\n", stdout)
}

func TestBranchConditionalNoSub(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello\nworld\n",
	})
	// T branches if NO substitution was made.
	// On "hello": s/hello/HI/ succeeds, T does not branch, s/HI/BYE/ runs → "BYE"
	// On "world": s/hello/HI/ fails, T branches to done, s/HI/BYE/ skipped → "world"
	stdout, _, code := cmdRun(t, `sed 's/hello/HI/;T done;s/HI/BYE/;:done' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "BYE\nworld\n", stdout)
}

// --- Next Line ---

func TestNext(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "line1\nline2\nline3\nline4\n",
	})
	// n prints current line (unless -n), reads next line into pattern space
	stdout, _, code := cmdRun(t, `sed -n 'n;p' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line2\nline4\n", stdout)
}

func TestNextAppend(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "line1\nline2\n",
	})
	// N appends next line to pattern space with embedded newline
	stdout, _, code := cmdRun(t, `sed -n 'N;p' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "line1")
	assert.Contains(t, stdout, "line2")
}

// --- Transliterate ---

func TestTransliterate(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello\n",
	})
	stdout, _, code := cmdRun(t, `sed 'y/helo/HELO/' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "HELLO\n", stdout)
}

// --- Quit ---

func TestQuit(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "line1\nline2\nline3\n",
	})
	// q prints current line then exits
	stdout, _, code := cmdRun(t, `sed '2q' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line1\nline2\n", stdout)
}

func TestQuitNoPrint(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "line1\nline2\nline3\n",
	})
	// Q exits without printing current line
	stdout, _, code := cmdRun(t, `sed '2Q' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "line1\n", stdout)
}

// --- Multiple Expressions ---

func TestMultipleExpressions(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello world\n",
	})
	stdout, _, code := cmdRun(t, `sed -e 's/hello/hi/' -e 's/world/earth/' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hi earth\n", stdout)
}

func TestSemicolonSeparator(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello world\n",
	})
	stdout, _, code := cmdRun(t, `sed 's/hello/hi/;s/world/earth/' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hi earth\n", stdout)
}

// --- Extended Regex ---

func TestExtendedRegex(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "abc123def\n",
	})
	stdout, _, code := cmdRun(t, `sed -E 's/[0-9]+/NUM/' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "abcNUMdef\n", stdout)
}

func TestExtendedRegexR(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "abc123def\n",
	})
	stdout, _, code := cmdRun(t, `sed -r 's/[0-9]+/NUM/' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "abcNUMdef\n", stdout)
}

// --- Stdin ---

func TestStdinPipe(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello world\n",
	})
	stdout, _, code := cmdRun(t, `cat input.txt | sed 's/world/earth/'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello earth\n", stdout)
}

func TestStdinDash(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello world\n",
	})
	stdout, _, code := cmdRun(t, `cat input.txt | sed 's/world/earth/' -`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello earth\n", stdout)
}

// --- Edge Cases ---

func TestEmptyFile(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "",
	})
	stdout, _, code := cmdRun(t, `sed 's/a/b/' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

func TestSingleLine(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "only line\n",
	})
	stdout, _, code := cmdRun(t, `sed 's/only/single/' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "single line\n", stdout)
}

func TestNoTrailingNewline(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "no newline",
	})
	stdout, _, code := cmdRun(t, `sed 's/no/with/' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "with newline\n", stdout)
}

func TestMultipleFiles(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"a.txt": "alpha\n",
		"b.txt": "beta\n",
	})
	stdout, _, code := cmdRun(t, `sed 's/^/> /' a.txt b.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "> alpha\n> beta\n", stdout)
}

// --- Error Cases ---

func TestMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `sed 's/a/b/' nonexistent.txt`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "sed:")
}

func TestNoScript(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `sed`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "sed:")
}

func TestInvalidRegex(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello\n",
	})
	_, stderr, code := cmdRun(t, `sed 's/[invalid/replacement/' input.txt`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "sed:")
}

func TestBlockedWriteCommand(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello\n",
	})
	_, stderr, code := cmdRun(t, `sed 'w output.txt' input.txt`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "blocked")
}

func TestBlockedExecuteCommand(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello\n",
	})
	_, stderr, code := cmdRun(t, `sed 'e' input.txt`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "blocked")
}

func TestBlockedInPlaceFlag(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello\n",
	})
	_, stderr, code := cmdRun(t, `sed -i 's/hello/bye/' input.txt`, dir)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "sed:")
}

func TestBlockedReadCommand(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello\n",
	})
	_, stderr, code := cmdRun(t, `sed 'r other.txt' input.txt`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "blocked")
}

func TestBlockedWriteFlag(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"input.txt": "hello\n",
	})
	_, stderr, code := cmdRun(t, `sed 's/hello/bye/w output.txt' input.txt`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "blocked")
}

// --- Help ---

func TestHelp(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `sed --help`, dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage:")
}
