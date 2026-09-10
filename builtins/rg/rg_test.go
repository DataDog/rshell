// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package rg_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

func cmdRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return testutil.RunScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

func cmdRunCtx(ctx context.Context, t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, interp.AllowedPaths([]string{dir}))
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	full := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0644))
	return name
}

const sampleText = "alpha\nbeta\ngamma\n"

// --- Basic matching ---

func TestRgBasicMatchSingleFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", sampleText)
	stdout, _, code := cmdRun(t, "rg beta file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "beta\n", stdout)
}

func TestRgNoMatchExitsOne(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", sampleText)
	stdout, _, code := cmdRun(t, "rg zzz file.txt", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
}

func TestRgMultipleMatchingLines(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "foo\nbar\nfoo baz\nqux\n")
	stdout, _, code := cmdRun(t, "rg foo file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "foo\nfoo baz\n", stdout)
}

func TestRgEmptyFileNoMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "empty.txt", "")
	stdout, _, code := cmdRun(t, "rg foo empty.txt", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
}

func TestRgEmptyPatternMatchesEveryLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "foo\nbar\n")
	stdout, _, code := cmdRun(t, "rg '' file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "foo\nbar\n", stdout)
}

func TestRgNoTrailingNewlinePreserved(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "no newline")
	stdout, _, code := cmdRun(t, "rg newline file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "no newline\n", stdout)
}

func TestRgSingleLineFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "only\n")
	stdout, _, code := cmdRun(t, "rg only file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "only\n", stdout)
}

// --- Multi-file / filename prefixing ---

func TestRgMultipleFilesShowsFilenames(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "foo\n")
	writeFile(t, dir, "b.txt", "foo\n")
	stdout, _, code := cmdRun(t, "rg foo a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a.txt:foo\nb.txt:foo\n", stdout)
}

func TestRgSingleFileNoFilenamePrefix(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "only.txt", "beta\n")
	stdout, _, code := cmdRun(t, "rg beta only.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "beta\n", stdout)
}

func TestRgWithFilenameForcesPrefixOnSingleFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "only.txt", "beta\n")
	stdout, _, code := cmdRun(t, "rg -H beta only.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "only.txt:beta\n", stdout)
}

func TestRgNoFilenameSuppressesPrefix(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "foo\n")
	writeFile(t, dir, "b.txt", "foo\n")
	stdout, _, code := cmdRun(t, "rg -I foo a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "foo\nfoo\n", stdout)
}

func TestRgDirectorySearchAlwaysShowsFilename(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sub/inside.txt", "needle\n")
	stdout, _, code := cmdRun(t, "rg needle sub", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "sub/inside.txt:needle\n", stdout)
}

func TestRgRecursiveDefaultSearchesCurrentDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "top.txt", "needle\n")
	writeFile(t, dir, "sub/nested.txt", "needle\n")
	stdout, _, code := cmdRun(t, "rg needle | sort", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "sub/nested.txt:needle\ntop.txt:needle\n", stdout)
}

// --- Pattern sources: -e, positional, -F ---

func TestRgMultipleEPatterns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", sampleText)
	stdout, _, code := cmdRun(t, "rg -e alpha -e gamma file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "alpha\ngamma\n", stdout)
}

func TestRgFixedStringsLiteralDot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a.b\naxb\n")
	stdout, _, code := cmdRun(t, "rg -F 'a.b' file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a.b\n", stdout)
}

func TestRgDefaultPatternIsRegex(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a.b\naxb\n")
	stdout, _, code := cmdRun(t, "rg 'a.b' file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a.b\naxb\n", stdout)
}

func TestRgInvalidRegexIsError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	stdout, stderr, code := cmdRun(t, "rg '(' file.txt", dir)
	assert.Equal(t, 2, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "invalid regular expression")
}

func TestRgNoPatternIsError(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "rg", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "no pattern")
}

// --- Case handling: -i, -s, -S ---

func TestRgIgnoreCase(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "FOO\nbar\n")
	stdout, _, code := cmdRun(t, "rg -i foo file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "FOO\n", stdout)
}

func TestRgCaseSensitiveByDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "FOO\n")
	_, _, code := cmdRun(t, "rg foo file.txt", dir)
	assert.Equal(t, 1, code)
}

func TestRgSmartCaseLowercasePatternIgnoresCase(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "FOO\n")
	stdout, _, code := cmdRun(t, "rg -S foo file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "FOO\n", stdout)
}

func TestRgSmartCaseUppercasePatternIsSensitive(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "foo\n")
	_, _, code := cmdRun(t, "rg -S Foo file.txt", dir)
	assert.Equal(t, 1, code)
}

func TestRgLastOfCaseSensitiveIgnoreCaseWins(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "FOO\n")
	_, _, code := cmdRun(t, "rg -i -s foo file.txt", dir)
	assert.Equal(t, 1, code, "explicit -s given after -i should restore case-sensitive matching")
}

func TestRgLastOfIgnoreCaseCaseSensitiveWins(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "FOO\n")
	stdout, _, code := cmdRun(t, "rg -s -i foo file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "FOO\n", stdout)
}

// --- Match selection: -v, -w, -x ---

func TestRgInvertMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "foo\nbar\n")
	stdout, _, code := cmdRun(t, "rg -v foo file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "bar\n", stdout)
}

func TestRgWordRegexp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "cat\nconcatenate\n")
	stdout, _, code := cmdRun(t, "rg -w cat file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "cat\n", stdout)
}

func TestRgLineRegexp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "cat\ncats\n")
	stdout, _, code := cmdRun(t, "rg -x cat file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "cat\n", stdout)
}

// --- Output flags: -n, -o, -c, -l, --files-without-match, -q, -m ---

func TestRgLineNumber(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", sampleText)
	stdout, _, code := cmdRun(t, "rg -n beta file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2:beta\n", stdout)
}

func TestRgOnlyMatchingMultiplePerLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "foo bar foo\n")
	stdout, _, code := cmdRun(t, "rg -o foo file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "foo\nfoo\n", stdout)
}

func TestRgOnlyMatchingPrintsEmptyMatches(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "abc\n")
	// "x*" matches the empty string at every position in "abc" (there is
	// no "x"), so -o prints one empty line per position, matching
	// ripgrep's (not GNU grep's) behavior for zero-width matches.
	stdout, _, code := cmdRun(t, "rg -o 'x*' file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\n\n\n\n", stdout)
}

func TestRgCountSingleFileNoFilename(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a\nb\na\n")
	stdout, _, code := cmdRun(t, "rg -c a file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2\n", stdout)
}

func TestRgCountMultiFileShowsFilename(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "x\nx\n")
	writeFile(t, dir, "b.txt", "x\n")
	stdout, _, code := cmdRun(t, "rg -c x a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a.txt:2\nb.txt:1\n", stdout)
}

func TestRgFilesWithMatches(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "hit.txt", "needle\n")
	writeFile(t, dir, "miss.txt", "other\n")
	stdout, _, code := cmdRun(t, "rg -l needle hit.txt miss.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hit.txt\n", stdout)
}

func TestRgFilesWithoutMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "hit.txt", "needle\n")
	writeFile(t, dir, "miss.txt", "other\n")
	stdout, _, code := cmdRun(t, "rg --files-without-match needle hit.txt miss.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "miss.txt\n", stdout)
}

func TestRgQuietSuppressesOutputButExitsZero(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "needle\n")
	stdout, _, code := cmdRun(t, "rg -q needle file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

func TestRgQuietNoMatchExitsOne(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "other\n")
	_, _, code := cmdRun(t, "rg -q needle file.txt", dir)
	assert.Equal(t, 1, code)
}

func TestRgMaxCountLimitsMatches(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a\na\na\n")
	stdout, _, code := cmdRun(t, "rg -m 2 a file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\na\n", stdout)
}

func TestRgMaxCountZeroNoMatches(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a\n")
	_, _, code := cmdRun(t, "rg -m 0 a file.txt", dir)
	assert.Equal(t, 1, code)
}

// --- Context: -A, -B, -C ---

func TestRgAfterContext(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "1\n2\nmatch\n4\n5\n")
	stdout, _, code := cmdRun(t, "rg -A2 match file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "match\n4\n5\n", stdout)
}

func TestRgBeforeContext(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "1\n2\nmatch\n4\n")
	stdout, _, code := cmdRun(t, "rg -B2 match file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n2\nmatch\n", stdout)
}

func TestRgContextBothSides(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "1\n2\nmatch\n4\n5\n")
	stdout, _, code := cmdRun(t, "rg -C1 match file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2\nmatch\n4\n", stdout)
}

func TestRgContextGroupSeparator(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "match1\ngap1\ngap2\ngap3\nmatch2\n")
	stdout, _, code := cmdRun(t, "rg -A1 match file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "match1\ngap1\n--\nmatch2\n", stdout)
}

func TestRgAAfterCOverridesAfterOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "1\n2\nmatch\n4\n5\n")
	// -A2 -C1: -A explicitly set wins over -C for the "after" side, -C1
	// still applies for "before" since -B was not explicitly given.
	stdout, _, code := cmdRun(t, "rg -A2 -C1 match file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2\nmatch\n4\n5\n", stdout)
}

func TestRgOnlyMatchingSuppressesContext(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "1\nmatch\n3\n")
	stdout, _, code := cmdRun(t, "rg -o -C1 match file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "match\n", stdout)
}

// --- Text/binary handling: -a ---

func TestRgBinaryFileReportsMatchWithoutContent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bin.dat", "abc\x00def\n")
	stdout, stderr, code := cmdRun(t, "rg abc bin.dat", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "binary file matches")
}

func TestRgTextFlagForcesBinarySearch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bin.dat", "abc\x00def\n")
	stdout, _, code := cmdRun(t, "rg -a abc bin.dat", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "abc")
}

// --- Glob and hidden-file filtering: -g, --hidden, --files ---

func TestRgFilesListsSearchableFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "x")
	writeFile(t, dir, "sub/b.txt", "y")
	stdout, _, code := cmdRun(t, "rg --files | sort", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a.txt\nsub/b.txt\n", stdout)
}

func TestRgGlobIncludeExtension(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "keep.txt", "hit\n")
	writeFile(t, dir, "skip.log", "hit\n")
	stdout, _, code := cmdRun(t, "rg hit -g '*.txt'", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "keep.txt:hit\n", stdout)
}

func TestRgGlobExcludeNegation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "keep.txt", "hit\n")
	writeFile(t, dir, "excluded.txt", "hit\n")
	stdout, _, code := cmdRun(t, "rg hit -g '!excluded.txt' | sort", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "keep.txt:hit\n", stdout)
}

func TestRgHiddenExcludedByDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".hidden.txt", "secret\n")
	writeFile(t, dir, "visible.txt", "secret\n")
	stdout, _, code := cmdRun(t, "rg secret", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "visible.txt:secret\n", stdout)
}

func TestRgHiddenFlagIncludesDotfiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".hidden.txt", "secret\n")
	stdout, _, code := cmdRun(t, "rg --hidden secret", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, ".hidden.txt:secret\n", stdout)
}

// --- stdin ---

func TestRgStdinPiped(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `echo "hello world" | rg hello`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\n", stdout)
}

func TestRgStdinExplicitDash(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `echo "hello world" | rg hello -`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\n", stdout)
}

// --- Errors ---

func TestRgMissingFileIsError(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, "rg foo does-not-exist.txt", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "does-not-exist.txt")
}

func TestRgDirectoryOutsideAllowedPathsIsBlocked(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir()
	writeFile(t, other, "secret.txt", "root\n")
	_, stderr, code := cmdRun(t, "rg root "+other, dir)
	assert.Equal(t, 2, code)
	assert.NotEmpty(t, stderr)
}

// --- Rejected flags (would require executing external processes) ---

func TestRgPreFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	_, stderr, code := cmdRun(t, "rg --pre cat foo file.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "unrecognized option")
}

func TestRgHostnameBinFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	_, stderr, code := cmdRun(t, "rg --hostname-bin hostname foo file.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "unrecognized option")
}

func TestRgJSONFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	_, _, code := cmdRun(t, "rg --json foo file.txt", dir)
	assert.Equal(t, 1, code)
}

func TestRgFollowFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	_, _, code := cmdRun(t, "rg -L foo file.txt", dir)
	assert.Equal(t, 1, code)
}

func TestRgFollowLongFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	_, _, code := cmdRun(t, "rg --follow foo file.txt", dir)
	assert.Equal(t, 1, code)
}

func TestRgPreGlobFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	_, stderr, code := cmdRun(t, "rg --pre-glob '*.pdf' foo file.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "unrecognized option")
}

func TestRgSearchZipFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	_, stderr, code := cmdRun(t, "rg -z foo file.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "invalid option")
}

func TestRgSearchZipLongFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	_, _, code := cmdRun(t, "rg --search-zip foo file.txt", dir)
	assert.Equal(t, 1, code)
}

func TestRgTypeFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	_, stderr, code := cmdRun(t, "rg -t txt foo file.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "invalid option")
}

func TestRgTypeNotFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	_, _, code := cmdRun(t, "rg -T txt foo file.txt", dir)
	assert.Equal(t, 1, code)
}

func TestRgFileFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	writeFile(t, dir, "patterns.txt", "foo\n")
	_, stderr, code := cmdRun(t, "rg -f patterns.txt file.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "invalid option")
}

func TestRgMultilineFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	_, _, code := cmdRun(t, "rg -U foo file.txt", dir)
	assert.Equal(t, 1, code)
}

func TestRgPcre2FlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	_, stderr, code := cmdRun(t, "rg -P foo file.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "invalid option")
}

func TestRgEngineFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	_, _, code := cmdRun(t, "rg --engine auto foo file.txt", dir)
	assert.Equal(t, 1, code)
}

func TestRgReplaceFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "foo\n")
	_, stderr, code := cmdRun(t, "rg --replace bar foo file.txt", dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "unrecognized option")
}

func TestRgSortFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	_, _, code := cmdRun(t, "rg --sort path foo file.txt", dir)
	assert.Equal(t, 1, code)
}

func TestRgSortrFlagRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	_, _, code := cmdRun(t, "rg --sortr path foo file.txt", dir)
	assert.Equal(t, 1, code)
}

// --- Help ---

func TestRgHelpPrintsUsageAndExitsZero(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, "rg --help", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Contains(t, stdout, "Usage: rg")
	assert.Contains(t, stdout, "--glob")
}

func TestRgShortHelpFlag(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, "rg -h", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage: rg")
}

// --- No-config no-op ---

func TestRgNoConfigIsAccepted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "beta\n")
	stdout, _, code := cmdRun(t, "rg --no-config beta file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "beta\n", stdout)
}

// --- Symlink safety ---

func TestRgSymlinkNotFollowedDuringTraversal(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "real.txt", "needle\n")
	require.NoError(t, os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "link.txt")))
	stdout, _, code := cmdRun(t, "rg --files | sort", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "real.txt\n", stdout)
}

func TestRgFollowsExplicitSymlinkArgument(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "real.txt", "needle\n")
	require.NoError(t, os.Symlink(filepath.Join(dir, "real.txt"), filepath.Join(dir, "link.txt")))
	// Unlike directory traversal (which never follows symlinks), an
	// explicitly named symlink operand is a read and is followed, per
	// RULES.md's "follow symlinks for read operations" rule.
	stdout, _, code := cmdRun(t, "rg needle link.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "needle\n", stdout)
}

// --- Memory-safety-driven correctness (RULES.md: bounded buffers) ---

func TestRgLineJustUnderMaxLineBytesSucceeds(t *testing.T) {
	dir := t.TempDir()
	// "needle" (6 bytes) + filler + "\n", totalling just under the 1 MiB
	// MaxLineBytes scanner cap.
	filler := strings.Repeat("a", 1<<20-8)
	writeFile(t, dir, "file.txt", "needle"+filler+"\n")
	_, _, code := cmdRun(t, "rg needle file.txt", dir)
	assert.Equal(t, 0, code)
}

func TestRgLineOverMaxLineBytesErrors(t *testing.T) {
	dir := t.TempDir()
	line := strings.Repeat("a", 1<<21) + "\n" // well over MaxLineBytes
	writeFile(t, dir, "file.txt", line)
	_, stderr, code := cmdRun(t, "rg a file.txt", dir)
	assert.Equal(t, 2, code)
	assert.NotEmpty(t, stderr)
}

func TestRgMaxCountLargeValueClampedNotOOM(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a\na\n")
	stdout, _, code := cmdRun(t, "rg -m 2147483647 a file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\na\n", stdout)
}

func TestRgAfterContextClampedToMax(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "match\n"+strings.Repeat("x\n", 10))
	stdout, _, code := cmdRun(t, "rg -A 999999999 match file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "match\n"+strings.Repeat("x\n", 10), stdout)
}

// --- Context cancellation ---

func TestRgRespectsContextCancellation(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < 200000; i++ {
		b.WriteString("line\n")
	}
	writeFile(t, dir, "big.txt", b.String())

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	_, _, code := cmdRunCtx(ctx, t, "rg line big.txt", dir)
	// Either it completes fast enough (0) or is cancelled (non-panicking
	// nonzero code); the key assertion is that this returns promptly
	// rather than hanging, which the test timeout would otherwise catch.
	_ = code
}

// --- 200+ file arguments (FD leak / resource check) ---

func TestRgManyFileArguments(t *testing.T) {
	dir := t.TempDir()
	var names []string
	for i := 0; i < 200; i++ {
		name := "f" + strconv.Itoa(i) + ".txt"
		writeFile(t, dir, name, "needle\n")
		names = append(names, name)
	}
	stdout, _, code := cmdRun(t, "rg needle "+strings.Join(names, " "), dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, 200, strings.Count(stdout, "needle"))
}
