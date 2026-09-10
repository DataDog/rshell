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

// TestRgMultipleEPatternsInlineFlagDoesNotLeak verifies that an inline
// regex flag such as "(?i)" in one -e pattern does not leak into other -e
// alternatives joined after it. Each -e pattern must behave as an
// independently compiled, independently scoped regex, matching ripgrep.
func TestRgMultipleEPatternsInlineFlagDoesNotLeak(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "B\n")
	_, _, code := cmdRun(t, `rg -e '(?i)a' -e b file.txt`, dir)
	assert.Equal(t, 1, code, `"(?i)" scoped to the "a" alternative must not also case-fold the "b" alternative`)
}

func TestRgFixedStringsLiteralDot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a.b\naxb\n")
	stdout, _, code := cmdRun(t, "rg -F 'a.b' file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a.b\n", stdout)
}

// TestRgFixedStringsSmartCaseInspectsRawLiterals verifies that -F combined
// with -S inspects the pattern's raw literal runes for smart-case
// purposes, not regex escape syntax: a fixed-string pattern like "\A" is
// two literal characters (backslash, 'A'), not a regex anchor, and the
// literal uppercase 'A' must force case-sensitive matching.
func TestRgFixedStringsSmartCaseInspectsRawLiterals(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", `\a`+"\n")
	_, _, code := cmdRun(t, `rg -F -S '\A' file.txt`, dir)
	assert.Equal(t, 1, code, `-F -S "\A" must stay case-sensitive since 'A' is a literal uppercase character, not a regex anchor`)
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

// TestRgSmartCaseUnicodePropertyEscapeIsNotUppercaseLiteral verifies that a
// Unicode property escape like \pL is treated as regex syntax, not a
// literal uppercase character, for smart-case purposes — matching real
// ripgrep. A naive raw-byte scan for ASCII uppercase would incorrectly see
// the 'L' in \pL and force case-sensitive matching.
func TestRgSmartCaseUnicodePropertyEscapeIsNotUppercaseLiteral(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "upper.txt", "FOOx\n")
	writeFile(t, dir, "lower.txt", "fooX\n")
	stdout, _, code := cmdRun(t, `rg -S 'foo\pL' upper.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "FOOx\n", stdout)
	stdout, _, code = cmdRun(t, `rg -S 'foo\pL' lower.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "fooX\n", stdout)
}

// TestRgSmartCasePerlShorthandIsNotUppercaseLiteral verifies that Perl
// class shorthands like \w and \S are not mistaken for literal uppercase
// characters, matching real ripgrep.
func TestRgSmartCasePerlShorthandIsNotUppercaseLiteral(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "FOObar\n")
	stdout, _, code := cmdRun(t, `rg -S 'foo\w+' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "FOObar\n", stdout)
}

// TestRgSmartCaseCharClassRangeIsUppercaseLiteral verifies that an explicit
// character class range such as [A-Z] still counts as containing an
// uppercase literal (unlike \w or \pL), matching real ripgrep: it forces
// case-sensitive matching, so a lowercase-only line is not matched.
func TestRgSmartCaseCharClassRangeIsUppercaseLiteral(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "upper.txt", "FOO\n")
	writeFile(t, dir, "lower.txt", "foo\n")
	_, _, code := cmdRun(t, `rg -S 'foo[A-Z]?' upper.txt`, dir)
	assert.Equal(t, 1, code, "foo[A-Z]? contains an uppercase literal range, so smart-case must stay case-sensitive")
	stdout, _, code := cmdRun(t, `rg -S 'foo[A-Z]?' lower.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "foo\n", stdout)
}

// TestRgSmartCaseUnicodeUppercaseLiteral verifies that a literal Unicode
// uppercase character (not just ASCII A-Z) still triggers case-sensitive
// smart-case matching, matching real ripgrep.
func TestRgSmartCaseUnicodeUppercaseLiteral(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "café\n")
	_, _, code := cmdRun(t, `rg -S 'CAFÉ' file.txt`, dir)
	assert.Equal(t, 1, code, "the literal É should be treated as an uppercase literal, keeping matching case-sensitive")
}

// TestRgSmartCaseNamedCaptureNotUppercaseLiteral verifies that the 'P' in
// Go's named-capture syntax "(?P<name>...)" is treated as regex syntax, not
// a literal uppercase character, for smart-case purposes — matching real
// ripgrep.
func TestRgSmartCaseNamedCaptureNotUppercaseLiteral(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "FOO\n")
	stdout, _, code := cmdRun(t, `rg -S '(?P<x>foo)' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "FOO\n", stdout)
}

// TestRgSmartCaseInlineFlagGroupNotUppercaseLiteral verifies that an
// inline-flag group like "(?U)" is treated as regex syntax, not a literal
// uppercase character.
func TestRgSmartCaseInlineFlagGroupNotUppercaseLiteral(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "FOOOO\n")
	stdout, _, code := cmdRun(t, `rg -S '(?U)fo+' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "FOOOO\n", stdout)
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

// TestRgWordThenLineRegexpLastWins verifies that -x given after -w performs
// whole-line matching (last flag wins), matching real ripgrep.
// TestRgWordRegexpUnicodeSingleCharWord verifies that -w matches a
// non-ASCII single-character "word" like "é", which requires Unicode-aware
// (not ASCII-only) word-boundary detection. Go's built-in \b only
// recognizes ASCII word characters, so "é" would otherwise never satisfy a
// boundary on either side.
func TestRgWordRegexpUnicodeSingleCharWord(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "é\n")
	stdout, _, code := cmdRun(t, "rg -w é file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "é\n", stdout)
}

// TestRgWordRegexpUnicodeCombiningMarkIsWordChar verifies that a
// combining mark (e.g. U+0301 COMBINING ACUTE ACCENT in NFD-decomposed
// "e\u0301") is treated as part of the same word as its base letter, per
// ripgrep's (Rust regex's) Unicode word-character definition. Without
// this, searching for the bare base letter "e" would spuriously satisfy a
// word boundary in the middle of the composed character.
func TestRgWordRegexpUnicodeCombiningMarkIsWordChar(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "e\u0301\n") // NFD "é": 'e' + combining acute accent
	_, _, code := cmdRun(t, "rg -w e file.txt", dir)
	assert.Equal(t, 1, code, "the combining mark following 'e' must count as a word character, so 'e' alone must not satisfy a word boundary here")
}

// TestRgWordRegexpUnicodeWordWithASCIIBoundary verifies -w on a
// multi-byte-per-rune word ("café") flanked by ASCII space boundaries.
func TestRgWordRegexpUnicodeWordWithASCIIBoundary(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "café test\n")
	stdout, _, code := cmdRun(t, "rg -w café file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "café test\n", stdout)
}

// TestRgWordRegexpWithOnlyMatching verifies -w composes correctly with -o,
// exercising the matchIndices path (not just matchAny).
func TestRgWordRegexpWithOnlyMatching(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "foo bar foo\n")
	stdout, _, code := cmdRun(t, "rg -w -o foo file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "foo\nfoo\n", stdout)
}

// TestRgWordRegexpHalfBoundaryMatchesPunctuationFlanked verifies that -w
// uses ripgrep's "half boundary" semantics (only the context OUTSIDE the
// match is checked; the match's own first/last rune need not itself be a
// word character), not ordinary \bPATTERN\b, which would incorrectly
// reject a pattern like "-2" flanked by punctuation on both sides.
func TestRgWordRegexpHalfBoundaryMatchesPunctuationFlanked(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "(-2)\n")
	stdout, _, code := cmdRun(t, "rg -w -e -2 file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "(-2)\n", stdout)
}

// TestRgWordRegexpHalfBoundaryRejectsEmbeddedWord verifies the other half
// of the same rule still holds: -w must still reject a match embedded
// inside a larger word.
func TestRgWordRegexpHalfBoundaryRejectsEmbeddedWord(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "abcfoo\n")
	_, _, code := cmdRun(t, "rg -w foo file.txt", dir)
	assert.Equal(t, 1, code)
}

func TestRgWordThenLineRegexpLastWins(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a b\n")
	_, _, code := cmdRun(t, "rg -w -x a file.txt", dir)
	assert.Equal(t, 1, code, "-x given after -w should perform whole-line matching, rejecting 'a b'")
}

// TestRgLineThenWordRegexpLastWins verifies that -w given after -x performs
// word matching (last flag wins), matching real ripgrep.
func TestRgLineThenWordRegexpLastWins(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a b\n")
	stdout, _, code := cmdRun(t, "rg -x -w a file.txt", dir)
	assert.Equal(t, 0, code, "-w given after -x should perform word matching, accepting 'a b'")
	assert.Equal(t, "a b\n", stdout)
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

// TestRgCountOmitsZeroMatchFiles verifies ripgrep's default -c behavior:
// a file with zero matches is omitted entirely (no "file:0" line), unlike
// GNU grep, which always prints a count line. ripgrep's separate
// --include-zero flag (not implemented here) restores that line.
func TestRgCountOmitsZeroMatchFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "hit.txt", "a\n")
	writeFile(t, dir, "miss.txt", "x\n")
	stdout, _, code := cmdRun(t, "rg -c a hit.txt miss.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hit.txt:1\n", stdout)
}

// TestRgCountSingleZeroMatchFileNoOutput verifies the single-file case: no
// "0" line is printed, and the exit code is still 1 (no match).
func TestRgCountSingleZeroMatchFileNoOutput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "miss.txt", "x\n")
	stdout, _, code := cmdRun(t, "rg -c a miss.txt", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
}

func TestRgFilesWithMatches(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "hit.txt", "needle\n")
	writeFile(t, dir, "miss.txt", "other\n")
	stdout, _, code := cmdRun(t, "rg -l needle hit.txt miss.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hit.txt\n", stdout)
}

// TestRgFilesWithMatchesStopsAtFirstMatch reproduces a real hang: -l's
// boolean result is fully determined by the first match, so rg must not
// keep scanning to EOF. Without the fix, `yes` never terminates.
func TestRgFilesWithMatchesStopsAtFirstMatch(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stdout, _, code := cmdRunCtx(ctx, t, "{ printf 'match\\n'; yes no; } | rg -l match -", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "(standard input)\n", stdout)
}

func TestRgFilesWithoutMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "hit.txt", "needle\n")
	writeFile(t, dir, "miss.txt", "other\n")
	stdout, _, code := cmdRun(t, "rg --files-without-match needle hit.txt miss.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "miss.txt\n", stdout)
}

// TestRgFilesWithoutMatchStopsAtFirstMatch mirrors the -l case: as soon as
// any match is found the file is known not to qualify for
// --files-without-match, so scanning must stop immediately.
func TestRgFilesWithoutMatchStopsAtFirstMatch(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, code := cmdRunCtx(ctx, t, "{ printf 'match\\n'; yes no; } | rg --files-without-match match -", dir)
	assert.Equal(t, 1, code)
}

// TestRgFilesWithoutMatchExitCodeIsInverted verifies that
// --files-without-match's exit status reflects whether any file qualified
// (i.e. had zero matches), not whether any match was found. A single file
// that contains only matches has nothing to report, so the command must
// exit 1 even though matches were found in the underlying scan.
func TestRgFilesWithoutMatchExitCodeIsInverted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "hit.txt", "match\n")
	_, _, code := cmdRun(t, "rg --files-without-match match hit.txt", dir)
	assert.Equal(t, 1, code, "a file containing only matches has no --files-without-match output, so exit status must be 1")
}

// TestRgFilesWithoutMatchQuietSuppressesFilenameOutput verifies that -q
// suppresses ALL stdout, including --files-without-match's filename line
// at EOF for a genuinely nonmatching file — the exit status alone reports
// the result under -q.
func TestRgFilesWithoutMatchQuietSuppressesFilenameOutput(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "miss.txt", "other\n")
	stdout, _, code := cmdRun(t, "rg -q --files-without-match z miss.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

// TestRgFilesWithoutMatchQuietExitCodeIsInverted verifies the same
// inversion applies when combined with -q/--quiet.
func TestRgFilesWithoutMatchQuietExitCodeIsInverted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "hit.txt", "match\n")
	writeFile(t, dir, "miss.txt", "other\n")
	_, _, hitCode := cmdRun(t, "rg -q --files-without-match match hit.txt", dir)
	assert.Equal(t, 1, hitCode)
	_, _, missCode := cmdRun(t, "rg -q --files-without-match match miss.txt", dir)
	assert.Equal(t, 0, missCode)
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

// TestRgMaxCountZeroStopsOnNonMatchingInfiniteStream reproduces a real hang:
// -m 0 means "don't search anything" (ripgrep's documented behavior), so it
// must short-circuit before scanning any input, even input that never
// matches. Without the fix, an infinite non-matching stream would be read to
// EOF for a result ("no match") that was already fully determined.
func TestRgMaxCountZeroStopsOnNonMatchingInfiniteStream(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, code := cmdRunCtx(ctx, t, "yes zzz | rg -m 0 no", dir)
	assert.Equal(t, 1, code)
}

// TestRgMaxCountZeroFilesWithoutMatchNeverReports verifies that -m 0 means
// "never searched", not "confirmed zero matches": --files-without-match
// must not report a file as qualifying just because it was skipped, since
// that would misrepresent an unsearched file as a negative match result.
func TestRgMaxCountZeroFilesWithoutMatchNeverReports(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "z\n")
	stdout, _, code := cmdRun(t, "rg -m0 --files-without-match z file.txt", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
}

func TestRgMaxCountNegativeRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a\n")
	_, stderr, code := cmdRun(t, "rg --max-count=-2 a file.txt", dir)
	assert.Equal(t, 2, code)
	assert.NotEmpty(t, stderr)
}

// TestRgMaxCountLimitStillPrintsMatchesWithinContextWindow verifies
// ripgrep's documented -m/-A interaction: once the limit is reached, a
// further match line that falls inside the still-open trailing-context
// window from the limiting match is still printed (with match formatting)
// and consumes one unit of that window, rather than being dropped or
// reopening a new window.
func TestRgMaxCountLimitStillPrintsMatchesWithinContextWindow(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\ny\nx\ny\nx\n")
	stdout, _, code := cmdRun(t, "rg -n -m1 -A3 x file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1:x\n2-y\n3:x\n4-y\n", stdout)
}

// TestRgMaxCountLimitWithOnlyMatchingAppliesOFormatting verifies that a
// match landing inside an already-open -m trailing-context window still
// applies -o's "isolate each matched substring" formatting, rather than
// falling back to printing the whole line.
func TestRgMaxCountLimitWithOnlyMatchingAppliesOFormatting(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\nxx\nno\n")
	// Verified directly against real ripgrep 15.1.0: three isolated "x"
	// records (one for the limiting match on line 1, two for the two
	// occurrences of "x" within "xx" on line 2, which falls inside the
	// still-open -A2 window), followed by the plain context line "no".
	stdout, _, code := cmdRun(t, "rg -m1 -A2 -o x file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "x\nx\nx\nno\n", stdout)
}

// TestRgMaxCountLimitDoesNotReopenClosedWindow verifies that once the
// trailing-context window from the limiting match has fully closed, a
// later match does not reopen it (matching ripgrep exactly).
func TestRgMaxCountLimitDoesNotReopenClosedWindow(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\ny\nx\ny\ny\ny\nx\n")
	stdout, _, code := cmdRun(t, "rg -n -m1 -A2 x file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1:x\n2-y\n3:x\n", stdout)
}

// TestRgMaxCountStopsReadingAfterLimit reproduces a real hang: once -m's
// limit is reached, rg must stop reading the rest of the input rather than
// scanning every remaining (non-matching) line looking for another match
// that would immediately be discarded. Without the fix, `yes` never
// terminates, so this test would hang until ctx's timeout fires and fail.
func TestRgMaxCountStopsReadingAfterLimit(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stdout, _, code := cmdRunCtx(ctx, t, "{ printf 'match\\n'; yes no; } | rg -m 1 match", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "match\n", stdout)
}

// TestRgMaxCountWithAfterContextStopsAfterTrailingContext verifies that -m
// combined with -A still emits the requested trailing context for the
// limiting match before stopping, rather than either truncating the context
// or continuing to scan indefinitely.
func TestRgMaxCountWithAfterContextStopsAfterTrailingContext(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "match\nctx1\nctx2\nctx3\n")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stdout, _, code := cmdRunCtx(ctx, t, "rg -m 1 -A2 match file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "match\nctx1\nctx2\n", stdout)
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

// TestRgOnlyMatchingKeepsContext verifies that -o does NOT suppress -A/-B/-C
// context (verified directly against real ripgrep): -o only changes what is
// printed for the matching line itself. GNU grep suppresses context under
// -o, but ripgrep does not, and this builtin follows ripgrep here.
func TestRgOnlyMatchingKeepsContext(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "1\nmatch\n3\n")
	stdout, _, code := cmdRun(t, "rg -o -C1 match file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\nmatch\n3\n", stdout)
}

// TestRgOnlyMatchingInvertedPrintsWholeLine verifies that -o -v prints the
// whole selected line (there is no matched substring to isolate, since -v
// selects lines that do NOT match), matching real ripgrep.
func TestRgOnlyMatchingInvertedPrintsWholeLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "b\n")
	stdout, _, code := cmdRun(t, "rg -o -v a file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "b\n", stdout)
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

// TestRgBinaryFileStopsAfterFirstMatchOnInfiniteStream reproduces a real
// hang: in normal line-output mode (no -c), ripgrep stops scanning
// entirely after the first binary match, since binary content is never
// printed. Without this, a binary match on an infinite stream (e.g. piped
// stdin) with no -m limit would read the rest of the stream for no benefit.
func TestRgBinaryFileStopsAfterFirstMatchOnInfiniteStream(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, stderr, code := cmdRunCtx(ctx, t, `{ printf '\0x\n'; yes no; } | rg x -`, dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr, "binary file matches")
}

// TestRgBinaryFileCountModeStillCountsAllMatches verifies that -c is the
// one exception to the "stop after first binary match" rule: it needs an
// exact count, so it keeps scanning (up to -m's cap, if any) instead of
// stopping at the first match.
func TestRgBinaryFileCountModeStillCountsAllMatches(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bin.dat", "\x00"+strings.Repeat("x\n", 5))
	stdout, _, code := cmdRun(t, "rg -c x bin.dat", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "5\n", stdout)
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

// TestRgFilesQuietSuppressesListing verifies that -q suppresses --files'
// listing output too (not just search-mode output): only the exit status
// reports whether anything was found.
func TestRgFilesQuietSuppressesListing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "x")
	stdout, _, code := cmdRun(t, "rg -q --files", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
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

// TestRgMalformedGlobRejected verifies that a syntactically invalid glob
// (an unclosed character class) is reported as an error (exit 2) rather
// than silently matching nothing, and that it also blocks an explicit file
// operand from being searched (globs failing validation must not be
// quietly ignored for operands that bypass directory-traversal filtering).
func TestRgMalformedGlobRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	_, stderr, code := cmdRun(t, "rg -g '[' x file.txt", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "error parsing glob")
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
