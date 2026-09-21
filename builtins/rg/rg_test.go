// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package rg_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/builtins/rg"
	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/internal/interpoption"
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

// TestRgEmptyDirectoryOperandStillShowsFilenameForOtherOperand verifies
// that a directory operand yielding zero searchable files (an empty
// directory here) still triggers the "any directory operand enables path
// prefixes" guarantee for every other operand in the same invocation,
// matching real ripgrep exactly: the filename decision must be based on
// whether any operand WAS a directory, not on whether traversal happened
// to discover any files.
func TestRgEmptyDirectoryOperandStillShowsFilenameForOtherOperand(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "empty-dir"), 0755))
	writeFile(t, dir, "file", "x\n")
	stdout, _, code := cmdRun(t, "rg x empty-dir file", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "file:x\n", stdout)
}

// TestRgGlobFilteredDirectoryOperandStillShowsFilename mirrors the empty-
// directory case: a directory operand whose entire contents are excluded
// by -g also yields zero files, and must still trigger path prefixes for
// other operands.
func TestRgGlobFilteredDirectoryOperandStillShowsFilename(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "dir2/skip.log", "z\n")
	writeFile(t, dir, "file2", "z\n")
	stdout, _, code := cmdRun(t, "rg -g '*.txt' z dir2 file2", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "file2:z\n", stdout)
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

// TestRgNewlineRequiredPatternRejected verifies that a pattern whose only
// possible match requires a literal newline character is rejected with
// exit 2, matching real ripgrep's own message exactly (verified
// directly): a per-line scanner (this implementation never accepts
// -U/--multiline) can never satisfy such a pattern.
func TestRgNewlineRequiredPatternRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	for _, pat := range []string{`\n`, `a\n`, `\na`, `[\n]`, `(\n)`, `(?:\n)`, `\n+`} {
		_, stderr, code := cmdRun(t, "rg '"+pat+"' file.txt", dir)
		assert.Equal(t, 2, code, "pattern %q", pat)
		assert.Contains(t, stderr, `the literal "\n" is not allowed in a regex`, "pattern %q", pat)
	}
}

// TestRgNewlineOptionalPatternAccepted verifies the other side of the
// same rule: a pattern that CAN match something other than a newline
// (a negated class containing \n, a class containing \n among other
// runes, or an alternation where at least one branch does not require a
// newline) is accepted, matching real ripgrep exactly — only a pattern
// whose EVERY possible match requires \n is rejected.
func TestRgNewlineOptionalPatternAccepted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "ax\n")
	for _, pat := range []string{`[^\n]`, `[a\n]`, `x|\n`} {
		_, _, code := cmdRun(t, "rg '"+pat+"' file.txt", dir)
		assert.Equal(t, 0, code, "pattern %q", pat)
	}
}

// TestRgFixedStringsNewlineByteRejected verifies -F (fixed-strings) mode
// applies the same newline rejection to a literal raw newline byte in
// the pattern (verified directly), even though -F never interprets
// escape sequences like \n as anything but two literal characters.
func TestRgFixedStringsNewlineByteRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	_, stderr, code := cmdRun(t, "rg -F $'a\\nb' file.txt", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, `the literal "\n" is not allowed in a regex`)
}

// TestRgFixedStringsBackslashNAccepted verifies that -F's literal
// backslash-n (two characters, not an actual newline byte) is NOT
// rejected, since -F never interprets it as the newline escape sequence
// (verified directly against real ripgrep).
func TestRgFixedStringsBackslashNAccepted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, `file.txt`, `a\nb`+"\n")
	stdout, _, code := cmdRun(t, `rg -F 'a\nb' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, `a\nb`+"\n", stdout)
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

// TestRgWordRegexpZeroWidthMatchesAtNonWordBoundaries is a regression
// test: -w must not unconditionally reject every zero-width match from a
// pattern that can match the empty string. Verified directly against
// real ripgrep: on the line "abc !", "-w -c -o ”" reports exactly 2
// matches (immediately before '!' — between the space and '!' — and at
// end-of-line, immediately after '!'), not 0. The other four candidate
// positions (start-of-line before 'a', and immediately after each of
// 'a', 'b', 'c') are correctly rejected since a word character sits
// directly on the side that must be non-word.
func TestRgWordRegexpZeroWidthMatchesAtNonWordBoundaries(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "abc !\n")
	stdout, _, code := cmdRun(t, "rg -w -c -o '' file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2\n", stdout)
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

// TestRgCountOnlyMatchingCountsIndividualMatches verifies that -c combined
// with plain -o counts individual matched substrings, not selected lines,
// matching real ripgrep exactly: a line "xx" contributes 2 to the count
// for pattern "x", not 1.
func TestRgCountOnlyMatchingCountsIndividualMatches(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "xx\nyy\nxxx\n")
	stdout, _, code := cmdRun(t, "rg -c -o x f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "5\n", stdout) // 2 (from "xx") + 3 (from "xxx")
}

// TestRgCountOnlyMatchingInvertedCountsLines verifies the other half of
// the same rule: -c -o -v has no matched substring to enumerate (the line
// was selected because the pattern did NOT match it), so it still counts
// selected lines, same as -c without -o.
func TestRgCountOnlyMatchingInvertedCountsLines(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "xx\nyy\nxxx\n")
	stdout, _, code := cmdRun(t, "rg -c -o -v x f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout) // only "yy" does not match "x"
}

// TestRgCountOnlyMatchingWithMaxCountLimitsLinesNotMatches verifies that
// -m caps the number of matching LINES processed, not the number of
// individual matches the resulting -c -o count may enumerate per line:
// with -m2, both matching lines are still fully counted (2 + 3 = 5), even
// though 5 exceeds 2.
func TestRgCountOnlyMatchingWithMaxCountLimitsLinesNotMatches(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "xx\nxxx\nxxxx\n")
	stdout, _, code := cmdRun(t, "rg -c -o -m2 x f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "5\n", stdout)
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
	assert.Equal(t, "<stdin>\n", stdout)
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

// TestRgMaxCountZeroShortCircuitsBeforeOperandResolution is a regression
// test: -m 0 must short-circuit before any operand is even resolved
// (StatFile'd/opened), not just before scanning an opened file's content.
// Verified directly against real ripgrep: "rg -m0 x missing_file" and
// "rg -m0 'invalid[' f" (an invalid pattern) both exit 1 ("no matches"),
// never reaching the "file not found" or "invalid regex" errors that
// resolving the operand or compiling the pattern would otherwise produce
// (which exit 2). Without short-circuiting before operand resolution, a
// nonexistent path or an invalid pattern combined with -m0 would
// incorrectly report an error instead of ripgrep's documented "won't
// search anything" behavior.
func TestRgMaxCountZeroShortCircuitsBeforeOperandResolution(t *testing.T) {
	dir := t.TempDir()

	_, _, code := cmdRun(t, "rg -m0 x missing_file_xyz", dir)
	assert.Equal(t, 1, code)

	writeFile(t, dir, "f.txt", "a\n")
	_, _, code = cmdRun(t, "rg -m0 'invalid[' f.txt", dir)
	assert.Equal(t, 1, code)
}

func TestRgMaxCountNegativeRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a\n")
	_, stderr, code := cmdRun(t, "rg --max-count=-2 a file.txt", dir)
	assert.Equal(t, 2, code)
	assert.NotEmpty(t, stderr)
}

// TestRgNegativeMaxCountRejectedEvenWithHelp verifies that numeric-flag
// validation happens BEFORE the --help short-circuit, matching the house
// convention (see head's registerFlags) and verified directly against
// real ripgrep: "rg --max-count=-1 --help" exits 2 with the invalid-value
// error, never printing help.
func TestRgNegativeMaxCountRejectedEvenWithHelp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a\n")
	stdout, stderr, code := cmdRun(t, "rg --max-count=-1 --help file.txt", dir)
	assert.Equal(t, 2, code)
	assert.Equal(t, "", stdout)
	assert.NotEmpty(t, stderr)
}

// TestRgNegativeAfterContextRejectedEvenWithHelp mirrors the -m case for
// -A, also verified directly against real ripgrep.
func TestRgNegativeAfterContextRejectedEvenWithHelp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a\n")
	stdout, stderr, code := cmdRun(t, "rg -A -1 --help file.txt", dir)
	assert.Equal(t, 2, code)
	assert.Equal(t, "", stdout)
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

// TestRgContextSeparatorSuppressedWhenBeforeContextBridgesGroups is a
// regression test: the "--" group separator decision must be based on
// the EARLIEST line a match group is actually about to print (the first
// still-relevant buffered before-context line, if any), not the match's
// own line number. Verified directly against real ripgrep 15.1.0: with
// matches on lines 1 and 4 and -B2, lines 2-3 (line 4's before-context)
// immediately follow line 1, so ripgrep prints all four lines
// CONTINUOUSLY with no "--" separator, even though line 4 itself is not
// adjacent to line 1 — the buffered before-context bridges the gap.
func TestRgContextSeparatorSuppressedWhenBeforeContextBridgesGroups(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "match1\nctx2\nctx3\nmatch4\n")
	stdout, _, code := cmdRun(t, "rg -B2 match file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "match1\nctx2\nctx3\nmatch4\n", stdout)
}

// TestRgContextSeparatorStillPrintedForRealGap is the contrasting case:
// when the before-context does NOT fully bridge the gap between two
// match groups, the separator must still be printed — verified directly
// against real ripgrep: matches on lines 1 and 6 with -B1 (line 6's
// before-context is only line 5, leaving lines 2-4 as a genuine gap)
// still print "--" between them.
func TestRgContextSeparatorStillPrintedForRealGap(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "match1\nctx2\nctx3\nctx4\nctx5\nmatch6\n")
	stdout, _, code := cmdRun(t, "rg -B1 match file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "match1\n--\nctx5\nmatch6\n", stdout)
}

// TestRgCarriageReturnPreservedAsLineContent is a regression test: a CR
// immediately before a line's '\n' must be treated as ordinary line
// content, not stripped as part of the line-ending delimiter, matching
// ripgrep exactly — verified directly against real ripgrep 15.1.0 on a
// CRLF file containing "x\r\n": "x$" does NOT match (the '\r' breaks the
// end-of-line anchor), a literal "\r" pattern DOES match, and in both
// the plain "x" case and the literal-\r case, output preserves the '\r'
// ("x\r\n" end to end). bufio.ScanLines' built-in CR-stripping would
// otherwise silently drop it, making "x$" wrongly match and losing the
// '\r' from output.
func TestRgCarriageReturnPreservedAsLineContent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "x\r\n")

	_, _, code := cmdRun(t, `rg 'x$' f.txt`, dir)
	assert.Equal(t, 1, code) // no match: the CR breaks the $ anchor

	stdout, _, code := cmdRun(t, "rg $'x\\r' f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "x\r\n", stdout)

	stdout, _, code = cmdRun(t, `rg x f.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "x\r\n", stdout)
}

// TestRgZeroContextTreatedAsNoContext is a regression test: "-C0" (or
// "-A0 -B0") sets the after/before-context FLAGS but resolves to zero
// context, which ripgrep treats exactly like no context at all — no "--"
// group separator, since there is no actual context to create a visual
// gap between match groups — verified directly against real ripgrep
// 15.1.0: matching lines separated by a nonmatching line print
// consecutively under "-C0"/"-A0 -B0", with no separator, unlike "-C1" or
// any other positive context size on the same input.
func TestRgZeroContextTreatedAsNoContext(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "x\nn\nx\n")

	stdout, _, code := cmdRun(t, `rg -C0 x f.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "x\nx\n", stdout)

	stdout, _, code = cmdRun(t, `rg -A0 -B0 x f.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "x\nx\n", stdout)

	// Contrast: any positive context size still gets the separator/context
	// treatment normally.
	stdout, _, code = cmdRun(t, `rg -C1 x f.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "x\nn\nx\n", stdout)
}

// TestRgContextSeparatorBetweenFiles is a regression test: the "--"
// context-group separator must span across files searched in the same
// invocation, not just between groups within a single file — verified
// directly against real ripgrep 15.1.0: "rg -A1 x a b" (two files, one
// single-line match each) prints "a:x\n--\nb:x\n", including the
// separator between the two files' groups. A non-matching file given
// between two matching ones does not itself trigger a spurious separator
// or reset this cross-file state (still exactly one "--", between the
// two matching files' groups); a mode that never prints context/
// separators at all (-l, -c) or that was never given a context flag
// prints the two files back-to-back with no separator, matching ripgrep
// exactly in every case.
func TestRgContextSeparatorBetweenFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "x\n")
	writeFile(t, dir, "b.txt", "x\n")
	writeFile(t, dir, "nomatch.txt", "y\n")

	stdout, _, code := cmdRun(t, "rg -A1 x a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a.txt:x\n--\nb.txt:x\n", stdout)

	stdout, _, code = cmdRun(t, "rg -A1 x a.txt nomatch.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a.txt:x\n--\nb.txt:x\n", stdout)

	stdout, _, code = cmdRun(t, "rg -A1 x nomatch.txt a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a.txt:x\n--\nb.txt:x\n", stdout)

	stdout, _, code = cmdRun(t, "rg x a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a.txt:x\nb.txt:x\n", stdout)

	stdout, _, code = cmdRun(t, "rg -l -A1 x a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a.txt\nb.txt\n", stdout)

	stdout, _, code = cmdRun(t, "rg -c -A1 x a.txt b.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a.txt:1\nb.txt:1\n", stdout)
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

// TestRgRecursivelyDiscoveredBinaryFileSkipped verifies ripgrep's
// discovery-source-dependent binary semantics: a file found by
// recursively walking a directory operand is silently skipped when it
// looks binary (no match, no notice, exit 1), unlike an explicitly named
// file (see TestRgBinaryFileReportsMatchWithoutContent), which still
// reports the match via the "binary file matches" notice.
func TestRgRecursivelyDiscoveredBinaryFileSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sub/bin.dat", "abc\x00def\n")
	stdout, stderr, code := cmdRun(t, "rg abc sub", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)
}

// TestRgRecursivelyDiscoveredBinaryFileCountModeSkipped verifies the skip
// applies to -c too: a discovered binary file must not contribute a count.
func TestRgRecursivelyDiscoveredBinaryFileCountModeSkipped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sub/bin.dat", "abc\x00def\n")
	_, _, code := cmdRun(t, "rg -c abc sub", dir)
	assert.Equal(t, 1, code)
}

// TestRgBinaryProbeWindowMatchesRealRipgrep verifies the binary-detection
// probe window is 64 KiB, matching real ripgrep exactly (verified
// directly): a NUL byte at offset 65535 is caught by the probe (the whole
// file is treated as binary, fully suppressed for a discovered file), but
// a NUL at offset 65536 is not (a discovered file falls back to
// late-detection semantics — whatever matched before the NUL is printed,
// plus a notice — rather than being fully skipped). Using grep's smaller
// 32 KiB probe here would diverge from real ripgrep for files with binary
// content between 32 KiB and 64 KiB in.
func TestRgBinaryProbeWindowMatchesRealRipgrep(t *testing.T) {
	dir := t.TempDir()

	// NUL at offset 65535 (within the 64 KiB probe): discovered file must
	// be fully skipped, no leaked match.
	withinProbe := "needle\n" + strings.Repeat("a", 65535-7) + "\x00"
	writeFile(t, dir, "within/f.txt", withinProbe)
	stdout, _, code := cmdRun(t, "rg needle within", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)

	// NUL at offset 65536 (just past the 64 KiB probe): late detection,
	// the earlier match is still printed.
	beyondProbe := "needle\n" + strings.Repeat("a", 65536-7) + "\x00"
	writeFile(t, dir, "beyond/f.txt", beyondProbe)
	stdout, stderr, code := cmdRun(t, "rg needle beyond", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "needle")
	assert.Contains(t, stderr, "binary file matches")
}

// runScriptWithStdinFile runs script with stdin set directly to f (an
// *os.File, e.g. the read end of an os.Pipe). interp.StdIO passes an
// *os.File through unmodified rather than relaying it through its own
// internal copy goroutine (which uses a large fixed-size buffer and
// would otherwise coalesce away any short-read timing this test
// deliberately introduces), so the exact Read-call chunking performed by
// the writer on the other end of the pipe is preserved all the way to
// the rg builtin's own probe read.
func runScriptWithStdinFile(t *testing.T, script string, f *os.File, dir string) (string, string, int) {
	t.Helper()
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(script), "")
	require.NoError(t, err)

	var outBuf, errBuf bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(f, &outBuf, &errBuf),
		interpoption.AllowAllCommands().(interp.RunnerOption),
		interp.AllowedPaths([]string{dir}),
	)
	require.NoError(t, err)
	defer runner.Close()
	runner.Dir = dir

	err = runner.Run(context.Background(), prog)
	exitCode := 0
	if err != nil {
		var es interp.ExitStatus
		if errors.As(err, &es) {
			exitCode = int(es)
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// TestRgBinaryProbeSurvivesShortReads is a regression test: the binary
// probe must fill its entire intended window using io.ReadFull (looping
// internally across multiple underlying Read calls, each of which reads
// from the pipe below its full buffer size below), not a single Read
// call, which io.Reader's contract does not guarantee will fill the
// caller's buffer even when more data is eventually available. A pipe
// write smaller than a pipe read's buffer size reliably produces a short
// read on the reading end (unlike a real regular file, where the
// underlying read syscall typically returns everything requested in one
// call — which is why TestRgBinaryProbeWindowMatchesRealRipgrep, using a
// real file, cannot exercise this path). A NUL byte placed within the
// probe window, but reachable only via a LATER short Read, must still be
// caught by the initial probe. stdin is an explicit operand (like an
// explicit file), so ripgrep's explicit-file binary semantics apply: the
// match is still reported via the "binary file matches" notice (exit 0),
// rather than being silently skipped the way a directory-discovered
// binary file would be — verified directly against real ripgrep with
// this exact content. What this test actually guards against is a
// missed detection: if the probe's short-read bug caused the NUL to be
// missed entirely, the file would instead be scanned as text and "needle"
// would leak to stdout.
func TestRgBinaryProbeSurvivesShortReads(t *testing.T) {
	dir := t.TempDir()
	pr, pw, err := os.Pipe()
	require.NoError(t, err)

	// NUL at offset 100 (well within the 64 KiB probe window).
	content := []byte("needle\n" + strings.Repeat("a", 93) + "\x00" + strings.Repeat("a", 65536))
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer pw.Close()
		// Write in small (16-byte) chunks, one at a time, each separated
		// by a brief pause: without the pause, the writer can race ahead
		// of the reader and the kernel pipe buffer coalesces many writes
		// together before the probe's first Read call ever happens,
		// silently defeating the whole point of this test (the probe
		// would see one large read regardless of the fix). The pause
		// forces the reader to actually observe a short read (up to the
		// 16 bytes written so far) before more data becomes available.
		// Only the region up through and just past the NUL byte (offset
		// 100) needs this pacing; once the probe's first Read has
		// definitely already happened, the remainder can be written in
		// one large, fast chunk without weakening the test.
		const pacedPrefix = 128
		for i := 0; i < pacedPrefix; i += 16 {
			end := i + 16
			if _, werr := pw.Write(content[i:end]); werr != nil {
				return
			}
			time.Sleep(2 * time.Millisecond)
		}
		if _, werr := pw.Write(content[pacedPrefix:]); werr != nil {
			return
		}
	}()
	defer func() { <-done }()

	stdout, stderr, code := runScriptWithStdinFile(t, "rg needle -", pr, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "binary file matches")
}

// TestRgRecursivelyDiscoveredBinaryFileTextModeStillSearched verifies that
// -a/--text overrides the discovery-source skip: with binary detection
// disabled entirely, a recursively discovered file is still searched as
// text regardless of how it was named.
func TestRgRecursivelyDiscoveredBinaryFileTextModeStillSearched(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sub/bin.dat", "abc\x00def\n")
	stdout, _, code := cmdRun(t, "rg -a abc sub", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "abc")
}

// TestRgExplicitOperandOverlappingDirectoryOperandStaysExplicit verifies
// that naming a file directly (in addition to a directory operand that
// also reaches it) preserves its explicit-file binary semantics
// (reporting the match), regardless of which operand is given first.
// Without this, expandOperands's deduplication would let the directory
// operand's discovered-by-traversal marking win, silently skipping the
// file's binary match even though it was also named explicitly.
// TestRgExplicitOperandOverlappingDirectoryOperandStaysExplicit is also a
// regression test for per-OCCURRENCE (not per-path) binary provenance:
// discoveredByTraversal is tracked directly on each fileEntry (see its
// doc comment), not via a shared map keyed by path that could
// accidentally reclassify every occurrence of that path once one
// occurrence is found to be explicit. The binary-match notice must
// appear EXACTLY ONCE regardless of operand order (verified directly
// against real ripgrep): the explicit occurrence reports it, and the
// directory-discovered occurrence of the SAME path is independently
// still silently skipped.
func TestRgExplicitOperandOverlappingDirectoryOperandStaysExplicit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sub/bin.dat", "needle\x00\n")

	stdout, stderr, code := cmdRun(t, "rg needle sub/bin.dat sub", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, 1, strings.Count(stderr, "binary file matches"))

	stdout, stderr, code = cmdRun(t, "rg needle sub sub/bin.dat", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, 1, strings.Count(stderr, "binary file matches"))
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

// TestRgGlobLaterIncludeReAdmitsExcludedDirectory verifies ripgrep's
// documented "glob given later takes precedence" rule applies to
// directory pruning too: an earlier "!foo/**" exclusion can be re-admitted
// by a later "foo/**" include, so traversal must not permanently prune a
// directory the moment any earlier exclude glob matches it.
func TestRgGlobLaterIncludeReAdmitsExcludedDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "foo/bar/a", "x\n")
	// Explicit "." operand → "./" display prefix (see
	// TestRgManyFilesAcrossManyDirectoriesDiscovered's comment).
	stdout, _, code := cmdRun(t, "rg -g '!foo/**' -g 'foo/**' x .", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./foo/bar/a:x\n", stdout)
}

// TestRgGlobDoubleStarCrossesPathSeparators verifies that "**" in a glob
// matches any number of path components (including zero), not just a
// single component the way a bare "*" would under filepath.Match.
func TestRgGlobDoubleStarCrossesPathSeparators(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a/b/c/f.txt", "x\n")
	writeFile(t, dir, "a/f.txt", "y\n")
	stdout, _, code := cmdRun(t, "rg --files -g 'a/**/f.txt' | sort", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a/b/c/f.txt\na/f.txt\n", stdout)
}

// TestRgGlobLeadingSlashRootsPattern verifies that a leading '/' in a -g
// pattern anchors it at the search root, per gitignore glob rules (which
// ripgrep's own --help documents -g as following) — verified directly
// against real ripgrep: with files at "a/f" and "x/a/f", "-g '/a/f'"
// matches only "a/f", not the deeper "x/a/f"; with files at top-level
// "f" and nested "a/f", "-g '/f'" matches only the top-level "f", unlike
// a bare non-anchored "f" glob which matches at any depth.
func TestRgGlobLeadingSlashRootsPattern(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a/f", "x\n")
	writeFile(t, dir, "x/a/f", "y\n")
	writeFile(t, dir, "f", "z\n")

	stdout, _, code := cmdRun(t, "rg --files -g '/a/f'", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a/f\n", stdout)

	stdout, _, code = cmdRun(t, "rg --files -g '/f'", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "f\n", stdout)

	// A bare, non-anchored "f" (for contrast) matches at any depth.
	stdout, _, code = cmdRun(t, "rg --files -g 'f' | sort", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a/f\nf\nx/a/f\n", stdout)
}

// TestRgGlobLeadingSlashWithDoubleStarStillRoots verifies the leading-'/'
// anchor combines correctly with a trailing "**": verified directly
// against real ripgrep, with files at "a/b/f" and "x/a/b/f", "-g
// '/a/**'" matches only the top-level "a/b/f", not the deeper "x/a/b/f".
func TestRgGlobLeadingSlashWithDoubleStarStillRoots(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a/b/f", "x\n")
	writeFile(t, dir, "x/a/b/f", "y\n")

	stdout, _, code := cmdRun(t, "rg --files -g '/a/**'", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a/b/f\n", stdout)
}

// TestRgAggregatePatternTooLargeRejected is a DoS regression test:
// several MiB of combined -e pattern text (well within the shell's own
// script-size limit) must be rejected before syntax.Parse/regexp.Compile
// ever sees it, since Go's regexp compiler can build AST/program
// structures whose size grows well beyond the input pattern's own byte
// length. A single, very long pattern and several shorter patterns whose
// combined length exceeds the cap are both rejected; the cap is on the
// AGGREGATE across every -e/positional pattern, not any single one.
func TestRgAggregatePatternTooLargeRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")

	big := strings.Repeat("a", rg.MaxAggregatePatternBytes+1)
	_, stderr, code := cmdRun(t, "rg "+big+" f.txt", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "combined pattern length exceeds")

	half := strings.Repeat("b", rg.MaxAggregatePatternBytes/2+1)
	_, stderr, code = cmdRun(t, fmt.Sprintf("rg -e %s -e %s f.txt", half, half), dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "combined pattern length exceeds")

	// Just under the cap still compiles and searches normally.
	ok := strings.Repeat("a", rg.MaxAggregatePatternBytes-1)
	stdout, _, code := cmdRun(t, "rg -F "+ok+" f.txt", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
}

// TestRgManyEmptyPatternsRejectedByMinCharge is a DoS regression test:
// charging only len(p) per pattern lets an empty (or otherwise very
// short) pattern consume zero (or near-zero) of the aggregate byte
// budget, so a script could otherwise supply many hundreds of thousands
// of individual -e patterns (well within the shell's own script-size
// limit) without ever tripping MaxAggregatePatternBytes, each still
// triggering its own regexp.Compile call. MinPatternCharge charges at
// least a fixed floor per pattern regardless of its own length, bounding
// the maximum pattern COUNT any invocation can supply, independent of
// how short any individual pattern is.
func TestRgManyEmptyPatternsRejectedByMinCharge(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")

	var sb strings.Builder
	sb.WriteString("rg ")
	// One more than MaxAggregatePatternBytes/MinPatternCharge empty
	// patterns must be rejected, even though their combined LENGTH
	// (every pattern is 0 bytes) is nowhere near MaxAggregatePatternBytes.
	for i := 0; i < rg.MaxAggregatePatternBytes/rg.MinPatternCharge+1; i++ {
		sb.WriteString("-e '' ")
	}
	sb.WriteString("f.txt")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, stderr, code := cmdRunCtx(ctx, t, sb.String(), dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "combined pattern length exceeds")
}

// TestRgUnicodeWordDigitSpaceClassesMatchNonASCII verifies that \w, \d,
// and \s match Unicode categories, not just ASCII ranges, matching
// ripgrep's default Unicode mode — verified directly against real
// ripgrep 15.1.0: \w matches "é" (Unicode letter), \d matches "٣"
// (U+0663 ARABIC-INDIC DIGIT THREE, category Nd), and \s matches U+00A0
// NO-BREAK SPACE. Without Unicode translation, Go's regexp would give
// ASCII-only semantics for all three and none of these would match.
func TestRgUnicodeWordDigitSpaceClassesMatchNonASCII(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "caf\u00e9\n\u0663\n\u00a0\n")

	stdout, _, code := cmdRun(t, `rg -o '\w+' f.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "caf\u00e9\n\u0663\n", stdout)

	stdout, _, code = cmdRun(t, `rg -o '\d' f.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\u0663\n", stdout)

	stdout, _, code = cmdRun(t, `rg -c '\s' f.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout) // only the standalone NBSP line has a non-newline \s match
}

// TestRgUnicodeNegatedClassesMatchByComplement verifies \D and \W (the
// negations) reject exactly the runes their positive counterpart accepts,
// including Unicode ones — verified directly against real ripgrep: \D
// (not a Unicode digit) does not match "٣" (category Nd) but does match
// "a" and "!"; \W (not a Unicode word character) does not match "a" or
// "٣" (a Unicode letter and a Unicode digit, both word characters) but
// does match "!".
func TestRgUnicodeNegatedClassesMatchByComplement(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\u0663!\u00e9\n")

	stdout, _, code := cmdRun(t, `rg -o '\D' f.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n!\n\u00e9\n", stdout)

	stdout, _, code = cmdRun(t, `rg -o '\W' f.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "!\n", stdout)
}

// TestRgUnicodeClassNestedInsideBracketExpression verifies \d/\D/\s/\w
// (but not \S/\W, a documented limitation — see
// errNegatedClassInBracketNotSupported) translate correctly when used as
// a MEMBER of an existing "[...]" character class, not just standalone.
func TestRgUnicodeClassNestedInsideBracketExpression(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\u0663!\n")

	// [\dx] matches the Unicode digit and would also match a literal 'x'.
	stdout, _, code := cmdRun(t, `rg -o '[\dx]' f.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\u0663\n", stdout)

	// [\D!] matches everything except a Unicode digit (so 'a' and '!' via
	// \D alone), still excluding the digit itself.
	stdout, _, code = cmdRun(t, `rg -o '[\D]' f.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n!\n", stdout)
}

// TestRgUnicodeNegatedClassInBracketRejected verifies \S/\W used as a
// MEMBER of an existing "[...]" bracket (alongside another member) is
// rejected with a clear error rather than silently mistranslated: Go's
// regexp/syntax cannot express "the complement of this multi-range union"
// as a bracket member (see errNegatedClassInBracketNotSupported's doc
// comment for why), so this combination is an explicit, documented
// limitation rather than a silently wrong result.
func TestRgUnicodeNegatedClassInBracketRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")

	_, stderr, code := cmdRun(t, `rg '[\Sx]' f.txt`, dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "not supported inside a")

	_, stderr, code = cmdRun(t, `rg '[a\W]' f.txt`, dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "not supported inside a")
}

// TestRgWordBoundaryEscapeRejected verifies an inline \b/\B word-boundary
// escape is rejected with a clear error rather than silently applying
// Go's ASCII-only boundary semantics, which disagree with ripgrep's own
// Unicode-aware \b/\B for non-ASCII input — verified directly against
// real ripgrep 15.1.0: "caf\b" does NOT match "café" (since 'é' is
// itself a Unicode word character, there is no boundary between 'f' and
// 'é'), the opposite of what Go's ASCII-only \b would report.
func TestRgWordBoundaryEscapeRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "caf\u00e9\n")

	_, stderr, code := cmdRun(t, `rg 'caf\b' f.txt`, dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "word-boundary escape is not supported")

	_, stderr, code = cmdRun(t, `rg 'caf\B' f.txt`, dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "word-boundary escape is not supported")
}

// TestRgPosixClassInsideBracketWithUnicodeClass is a regression test: a
// POSIX character class "[:name:]" (e.g. "[:alpha:]") used as a MEMBER
// of an already-open "[...]" bracket expression alongside a \\w/\\d/\\s
// shorthand (e.g. "[[:alpha:]\\w]") must not be corrupted by
// translateUnicodeClasses' bracket-depth tracking — verified directly
// against real ripgrep 15.1.0, which accepts this combination and
// matches 'a'. The POSIX class's OWN internal ']' (immediately after
// "name:") must not be mistaken for the outer bracket's closing ']',
// which would otherwise emit the \\w/\\d/\\s member as an invalid or
// silently-non-matching NESTED bracket instead of a correctly unioned
// member.
func TestRgPosixClassInsideBracketWithUnicodeClass(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n1\n!\n")

	stdout, _, code := cmdRun(t, `rg -o '[[:alpha:]\w]' f.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n1\n", stdout)

	// Order reversed: the Unicode shorthand before the POSIX class.
	stdout, _, code = cmdRun(t, `rg -o '[\w[:digit:]]' f.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n1\n", stdout)

	// The POSIX class alone (no Unicode shorthand) must still work.
	stdout, _, code = cmdRun(t, `rg -o '[[:alpha:]]' f.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n", stdout)
}

// TestRgWordCharMatchesFullUnicodeAlphabeticAndJoinControl is a
// regression test: ripgrep's Unicode \\w is documented (and verified
// directly against real ripgrep 15.1.0) as \\p{Alphabetic} ∪ \\p{M} ∪
// \\p{Nd} ∪ \\p{Pc} ∪ \\p{Join_Control} — NOT simply
// \\p{L}\\p{M}\\p{Nd}\\p{Pc} (an earlier, narrower version of this
// translation used exactly that subset). \\p{L} alone omits Unicode's
// derived "Alphabetic" property, which also includes general category Nl
// (e.g. U+2167 ROMAN NUMERAL EIGHT) and a further Other_Alphabetic set;
// Join_Control (U+200C ZERO WIDTH NON-JOINER, U+200D ZERO WIDTH JOINER)
// is not in any of L/M/Nd/Pc/Nl/Other_Alphabetic at all, but ripgrep
// still treats it as a word character bridging two letters into one
// contiguous match.
func TestRgWordCharMatchesFullUnicodeAlphabeticAndJoinControl(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "roman.txt", "\u2167\n")
	writeFile(t, dir, "jc.txt", "a\u200cb\n")

	stdout, _, code := cmdRun(t, `rg '\w' roman.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\u2167\n", stdout)

	stdout, _, code = cmdRun(t, `rg -o '\w+' jc.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\u200cb\n", stdout)
}

// TestRgGlobManyDoubleStarsBoundedTime is a DoS regression test: a glob
// with many "**" segments matched against a long, non-matching directory
// path must not exhibit combinatorial blowup. globMatchSegments uses
// dynamic programming (O(pattern_segments*path_segments)) specifically to
// avoid this; a naive recursive-backtracking implementation of the same
// semantics would branch exponentially here and never finish within the
// test's timeout. This pattern (20 segments) is well under
// MaxGlobSegments, so it exercises globMatchSegments' own DP algorithm
// rather than the segment-count rejection in validateGlobs.
func TestRgGlobManyDoubleStarsBoundedTime(t *testing.T) {
	dir := t.TempDir()
	deep := strings.Repeat("a/", 20) + "b.txt"
	writeFile(t, dir, deep, "x\n")
	pat := strings.Repeat("**/", 20) + "nomatch"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, code := cmdRunCtx(ctx, t, "rg --files -g '"+pat+"'", dir)
	assert.Equal(t, 1, code)
}

// TestRgGlobSegmentCountAtCapBoundedTime verifies globMatchSegments' DP
// itself stays fast at the largest segment count validateGlobs still
// accepts (MaxGlobSegments): the DP's O(np*na) cost is bounded by
// MaxTraversalDepth on the path side regardless of pattern length, so a
// glob at the cap matched against many candidate paths during traversal
// must still complete quickly — this is the scenario the now-rejected
// (see TestRgGlobExceedingSegmentCapRejected) 200,000-segment pattern used
// to exercise, before the cap made that pattern itself invalid.
func TestRgGlobSegmentCountAtCapBoundedTime(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a/b/c/f.txt", "x\n")
	pat := strings.Repeat("a/", 4095) + "nomatch"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, code := cmdRunCtx(ctx, t, "rg --files -g '"+pat+"'", dir)
	assert.Equal(t, 1, code)
}

// TestRgGlobExceedingSegmentCapRejected is a DoS-hardening regression
// test: globMatchSegments' DP cost is O(len(patSegs)*len(pathSegs)); the
// path side is already bounded by MaxTraversalDepth (256), but pattern
// segment count is otherwise attacker-controlled up to the shell script
// size limit (a glob argument could contain millions of '/' characters),
// so a single -g pattern matched against many candidate paths during
// traversal could otherwise perform hundreds of millions of comparisons
// and monopolize CPU well past the shell's own timeout even though at
// most one directory entry ever matches. validateGlobs now rejects any
// glob with more than MaxGlobSegments segments outright (exit 2) rather
// than allowing the DP to run to completion. This is an intentional
// hardening divergence from ripgrep, which accepts such patterns (see
// docs/RULES.md's DoS-hardening exception policy); MaxGlobSegments
// (4096) is far beyond any legitimate real-world glob.
func TestRgGlobExceedingSegmentCapRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a/b/c/f.txt", "x\n")
	pat := strings.Repeat("a/", 200_000) + "nomatch"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, stderr, code := cmdRunCtx(ctx, t, "rg --files -g '"+pat+"'", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "too many path segments")
}

// TestRgAggregateGlobSegmentCapRejected is a DoS regression test: many
// separate -g globs, each individually well under MaxGlobSegments, must
// still be rejected once their SUMMED segment count exceeds
// MaxAggregateGlobSegments. Without this aggregate cap, pathAllowed's
// per-glob DP cost (each proportional to that glob's own segment count)
// would be paid once per configured glob for every candidate path visited
// during traversal, so many globs at or near the per-pattern cap could
// restore an effectively unbounded total amount of work even though each
// single glob passed its own MaxGlobSegments check.
func TestRgAggregateGlobSegmentCapRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a/b/c/f.txt", "x\n")

	// Each glob has 100 segments (well under MaxGlobSegments=4096), but 50
	// of them combined exceed MaxAggregateGlobSegments (4096).
	var script strings.Builder
	script.WriteString("rg --files")
	for i := 0; i < 50; i++ {
		script.WriteString(" -g '" + strings.Repeat("a/", 99) + "nomatch" + strconv.Itoa(i) + "'")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, stderr, code := cmdRunCtx(ctx, t, script.String(), dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "combined -g/--glob patterns have too many path segments")
}

// TestRgTooManyPathOperandsRejected is a DoS regression test: rshell
// deliberately does not deduplicate repeated operands, so a script
// containing many repetitions of a short explicit-file/stdin operand
// (well within the shell's own script-size limit) must be rejected
// outright (exit 2), before any StatFile/traversal work begins for any
// of them — MaxTotalDiscoveredFiles/MaxTotalDiscoveredPathBytes only
// bound files DISCOVERED by directory traversal, never explicit operand
// occurrences appended directly. This does not exercise MaxPathOperands
// at its full 100,000 scale (the point of the fix is that this check is
// O(1) and never reaches per-operand work at all, so an even larger
// count is equally fast to reject); a moderately-sized script that
// clearly exceeds the cap is enough to confirm the check fires and
// exits quickly rather than doing unbounded per-operand work first.
func TestRgTooManyPathOperandsRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f", "a\n")

	var sb strings.Builder
	sb.WriteString("rg x ")
	for i := 0; i < rg.MaxPathOperands+1; i++ {
		sb.WriteString("f ")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, stderr, code := cmdRunCtx(ctx, t, sb.String(), dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "too many path operands")
}

// TestRgManyExplicitOperandsUnderCapStillWork is a sanity check that the
// new MaxPathOperands cap does not regress ordinary (even fairly large)
// operand lists: repeating the same explicit file operand many times,
// well under the cap, must still search and report every occurrence
// (rshell does not deduplicate operands).
func TestRgManyExplicitOperandsUnderCapStillWork(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f", "x\n")

	const n = 500
	var sb strings.Builder
	sb.WriteString("rg -c x")
	for i := 0; i < n; i++ {
		sb.WriteString(" f")
	}

	stdout, _, code := cmdRun(t, sb.String(), dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, strings.Repeat("f:1\n", n), stdout)
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

// TestRgGlobWithSlashNeverRevealsHiddenDirectory verifies real ripgrep's
// precise rule for -g overriding hidden-file filtering for a TOP-LEVEL
// hidden directory (verified directly across many pattern shapes): a
// glob overrides the hidden-entry skip exactly when it matches the
// hidden entry's OWN path. None of ".cache/**", ".cache/*", ".cache/a",
// or ".cache" match the path ".cache" itself: the first two require at
// least one path segment strictly inside ".cache" (globMatchSegments
// treats a trailing "**"/"*" run as requiring >=1 extra component, not
// zero, matching real ripgrep — verified directly: "-g 'a/**'" does not
// match a bare path "a"), and ".cache/a" names a child rather than
// ".cache" itself. This is narrower than "any '/'-containing glob never
// reveals a hidden directory" — see
// TestRgGlobSlashPatternRevealsNestedHiddenEntry for the case where a
// '/'-containing glob DOES reveal a hidden entry that sits below a
// still-visible ancestor, by matching that entry's own path directly.
func TestRgGlobWithSlashNeverRevealsHiddenDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".cache/a", "x\n")

	for _, glob := range []string{".cache/**", ".cache/*", ".cache/a", ".cache"} {
		stdout, _, code := cmdRun(t, "rg --files -g '"+glob+"'", dir)
		assert.Equal(t, 1, code, "glob %q must not reveal the hidden .cache directory", glob)
		assert.Equal(t, "", stdout, "glob %q must not reveal the hidden .cache directory", glob)
	}
}

// TestRgGlobBareStarRevealsHiddenFiles verifies the other side of the same
// rule: a bare "*"/"**" (no '/') is not a special case — it is simply the
// same no-'/' basename match as any other pattern, and does reveal a
// matching hidden top-level file, same as e.g. "*.txt" would.
func TestRgGlobBareStarRevealsHiddenFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".hidden.txt", "x\n")
	writeFile(t, dir, "visible.txt", "y\n")

	for _, glob := range []string{"*", "**"} {
		stdout, _, code := cmdRun(t, "rg --files -g '"+glob+"' | sort", dir)
		assert.Equal(t, 0, code)
		assert.Equal(t, ".hidden.txt\nvisible.txt\n", stdout, "glob %q", glob)
	}
}

// TestRgGlobHiddenOverrideStillFiltersUnderHiddenFlag verifies that once
// --hidden has independently allowed traversal into a hidden directory, a
// '/'-containing glob still applies its normal include/exclude filtering
// to entries inside it (the glob is not simply ignored once inside — it
// only fails to be the thing that authorizes entry in the first place).
func TestRgGlobHiddenOverrideStillFiltersUnderHiddenFlag(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".cache/visible_inside.txt", "x\n")

	stdout, _, code := cmdRun(t, "rg --files --hidden -g '.cache/visible_inside.txt'", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, ".cache/visible_inside.txt\n", stdout)

	_, _, code = cmdRun(t, "rg --files --hidden -g '.cache/nomatch.txt'", dir)
	assert.Equal(t, 1, code)
}

// TestRgGlobSlashPatternRevealsNestedHiddenEntry verifies that a
// '/'-containing glob CAN reveal a hidden entry that sits below a
// still-VISIBLE ancestor directory, as long as the glob matches that
// hidden entry's own path directly (verified directly against real
// ripgrep): with a visible "sub/" containing a hidden file "sub/.h" and
// a hidden directory "sub/.hiddendir/", "-g 'sub/*'" reveals the hidden
// FILE (its exact path matches "sub/*"), and "-g 'sub/**'" reveals the
// hidden DIRECTORY itself and everything inside it ("**" matching one
// component exactly reaches "sub/.hiddendir"). This is the opposite of
// TestRgGlobWithSlashNeverRevealsHiddenDirectory's top-level case, where
// the hidden directory itself has no visible ancestor for the glob to
// anchor on.
func TestRgGlobSlashPatternRevealsNestedHiddenEntry(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sub/.h", "x\n")
	writeFile(t, dir, "sub/vis.txt", "y\n")
	writeFile(t, dir, "sub/.hiddendir/f.txt", "z\n")

	// No explicit "." operand here (the implicit no-operand default is
	// used, matching this test's own focus on -g/hidden-glob semantics
	// rather than the display-path prefixing exercised by
	// TestRgGlobLaterIncludeReAdmitsExcludedDirectory and
	// TestRgManyFilesAcrossManyDirectoriesDiscovered).
	stdout, _, code := cmdRun(t, "rg --files -g 'sub/*'", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "sub/.h\nsub/vis.txt\n", stdout)

	stdout, _, code = cmdRun(t, "rg --files -g 'sub/**'", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "sub/.h\nsub/.hiddendir/f.txt\nsub/vis.txt\n", stdout)

	// But a glob that only matches something INSIDE the hidden directory,
	// without matching the hidden directory's own path, still cannot
	// reveal it (the hidden directory itself must already be visible
	// before matching descends further, same as the top-level case).
	_, _, code = cmdRun(t, "rg --files -g 'sub/.hiddendir/*'", dir)
	assert.Equal(t, 1, code)
}

// TestRgGlobTrailingDoubleStarRequiresExtraSegment is a regression test
// for globMatchSegments' trailing-"**" rule: a pattern ending in one or
// more "**" segments must not match the exact path named by its
// non-"**" prefix — verified directly against real ripgrep: "-g
// 'a/**'" does not match a literal path "a" (only paths strictly inside
// a directory named "a", like "a/x"), and this holds even with more
// than one trailing "**" segment ("a/**/**"). This is the trailing-run
// case specifically; a "**" in the MIDDLE of a pattern, or a LEADING
// "**", can still consume zero components (see
// TestRgGlobDoubleStarCrossesPathSeparators and this test's own
// mid/leading assertions).
func TestRgGlobTrailingDoubleStarRequiresExtraSegment(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a", "bare file named a\n")
	writeFile(t, dir, "a2/x", "one level inside a2\n")
	writeFile(t, dir, "a3/y", "zero levels between a3 and y\n")

	// No explicit "." operand (see TestRgGlobSlashPatternRevealsNestedHiddenEntry's comment).
	// Trailing "**" (and "**/**") must NOT match the bare prefix itself.
	_, _, code := cmdRun(t, "rg --files -g 'a/**'", dir)
	assert.Equal(t, 1, code)

	// But it DOES match anything strictly inside.
	stdout, _, code := cmdRun(t, "rg --files -g 'a2/**'", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a2/x\n", stdout)

	// A middle "**" (still followed by a fixed segment, unlike the
	// trailing case above) can consume zero components.
	stdout, _, code = cmdRun(t, "rg --files -g 'a3/**/y'", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a3/y\n", stdout)
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

// TestRgStdinFilenameLabelMatchesRipgrep verifies that stdin's
// filename-bearing output uses "<stdin>", matching real ripgrep exactly,
// not the POSIX-style "(standard input)" label grep uses.
func TestRgStdinFilenameLabelMatchesRipgrep(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `echo x | rg -H x -`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "<stdin>:x\n", stdout)
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

// TestRgExplicitOperandDisplayPathPreservesSpelling verifies that an
// explicit file operand's filename-bearing output uses the operand
// exactly as given, not a cleaned path — verified directly against real
// ripgrep: "rg -H x ./f" prints "./f:x" and "rg -H x a/../f" prints
// "a/../f:x", neither normalized, even though the underlying file access
// itself must still go through the cleaned path for the sandbox.
func TestRgExplicitOperandDisplayPathPreservesSpelling(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f", "x\n")
	require.NoError(t, os.Mkdir(filepath.Join(dir, "a"), 0o755))

	stdout, _, code := cmdRun(t, "rg -H x ./f", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./f:x\n", stdout)

	stdout, _, code = cmdRun(t, "rg -H x a/../f", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a/../f:x\n", stdout)
}

// TestRgDirectoryOperandDisplayPathPreservesSpelling verifies that a
// directory operand's original spelling is prepended to every path
// discovered beneath it, unmodified — verified directly against real
// ripgrep: with a file at "sub/f", "rg -H x ." prints "./sub/f:x" (the
// leading "./" is kept, unlike a cleaned join which would produce
// "sub/f"), "rg -H x ./sub" also prints "./sub/f:x", and "rg -H x
// sub/." prints "sub/./f:x" (the redundant "/." is kept too).
func TestRgDirectoryOperandDisplayPathPreservesSpelling(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sub/f", "x\n")

	stdout, _, code := cmdRun(t, "rg -H x .", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./sub/f:x\n", stdout)

	stdout, _, code = cmdRun(t, "rg -H x ./sub", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./sub/f:x\n", stdout)

	stdout, _, code = cmdRun(t, "rg -H x sub/.", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "sub/./f:x\n", stdout)
}

// TestRgImplicitDefaultOperandHasNoDisplayPrefix verifies the distinction
// between an implicit no-operand default and an explicit "." operand
// (verified directly against real ripgrep): with no path operand at all,
// "rg needle" prints bare "top.txt:needle" (no "./" prefix, at any
// depth), while an explicit "rg needle ." on the same tree prints
// "./top.txt:needle" — same search, different display root, because
// ripgrep only prepends the operand's own spelling, and there is no
// operand to prepend when none was given.
func TestRgImplicitDefaultOperandHasNoDisplayPrefix(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "top.txt", "needle\n")
	writeFile(t, dir, "sub/nested.txt", "needle\n")

	stdout, _, code := cmdRun(t, "rg needle", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "sub/nested.txt:needle\ntop.txt:needle\n", stdout)

	stdout, _, code = cmdRun(t, "rg needle .", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./sub/nested.txt:needle\n./top.txt:needle\n", stdout)
}

// TestRgFilesListDisplayPathPreservesSpelling is the --files analog of
// TestRgDirectoryOperandDisplayPathPreservesSpelling: --files' listing
// must show the same unmodified operand-prefixed paths as ordinary
// search output, since both draw from the same expandOperands result.
func TestRgFilesListDisplayPathPreservesSpelling(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "sub/f", "x\n")

	stdout, _, code := cmdRun(t, "rg --files .", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./sub/f\n", stdout)

	stdout, _, code = cmdRun(t, "rg --files", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "sub/f\n", stdout)
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

// TestRgLineExactlyAtMaxLineBytesSucceeds verifies the precise boundary:
// a line whose CONTENT (excluding the trailing newline) is exactly
// MaxLineBytes long must succeed, not fail with "token too long". The
// package doc comment (and RULES.md) document that only lines EXCEEDING
// the cap fail; bufio.Scanner's ScanLines needs one extra byte of buffer
// beyond the content length to recognize and strip the trailing
// delimiter, so the scanner buffer itself must be sized MaxLineBytes+1,
// not MaxLineBytes, or this exact-boundary case spuriously fails.
func TestRgLineExactlyAtMaxLineBytesSucceeds(t *testing.T) {
	dir := t.TempDir()
	line := strings.Repeat("a", rg.MaxLineBytes) + "\n" // exactly at the cap
	writeFile(t, dir, "file.txt", line)
	_, _, code := cmdRun(t, "rg a file.txt", dir)
	assert.Equal(t, 0, code)
}

// TestRgLineOneByteOverMaxLineBytesErrors verifies the other side of the
// same boundary: a line whose content is exactly one byte OVER
// MaxLineBytes must still fail, so the scanner-buffer +1 fix does not
// accidentally loosen the cap by more than the one byte ScanLines itself
// needs.
func TestRgLineOneByteOverMaxLineBytesErrors(t *testing.T) {
	dir := t.TempDir()
	line := strings.Repeat("a", rg.MaxLineBytes+1) + "\n"
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

// TestRgManyFilesAcrossManyDirectoriesDiscovered is a regression/sanity
// check for the aggregate traversal budget (MaxTotalDiscoveredFiles): a
// tree spread across many directories, each individually far under
// MaxDirEntriesPerLevel, must still be discovered and searched correctly
// at ordinary real-world scale (well under the 1,000,000-file aggregate
// cap). The cap itself is sized the same order of magnitude as this
// codebase's other large aggregate safety bounds (e.g. du's
// maxDedupEntries) and, like those, is not exercised at full scale in a
// Go test (that would require actually creating a million files on
// disk); this test only guards against a regression in the normal case
// caused by the budget-tracking bookkeeping itself.
func TestRgManyFilesAcrossManyDirectoriesDiscovered(t *testing.T) {
	dir := t.TempDir()
	const numDirs, filesPerDir = 20, 50
	for i := 0; i < numDirs; i++ {
		for j := 0; j < filesPerDir; j++ {
			name := fmt.Sprintf("d%d/f%d.txt", i, j)
			content := "other\n"
			if i == 0 && j == 0 {
				content = "needle\n"
			}
			writeFile(t, dir, name, content)
		}
	}
	// An explicit "." operand gets a "./" display prefix, unlike the
	// implicit no-operand default (verified directly: "rg needle ."
	// prints "./top.txt:needle", while bare "rg needle" prints
	// "top.txt:needle" for the same file).
	stdout, _, code := cmdRun(t, "rg -c needle .", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./d0/f0.txt:1\n", stdout)
}

// TestRgManyEmptyDirectoriesDiscoveryStillCompletes is a regression/
// sanity check that MaxTotalDiscoveredFiles/MaxTotalDiscoveredPathBytes
// now charge directory FRAMES pushed onto walkDir's traversal stack (not
// just regular files added to the result list): a tree containing only
// empty directories, with no regular files at all, previously left both
// budgets completely untouched regardless of how many directories were
// traversed. At ordinary scale, a directory-only tree must still be
// discovered and produce correct (empty) results without hanging or
// erroring — like the existing MaxTotalDiscoveredFiles sanity check
// above, this does not exercise the cap at its full 1,000,000 scale
// (that would require an impractically slow test), consistent with the
// established precedent for the du/ls builtins' own aggregate caps.
func TestRgManyEmptyDirectoriesDiscoveryStillCompletes(t *testing.T) {
	dir := t.TempDir()
	const numDirs = 500
	for i := 0; i < numDirs; i++ {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, fmt.Sprintf("empty%d", i)), 0755))
	}
	writeFile(t, dir, "needle.txt", "needle\n")
	stdout, _, code := cmdRun(t, "rg needle .", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./needle.txt:needle\n", stdout)
}

// TestRgTraversalPathByteBudgetSharedAcrossOperands is a regression/
// sanity check for MaxTotalDiscoveredPathBytes (the cumulative path-byte
// cap, independent of MaxTotalDiscoveredFiles' entry count cap): at
// ordinary scale, discovery across multiple directory operands sharing
// one budget must still find every file correctly. Like
// MaxTotalDiscoveredFiles itself, the byte cap is not exercised at its
// full 128 MiB scale in a Go test (that would require gigabytes of path
// data); this only guards against a regression in the normal case caused
// by the added budget-tracking bookkeeping.
func TestRgTraversalPathByteBudgetSharedAcrossOperands(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a/f1.txt", "needle\n")
	writeFile(t, dir, "b/f2.txt", "needle\n")
	stdout, _, code := cmdRun(t, "rg -c needle a b", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a/f1.txt:1\nb/f2.txt:1\n", stdout)
}

// TestRgManySeparateDirectoryOperandsRootFramesCharged is a regression/
// sanity check: each directory OPERAND's own root frame is now charged
// against the shared fileBudget/byteBudget pair before it is pushed onto
// walkDir's traversal stack, not just child frames discovered during
// that operand's own traversal (without this, many separate directory
// operands — "rg x d1 d2 d3 ...", each individually empty — would each
// perform their own ReadDir call while the shared budget never changed
// for any of them, since every operand's first frame IS its root frame).
// Like the existing MaxTotalDiscoveredFiles/MaxTotalDiscoveredPathBytes
// sanity checks elsewhere in this file, this does not exercise the cap
// at its full 1,000,000-entry scale (impractically slow in a Go test);
// it only verifies the added root-frame charging does not regress
// ordinary multi-operand discovery at normal scale.
func TestRgManySeparateDirectoryOperandsRootFramesCharged(t *testing.T) {
	dir := t.TempDir()
	const numDirs = 200
	var dirNames []string
	for i := 0; i < numDirs; i++ {
		name := fmt.Sprintf("empty%d", i)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, name), 0755))
		dirNames = append(dirNames, name)
	}
	writeFile(t, dir, "needle.txt", "needle\n")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stdout, _, code := cmdRunCtx(ctx, t, "rg needle . "+strings.Join(dirNames, " "), dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./needle.txt:needle\n", stdout)
}

// TestRgDuplicateExplicitFileOperandSearchedTwice verifies that naming
// the same file as more than one operand searches (and reports) it once
// per occurrence, matching real ripgrep exactly (verified directly:
// "rg x f f" prints two "f:x" lines) — explicit operands are not
// deduplicated the way directory-traversal results within one operand
// naturally are (a file cannot be visited twice within a single tree
// walk).
func TestRgDuplicateExplicitFileOperandSearchedTwice(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f", "x\n")
	stdout, _, code := cmdRun(t, "rg x f f", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "f:x\nf:x\n", stdout)
}

// TestRgOverlappingDirectoryOperandsSearchFileTwice verifies the same
// no-deduplication rule applies across directory operands too: a file
// reachable from two different directory operands given in the same
// command is searched (and reported) once per operand that reaches it,
// matching real ripgrep exactly.
func TestRgOverlappingDirectoryOperandsSearchFileTwice(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a/shared/f", "z\n")
	stdout, _, code := cmdRun(t, "rg z a a/shared", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a/shared/f:z\na/shared/f:z\n", stdout)
}
