// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package uniq_test

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
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
	_ "github.com/DataDog/rshell/interp/builtins/uniq"
)

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

func runScript(t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return runScriptCtx(context.Background(), t, script, dir, opts...)
}

func cmdRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return runScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(content), 0644))
	return name
}

// --- Default behaviour ---

func TestDefaultRemovesDuplicates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\nb\nb\nc\n")
	stdout, stderr, code := cmdRun(t, "uniq f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "a\nb\nc\n", stdout)
}

func TestDefaultAllUnique(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\nb\nc\n")
	stdout, _, code := cmdRun(t, "uniq f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\nc\n", stdout)
}

func TestDefaultEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "")
	stdout, stderr, code := cmdRun(t, "uniq f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "", stdout)
}

func TestDefaultNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na")
	stdout, _, code := cmdRun(t, "uniq f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n", stdout)
}

func TestDefaultSingleLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "hello\n")
	stdout, _, code := cmdRun(t, "uniq f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
}

func TestDefaultNonAdjacentDuplicates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\nb\na\n")
	stdout, _, code := cmdRun(t, "uniq f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\na\n", stdout)
}

// --- Count flag ---

func TestCountFlag(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\nb\n")
	stdout, _, code := cmdRun(t, "uniq -c f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "      2 a\n      1 b\n", stdout)
}

func TestCountFlagAllUnique(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\nb\n")
	stdout, _, code := cmdRun(t, "uniq -c f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "      1 a\n      1 b\n", stdout)
}

// --- Repeated flag ---

func TestRepeatedFlag(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\nb\n")
	stdout, _, code := cmdRun(t, "uniq -d f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n", stdout)
}

func TestRepeatedFlagNoDuplicates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\nb\n")
	stdout, _, code := cmdRun(t, "uniq -d f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

// --- Unique flag ---

func TestUniqueFlag(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\nb\n")
	stdout, _, code := cmdRun(t, "uniq -u f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "b\n", stdout)
}

func TestUniqueFlagAllDuplicates(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\n")
	stdout, _, code := cmdRun(t, "uniq -u f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

func TestRepeatedAndUniqueFlags(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\nb\n")
	stdout, _, code := cmdRun(t, "uniq -d -u f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

// --- Ignore case ---

func TestIgnoreCase(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "A\na\n")
	stdout, _, code := cmdRun(t, "uniq -i f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "A\n", stdout)
}

func TestIgnoreCaseLongForm(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "A\na\n")
	stdout, _, code := cmdRun(t, "uniq --ignore-case f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "A\n", stdout)
}

func TestCaseSensitiveByDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "A\na\n")
	stdout, _, code := cmdRun(t, "uniq f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "A\na\n", stdout)
}

// --- Skip fields ---

func TestSkipFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a a\nb a\n")
	stdout, _, code := cmdRun(t, "uniq -f1 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a a\n", stdout)
}

func TestSkipFieldsDifferent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a a\nb b\n")
	stdout, _, code := cmdRun(t, "uniq -f 1 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a a\nb b\n", stdout)
}

func TestSkipFieldsTabs(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\ta\na\ta\n")
	stdout, _, code := cmdRun(t, "uniq -f 1 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\ta\n", stdout)
}

func TestSkipTwoFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a a c\nb a c\n")
	stdout, _, code := cmdRun(t, "uniq -f 2 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a a c\n", stdout)
}

// --- Skip chars ---

func TestSkipChars(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "aaa\naaa\n")
	stdout, _, code := cmdRun(t, "uniq -s 1 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "aaa\n", stdout)
}

func TestSkipCharsDifferent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "baa\naaa\n")
	stdout, _, code := cmdRun(t, "uniq -s 2 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "baa\n", stdout)
}

func TestSkipCharsBeyondLength(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "abc\nabcd\n")
	stdout, _, code := cmdRun(t, "uniq -s 4 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "abc\n", stdout)
}

func TestSkipCharsZero(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "abc\nabcd\n")
	stdout, _, code := cmdRun(t, "uniq -s 0 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "abc\nabcd\n", stdout)
}

// --- Check chars ---

func TestCheckChars(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "abc\nabcd\n")
	stdout, _, code := cmdRun(t, "uniq -w 0 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "abc\n", stdout)
}

func TestCheckCharsOne(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a a\nb a\n")
	stdout, _, code := cmdRun(t, "uniq -w 1 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a a\nb a\n", stdout)
}

func TestCheckCharsWithSkipFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a a a\nb a c\n")
	stdout, _, code := cmdRun(t, "uniq -f 1 -w 1 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a a a\n", stdout)
}

// --- Skip fields + skip chars ---

func TestSkipFieldsAndChars(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a aaa\nb ab\n")
	stdout, _, code := cmdRun(t, "uniq -f 1 -s 1 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a aaa\nb ab\n", stdout)
}

func TestSkipFieldsAndCharsEqual(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a aaa\nb aaa\n")
	stdout, _, code := cmdRun(t, "uniq -f 1 -s 1 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a aaa\n", stdout)
}

// --- All-repeated flag (-D) ---

func TestAllRepeatedDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\n")
	stdout, _, code := cmdRun(t, "uniq -D f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\na\n", stdout)
}

func TestAllRepeatedSeparate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\nb\nc\nc\n")
	stdout, _, code := cmdRun(t, "uniq --all-repeated=separate f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\na\n\nc\nc\n", stdout)
}

func TestAllRepeatedPrepend(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\nb\nc\nc\n")
	stdout, _, code := cmdRun(t, "uniq --all-repeated=prepend f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\na\na\n\nc\nc\n", stdout)
}

func TestAllRepeatedPrependNoRepeats(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\nb\n")
	stdout, _, code := cmdRun(t, "uniq --all-repeated=prepend f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

func TestAllRepeatedBadOption(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")
	_, stderr, code := cmdRun(t, "uniq --all-repeated=badoption f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "uniq:")
}

func TestAllRepeatedWithCount(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")
	_, stderr, code := cmdRun(t, "uniq -D -c f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "meaningless")
}

// --- Group flag ---

func TestGroupDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\nb\n")
	stdout, _, code := cmdRun(t, "uniq --group f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\na\n\nb\n", stdout)
}

func TestGroupPrepend(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\nb\n")
	stdout, _, code := cmdRun(t, "uniq --group=prepend f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\na\na\n\nb\n", stdout)
}

func TestGroupAppend(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\nb\n")
	stdout, _, code := cmdRun(t, "uniq --group=append f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\na\n\nb\n\n", stdout)
}

func TestGroupBoth(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\nb\n")
	stdout, _, code := cmdRun(t, "uniq --group=both f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\na\na\n\nb\n\n", stdout)
}

func TestGroupEmptyInput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "")
	stdout, _, code := cmdRun(t, "uniq --group=prepend f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

func TestGroupWithCount(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")
	_, stderr, code := cmdRun(t, "uniq --group -c f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "mutually exclusive")
}

func TestGroupWithRepeated(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")
	_, stderr, code := cmdRun(t, "uniq --group -d f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "mutually exclusive")
}

func TestGroupWithUnique(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")
	_, stderr, code := cmdRun(t, "uniq --group -u f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "mutually exclusive")
}

func TestGroupWithAllRepeated(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")
	_, stderr, code := cmdRun(t, "uniq --group -D f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "mutually exclusive")
}

func TestGroupBadOption(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")
	_, stderr, code := cmdRun(t, "uniq --group=badoption f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "uniq:")
}

// --- Zero-terminated ---

func TestZeroTerminated(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\x00a\x00b")
	stdout, _, code := cmdRun(t, "uniq -z f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\x00b\x00", stdout)
}

func TestZeroTerminatedNewlinesInContent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\n")
	stdout, _, code := cmdRun(t, "uniq -z f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\na\n\x00", stdout)
}

func TestZeroTerminatedLongForm(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\x00a\x00b")
	stdout, _, code := cmdRun(t, "uniq --zero-terminated f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\x00b\x00", stdout)
}

// --- Stdin ---

func TestStdinImplicit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src.txt", "a\na\nb\n")
	stdout, _, code := cmdRun(t, "uniq < src.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\n", stdout)
}

func TestStdinDash(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "src.txt", "a\na\nb\n")
	stdout, _, code := cmdRun(t, "uniq - < src.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\n", stdout)
}

func TestPipeInput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\nb\n")
	stdout, _, code := cmdRun(t, "cat f.txt | uniq", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\n", stdout)
}

// --- Help ---

func TestHelp(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, "uniq --help", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Contains(t, stdout, "Usage:")
}

func TestHelpShort(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, "uniq -h", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage:")
}

// --- Error cases ---

func TestMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "uniq nonexistent.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "uniq:")
}

func TestExtraOperand(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "a\n")
	writeFile(t, dir, "b.txt", "b\n")
	_, stderr, code := cmdRun(t, "uniq a.txt b.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "extra operand")
}

func TestUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "uniq --no-such-flag", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "uniq:")
}

func TestUnknownShortFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "uniq -X", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "uniq:")
}

// --- 8-bit chars and NUL in content ---

func TestEightBitChars(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "ö\nv\n")
	stdout, _, code := cmdRun(t, "uniq f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "ö\nv\n", stdout)
}

func TestNullBytesInContent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\x00a\na\n")
	stdout, _, code := cmdRun(t, "uniq f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\x00a\na\n", stdout)
}

// --- Context cancellation ---

func TestContextCancellation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\nb\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _ = runScriptCtx(ctx, t, "uniq f.txt", dir, interp.AllowedPaths([]string{dir}))
}

// --- CRLF ---

func TestCRLFPreserved(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\r\na\r\nb\r\n")
	stdout, _, code := cmdRun(t, "uniq f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\r\nb\r\n", stdout)
}

// --- Double dash ---

func TestDoubleDash(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "-d", "hello\n")
	stdout, _, code := cmdRun(t, "uniq -- -d", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
}

// --- Outside allowed paths ---

func TestOutsideAllowedPaths(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "uniq /etc/passwd", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "uniq:")
}

// --- Nil stdin ---

func TestNilStdin(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := runScript(t, "uniq", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "", stdout)
}

// --- Abbreviation matching for --all-repeated and --group ---

func TestAllRepeatedAbbrev(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\n")
	stdout, _, code := cmdRun(t, "uniq --all-repeated=s f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\na\n", stdout)
}

func TestGroupAbbrev(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\nb\n")
	stdout, _, code := cmdRun(t, "uniq --group=p f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\na\na\n\nb\n", stdout)
}

// --- All-repeated with -w (check-chars) ---

func TestAllRepeatedWithCheckChars(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a a\na b\n")
	stdout, _, code := cmdRun(t, "uniq -D -w1 f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a a\na b\n", stdout)
}

// --- Group single group cases ---

func TestGroupSingleGroupPrepend(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\n")
	stdout, _, code := cmdRun(t, "uniq --group=prepend f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\na\na\n", stdout)
}

func TestGroupSingleGroupAppend(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\n")
	stdout, _, code := cmdRun(t, "uniq --group=append f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\na\n\n", stdout)
}

func TestGroupSingleGroupSeparate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\na\n")
	stdout, _, code := cmdRun(t, "uniq --group=separate f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\na\n", stdout)
}
