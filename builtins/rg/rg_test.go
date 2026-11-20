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
	"io"
	"os"
	"path/filepath"
	"runtime"
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

// TestRgOptionalNewlinePatternRejected is a regression test: ripgrep
// rejects a pattern where a QUANTIFIED (optional or repeated)
// sub-expression denotes ONLY the newline rune, even though a
// zero-repetition match of such a pattern would not itself consume a
// newline — verified directly against real ripgrep 15.1.0: \n?, \n*,
// \n{0,1}, \n{0,3}, [\n]?, [\n]*, and a\n? (a quantified pure-newline
// sub-expression anywhere in a concatenation) are ALL rejected exactly
// like bare \n itself, with the same "the literal \"\\n\" is not allowed
// in a regex" message and exit 2. The quantifier's own min/max bounds
// (including a min of 0) do not change what the quantified body itself
// denotes, so they must not be consulted when deciding whether the
// pattern is rejected.
func TestRgOptionalNewlinePatternRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "abc\n")
	for _, pat := range []string{
		`\n?`, `\n*`, `\n{0,1}`, `\n{0,3}`, `\n{2,3}`,
		`[\n]?`, `[\n]*`, `[\n]+`, `a\n?`,
	} {
		_, stderr, code := cmdRun(t, "rg '"+pat+"' file.txt", dir)
		assert.Equal(t, 2, code, "pattern %q", pat)
		assert.Contains(t, stderr, `the literal "\n" is not allowed in a regex`, "pattern %q", pat)
	}
}

// TestRgOptionalMixedClassWithNewlineAccepted is the contrasting case:
// a quantified class that denotes something OTHER than just newline
// (e.g. "a" in addition to "\n") is accepted normally, since the
// quantified expression can always avoid consuming a newline by taking
// the non-newline branch instead — verified directly against real
// ripgrep.
func TestRgOptionalMixedClassWithNewlineAccepted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "abc\n")
	for _, pat := range []string{`[a\n]?`, `[a\n]*`, `[a\n]`} {
		_, _, code := cmdRun(t, "rg '"+pat+"' file.txt", dir)
		assert.Equal(t, 0, code, "pattern %q", pat)
	}
}

// TestRgExactZeroRepetitionNewlineAccepted is a DIFFERENT, narrower
// case than TestRgOptionalNewlinePatternRejected above: an EXACT zero
// repetition (\n{0} or \n{0,0}, as opposed to \n{0,1}/\n{0,3}, which
// genuinely COULD still consume a newline on a non-zero repetition) can
// NEVER consume a newline under ANY repetition count, since the only
// permitted count is zero — verified directly against real ripgrep
// 15.1.0: \n{0} and \n{0,0} are BOTH accepted ("rg -c -o '\n{0}'"
// against "a\n" reports count 2, an empty match at every position),
// unlike \n{0,1}/\n{0,3}, which ripgrep rejects exactly like bare \n
// (see TestRgOptionalNewlinePatternRejected's own doc comment). This
// already works correctly without any dedicated special-case: Go's own
// regexp/syntax.Regexp.Simplify (called by requiresNewlineMatch before
// mustMatchNewline ever inspects the parsed tree) already rewrites an
// exact-zero-repetition OpRepeat/OpStar/OpQuest node into a plain
// OpEmptyMatch node BEFORE mustMatchNewline's own OpRepeat/OpStar/
// OpQuest branches (which otherwise ignore the quantifier's own
// min/max bounds entirely, by design — see those branches' own
// comments) ever get a chance to see it, confirmed directly via a
// throwaway script calling syntax.Parse(`\n{0}`,
// syntax.Perl).Simplify() (not itself committed here). A group-wrapped
// form ((?:\n){0}) and a bracket-class form ([\n]{0}) are verified
// here too, confirming this holds for every syntactic shape an exact
// zero repetition can take, not merely the bare-shorthand case.
func TestRgExactZeroRepetitionNewlineAccepted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a\n")
	for _, pat := range []string{`\n{0}`, `\n{0,0}`, `(?:\n){0}`, `[\n]{0}`} {
		stdout, _, code := cmdRun(t, "rg -c -o -e '"+pat+"' file.txt", dir)
		assert.Equal(t, 0, code, "pattern %q", pat)
		assert.Equal(t, "2\n", stdout, "pattern %q", pat)
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

// TestRgSmartCaseHexEscapedUppercaseLiteral is a regression test: a
// hex-escaped literal character (\xHH or \x{HHHH}) must have its
// REPRESENTED rune inspected for uppercase, not be skipped as opaque
// escape syntax — verified directly against real ripgrep 15.1.0: \x41
// denotes literal 'A', so "rg -S '\\x41'" does NOT match lowercase "a"
// (stays case-sensitive), matching a literal 'A' would. \x61 (lowercase
// 'a') behaves the opposite way, confirming the fix inspects the actual
// decoded value rather than, say, always treating a \x escape as
// uppercase.
func TestRgSmartCaseHexEscapedUppercaseLiteral(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "lower.txt", "a\n")
	writeFile(t, dir, "upper.txt", "A\n")

	_, _, code := cmdRun(t, `rg -S '\x41' lower.txt`, dir)
	assert.Equal(t, 1, code, `\x41 denotes uppercase 'A'; smart-case must stay case-sensitive and not match lowercase "a"`)

	stdout, _, code := cmdRun(t, `rg -S '\x41' upper.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "A\n", stdout)

	// \x{41} (the braced form) must be decoded the same way.
	_, _, code = cmdRun(t, `rg -S '\x{41}' lower.txt`, dir)
	assert.Equal(t, 1, code, `\x{41} denotes uppercase 'A' too`)

	// \x61 denotes lowercase 'a': smart-case should stay case-INsensitive.
	stdout, _, code = cmdRun(t, `rg -S '\x61' lower.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n", stdout)
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

// TestRgWordRegexpZeroWidthAdvancesByWholeRuneNotByte is a regression
// test: matchIndices' -w retry loop must advance a whole UTF-8 rune at a
// time when guaranteeing forward progress past a zero-width match, not
// one byte at a time. A byte-at-a-time advance would restart the search
// (and, critically, hasWordBoundaries' own utf8.DecodeLastRune/
// DecodeRune calls) in the middle of a multi-byte rune's continuation
// bytes, each of which is individually invalid UTF-8 and decodes as a
// spurious utf8.RuneError — verified directly against real ripgrep
// 15.1.0: "rg -w -c -o ”" on a single 4-byte U+1F642 emoji character
// (plus trailing newline) reports 2 (the two real boundary positions,
// immediately before and immediately after the whole rune), not 5 (one
// spurious position per byte of the rune, plus the newline) that a
// byte-at-a-time advance would produce. Also verified with the emoji
// embedded between two ASCII word characters (no boundary anywhere, so
// no match at all) and with two consecutive emoji (3 real boundaries:
// start, between the two runes, and end).
func TestRgWordRegexpZeroWidthAdvancesByWholeRuneNotByte(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "single.txt", "\U0001F642\n")
	stdout, _, code := cmdRun(t, "rg -w -c -o '' single.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2\n", stdout)

	writeFile(t, dir, "embedded.txt", "a\U0001F642b\n")
	_, _, code = cmdRun(t, "rg -w -c -o '' embedded.txt", dir)
	assert.Equal(t, 1, code)

	writeFile(t, dir, "double.txt", "\U0001F642\U0001F642\n")
	stdout, _, code = cmdRun(t, "rg -w -c -o '' double.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "3\n", stdout)
}

// TestRgWordRegexpRetriesOverlappingCandidateAfterRejectedMatch is a
// regression test: rejecting a boundary-failing -w candidate must not
// hide a valid OVERLAPPING candidate that starts inside the rejected
// one's own span. Go's regexp.FindAllIndex always advances past a raw
// match's END before searching for the next one, regardless of whether
// that match will later be rejected by the boundary filter; a naive
// "filter FindAllIndex's own non-overlapping results" implementation
// therefore never even considers such an overlapping candidate at all.
// Verified directly against real ripgrep 15.1.0: on "a-bX " with -w
// 'a-b|bX', "a-b" is found first but rejected ('X' immediately after
// fails the right half-boundary), and "bX" (which starts inside "a-b"'s
// own span, at the 'b') is the only match ripgrep reports.
func TestRgWordRegexpRetriesOverlappingCandidateAfterRejectedMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a-bX \n")

	_, _, code := cmdRun(t, `rg -w 'a-b|bX' file.txt`, dir)
	assert.Equal(t, 0, code)

	stdout, _, code := cmdRun(t, `rg -o -w 'a-b|bX' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "bX\n", stdout, "only the retried, boundary-passing 'bX' candidate should be reported, not the rejected 'a-b'")

	stdout, _, code = cmdRun(t, `rg -c -o -w 'a-b|bX' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout)
}

// TestRgWordRegexpInvalidLeftBoundaryRejectsEveryAlternativeAtThatStart
// is a regression test pinning the TRUE reason "a-2X " with -w
// '-2|-2X' reports no match (exit 1): an INVALID LEFT boundary at that
// start position, shared identically by EVERY possible candidate
// length there — NOT an alternation-retry limitation, as an earlier
// version of this test's own doc comment incorrectly claimed (based on
// an incomplete investigation). The '-' in "a-2X " is preceded by 'a',
// a WORD character, which fails -w's own "half boundary" left-side
// requirement (non-word character or start-of-line) regardless of the
// match's own length or content — confirmed directly: a lone,
// non-alternated "-2" by itself (no alternation at all) ALSO fails
// here for the identical reason, and even "-2X" alone independently
// fails too, so NEITHER alternative could ever have passed, with or
// without retrying. This is corroborated by
// TestRgWordRegexpSameStartAlternativeNowRetried below, which proves a
// GENUINE same-start alternative retry (with a VALID, shared left
// boundary for both alternatives) now succeeds correctly.
func TestRgWordRegexpInvalidLeftBoundaryRejectsEveryAlternativeAtThatStart(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a-2X \n")
	_, _, code := cmdRun(t, `rg -w -e '-2|-2X' file.txt`, dir)
	assert.Equal(t, 1, code)

	// Confirm independently: neither alternative, matched alone (no
	// alternation at all), can pass here either — proving the rejection
	// is about this START position's own left boundary, not about
	// alternation order/retry.
	_, _, code = cmdRun(t, `rg -w -e '-2' file.txt`, dir)
	assert.Equal(t, 1, code, "'-2' alone also fails, confirming the left boundary (not alternation) is what rejects this")
	_, _, code = cmdRun(t, `rg -w -e '-2X' file.txt`, dir)
	assert.Equal(t, 1, code, "'-2X' alone also fails, confirming the left boundary (not alternation) is what rejects this")
}

// TestRgWordRegexpSameStartAlternativeNowRetried is a regression test:
// when the LEFTMOST alternative at a given START position has a VALID
// left boundary but fails ONLY its right boundary, a LONGER
// alternative starting at that exact same position IS retried —
// verified directly against real ripgrep 15.1.0: "printf 'ab \n' | rg
// -w -o -e 'a|ab' -" prints "ab", not nothing. "a" is tried first
// (RE2/Perl-style alternation order) and rejected ('b' immediately
// after fails the right boundary; the left boundary, start-of-line, is
// valid and shared by both alternatives), but "ab" (longer, at the
// SAME start) is retried and accepted (' ' after "ab" passes). This
// SUPERSEDES an earlier, now-corrected belief (previously pinned by
// what is now
// TestRgWordRegexpInvalidLeftBoundaryRejectsEveryAlternativeAtThatStart,
// using a DIFFERENT example whose rejection turned out to be caused by
// an invalid LEFT boundary shared by every alternative, not an
// alternation-retry limitation at all) that same-start alternative
// retry was a permanent, ripgrep-matching limitation of this
// implementation.
func TestRgWordRegexpSameStartAlternativeNowRetried(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "ab \n")
	stdout, _, code := cmdRun(t, `rg -w -o -e 'a|ab' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "ab\n", stdout)

	stdout, _, code = cmdRun(t, `rg -w -c -o -e 'a|ab' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout)
}

// TestRgWordRegexpRetryBoundedAgainstQuadraticBlowup is a regression
// test for a P1-severity DoS: with -w 'a+' against a line of roughly 1
// MiB of 'a' bytes followed by a word character, the greedy match is
// rejected at EVERY character position along that run (a rejected
// candidate's own fallback-advance only moves searchFrom forward by
// one rune, not past the whole rejected run), and prior to this fix
// EACH of those positions triggered its own expensive shrinking-
// substring retry AND its own expensive outer re.FindIndex call
// against the remaining (shrinking-by-one) slice — confirmed directly
// to exceed even a 60-second Go test timeout with no ctx deadline
// configured. forEachMatchIndex's word-mode loop now shares ONE
// cumulative byte budget (remainingRetryBudget) across EVERY
// shorterWordMatchAtStart call AND the outer loop's own re-matching
// cost for the whole line, bounding total work regardless of how many
// rejected positions a pathological line contains. Asserted via a
// generous but still catching wall-clock bound (not a strict
// millisecond budget, to avoid CI flakiness).
func TestRgWordRegexpRetryBoundedAgainstQuadraticBlowup(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("a", 1000000) + "b\n"
	writeFile(t, dir, "file.txt", content)

	done := make(chan struct{})
	var code int
	go func() {
		_, _, code = cmdRun(t, "rg -w -e 'a+' file.txt", dir)
		close(done)
	}()
	select {
	case <-done:
		assert.Equal(t, 1, code)
	case <-time.After(60 * time.Second):
		// 60s, not a tight bound: on a fast local machine this completes
		// in well under a second (0.37s), and even under the race
		// detector locally takes only ~6s, but a loaded/slow CI runner
		// can be substantially slower in absolute terms while still
		// being nowhere near the TENS OF SECONDS TO MINUTES this test
		// guards against regressing back to (confirmed directly: the
		// pre-fix code exceeded even a 60-second Go test timeout
		// entirely, with the process still running) — the goal here is
		// catching a reintroduced unbounded/quadratic blowup, not pinning
		// a specific millisecond budget. An earlier, tighter 10s bound
		// was observed to flake on a loaded CI runner despite the fix
		// being correct (completed in a a few seconds there, just over
		// that tighter bound).
		t.Fatal("rg -w 'a+' against a huge rejected run took too long, suggesting the shared retry budget regressed")
	}
}

// TestRgWordRegexpRetryNeverRebasesAnchorToSubsliceStart is a
// regression test: shorterWordMatchAtStart's own retry attempts must
// never let an internal "^"/"\A" anchor in the ORIGINAL pattern be
// re-evaluated as true at a retried candidate's own SUBSLICE start
// (when that start is not the true start of the line) — the exact same
// anchor-rebasing bug class forEachMatchIndex's own searchRe/
// unanchoredRe selection already fixes for the OUTER search, but
// shorterWordMatchAtStart's own retries did not originally receive
// that same treatment. Verified directly against real ripgrep 15.1.0:
// "printf 'x a-b\n' | rg -w -o -e 'a.|^a' -" has NO match at all — the
// leftmost raw match "a-" (via the "a." branch, at a nonzero line
// offset) is rejected on its right boundary, and the "^a" branch must
// NOT then be retried as if position 2 (where "a-" started) were the
// true start of the line.
func TestRgWordRegexpRetryNeverRebasesAnchorToSubsliceStart(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x a-b\n")
	_, stderr, code := cmdRun(t, `rg -w -o -e 'a.|^a' file.txt`, dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stderr)

	// Confirm the ^a branch DOES still correctly match at the TRUE
	// start of a line (start==0), proving this is a targeted fix for
	// the subslice-rebasing case specifically, not a wholesale
	// disabling of the ^a branch.
	writeFile(t, dir, "file2.txt", "a-b\n")
	stdout, _, code := cmdRun(t, `rg -w -o -e 'a.|^a' file2.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n", stdout)
}

// TestRgWordRegexpRetryNeverRebasesEndAnchorToSubsliceEnd is the
// SYMMETRIC counterpart of the START-anchor test above, for a
// TRAILING anchor ("$"/"\z") instead: a retry candidate's own END is
// also not necessarily the true end of the line, so an internal "$"
// must never be re-evaluated as true at the retried candidate's own
// SUBSLICE end when that end is not the true line end — verified
// directly against real ripgrep 15.1.0: "printf 'a-b\n' | rg -w -o -e
// 'a.|a$' -" has NO match at all. The leftmost raw match "a-" (via the
// "a." branch) is rejected on its right boundary ('b' immediately
// after), and the "a$" branch must NOT then be retried against the
// one-byte candidate slice "a" as if its own end (position 1) were
// the true line end (position 3).
func TestRgWordRegexpRetryNeverRebasesEndAnchorToSubsliceEnd(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a-b\n")
	_, stderr, code := cmdRun(t, `rg -w -o -e 'a.|a$' file.txt`, dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stderr)

	// Confirm the a$ branch DOES still correctly match at the TRUE end
	// of a line, proving this is a targeted fix for the subslice-
	// rebasing case specifically, not a wholesale disabling of the a$
	// branch.
	writeFile(t, dir, "file2.txt", "x-a\n")
	stdout, _, code := cmdRun(t, `rg -w -o -e 'x.|a$' file2.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n", stdout)
}

// TestRgWordRegexpRetriesAlternativesInTextualOrderNotByLength is a
// regression test: when multiple alternatives at a boundary-rejected
// candidate's start could independently satisfy the boundary check at
// DIFFERENT lengths, the retry must prefer them in their ORIGINAL
// TEXTUAL (pattern) ORDER, not ordered by resulting match length — an
// earlier version of this retry mechanism (which only had phases
// ordered by length: shorter-first, then longer) wrongly preferred
// the SHORTEST valid alternative regardless of where it appeared in
// the pattern text. Verified directly against real ripgrep 15.1.0: "
// a-b " with -w -o -e 'a.|a..|a' prints "a-b" (the SECOND alternative,
// "a..", matched via its own natural length of 3), not "a" (the
// shortest, LAST alternative, which length-only ordering wrongly
// preferred) — the leftmost alternative "a." is rejected first (its
// own right boundary fails, 'b' immediately after), and "a.." (next in
// textual order) is retried and accepted before "a" (last in textual
// order) is ever considered, even though "a" would ALSO have passed
// if tried.
func TestRgWordRegexpRetriesAlternativesInTextualOrderNotByLength(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", " a-b \n")
	stdout, _, code := cmdRun(t, `rg -w -o -e 'a.|a..|a' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a-b\n", stdout)
}

// TestRgWordRegexpRetryContinuesWithinSameAlternativeBranch is a
// regression test: when a TOP-LEVEL alternative branch itself
// contains a quantifier (e.g. "-*"), rejecting its own greedy/longest
// exact-match length at a given start must still retry PROGRESSIVELY
// SHORTER lengths of that SAME branch before moving on to the NEXT
// alternative — an earlier version of the alternation-retry mechanism
// wrongly treated every branch as having exactly one possible match
// length, breaking out to the next branch immediately after the
// FIRST (longest) length failed its boundary check. Verified directly
// against real ripgrep 15.1.0: "printf '%s\n' '-----a' | rg -w -o -e
// '-*|z' -" prints "----" (4 dashes) — the greedy 5-dash match for the
// "-*" branch fails its right boundary ('a' follows), but ripgrep
// backtracks WITHIN that same branch to the shorter 4-dash length
// (which passes) before ever considering the "z" branch.
func TestRgWordRegexpRetryContinuesWithinSameAlternativeBranch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "-----a\n")
	stdout, _, code := cmdRun(t, `rg -w -o -e '-*|z' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "----\n", stdout)
}

// TestRgWordRegexpRetryRespectsLazyQuantifierDirection is a
// regression test: a top-level alternative branch containing a LAZY
// quantifier (e.g. "a.*?") must have its own retry candidates tried
// SHORTEST-first, extending only as far as NEEDED to satisfy the
// right boundary — NOT longest-first, which is only correct for a
// GREEDY branch (the default, and the common case with no quantifier
// at all). Verified directly against real ripgrep 15.1.0: "rg -w -o
// -e 'a.*?|z'" against "ab " prints "ab", not "ab " (with the
// trailing space, which a plain longest-first scan would wrongly
// settle on): the lazy ".*?"'s own leftmost raw match ("a", 0 extra
// chars) is rejected on its right boundary ('b' follows), and the
// retry must extend by the SMALLEST possible increment (one more
// char, matching "ab") rather than jumping straight to the longest
// possible extent.
func TestRgWordRegexpRetryRespectsLazyQuantifierDirection(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "ab \n")
	stdout, _, code := cmdRun(t, `rg -w -o -e 'a.*?|z' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "ab\n", stdout)
}

// TestRgFixedStringWordRegexpRetriesInGivenOrder is a regression
// test: -F (fixed-strings) combined with -w must also retry MULTIPLE
// -e patterns in the ORDER THEY WERE GIVEN, exactly like regex
// alternation branches do, not fall back to length-ordered retry
// phases — an earlier version of the alternation-retry mechanism
// never populated its ordered-alternatives list for fixed-string
// patterns at all. Verified directly against real ripgrep 15.1.0: "rg
// -F -w -o -e 'a-' -e 'a-b' -e 'a'" against "a-b " prints "a-b" — the
// first pattern "a-" is rejected on its own right boundary ('b'
// follows), and ripgrep retries the SECOND pattern "a-b" (which
// passes) before ever considering the third, "a".
func TestRgFixedStringWordRegexpRetriesInGivenOrder(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a-b \n")
	stdout, _, code := cmdRun(t, `rg -F -w -o -e 'a-' -e 'a-b' -e 'a' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a-b\n", stdout)
}

// TestRgWordRegexpRetryPreservesUnscopedInlineFlag is a regression
// test: an UNSCOPED inline flag group (e.g. "(?i)", with no trailing
// ":" that would otherwise scope it to just its own group's content)
// applies from its own position to the end of the ENCLOSING
// expression, exactly like standard regex semantics (confirmed
// directly via Go's own regexp package, not merely ripgrep: "(?i)a|AB"
// case-folds BOTH alternatives, not just the one it textually
// precedes) — so splitting a pattern into its own top-level
// alternatives for -w's own retry mechanism must still propagate that
// flag forward to every LATER alternative, not just the one(s)
// immediately following it in the pattern text. Verified directly
// against real ripgrep 15.1.0: "printf 'ab \n' | rg -w -o -e
// '(?i)a|AB' -" prints "ab" — the leftmost "a" (case-insensitive, via
// the leaked "(?i)") is rejected on its right boundary, and "AB" (ALSO
// case-insensitive, from the SAME leaked flag) is retried and matches
// the literal lowercase "ab". A SCOPED flag group ("(?i:a)", as
// opposed to the bare "(?i)"), in contrast, must NOT leak into a
// later sibling alternative.
func TestRgWordRegexpRetryPreservesUnscopedInlineFlag(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "ab \n")
	stdout, _, code := cmdRun(t, `rg -w -o -e '(?i)a|AB' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "ab\n", stdout)

	// A later alternative, textually BEFORE any flag group, must stay
	// unaffected by one appearing after it.
	writeFile(t, dir, "file2.txt", "A\n")
	_, _, code = cmdRun(t, `rg -o -e 'a|(?i)b|c' file2.txt`, dir)
	assert.Equal(t, 1, code, "the 'a' branch must stay case-sensitive, unaffected by a LATER (?i)")

	// A SCOPED flag group must NOT leak into a later sibling
	// alternative.
	writeFile(t, dir, "file3.txt", "ab \n")
	_, _, code = cmdRun(t, `rg -w -o -e '(?i:a)|AB' file3.txt`, dir)
	assert.Equal(t, 1, code, "a SCOPED (?i:a) must not leak case-insensitivity into the sibling AB alternative")
}

// TestRgWordRegexpRetryPropagatesFlagRemoval is a regression test:
// isBareInlineFlagGroup (and therefore splitTopLevelAlternatives' own
// pendingFlags propagation) must recognize a flag-REMOVAL bare group
// ("(?-i)"), not just a flag-ENABLING one ("(?i)") — an earlier
// version of this recognizer only accepted plain flag letters,
// silently leaving a previously-enabled flag incorrectly still active
// for later alternatives the pattern's own "(?-i)" was meant to turn
// back off. Verified directly against real ripgrep 15.1.0: "rg -w -o
// -e 'c.|(?i)x|(?-i)y|CZQ'" against "czq " has NO match at all —
// "(?-i)" turns case-folding back off before the final "CZQ"
// alternative, so it must stay case-SENSITIVE and not match lowercase
// "czq". Simply concatenating each bare group's own text in textual
// order (rather than tracking "effective flag state" by hand)
// reproduces this correctly, since Go's own regexp engine applies
// sequential flag changes exactly that way.
func TestRgWordRegexpRetryPropagatesFlagRemoval(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "czq \n")
	_, stderr, code := cmdRun(t, `rg -w -o -e 'c.|(?i)x|(?-i)y|CZQ' file.txt`, dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stderr)
}

// TestRgWordRegexpRetryCachesCompiledVariantsPerBranch is a
// regression test for a P1-severity performance gap: retrying many
// candidate lengths against the SAME alternative branch must compile
// each distinct anchor-stripped exact-match variant AT MOST ONCE (via
// retryRegexCache), not once PER candidateEnd — confirmed as a real,
// severe gap directly: a large (~120 KB) pattern, searched with -w
// against a 10,000-byte line where every position is rejected (forcing
// roughly 10,000 retry attempts), took well over 20 seconds before this
// fix (each attempt re-parsing AND re-compiling the entire large
// pattern from scratch), dropping to single-digit seconds afterward.
// Asserted via a generous wall-clock bound (not a tight budget, to
// avoid CI flakiness) that would still catch a reintroduced per-
// candidateEnd recompilation regression.
func TestRgWordRegexpRetryCachesCompiledVariantsPerBranch(t *testing.T) {
	dir := t.TempDir()
	// A moderately large pattern with a quantifier-heavy alternative,
	// forced into many retry attempts by a line that never satisfies
	// the right boundary. Sized to keep this test's own runtime
	// reasonable even under the race detector (confirmed directly: the
	// finding's own much larger ~120 KB/10,000-byte repro, while well
	// within the fix's own correctness bound, takes well over a minute
	// under -race due to that tool's own substantial overhead — this
	// smaller size still clearly demonstrates per-candidateEnd
	// recompilation would be catastrophic while keeping CI runtime
	// sane) rather than inflating this test's own timeout indefinitely
	// to accommodate an unnecessarily large repro.
	longPattern := strings.Repeat("x?", 10000) + ".*?"
	content := strings.Repeat("a", 2000) + "!\n"
	writeFile(t, dir, "file.txt", content)

	done := make(chan struct{})
	var code int
	go func() {
		_, _, code = cmdRun(t, "rg -w -e '"+longPattern+"|z' file.txt", dir)
		close(done)
	}()
	select {
	case <-done:
		_ = code
	case <-time.After(30 * time.Second):
		t.Fatal("took too long (>30s), suggesting the per-branch regex compilation cache regressed")
	}
}

// TestRgWordRegexpRetryExpandsTransparentGroupAlternatives is a
// regression test: real ripgrep's -w retry mechanism correctly
// backtracks through alternation NESTED inside a transparent group
// ("(...)"/"(?:...)"), not just a bare top-level "a|b|c" — an earlier
// version of the alternation-retry mechanism treated an ENTIRE group
// as one opaque branch, losing the internal priority ordering
// entirely. Verified directly against real ripgrep 15.1.0: "-w -o -e
// '(a.|a..|a)'" against "a-b " (a BARE group) and "-w -o -e
// 'x(a.|a..|a)y'" against "xa-by " (the SAME group EMBEDDED in
// surrounding literal text) both retry to the longest satisfying
// alternative ("a-b"/"xa-by"), not a length-only pick ("a"/"xay").
// Also verified for a group nested ONE level inside another
// transparent group.
func TestRgWordRegexpRetryExpandsTransparentGroupAlternatives(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bare.txt", "a-b \n")
	stdout, _, code := cmdRun(t, `rg -w -o -e '(a.|a..|a)' bare.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a-b\n", stdout)

	writeFile(t, dir, "embedded.txt", "xa-by \n")
	stdout, _, code = cmdRun(t, `rg -w -o -e 'x(a.|a..|a)y' embedded.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "xa-by\n", stdout)

	writeFile(t, dir, "nested.txt", "xa-b \n")
	stdout, _, code = cmdRun(t, `rg -w -o -e 'x(a.|(a..)|a)' nested.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "xa-b\n", stdout)
}

// TestRgWordRegexpRetryDeclinesQuantifiedGroupExpansion is a
// regression test: splicing a transparent group's own alternatives
// into the surrounding prefix/suffix text must be DECLINED entirely
// when the group itself is followed by a quantifier ("*", "+", "?",
// or "{n,m}") — "(a|ab)+" denotes REPEATING the group as a whole,
// where each repetition independently picks EITHER "a" OR "ab" (so
// "aba" is valid: "a" then "ab"), which splicing into separate
// branches "a+" and "ab+" (each only ever repeating ONE fixed choice
// throughout) cannot represent at all. Verified directly against real
// ripgrep 15.1.0: "rg -w -o -e '(a|ab)+'" against "aba " prints
// "aba". Declining the expansion falls back to treating the WHOLE
// quantified group as one opaque branch, which the existing length-
// based retry phases handle correctly (since they test whether the
// WHOLE regex matches exactly at each length, with no alternative-
// splicing semantics to get wrong).
func TestRgWordRegexpRetryDeclinesQuantifiedGroupExpansion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "aba \n")
	stdout, _, code := cmdRun(t, `rg -w -o -e '(a|ab)+' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "aba\n", stdout)
}

// TestRgWordRegexpTransparentGroupExpansionBoundedAgainstMemoryDos is
// a regression test for a P1-severity DoS: expandAlternativesWithTransparentGroups
// spliced the group's own surrounding PREFIX and SUFFIX into EVERY
// inner alternative, with no bound on the TOTAL output size — a
// pattern consisting of a large literal prefix followed by a group
// containing many THOUSANDS of (here, empty) alternatives, while
// individually well within every existing per-pattern byte budget,
// could still provoke an attempted multi-GiB allocation (prefix size
// * alternative count) before any input was ever searched. Verified
// as a real, confirmed gap directly: a ~200 KiB pattern (a ~100 KiB
// literal prefix + a group of ~100,000 empty alternatives) took well
// over 15 seconds (likely attempting an allocation on the order of 10
// GiB) before this fix. maxGroupExpansionOutputBytes now bounds the
// CUMULATIVE size of every spliced result, computed BEFORE any
// allocation happens, falling back to the ordinary (un-expanded)
// top-level split once exceeded.
func TestRgWordRegexpTransparentGroupExpansionBoundedAgainstMemoryDos(t *testing.T) {
	dir := t.TempDir()
	prefix := strings.Repeat("a", 20000)
	alts := strings.Repeat("|", 5000) // 5001 empty alternatives
	pattern := prefix + "(" + alts + ")"
	writeFile(t, dir, "file.txt", "x\n")

	done := make(chan struct{})
	var code int
	go func() {
		_, _, code = cmdRun(t, "rg -e '"+pattern+"' file.txt", dir)
		close(done)
	}()
	select {
	case <-done:
		_ = code
	case <-time.After(10 * time.Second):
		t.Fatal("took too long (>10s), suggesting the group-expansion output-size bound regressed")
	}
}

// TestRgWordRegexpTransparentGroupExpansionHandlesMultiByteRunes is a
// regression test: findSoleTopLevelTransparentGroup's own returned
// indices must be BYTE offsets into the original pattern string, not
// RUNE-count indices into []rune(pattern) — an earlier version
// returned rune indices directly, which the caller then used to
// slice the ORIGINAL byte string, corrupting every subsequent slice
// boundary whenever a multi-byte rune appeared before or inside the
// found group. Verified directly against real ripgrep 15.1.0: "rg -w
// -o -e 'é(a.|a..|a)'" against "éa-b " (é a 2-byte-encoded rune
// preceding the group) prints "éa-b", the longest satisfying
// alternative, not "éa" (a length-only pick, which is what the
// rune/byte index mismatch silently fell back to via a malformed,
// discarded generated alternative).
func TestRgWordRegexpTransparentGroupExpansionHandlesMultiByteRunes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "\xC3\xA9a-b \n")
	stdout, stderr, code := cmdRun(t, `rg -w -o -e 'é(a.|a..|a)' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "\xC3\xA9a-b\n", stdout)
}

// TestRgWordRegexpTransparentGroupExpansionKeepsInlineFlagsScoped is a
// regression test: expandAlternativesWithTransparentGroups spliced an
// inner alternative DIRECTLY between the group's own surrounding
// prefix and suffix text, with no group wrapping of its own — an
// inline flag INSIDE that alternative (e.g. "(?i)") would then
// wrongly leak into the suffix too, since an unscoped inline flag
// group applies from its position to the end of the ENCLOSING
// expression, and splicing without a wrapping group makes that
// enclosing expression the WHOLE spliced result rather than just the
// alternative itself. Verified directly against real ripgrep 15.1.0:
// "rg -w -o -e 'x((?i)a|b)c+'" against "xAcC " has no match at all
// (the group's own "(?i)" is scoped INSIDE the group, so the final
// uppercase "C" cannot satisfy the case-sensitive outer "c+"), but an
// unwrapped splice produced "x(?i)ac+", whose leaked "(?i)" let the
// RETRIED "xAcC" candidate wrongly satisfy "c+" against the uppercase
// "C".
func TestRgWordRegexpTransparentGroupExpansionKeepsInlineFlagsScoped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "xAcC \n")
	_, _, code := cmdRun(t, `rg -w -o -e 'x((?i)a|b)c+' file.txt`, dir)
	assert.Equal(t, 1, code)
}

// TestRgWordRegexpRetriesShorterMatchAtRejectedStart is a regression
// test: when a greedy quantified -w candidate's own right boundary is
// rejected, a SHORTER match of the SAME quantified sub-pattern at that
// exact same start position must still be tried before advancing past
// it entirely — verified directly against real ripgrep 15.1.0: on "-a"
// with -w '-*', the greedy match "-" (length 1) is found first but
// rejected ('a' immediately after fails the right boundary); ripgrep
// then retries the SAME '-*' at the SAME start with a shorter length
// (the empty string, length 0), whose right boundary (still 'a',
// non-word) and left boundary (start of line) both pass, reporting a
// single empty match at byte offset 0 — reports count 1, not 0. This is
// a DIFFERENT mechanism from, though implemented by the SAME function
// (shorterWordMatchAtStart) as, the longer-alternative retry
// TestRgWordRegexpSameStartAlternativeNowRetried documents (retrying a
// shorter length of the SAME quantified expression, as opposed to a
// longer length corresponding to a different alternative branch) —
// shorterWordMatchAtStart's own doc comment covers both phases.
func TestRgWordRegexpRetriesShorterMatchAtRejectedStart(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "-a\n")

	stdout, _, code := cmdRun(t, `rg -w -c -o -e '-*' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout)
}

// TestRgWordRegexpRetriesShorterMatchSuppressesAdjacentEmptyDuplicate
// is a regression test for a bug introduced by the fix above's own
// first implementation attempt: accepting a shorter retried match must
// still apply the same adjacent-empty-match suppression every OTHER
// accepted match already gets (see forEachMatchIndex's own
// lastNonEmptyEnd doc comment) — otherwise a shorter NON-EMPTY retried
// match (not the zero-width case above) is immediately followed by a
// spurious EXTRA empty match reported at its own end, which real
// ripgrep never reports. Verified directly against real ripgrep
// 15.1.0: on "-----a" with -w -c -o '-*', the greedy 5-dash match is
// rejected ('a' immediately after fails the right boundary), the
// shorter 4-dash match at the same start IS accepted (next char is '-',
// non-word, boundary passes), and ripgrep reports count 1 (just the
// 4-dash match), not 2 (which an unguarded retry-acceptance path would
// additionally report, by also accepting a zero-width match sitting
// exactly at the 4-dash match's own end).
func TestRgWordRegexpRetriesShorterMatchSuppressesAdjacentEmptyDuplicate(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "-----a\n")

	stdout, _, code := cmdRun(t, `rg -w -c -o -e '-*' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout)

	stdout, _, code = cmdRun(t, `rg -w -o -e '-*' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "----\n", stdout)
}

// TestRgShorterWordMatchNeverSplitsUTF8Rune is a regression test:
// shorterWordMatchAtStart's own candidateEnd must only ever land on a
// valid UTF-8 rune boundary, never mid-rune — verified directly against
// real ripgrep 15.1.0: "rg -w '.'" against UTF-8 "\xc3\xa9a" (é as a
// 2-byte rune) has NO match at all (the full é candidate's right
// boundary fails: 'a' immediately after is a word character), but
// without a rune-boundary check this loop would retry the 1-byte prefix
// "\xc3" of é's own encoding, which the exact-match regex wrongly
// accepts (its trailing invalid byte decodes as utf8.RuneError,
// satisfying "."), reporting a spurious match real ripgrep never
// produces.
func TestRgShorterWordMatchNeverSplitsUTF8Rune(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "accent.txt", "\xC3\xA9a\n")

	_, stderr, code := cmdRun(t, `rg -w -o -e '.' accent.txt`, dir)
	assert.Equal(t, 1, code, "no match: the full é candidate's right boundary fails, and no shorter mid-rune prefix must ever be tried")
	assert.Equal(t, "", stderr)
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

// TestRgOnlyMatchingSuppressesEmptyMatchImmediatelyAfterNonEmptyMatch is a
// regression test: after a NON-EMPTY match, a zero-width match candidate
// found at EXACTLY that match's own end position must be suppressed
// entirely, not reported as a separate (adjacent, empty) match — matching
// Go's own regexp.FindAllIndex (verified directly: FindAllIndex("x*",
// "x") returns only [[0,1]], never also a trailing [1,1]) and real
// ripgrep 15.1.0 (verified directly: "printf 'x\n' | rg -c -o 'x*' -"
// reports 1, not 2). Before forEachMatchIndex's lastNonEmptyEnd guard
// existed, resuming the search exactly at a non-empty match's own end
// position would immediately find and report that adjacent zero-width
// candidate too, both inflating a "-c -o" count and printing a spurious
// extra empty line under plain -o. A zero-width match that is NOT
// adjacent to a preceding non-empty match (e.g. "a|" against "aab",
// which has a genuine zero-width match at end-of-string, unrelated to
// either "a" match's own end) is still reported normally — this guard
// must not suppress every zero-width match, only ones sitting exactly at
// an immediately preceding non-empty match's end.
func TestRgOnlyMatchingSuppressesEmptyMatchImmediatelyAfterNonEmptyMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "one.txt", "x\n")
	stdout, _, code := cmdRun(t, "rg -c -o 'x*' one.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout)

	stdout, _, code = cmdRun(t, "rg -o 'x*' one.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "x\n", stdout, "must print only the one non-empty match, no spurious adjacent empty line")

	writeFile(t, dir, "two.txt", "xx\n")
	stdout, _, code = cmdRun(t, "rg -c -o 'x*' two.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout)

	writeFile(t, dir, "three.txt", "aab\n")
	stdout, _, code = cmdRun(t, "rg -o 'a|' three.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\na\n\n", stdout, "a genuine, non-adjacent zero-width match (at end of string) must still be reported")
}

// TestRgWordRegexpSuppressesEmptyMatchImmediatelyAfterNonEmptyMatch is
// the -w/--word-regexp counterpart to
// TestRgOnlyMatchingSuppressesEmptyMatchImmediatelyAfterNonEmptyMatch
// above: forEachMatchIndex's wordRegexp branch has its OWN separate
// zero-width-match loop (a different code path from the non-word
// branch, needed for its half-boundary retry logic), so the same
// adjacent-empty-match suppression had to be applied there
// independently — fixing only the non-word branch left this branch
// still broken. Verified directly against real ripgrep 15.1.0: "printf
// '%s\n' '-' | rg -w -c -o -e '-*' -" reports 1, not 2 (a zero-width
// candidate at the SAME position where the non-empty "-" match just
// ended independently passes hasWordBoundaries too, since that check
// only inspects the OUTSIDE context, not whether an earlier match
// already claimed this exact position, so without this guard it gets
// wrongly counted/printed as a second match).
func TestRgWordRegexpSuppressesEmptyMatchImmediatelyAfterNonEmptyMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "one.txt", "-\n")
	stdout, _, code := cmdRun(t, "rg -w -c -o -e '-*' one.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout)

	stdout, _, code = cmdRun(t, "rg -w -o -e '-*' one.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "-\n", stdout, "must print only the one non-empty match, no spurious adjacent empty line")
}

// TestRgOnlyMatchingPreservesStartAnchorAcrossIterations is a
// regression test: a "^"/"\A" (start-of-text/start-of-line) anchor
// must be evaluated only against the TRUE start of the line, not
// re-evaluated as true at each SUBSLICE position forEachMatchIndex's
// own iterative search resumes from — verified directly against real
// ripgrep 15.1.0: "printf 'aaa\n' | rg -c -o '^a' -" reports 1, not 3.
// Before unanchoredRe existed, forEachMatchIndex's non-word branch
// searched successive subslices (line[searchFrom:]) via re.FindIndex to
// stream matches one at a time for DoS safety, but Go's regexp engine
// evaluates "^"/"\A" relative to whatever slice it is GIVEN — so after
// accepting the first match at position 0, resuming the search from
// line[1:] made the SAME anchor spuriously true again at THAT
// subslice's own position 0, wrongly reporting a second (and third)
// match. Also covers the case in which the anchor is only PART of an
// alternation with an unanchored branch ("^a|b"), which still needs
// its own unanchored branch to keep matching normally at every later
// position — unlike a bare "^a", where matching should stop entirely
// after position 0 — verified directly: real ripgrep counts 4 for
// "^a|b" against "ababab" (the initial "a", plus all three "b"s), not
// 6 (which a naive "just make the anchor always true after the first
// search" fix would wrongly produce, since that would let the "^a"
// branch keep matching "a" at every later position too) and not 3
// (which leaving the bug entirely unfixed would still undercount to,
// since \A only ever matches once regardless). Also verified in -w
// (word-regexp) mode, which has its own separate iterative search loop
// with the identical bug.
func TestRgOnlyMatchingPreservesStartAnchorAcrossIterations(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "one.txt", "aaa\n")
	stdout, _, code := cmdRun(t, "rg -c -o '^a' one.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout)

	stdout, _, code = cmdRun(t, "rg -o '^a' one.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n", stdout, "must print only the one anchored match, not one per 'a' in the line")

	writeFile(t, dir, "two.txt", "ababab\n")
	stdout, _, code = cmdRun(t, "rg -c -o -e '^a|b' two.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "4\n", stdout, "the anchored 'a' branch matches once at position 0, plus all three 'b's")

	writeFile(t, dir, "three.txt", "a a a\n")
	stdout, _, code = cmdRun(t, "rg -w -c -o -e '^a' three.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout, "word-regexp mode has its own separate iterative search loop with the identical anchor bug")

	// An unanchored pattern is completely unaffected (regression check).
	writeFile(t, dir, "four.txt", "aaa\n")
	stdout, _, code = cmdRun(t, "rg -c -o 'a' four.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "3\n", stdout)
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
	// ripgrep's own "binary file matches" notice goes to stdout, not
	// stderr (verified directly: "rg abc bin.dat | wc -l" reports 1 with
	// real ripgrep).
	assert.Equal(t, "", stderr)
	assert.Contains(t, stdout, "binary file matches")
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
	stdout, _, code := cmdRunCtx(ctx, t, `{ printf '\0x\n'; yes no; } | rg x -`, dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "binary file matches")
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
	stdout, _, code = cmdRun(t, "rg needle beyond", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "needle")
	// A discovered file's late-NUL-after-a-match notice is the distinct
	// "WARNING: stopped searching..." wording (not "binary file
	// matches", which is only used for explicit files/stdin), and goes to
	// stdout, not stderr — verified directly against real ripgrep.
	assert.Contains(t, stdout, "WARNING: stopped searching binary file after match")
}

// TestRgDiscoveredFileLateNULFullySilencedInCountAndListModes is a
// regression test: unlike plain line-output mode (which prints whatever
// matched before a late-discovered NUL, plus a WARNING notice — see
// TestRgBinaryProbeWindowMatchesRealRipgrep), -c and
// --files-without-match on a directory-discovered file with a NUL found
// only DURING scanning (not caught by the initial 64 KiB probe) must
// report NOTHING for that file at all, discarding any match already
// counted from lines before the NUL — verified directly against real
// ripgrep 15.1.0: "rg -c" on a directory containing exactly this file
// exits 1 with no output, even though an earlier line in the file did
// match, while plain "rg" on the same directory DOES report that earlier
// match. -l, in contrast, is unaffected by binary detection whenever the
// match causing -l to already stop scanning occurs BEFORE the NUL is
// ever reached (verified: exit 0, filename listed) — this is not special
// -l handling for binary files, it is simply that -l's own "stop at
// first match" optimization means it never reaches the NUL line at all
// in this scenario.
func TestRgDiscoveredFileLateNULFullySilencedInCountAndListModes(t *testing.T) {
	dir := t.TempDir()
	content := "needle1\n" + strings.Repeat("x", 70000) + "\x00needle_after1\nneedle_after2\n"
	writeFile(t, dir, "sub/f.txt", content)

	stdout, _, code := cmdRun(t, "rg -c needle sub", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)

	stdout, _, code = cmdRun(t, "rg --files-without-match needle sub", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)

	// -l already stops at the first match ("needle1"), before ever
	// reaching the line containing the NUL, so it is unaffected here.
	stdout, _, code = cmdRun(t, "rg -l needle sub", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "sub/f.txt\n", stdout)

	// Plain mode still reports the earlier match plus the WARNING.
	stdout, _, code = cmdRun(t, "rg needle sub", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "sub/f.txt:needle1")
	assert.Contains(t, stdout, "WARNING: stopped searching binary file after match")
}

// TestRgDiscoveredFileLateNULInsideOverlongLineSilentlySkipped is a
// regression test: a NUL byte occurring past the initial 64 KiB probe,
// inside a single LINE longer than MaxLineBytes (1 MiB, with no
// newline), must still be recognized as a binary file and silently
// skipped for a recursively discovered file — matching real ripgrep
// 15.1.0 exactly (verified directly: 70 KiB of text, a NUL, then 1.1
// MiB more with no newline, searched recursively, exits 1 with no error
// at all). Before nulTracker existed, bufio.Scanner's own line-
// splitting would return false with bufio.ErrTooLong WITHOUT ever
// handing that line's bytes to searchFile at all (Go's bufio.Scanner
// discards an oversized token's content entirely, with no way to
// recover it via the scanner's public API once ErrTooLong fires), so
// this exact scenario produced a "token too long" ERROR (exit 2)
// instead of the silent skip real ripgrep applies to every other binary
// file discovered by traversal. A too-long line with NO NUL at all is
// still this implementation's own genuine memory-safety error (ripgrep
// itself has no line-length cap; MaxLineBytes is hardening this
// codebase deliberately adds beyond it), so that case is unaffected and
// still returns the error.
func TestRgDiscoveredFileLateNULInsideOverlongLineSilentlySkipped(t *testing.T) {
	dir := t.TempDir()
	content := strings.Repeat("a", 70*1024) + "\x00" + strings.Repeat("b", 1024*1024+100000)
	writeFile(t, dir, "sub/f.txt", content)

	stdout, stderr, code := cmdRun(t, "rg foo sub", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)

	// A too-long line with no NUL anywhere is still a genuine error,
	// unaffected by this fix.
	noNulContent := strings.Repeat("a", 1024*1024+500000) + "\n"
	writeFile(t, dir, "sub2/g.txt", noNulContent)
	_, stderr, code = cmdRun(t, "rg foo sub2", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "token too long")
}

// TestRgDiscoveredFileNULBeyondScannerBufferCapStillFound is a
// regression test distinct from the one above: this NUL sits PAST the
// point where bufio.Scanner's own internal buffer already gave up
// (MaxLineBytes+1 bytes into the still-unterminated line), not merely
// past the earlier 64 KiB binary-detection PROBE — nulTracker only
// observes bytes the SCANNER itself requests from the underlying
// reader, and the scanner never requests any bytes beyond its own
// buffer cap before giving up with bufio.ErrTooLong, so a NUL sitting
// further into the same line was still invisible to nulTracker alone
// (producing the same "token too long" error the earlier, narrower fix
// left unaddressed). Verified directly against real ripgrep 15.1.0,
// which has no line-length cap at all and finds a NUL at any distance
// instantly (verified: a NUL a full 10 MiB into an otherwise-text
// file, discovered via directory traversal, still exits 1 silently).
// Continuing to read directly from nulTracker (bypassing the already-
// given-up scanner) in bounded chunks after ErrTooLong fixes this,
// while a too-long line with genuinely no NUL anywhere within the
// additional scan bound still correctly falls through to the existing
// error.
func TestRgDiscoveredFileNULBeyondScannerBufferCapStillFound(t *testing.T) {
	dir := t.TempDir()
	// The NUL is placed AFTER MaxLineBytes (1 MiB) worth of text, so the
	// scanner's own buffer has already given up with ErrTooLong before
	// ever requesting the byte containing this NUL.
	content := strings.Repeat("a", 1024*1024+100000) + "\x00"
	writeFile(t, dir, "sub/f.txt", content)

	stdout, stderr, code := cmdRun(t, "rg foo sub", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)

	// A too-long line with no NUL anywhere is still a genuine error.
	noNulContent := strings.Repeat("a", 1024*1024+100000) + "\n"
	writeFile(t, dir, "sub2/g.txt", noNulContent)
	_, stderr, code = cmdRun(t, "rg foo sub2", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "token too long")
}

// TestRgExplicitFileBinaryDetectionNeverAffectsCountOrListModes is a
// regression test contrasting the discovered-file case above: for an
// EXPLICIT file/stdin operand, binary detection (early or late) never
// suppresses -c/-l/--files-without-match output at all — those modes
// count/list the file exactly as if every line were plain text,
// unaffected by any NUL — verified directly against real ripgrep 15.1.0.
// Only plain line-output mode is affected (stops scanning, prints the
// "binary file matches" notice), which other tests already cover.
func TestRgExplicitFileBinaryDetectionNeverAffectsCountOrListModes(t *testing.T) {
	dir := t.TempDir()
	content := "needle1\n" + strings.Repeat("x", 70000) + "\x00needle_after1\nneedle_after2\n"
	writeFile(t, dir, "f.txt", content)

	stdout, _, code := cmdRun(t, "rg -c needle f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "3\n", stdout)

	stdout, _, code = cmdRun(t, "rg -l needle f.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "f.txt\n", stdout)
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

	stdout, _, code := runScriptWithStdinFile(t, "rg needle -", pr, dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "binary file matches")
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

	stdout, _, code := cmdRun(t, "rg needle sub/bin.dat sub", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, 1, strings.Count(stdout, "binary file matches"))

	stdout, _, code = cmdRun(t, "rg needle sub sub/bin.dat", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, 1, strings.Count(stdout, "binary file matches"))
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

// TestRgFilesQuietStopsListingButStillValidatesLaterOperands is a
// regression test: "--files -q" (whose only observable output is the
// exit status) must stop LISTING after finding the FIRST eligible
// file — matching real ripgrep 15.1.0's own --help, which documents
// this exact combination as stopping at the first file not excluded by
// ignore rules — but EVERY operand's own existence/readability must
// still be validated and reported, regardless of operand order:
// verified directly against real ripgrep, BOTH "rg --files -q missing
// f" (missing given FIRST) AND "rg --files -q f missing" (missing
// given AFTER the first eligible file "f") report "missing: No such
// file or directory" on stderr, still exiting 0 since "f" was found.
// (An EARLIER version of this test wrongly asserted that an operand
// given AFTER the first eligible file produces NO error at all, based
// on an incomplete verification; this is the corrected version.)
// Without -q, in contrast, an error on ANY operand still forces exit 2
// regardless of other operands' success (verified: "rg --files missing
// f" prints "f" but still exits 2) — the exit-status-ignores-later-
// operand-errors behavior is specific to -q's own exit-code priority
// (match-or-found > error > not-found), not a general "--files always
// prioritizes success" rule.
func TestRgFilesQuietStopsListingButStillValidatesLaterOperands(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f", "x\n")

	_, stderr, code := cmdRun(t, "rg --files -q missing f", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr, "missing")

	_, stderr, code = cmdRun(t, "rg --files -q f missing", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr, "missing", "an operand given AFTER the first eligible file must still have its own existence validated and reported, matching real ripgrep")

	stdout, stderr, code := cmdRun(t, "rg --files missing f", dir)
	assert.Equal(t, 2, code, "without -q, an error on any operand still forces exit 2 regardless of other operands' success")
	assert.Equal(t, "f\n", stdout)
	assert.Contains(t, stderr, "missing")
}

// TestRgEmptyGlobIsNoOp is a regression test: a genuinely empty -g
// pattern ("", as opposed to a bare "!") must be a complete no-op,
// exactly as if -g had not been given at all — verified directly
// against real ripgrep 15.1.0: "rg --files -g ” dir" still lists every
// visible file. globMatch("", path) can never match any nonempty path,
// so an empty glob must not count as "an include glob is present" when
// deciding the default allow/deny for other paths, which would
// otherwise flip the default to deny and never re-allow anything (every
// file silently filtered out). Also verified combined with a real
// include and a real exclude glob, in both cases behaving as if the
// empty glob were simply absent.
func TestRgEmptyGlobIsNoOp(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "x\n")
	writeFile(t, dir, "g.log", "y\n")

	stdout, _, code := cmdRun(t, "rg --files -g '' . | sort", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./f.txt\n./g.log\n", stdout)

	stdout, _, code = cmdRun(t, "rg --files -g '' -g '*.txt' .", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./f.txt\n", stdout)

	stdout, _, code = cmdRun(t, "rg --files -g '' -g '!f.txt' .", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./g.log\n", stdout)
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

// TestRgGlobNegatedCharacterClassUsesGitignoreSyntax is a regression
// test: a DIFFERENT kind of negation from the whole-glob "!" prefix
// TestRgGlobExcludeNegation above covers — a gitignore-style negated
// CHARACTER CLASS inside a single glob, e.g. "[!a]" (matches anything
// EXCEPT 'a', per gitignore/ripgrep's own glob syntax, which -g's own
// --help documents as gitignore rules) — must be honored, not treated
// as a literal '!' character-class member the way Go's filepath.Match
// interprets "[!a]" on its own (verified directly: filepath.Match("
// [!a]", "a") and filepath.Match("[!a]", "b") return (true, false),
// backwards from gitignore's intent, since Go has no '!'-negation
// convention and instead parses "[!a]" as "match literal '!' or
// 'a'"). Verified directly against real ripgrep 15.1.0: in a directory
// containing files "a" and "b", "--files -g '[!a]'" lists "b"
// (excludes "a"). Only the FIRST character immediately after an
// unescaped '[' is ever treated as the negation marker, matching
// gitignore's own rule (the same as '^'): a '!' anywhere else inside
// the class is an ordinary literal member (verified directly: "-g
// '[a!]'" matches literal 'a' OR '!', and "-g '[!!]'" negates a class
// containing the single literal member '!', with only the FIRST '!'
// acting as the negation marker). Go's own "[^...]" negation syntax is
// ALSO accepted by real ripgrep's glob engine (verified directly: "-g
// '[^a]'" produces the identical, correctly-negated result to "[!a]"),
// so an already-Go-style negated class is unaffected by this
// translation either way.
func TestRgGlobNegatedCharacterClassUsesGitignoreSyntax(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a", "x")
	writeFile(t, dir, "b", "x")

	stdout, _, code := cmdRun(t, "rg --files -g '[!a]' .", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./b\n", stdout)

	// Go's own "[^...]" negation syntax already worked before this fix
	// and must remain unaffected.
	stdout, _, code = cmdRun(t, "rg --files -g '[^a]' .", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./b\n", stdout)

	// A non-leading '!' inside the class is an ordinary literal member,
	// not a negation marker.
	writeFile(t, dir, "!", "x")
	stdout, _, code = cmdRun(t, "rg --files -g '[a!]' . | sort", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./!\n./a\n", stdout)

	// A leading '!' still negates even when the class's only literal
	// member is itself '!'.
	stdout, _, code = cmdRun(t, "rg --files -g '[!!]' . | sort", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./a\n./b\n", stdout)

	// An escaped bracket ("\\[...\\]", a literal '[' and ']', not a
	// character class at all) must be unaffected by this translation.
	// Unix-only: Go's own filepath.Match docs state escaping is disabled
	// on Windows entirely ("'\\' is treated as path separator" there
	// instead) — a PRE-EXISTING platform divergence in the underlying
	// stdlib this rg implementation calls, unrelated to and unaffected
	// by gitignoreNegatedClassToGo's own fix, which only ever rewrites a
	// leading '!' and never touches or reinterprets '\\' at all.
	if runtime.GOOS != "windows" {
		writeFile(t, dir, "[c]", "x")
		stdout, _, code = cmdRun(t, `rg --files -g '\[c\]' .`, dir)
		assert.Equal(t, 0, code)
		assert.Equal(t, "./[c]\n", stdout)
	}
}

// TestRgGlobSlashInsideClassIsLiteralMember is a regression test: a
// '/' occurring INSIDE an unescaped "[...]" bracket character class is
// a literal class MEMBER, not a real path-component separator —
// verified directly against real ripgrep 15.1.0: in a directory
// containing a file named "a", "--files -g '[a/]'" lists it (the class
// matches literal 'a' OR literal '/', and "a" satisfies the 'a'
// alternative). Before splitGlobSegments existed, globMatch naively
// split on EVERY '/' in the pattern via strings.Split/strings.Contains,
// which wrongly split "[a/]" into malformed segments "[a" and "]" and
// excluded the file entirely (exit 1, when it should exit 0). A REAL
// path separator OUTSIDE any bracket, immediately followed by a class
// that ALSO happens to contain a literal '/', must still split
// correctly at that real separator (verified directly: "-g
// 'sub/[x/]'" against a file at "sub/x" still matches it) — only the
// '/' already inside the open bracket is treated as literal, not every
// '/' in the whole pattern.
func TestRgGlobSlashInsideClassIsLiteralMember(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a", "x")
	stdout, _, code := cmdRun(t, "rg --files -g '[a/]' .", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./a\n", stdout)

	writeFile(t, dir, "sub/x", "x")
	stdout, _, code = cmdRun(t, "rg --files -g 'sub/[x/]' .", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./sub/x\n", stdout)
}

// TestRgGlobSlashInsideClassStillAnchorsFullPathScope is a regression
// test: although a '/' inside an unescaped bracket class is a literal
// class MEMBER rather than a real path-component separator (see
// TestRgGlobSlashInsideClassIsLiteralMember above), its mere PRESENCE
// anywhere in the pattern text — bracketed or not — still disables the
// implicit "match at any depth" basename-matching prefix a genuinely
// slash-free pattern gets, exactly matching real ripgrep's own globset
// behavior (verified directly via --debug output: "-g '[a/]'" compiles
// to the full-path-anchored regex "^[a/]$", never gaining the
// "(?:/?|.*/)" any-depth prefix a slash-free "-g '[a]'" gets, which
// instead compiles to "^(?:/?|.*/)[a]$"). In a tree containing both a
// top-level "a" and a nested "d/a", real ripgrep's "-g '[a/]'" lists
// only the top-level "a": "d/a" (3 characters including the
// separator) cannot match a pattern anchored to match exactly ONE
// character against the WHOLE path. Before this fix, the scope
// decision incorrectly reused containsUnescapedSlashOutsideClass (built
// for the segment-splitting concern above, a DIFFERENT question) and
// wrongly treated "[a/]" as slash-free, applying basename-matching
// scope and incorrectly also listing the nested "d/a".
func TestRgGlobSlashInsideClassStillAnchorsFullPathScope(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a", "")
	writeFile(t, dir, "d/a", "")

	stdout, _, code := cmdRun(t, "rg --files -g '[a/]'", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n", stdout, "only the top-level 'a' should match; 'd/a' must not, since the full path cannot match a pattern anchored to exactly one character")
}

// TestRgGlobNegatedClassLeadingBracketEscaped is a regression test: a
// ']' immediately after a negation marker inside a "[...]" class (e.g.
// "[!]]" or "[^]]") is a LITERAL class member under gitignore/
// ripgrep's own leading-']'-is-literal convention — verified directly
// against real ripgrep 15.1.0: BOTH "--files -g '[!]]'" and "--files
// -g '[^]]'" match every one-character filename EXCEPT ']'. Before
// this fix, gitignoreNegatedClassToGo's own '!'-to-'^' rewriting turned
// "[!]]" into the invalid "[^]]", which Go's filepath.Match rejects
// outright as a syntax error — and the already-Go-syntax "[^]]"
// spelling was ALSO broken independently (a pre-existing gap unrelated
// to the '!'-rewriting fix, since a '^'-spelled glob bypassed that
// rewriting entirely and reached filepath.Match unmodified). Both
// spellings are fixed on non-Windows by escaping the leading ']' (\])
// instead of leaving it bare, which Go's filepath.Match DOES accept
// there. Windows is a DOCUMENTED, deliberate exception, not a gap this
// fix closes: Go's own filepath.Match disables escaping entirely on
// Windows (a lone '\\' there is its own path-separator character
// instead), and Go has no OTHER syntax to express a literal ']'
// inside ANY bracket at all (verified directly: even a POSITIVE class
// with ']' as its first member, e.g. "[]a]", is ALSO a syntax error
// under Go's filepath.Match — there is no leading-']'-is-literal
// convention in Go at all, on any platform, contrary to this test's
// own earlier assumption before that was verified directly) — so on
// Windows, this glob shape keeps its pre-existing loud rejection
// rather than trading it for a silently wrong match (which emitting an
// unescaped '\\' there would produce instead, corrupting the pattern
// with a spurious path separator).
func TestRgGlobNegatedClassLeadingBracketEscaped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a", "x")
	writeFile(t, dir, "]", "x")

	if runtime.GOOS == "windows" {
		_, _, code := cmdRun(t, "rg --files -g '[!]]' .", dir)
		assert.Equal(t, 2, code, "Windows keeps the pre-existing loud rejection for this glob shape, see this test's own doc comment")
		_, _, code = cmdRun(t, "rg --files -g '[^]]' .", dir)
		assert.Equal(t, 2, code)
	} else {
		stdout, _, code := cmdRun(t, "rg --files -g '[!]]' .", dir)
		assert.Equal(t, 0, code)
		assert.Equal(t, "./a\n", stdout)

		stdout, _, code = cmdRun(t, "rg --files -g '[^]]' .", dir)
		assert.Equal(t, 0, code)
		assert.Equal(t, "./a\n", stdout)
	}

	// An ordinary negated class (no leading ']') is unaffected, on every
	// platform.
	stdout, _, code := cmdRun(t, "rg --files -g '[!a]' .", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./]\n", stdout)
}

// TestRgGlobPositiveLeadingBracketEscaped is a regression test: a ']'
// as the FIRST member of a POSITIVE (non-negated) "[...]" class, e.g.
// "[]a]", is ALSO a literal class member under gitignore's leading-
// ']'-is-literal convention — verified directly against real ripgrep
// 15.1.0: "--files -g '[]a]'" matches filenames ']' and 'a' both.
// TestRgGlobNegatedClassLeadingBracketEscaped above only fixed this
// for a NEGATED class ("[!]]"/"[^]]"); the positive case is a
// SEPARATE code path in gitignoreNegatedClassToGo (no negation marker
// consumed at all before checking for a leading ']'), and was still
// broken independently until this test's own fix: "[]a]" reached Go's
// filepath.Match unchanged and was rejected as a syntax error during
// glob validation (Go has NO leading-']'-is-literal convention
// whatsoever, for a negated OR a positive class). Fixed the same way
// as the negated case, on non-Windows only (see that test's own doc
// comment for the full Windows-escaping-disabled rationale, which
// applies identically here).
func TestRgGlobPositiveLeadingBracketEscaped(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a", "x")
	writeFile(t, dir, "]", "x")

	if runtime.GOOS == "windows" {
		_, _, code := cmdRun(t, "rg --files -g '[]a]' .", dir)
		assert.Equal(t, 2, code, "Windows keeps the pre-existing loud rejection for this glob shape")
	} else {
		stdout, _, code := cmdRun(t, "rg --files -g '[]a]' . | sort", dir)
		assert.Equal(t, 0, code)
		assert.Equal(t, "./]\n./a\n", stdout)
	}

	// An ordinary positive class (no leading ']') is unaffected, on
	// every platform.
	writeFile(t, dir, "b", "x")
	stdout, _, code := cmdRun(t, "rg --files -g '[ab]' . | sort", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "./a\n./b\n", stdout)
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
// MEMBER of an existing "[...]" bracket ALONGSIDE ANOTHER MEMBER (not
// as the class's sole content — see TestRgLoneNegatedShorthandInBracket
// below for that exception) is rejected with a clear error rather than
// silently mistranslated: Go's regexp/syntax cannot express "the
// complement of this multi-range union" as a bracket member (see
// errNegatedClassInBracketNotSupported's doc comment for why), so this
// combination is an explicit, documented limitation rather than a
// silently wrong result.
func TestRgUnicodeNegatedClassInBracketRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")

	_, stderr, code := cmdRun(t, `rg '[\Sx]' f.txt`, dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "not supported inside a")

	_, stderr, code = cmdRun(t, `rg '[a\W]' f.txt`, dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "not supported inside a")

	// Two occurrences of the SAME shorthand still count as "alongside
	// another member", not a sole-member exception: \S\S is not \S
	// alone.
	_, stderr, code = cmdRun(t, `rg '[\S\S]' f.txt`, dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "not supported inside a")
}

// TestRgLoneNegatedShorthandInBracket is a regression test: \S or \W as
// the ENTIRE content of a "[...]" bracket, with nothing else alongside
// it (optionally negated by a leading "^"), must be accepted and
// behave exactly like the equivalent standalone escape — unlike the
// alongside-another-member case TestRgUnicodeNegatedClassInBracketRejected
// above covers, there is no union/negation-composition problem at all
// once \S/\W is the class's sole content: "[\S]" means exactly the same
// thing as standalone "\S", and "[^\S]" is the double negation of \S's
// own already-negated set, collapsing back to plain \s. Verified
// directly against real ripgrep 15.1.0: "[\S]" matches non-whitespace
// exactly like standalone \S, "[^\S]" matches whitespace exactly like
// standalone \s, and the same holds for \W/\w via "[\W]"/"[^\W]".
func TestRgLoneNegatedShorthandInBracket(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "space.txt", " \n")
	writeFile(t, dir, "nonspace.txt", "x\n")
	writeFile(t, dir, "word.txt", "x\n")
	writeFile(t, dir, "nonword.txt", "!\n")

	stdout, _, code := cmdRun(t, `rg '[\S]' nonspace.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "x\n", stdout)
	_, _, code = cmdRun(t, `rg '[\S]' space.txt`, dir)
	assert.Equal(t, 1, code)

	stdout, _, code = cmdRun(t, `rg '[^\S]' space.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, " \n", stdout)
	_, _, code = cmdRun(t, `rg '[^\S]' nonspace.txt`, dir)
	assert.Equal(t, 1, code)

	stdout, _, code = cmdRun(t, `rg '[\W]' nonword.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "!\n", stdout)
	_, _, code = cmdRun(t, `rg '[\W]' word.txt`, dir)
	assert.Equal(t, 1, code)

	stdout, _, code = cmdRun(t, `rg '[^\W]' word.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "x\n", stdout)
	_, _, code = cmdRun(t, `rg '[^\W]' nonword.txt`, dir)
	assert.Equal(t, 1, code)
}

// TestRgShorthandRangeEndpointRejected is a regression test: a Perl
// class shorthand (\d/\D/\s/\S/\w/\W) or Unicode property escape
// (\p{...}/\P{...}) used as one endpoint of an apparent "X-Y" range
// inside a "[...]" class must be rejected exactly like real ripgrep,
// not silently reinterpreted as a UNION the way Go's regexp compiler
// would otherwise accept it (e.g. "[\p{Nd}-a]" compiles successfully
// under Go as \p{Nd} ∪ '-' ∪ 'a', a completely different meaning from
// an intended-but-invalid range) — verified directly against real
// ripgrep 15.1.0: "[\d-a]", "[a-\d]", "[\d-\d]", "[\p{L}-a]", and
// "[a-\p{L}]" all reject with a regex parse error (exit 2), regardless
// of which side of the apparent range the shorthand appears on.
func TestRgShorthandRangeEndpointRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")
	for _, pat := range []string{
		`[\d-a]`, `[a-\d]`, `[\d-\d]`, `[x\d-a]`,
		`[\p{L}-a]`, `[a-\p{L}]`,
	} {
		_, stderr, code := cmdRun(t, "rg '"+pat+"' f.txt", dir)
		assert.Equal(t, 2, code, "pattern %q", pat)
		assert.Contains(t, stderr, "invalid range boundary", "pattern %q", pat)
	}
}

// TestRgShorthandRangeEndpointFalsePositivesAccepted is the contrasting
// case: a shorthand that merely appears NEAR a '-' but is not actually
// forming a range (a trailing dash right before the closing ']', a
// leading dash right after '['/'[^', or a shorthand as a plain member
// with no adjacent dash at all) must still be accepted normally,
// matching real ripgrep.
func TestRgShorthandRangeEndpointFalsePositivesAccepted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "5\n")
	for _, pat := range []string{`[\d-]`, `[-\d]`, `[\d]`, `[a\d]`} {
		stdout, _, code := cmdRun(t, "rg '"+pat+"' f.txt", dir)
		assert.Equal(t, 0, code, "pattern %q", pat)
		assert.Equal(t, "5\n", stdout, "pattern %q", pat)
	}
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

// TestRgQELiteralQuotingEscapeRejected is a regression test: \Q/\E
// (Perl/Java-style literal-quoting escapes) must be rejected with exit
// 2, matching real ripgrep 15.1.0 exactly (verified directly: "rg
// '\Qabc\E' f" exits 2 with an "unrecognized escape sequence" parse
// error). Go's regexp compiler, unlike ripgrep's own regex engine,
// silently ACCEPTS \Q...\E as an RE2-specific extension (verified
// directly: regexp.Compile(`\Qabc\E`) succeeds), so without this
// rejection this implementation would silently accept and match syntax
// real ripgrep considers invalid, diverging from advertised
// ripgrep-compatible behavior.
func TestRgQELiteralQuotingEscapeRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "xabcy\n")

	_, stderr, code := cmdRun(t, `rg '\Qabc\E' f.txt`, dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "literal-quoting escape is not supported")

	_, stderr, code = cmdRun(t, `rg '\Eabc' f.txt`, dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "literal-quoting escape is not supported")

	// An ordinary pattern with no \Q/\E must still work normally.
	stdout, _, code := cmdRun(t, `rg abc f.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "xabcy\n", stdout)
}

// TestRgBackslashDigitEscapeRejected is a regression test: a backslash
// followed by ANY decimal digit (\0 through \9, or a longer digit run)
// must be rejected with exit 2, matching real ripgrep 15.1.0's own
// "backreferences are not supported" parse error exactly (verified
// directly against every one of \0 through \9, plus longer runs like
// \12/\123/\777, all of which real ripgrep rejects uniformly). Go's own
// regexp compiler, unlike ripgrep's regex engine, silently ACCEPTS a
// leading-zero or multi-digit run of this form as a legitimate OCTAL
// character escape and compiles/matches it successfully (verified
// directly: "printf 'Sa\n' | rg '\123a' -" would otherwise match "Sa",
// since octal 0123 = 'S', without this rejection) — only a BARE single
// non-zero digit (e.g. a lone \1) happens to already fail Go's own
// compile step with an unrelated "invalid escape sequence" error, so
// relying on Go's compiler alone would miss most of ripgrep's actual
// rejection surface. Also verifies the escaped-backslash distinction
// (\\1, a literal backslash followed by a literal digit, is NOT a
// backslash-digit escape and must still be accepted) and that this
// rejection produces the CORRECT, specific error text even for a digit
// run Go's regexp/syntax parser would otherwise decode as an octal
// escape for a literal newline (\12 = octal 012 = '\n'), which would
// otherwise be intercepted first by the unrelated newline-rejection
// check with the wrong error message.
func TestRgBackslashDigitEscapeRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "Sa\n")

	for _, p := range []string{`\0`, `\1`, `\7`, `\8`, `\9`, `\12`, `\123a`, `\777`} {
		_, stderr, code := cmdRun(t, "rg '"+p+"' f.txt", dir)
		assert.Equal(t, 2, code, "pattern %q", p)
		assert.Contains(t, stderr, "backreferences are not supported", "pattern %q", p)
	}

	// An escaped backslash followed by a digit is NOT a backslash-digit
	// escape: the digit stands on its own as a literal character.
	writeFile(t, dir, "g.txt", "\\1\n")
	stdout, _, code := cmdRun(t, `rg -e '\\1' g.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\\1\n", stdout)

	// An ordinary pattern with no backslash-digit escape must still work
	// normally.
	stdout, _, code = cmdRun(t, `rg Sa f.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "Sa\n", stdout)
}

// TestRgUnicodeEscapesTranslated is a regression test: ripgrep's own
// \uHHHH (exactly 4 hex digits), \UHHHHHHHH (exactly 8 hex digits), and
// braced \u{H...}/\U{H...} (1-6 hex digits, \u and \U behaving
// IDENTICALLY once braced) Unicode code point escapes must be accepted
// and matched, not rejected — verified directly against real ripgrep
// 15.1.0, which accepts all of \u0041, \u{41}, \U00000041, and \U{41}
// and matches 'A' for each. Go's regexp compiler has no \u/\U escape
// syntax of its own at all (verified directly: regexp.Compile(`\u0041`)
// fails with "invalid escape sequence: `\u`"), so these are translated
// to Go's own \x{HEX} form. The non-braced forms consume EXACTLY the
// fixed digit count, leaving any additional digits as separate literal
// characters (verified directly against real ripgrep: \u00041 against
// "\x041" matches \u0004, a control character, followed by a literal
// '1', NOT a 5-digit codepoint) — this is asserted below via a control
// character rather than another printable digit, so an off-by-one in
// the consumed width cannot accidentally still "look right".
func TestRgUnicodeEscapesTranslated(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "A\n")

	for _, p := range []string{`\u0041`, `\u{41}`, `\U00000041`, `\U{41}`} {
		stdout, _, code := cmdRun(t, "rg '"+p+"' f.txt", dir)
		assert.Equal(t, 0, code, "pattern %q", p)
		assert.Equal(t, "A\n", stdout, "pattern %q", p)
	}

	// A longer, non-BMP codepoint (an emoji) via both the braced and
	// exactly-8-digit \U forms.
	writeFile(t, dir, "emoji.txt", "\U0001F642\n")
	stdout, _, code := cmdRun(t, `rg -o '\u{1F642}' emoji.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\U0001F642\n", stdout)
	stdout, _, code = cmdRun(t, `rg -o '\U0001F642' emoji.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\U0001F642\n", stdout)

	// Malformed escapes still exit 2 (real ripgrep also rejects all of
	// these, just with its own, different error text than this
	// implementation's Go-compiler-driven \x{...} rejection).
	for _, p := range []string{`\u12`, `\u{}`, `\u{110000}`} {
		_, _, code := cmdRun(t, "rg '"+p+"' f.txt", dir)
		assert.Equal(t, 2, code, "pattern %q", p)
	}

	// Exact-digit consumption: \u00041 = \u0004 (a control character) +
	// literal '1', not a malformed/misparsed 5-digit codepoint.
	writeFile(t, dir, "ctrl.txt", "\x041\n")
	stdout, _, code = cmdRun(t, `rg -o '\u00041' ctrl.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\x041\n", stdout)

	// -S/--smart-case must still detect an uppercase Unicode escape's
	// REPRESENTED rune, exactly like it already does for \x (verified
	// directly against real ripgrep: "rg -S '\u0041'" does NOT match
	// lowercase "a", i.e. ripgrep stays case-sensitive because \u0041
	// denotes uppercase 'A').
	writeFile(t, dir, "lower.txt", "a\n")
	_, _, code = cmdRun(t, `rg -S '\u0041' lower.txt`, dir)
	assert.Equal(t, 1, code, "smart-case must stay case-sensitive for an uppercase \\u escape")
}

// TestRgUnicodePropertyGoLacksButExposesViaStdlibIsTranslated is a
// regression test: a standalone (not inside "[...]") \p{Name}/\P{Name}
// token naming a Unicode binary property Go's regexp/syntax does not
// support directly (it only supports general categories and scripts),
// but that Go's OWN standard library exposes via unicode.Properties
// (e.g. "White_Space", "ASCII_Hex_Digit", "Dash"), must still be
// accepted and matched — verified directly against real ripgrep
// 15.1.0, which accepts \p{White_Space} (via Rust's regex crate's own
// much larger Unicode property table) and matches a space character.
// Before this fix, EVERY \p{Name} token was copied through to Go's
// regexp.Compile unchanged regardless of whether Go recognized Name,
// so this exact pattern was rejected with exit 2 ("invalid character
// class range"). The translation reuses rangeTableClassMembers (via
// unicode.Properties[name]) to build an explicit \x{lo}-\x{hi} member
// set wrapped in "[...]"/"[^...]" for \p/\P respectively. Scope is
// deliberately bounded to names unicode.Properties exposes (every one
// of which was separately verified to also be ripgrep-accepted) — a
// property NEITHER Go's regexp/syntax NOR unicode.Properties knows
// (e.g. \p{Emoji}, which real ripgrep DOES accept via Rust's own larger
// property table that Go's stdlib has no equivalent of) remains
// correctly rejected, a documented, intentional remainder rather than
// an oversight. An in-bracket occurrence (e.g. "[\p{White_Space}a]")
// is a separate, out-of-scope case left unchanged (still rejected,
// matching this fix's own pre-existing behavior for that shape).
func TestRgUnicodePropertyGoLacksButExposesViaStdlibIsTranslated(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "ws.txt", "a b\n")
	stdout, _, code := cmdRun(t, `rg '\p{White_Space}' ws.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a b\n", stdout)

	// Negated form.
	writeFile(t, dir, "abc.txt", "abc\n")
	stdout, _, code = cmdRun(t, `rg '\P{White_Space}' abc.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "abc\n", stdout)

	// A second, independently verified property (an ASCII hex digit
	// run), confirming this is not special-cased to White_Space alone.
	writeFile(t, dir, "hex.txt", "0x1A\n")
	stdout, _, code = cmdRun(t, `rg -o '\p{ASCII_Hex_Digit}+' hex.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "0\n1A\n", stdout)

	// A property neither Go's regexp/syntax nor unicode.Properties
	// knows (Emoji) remains correctly rejected -- a documented,
	// intentional remainder, not a regression.
	_, _, code = cmdRun(t, `rg '\p{Emoji}' ws.txt`, dir)
	assert.Equal(t, 2, code, "Emoji is not in unicode.Properties and must remain rejected")

	// An in-bracket occurrence remains out of scope (still rejected).
	_, _, code = cmdRun(t, `rg '[\p{White_Space}a]' ws.txt`, dir)
	assert.Equal(t, 2, code, "in-bracket \\p{Name} translation is out of scope for this fix")
}

// TestRgSmartCaseIgnoresGeneratedPropertyRangeUppercase is a
// regression test: -S/--smart-case's own hasUpper detection must
// inspect the pattern the USER wrote, not the text AFTER
// translateUnicodeClasses has expanded a \p{Name} property into
// explicit \x{lo}-\x{hi} ranges (via rangeTableClassMembers) — an
// earlier version called hasUpper on the ALREADY-translated text,
// mistaking a GENERATED range's own uppercase boundary characters
// (e.g. \p{ASCII_Hex_Digit}'s own A-F range) for a literal uppercase
// character the user wrote, wrongly forcing case-sensitive matching
// for an all-lowercase pattern. Verified directly against real
// ripgrep 15.1.0: "rg -S -o 'foo\\p{ASCII_Hex_Digit}'" against "FOOA"
// matches (the pattern is all-lowercase LITERALS; \p{Name}'s own
// property definition happening to include uppercase hex digits is
// not a literal character choice). hasUpper now runs on
// compilePatterns' own ORIGINAL (pre-translation) pattern text, and
// gained its own \u/\U case (mirroring its existing \x case) so a
// GENUINE user-written Unicode escape is still correctly decoded and
// inspected directly from that original text, without relying on
// translateUnicodeClasses' own \x{HEX} rewriting having already run.
func TestRgSmartCaseIgnoresGeneratedPropertyRangeUppercase(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "FOOA\n")
	stdout, _, code := cmdRun(t, `rg -S -o 'foo\p{ASCII_Hex_Digit}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "FOOA\n", stdout)

	// A GENUINE literal uppercase character in the pattern (not a
	// generated range) must still force case-sensitive matching.
	writeFile(t, dir, "file2.txt", "fooa\n")
	_, _, code = cmdRun(t, `rg -S -o 'FOO\p{ASCII_Hex_Digit}' file2.txt`, dir)
	assert.Equal(t, 1, code, "a literal uppercase FOO in the pattern must still force case-sensitive matching")

	// A genuine \u/\U Unicode escape must still be correctly detected
	// as uppercase from the ORIGINAL (pre-translation) text.
	writeFile(t, dir, "file3.txt", "a\n")
	_, _, code = cmdRun(t, `rg -S '\u0041' file3.txt`, dir)
	assert.Equal(t, 1, code, "smart-case must stay case-sensitive for an uppercase \\u escape, detected from the original pattern text")
}

// TestRgUnicodePropertyRangeTableNoUint16OverflowCorruption is a
// regression test: rangeTableClassMembers must iterate a
// unicode.Range16 entry's Lo/Hi/Stride in a type WIDER than their own
// uint16 field type, not uint16 itself — verified as a real,
// confirmed-against-real-ripgrep bug directly: the actual Unicode
// "Dash" property's own R16 table contains an entry
// Lo=0x30a0,Hi=0xfe31,Stride=52625, where 0x30a0+52625 lands EXACTLY on
// 0xfe31 (Hi, correctly the final real member), but adding Stride a
// SECOND time wraps uint16 arithmetic to 0xcbc2 — still <= Hi under an
// unsigned uint16 comparison, so a uint16-typed loop variable kept
// running and appended hundreds of code points that are NOT real Dash
// members at all (confirmed: real ripgrep's \p{Dash} does not match
// U+CBC2, a CJK Unified Ideograph). 10 of the 34 properties
// translatedUnicodePropertyMembers covers are affected by this same
// overflow pattern in at least one R16 entry (Diacritic,
// Sentence_Terminal, Logical_Order_Exception, Pattern_Syntax, STerm,
// Variation_Selector, Dash, Other_Grapheme_Extend, Other_Math,
// Other_Default_Ignorable_Code_Point), independently confirmed via a
// throwaway script simulating every unicode.Properties entry's own R16
// loop (not itself committed here). This also affects wordCharMembers'
// own existing use of rangeTableClassMembers for \w's Unicode
// translation, though neither Other_Alphabetic nor Join_Control (the
// two tables \w's own translation uses) happens to be among the
// affected 10, so \w itself was not actually corrupted by this bug in
// practice — only newly exposed, generically, by this round's
// standalone-\p{Name}-translation feature applying the same helper to
// arbitrary properties.
func TestRgUnicodePropertyRangeTableNoUint16OverflowCorruption(t *testing.T) {
	dir := t.TempDir()
	// U+CBC2 (a CJK ideograph, NOT a dash) must not match \p{Dash}.
	writeFile(t, dir, "notdash.txt", "\uCBC2\n")
	_, _, code := cmdRun(t, `rg '\p{Dash}' notdash.txt`, dir)
	assert.Equal(t, 1, code, "U+CBC2 is not a real Dash member; the pre-fix uint16 overflow wrongly matched it")

	// A real dash (hyphen-minus) still matches.
	writeFile(t, dir, "dash.txt", "a-b\n")
	stdout, _, code := cmdRun(t, `rg -o '\p{Dash}' dash.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "-\n", stdout)

	// The Hi endpoint of the overflowing R16 entry (U+FE31) is a genuine
	// Dash member and must still match -- the fix must not merely stop
	// the overflow by truncating the range too early.
	writeFile(t, dir, "hiendpoint.txt", "\uFE31\n")
	stdout, _, code = cmdRun(t, `rg -o '\p{Dash}' hiendpoint.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\uFE31\n", stdout)
}

// TestRgOnlyMatchingPlainZeroWidthAdvancesByteWiseNotRuneWise is a
// regression test: forEachMatchIndex's NON-word (plain, no -w) zero-
// width-match advancement must move forward by ONE BYTE, not one whole
// UTF-8 rune — verified directly against real ripgrep 15.1.0: "printf
// '%s\n' '\xc3\xa9a' | rg -c -o ”" (the UTF-8 bytes of "\u00e9a",
// i.e. "éa" where é is a 2-byte-encoded rune) reports 4 (a zero-width
// match at EVERY byte offset: 0, 1 -- the continuation byte inside é's
// own 2-byte encoding -- 2, and 3), not 3 (which rune-wise advancement
// wrongly produces, by skipping the mid-rune continuation-byte offset
// entirely). This is the OPPOSITE of the wordRegexp (-w) branch's own
// requirement (see TestRgWordRegexpZeroWidthAdvancesByWholeRuneNotByte,
// which deliberately needs rune-wise advancement so hasWordBoundaries'
// own UTF-8 decoding never resumes mid-rune) -- a non-word, no-
// boundary-semantics -o pattern has no such requirement, and real
// ripgrep's own byte-wise behavior here confirms advancing by a whole
// rune in this branch was simply wrong, not merely a stricter choice.
func TestRgOnlyMatchingPlainZeroWidthAdvancesByteWiseNotRuneWise(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "accent.txt", "\xC3\xA9a\n")
	stdout, _, code := cmdRun(t, "rg -c -o '' accent.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "4\n", stdout)

	writeFile(t, dir, "emoji.txt", "\U0001F600a\n")
	stdout, _, code = cmdRun(t, "rg -c -o '' emoji.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "6\n", stdout)
}

// TestRgNonWordResumeNeverSearchesMidRune is a regression test for a
// side effect of the byte-wise zero-width advancement fix above: once
// forEachMatchIndex resumes a search at searchFrom landing mid-rune (one
// byte past an accepted zero-width match sitting right before a
// multi-byte rune), the regex engine must never be asked to find a
// NON-EMPTY match STARTING at that invalid position — Go's regexp
// engine, given a byte slice beginning with an invalid UTF-8
// continuation byte, silently decodes it as a single utf8.RuneError and
// lets "." match it as if it were one real character, which real
// ripgrep 15.1.0 never does (Rust's regex crate only ever starts a
// non-empty match at a genuine char boundary). Verified directly:
// "printf '%s\n' '\xc3\xa9a' | rg -c -o '^|.'" (UTF-8 for "éa") reports
// 2 (an empty match at position 0 via the \A branch, then "a" at
// position 2 — the invalid mid-rune position 1 produces NO match at
// all, neither empty nor spurious), not 3, which the uncorrected
// behavior wrongly produced by accepting a spurious one-byte match of
// the RuneError-decoded continuation byte at position 1.
func TestRgNonWordResumeNeverSearchesMidRune(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "accent.txt", "\xC3\xA9a\n")

	stdout, _, code := cmdRun(t, `rg -c -o -e '^|.' accent.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2\n", stdout)

	stdout, _, code = cmdRun(t, `rg -o -e '^|.' accent.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\na\n", stdout, "the empty match's own blank line, then 'a' -- never a spurious third match for the invalid mid-rune continuation byte")
}

// TestRgDotNeverMatchesInvalidUTF8Byte is a regression test:
// forEachMatchIndex's non-word branch must never let "." (or any
// construct not reduced to a trivial always-empty match) match a
// genuinely invalid UTF-8 byte, even one that is its OWN rune start
// (unlike the mid-rune-continuation-byte case covered by
// TestRgNonWordResumeNeverSearchesMidRune above, utf8.RuneStart alone
// does not catch this: a byte like 0xff passes RuneStart's "not a
// continuation byte" check yet is never a valid UTF-8 lead byte
// either). Verified directly against real ripgrep 15.1.0: "rg -a '.'"
// against a file containing a single invalid 0xff byte exits 1 with
// no output; this implementation's own re.Match-based existence
// check previously wrongly reported a match (and printed the raw
// line) via Go's regexp engine decoding the invalid byte as
// utf8.RuneError and letting "." match it as if it were a real
// character.
func TestRgDotNeverMatchesInvalidUTF8Byte(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.bin"), []byte{0xff, '\n'}, 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := cmdRun(t, "rg -a '.' bad.bin", dir)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Equal(t, "", stderr)

	_, _, code = cmdRun(t, "rg -a -o '.' bad.bin", dir)
	assert.Equal(t, 1, code)

	_, _, code = cmdRun(t, "rg -a -w '.' bad.bin", dir)
	assert.Equal(t, 1, code, "the same gap applies equally in -w/wordRegexp mode")
}

// TestRgGreedyMatchNeverSpansAcrossInvalidUTF8Byte is a regression
// test: a greedy construct like ".+" must stop AT an invalid UTF-8
// byte, never span across it as a single match, matching real
// ripgrep's own "invalid byte is an uncrossable wall" model exactly
// (verified directly: "printf 'aa\\xffbb\\n' | rg -a -o '.+'" against
// real ripgrep 15.1.0 reports "aa" and "bb" as TWO separate matches,
// never "aa\xffbb" as one). firstInvalidUTF8ByteOffset truncates a
// match's own end back to the start of the first invalid byte it
// spans, so the search loop naturally finds the valid run before it,
// then (after skipping the invalid byte itself) the valid run after
// it as a second, independent match.
func TestRgGreedyMatchNeverSpansAcrossInvalidUTF8Byte(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.bin"), []byte{'a', 'a', 0xff, 'b', 'b', '\n'}, 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, _, code := cmdRun(t, "rg -a -o '.+' bad.bin", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "aa\nbb\n", stdout)
}

// TestRgWallBoundedSearchNeverReportsFalseMatchAcrossInvalidByte is a
// regression test: a greedy match's search window is bounded to stop
// at the next invalid UTF-8 byte (the "wall"), so a pattern needing
// content ONLY reachable by crossing that wall never gets a chance to
// match ACROSS it in the first place — a pattern like "a.*b" needs a
// 'b' AFTER whatever "." consumed, and if that 'b' is only reachable
// by crossing the invalid byte, no match is found there at all.
// Verified directly against real ripgrep 15.1.0: with bytes
// "a\xffb\n", "rg -a -o 'a.*b'" exits 1 (no match at all), not
// reporting "a" (which an EARLIER, now-replaced truncate-the-match-
// after-the-fact approach could wrongly produce without its own
// extra revalidation step). A SEPARATE, later valid segment where the
// pattern CAN fully match independently (past the wall, in its own
// bounded window) is still correctly found (verified directly: "rg
// -a -o 'a.*b'" against "xx\xffayybzz\n" prints "ayyb", matched
// entirely within the window AFTER the invalid byte).
func TestRgWallBoundedSearchNeverReportsFalseMatchAcrossInvalidByte(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "bad.bin"), []byte{'a', 0xff, 'b', '\n'}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, code := cmdRun(t, "rg -a -o 'a.*b' bad.bin", dir)
	assert.Equal(t, 1, code)

	writeFile(t, dir, "later.bin", "xx\xffayybzz\n")
	stdout, _, code := cmdRun(t, "rg -a -o 'a.*b' later.bin", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "ayyb\n", stdout)

	// The same wall-bounded search mechanism applies equally in
	// -w/wordRegexp mode.
	if err := os.WriteFile(filepath.Join(dir, "bad_w.bin"), []byte{'a', 0xff, 'b', '\n'}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, code = cmdRun(t, "rg -a -w -o 'a.*b' bad_w.bin", dir)
	assert.Equal(t, 1, code)
}

// TestRgWallBoundedSearchIsLinearNotQuadratic is a regression test
// for a P1-severity quadratic DoS: an EARLIER approach to invalid-
// UTF-8 handling found a greedy match spanning the WHOLE rest of the
// line, truncated it back to the first invalid byte it crossed, and
// (upon that truncated candidate failing its own revalidation against
// the pattern) retried from just past the ORIGINAL match's start —
// for a line with many 'a' characters each immediately followed by an
// invalid byte, searched with a pattern requiring content unreachable
// without crossing every single one, this forced a FULL remaining-
// line FindIndex call at EVERY SUCH POSITION, confirmed directly as a
// genuine O(line-length²) gap (20,000 positions took over 1.7
// seconds). Bounding the SEARCH WINDOW itself to the next invalid
// byte (rather than truncating a match after the fact) means each
// FindIndex call only ever costs O(window width), making the total
// cost O(line length) — confirmed directly: 500,000 such positions
// (far more than the 20,000 that took 1.7s under the old approach)
// now completes in single-digit milliseconds, well under this test's
// own 2-second budget (chosen tightly enough that a REGRESSION back
// to quadratic behavior at this scale would still fail it, unlike an
// earlier version of this test with a 10-second budget that merely
// proved "bounded," not "linear").
func TestRgWallBoundedSearchIsLinearNotQuadratic(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := 0; i < 500000; i++ {
		sb.WriteByte('a')
		sb.WriteByte(0xff)
	}
	sb.WriteByte('\n')
	if err := os.WriteFile(filepath.Join(dir, "dos.bin"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		cmdRun(t, "rg -a -o 'a.*ZZZZ' dos.bin", dir)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("took too long (>2s), suggesting the wall-bounded search regressed back to quadratic behavior")
	}
}

// TestRgWordRegexpWallBoundedSearchIsLinearNotQuadratic is the
// -w/wordRegexp-mode counterpart of
// TestRgWallBoundedSearchIsLinearNotQuadratic above: the identical
// quadratic gap applied equally to the word-regexp branch (confirmed
// directly: 50,000 positions took over 1.5 seconds there too, under
// the SAME truncate-then-revalidate approach), fixed by applying the
// identical wall-bounded search window mechanism.
func TestRgWordRegexpWallBoundedSearchIsLinearNotQuadratic(t *testing.T) {
	dir := t.TempDir()
	var sb strings.Builder
	for i := 0; i < 500000; i++ {
		sb.WriteByte('a')
		sb.WriteByte(0xff)
	}
	sb.WriteByte('b')
	sb.WriteByte('\n')
	if err := os.WriteFile(filepath.Join(dir, "dos_w.bin"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		cmdRun(t, "rg -a -w -o 'a.*b' dos_w.bin", dir)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("took too long (>2s), suggesting the word-regexp wall-bounded search regressed back to quadratic behavior")
	}
}

// TestRgWallTrackerAmortizedNotPerQueryRescan is a regression test
// for a P1-severity quadratic DoS: an earlier version of the wall-
// bounded search above called firstInvalidUTF8ByteOffset(line[searchFrom:])
// DIRECTLY on every single outer-loop iteration, re-scanning the
// ENTIRE remaining suffix of the line from scratch each time —
// confirmed as a genuine, severe gap directly: a pattern producing a
// zero-width match at every byte position (the bare empty pattern)
// against a 1 MiB entirely-valid-UTF-8 line advances searchFrom by
// exactly one byte per iteration, so that direct call alone re-scans
// roughly 1 MiB, then 1 MiB-1, then 1 MiB-2, and so on for every one
// of those ~1,000,000 iterations — O(line length²) total, confirmed
// to exceed even a 10-second test timeout, while real ripgrep 15.1.0
// handles the identical input in under 110ms. wallTracker now caches
// the result of its own last scan and resumes forward from there
// instead, giving amortized O(1) per query / O(line length) total.
func TestRgWallTrackerAmortizedNotPerQueryRescan(t *testing.T) {
	dir := t.TempDir()
	line := strings.Repeat("a", 1<<20)
	if err := os.WriteFile(filepath.Join(dir, "big.txt"), []byte(line+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		cmdRun(t, "rg -c -o '' big.txt", dir)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		// 15s, not a tighter bound: confirmed under `go test -race`
		// (which CI always runs with) this takes ~1.7s on a fast
		// machine, but CI runners observed roughly 2x slower than
		// that pushed a 2s bound over the edge (confirmed: a real CI
		// run failed at 2.01s against a 2s timeout) -- see
		// TestRgGroupExpansionBudgetSharedAcrossAllPatterns's own
		// comment for the identical CI-slowness rationale applied
		// there too.
		t.Fatal("took too long (>15s), suggesting the wall tracker's amortized caching regressed back to per-query rescanning")
	}

	// The SAME scenario in -w/wordRegexp mode (which has its own,
	// separately wired wallTracker usage via the SAME shared instance).
	wline := strings.Repeat("a ", 1<<19)
	if err := os.WriteFile(filepath.Join(dir, "big_w.txt"), []byte(wline+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	done2 := make(chan struct{})
	go func() {
		cmdRun(t, "rg -w -c -o '' big_w.txt", dir)
		close(done2)
	}()
	select {
	case <-done2:
	case <-time.After(15 * time.Second):
		t.Fatal("took too long (>15s) in -w mode")
	}
}

// TestRgWallTrackerHandlesMidRuneQueryNotJustRuneAlignedCache is a
// regression test for a correctness bug introduced by
// TestRgWallTrackerAmortizedNotPerQueryRescan's own caching fix
// above: a cached scan built from a RUNE-ALIGNED starting position
// (e.g. position 0, right before a 2-byte rune like é) decodes that
// multi-byte rune as ONE unit and so never independently visits (or
// flags) a position landing MID-RUNE within it (e.g. position 1, é's
// own second byte) — yet forEachMatchIndex's own byte-wise zero-
// width-match advancement can and does legitimately resume a search
// at exactly such a mid-rune position. An earlier version of the
// cache wrongly trusted ANY query position falling within its
// already-scanned span as "already proven valid," silently papering
// over this gap and letting a THIRD, spurious match be reported where
// real ripgrep reports only two. wallTracker.nextAtOrAfter now checks
// the queried position itself DIRECTLY (a single bounded DecodeRune
// call) before ever consulting the cache.
func TestRgWallTrackerHandlesMidRuneQueryNotJustRuneAlignedCache(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "accent.txt", "\xC3\xA9a\n")

	stdout, _, code := cmdRun(t, `rg -c -o -e '^|.' accent.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2\n", stdout)

	stdout, _, code = cmdRun(t, `rg -o -e '^|.' accent.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "\na\n", stdout, "the empty match's own blank line, then 'a' -- never a spurious third match for the invalid mid-rune continuation byte, even once the wall position is cached")
}

// TestRgGroupExpansionBudgetSharedAcrossAllPatterns is a regression
// test for a P1-severity DoS: expandAlternativesWithTransparentGroups'
// own output-size cap was applied INDEPENDENTLY to each call, reset
// to a fresh full allowance for every top-level -e/positional
// pattern, so the AGGREGATE expansion across many patterns — each
// individually staying under that per-pattern cap — was not bounded
// at all. Confirmed as a real, severe gap directly: 32 patterns, each
// a ~4 KiB literal prefix plus a group of ~4,000 empty alternatives
// (summing to well under every raw-pattern-size budget), independently
// expand to just under the (then per-pattern) cap EACH, summing in
// aggregate to roughly 32x that amount, with the resulting ~128,000
// total expanded branches then all needing to be compiled — took over
// 10 seconds before this fix. A single budget, shared via a pointer
// across every pattern in the SAME invocation and never replenished
// between them, closes this regardless of how many separate patterns
// it is spread across.
func TestRgGroupExpansionBudgetSharedAcrossAllPatterns(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "x\n")

	prefix := strings.Repeat("a", 4000)
	alts := strings.Repeat("|", 4000)
	pattern := prefix + "(" + alts + ")"

	var sb strings.Builder
	sb.WriteString("rg")
	for i := 0; i < 32; i++ {
		sb.WriteString(" -e '")
		sb.WriteString(pattern)
		sb.WriteString("'")
	}
	sb.WriteString(" f.txt")

	done := make(chan struct{})
	go func() {
		cmdRun(t, sb.String(), dir)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		// 30s, not a tighter bound: this test is ~13x slower under
		// `go test -race` (confirmed directly: 7.95s under -race vs
		// 0.6s without) due to race-detector instrumentation overhead
		// alone, unrelated to this fix's own actual performance -- and
		// a real CI run (which always runs with -race) still timed
		// out at 15.01s against an earlier, tighter 15s bound, so CI
		// runners are observed to run meaningfully slower than this
		// 7.95s local -race measurement too.
		t.Fatal("took too long (>30s), suggesting the shared group-expansion budget regressed back to a per-pattern allowance")
	}
}

// TestRgTrailingAnchorNeverSatisfiedByWallBoundary is a regression
// test: a bounded search WINDOW's own end (at an invalid UTF-8 byte,
// not the true line end) must never let a trailing "$"/"\z" anchor
// (including the implicit one -x/--line-regexp wraps the whole
// pattern in) wrongly match there, exactly like a retry candidate's
// own end must never satisfy "$"/"\z" elsewhere in this package.
// Verified directly against real ripgrep 15.1.0: with bytes
// "a\xffb\n", "rg -a -x -o -e 'a.*b|a'" exits 1 (since -x requires
// the WHOLE line to match, and "a" alone is only the first byte of a
// 3-byte line), but an earlier version of this invalid-UTF-8 handling
// wrongly accepted the second alternative "a" as a whole-line match
// against the window bounded at the wall. The equivalent explicit
// "$" case (without -x) is also covered.
func TestRgTrailingAnchorNeverSatisfiedByWallBoundary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "f.bin"), []byte{'a', 0xff, 'b', '\n'}, 0o644); err != nil {
		t.Fatal(err)
	}

	_, _, code := cmdRun(t, "rg -a -x -o -e 'a.*b|a' f.bin", dir)
	assert.Equal(t, 1, code)

	_, _, code = cmdRun(t, "rg -a -o -e 'a.*b|a$' f.bin", dir)
	assert.Equal(t, 1, code)

	// A trailing anchor genuinely at the TRUE line end (no invalid
	// byte intervening) must still correctly match.
	writeFile(t, dir, "g.txt", "xa\n")
	stdout, _, code := cmdRun(t, "rg -o -e 'a$' g.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\n", stdout)
}

// TestRgWordBoundaryNeverSatisfiedByAdjacentInvalidByte is a
// regression test: hasWordBoundaries' own left/right half-boundary
// checks must treat an adjacent INVALID UTF-8 byte as FAILING that
// side (the same as a word character would), not as satisfying it
// (the way an ordinary non-word character or the true line
// start/end would) — confirmed as a real, confirmed-against-real-
// ripgrep gap directly: "rg -a -w -o 'ab'" against "ab" immediately
// followed by an invalid byte has no match at all under real ripgrep
// 15.1.0, and "rg -a -w -c -o ”" against JUST a lone invalid byte
// (where the only possible left-half check, start-of-line, is
// trivially satisfied) followed by a newline reports ZERO matches,
// not one — so the RIGHT-half check against that invalid byte must
// independently fail too, confirming an adjacent invalid byte fails a
// boundary check in EITHER direction, not merely the one a naive
// "RuneError decodes as non-word, so treat it like one" reading would
// suggest.
func TestRgWordBoundaryNeverSatisfiedByAdjacentInvalidByte(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "right.bin"), []byte{'a', 'b', 0xff, 'c', '\n'}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, code := cmdRun(t, "rg -a -w -o 'ab' right.bin", dir)
	assert.Equal(t, 1, code, "right boundary must not be satisfied by an adjacent invalid byte")

	if err := os.WriteFile(filepath.Join(dir, "left.bin"), []byte{'a', 0xff, 'b', 'c', '\n'}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, code = cmdRun(t, "rg -a -w -o 'bc' left.bin", dir)
	assert.Equal(t, 1, code, "left boundary must not be satisfied by an adjacent invalid byte")

	if err := os.WriteFile(filepath.Join(dir, "lone.bin"), []byte{0xff, '\n'}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, code = cmdRun(t, "rg -a -w -c -o '' lone.bin", dir)
	assert.Equal(t, 1, code, "a lone invalid byte must satisfy neither half-boundary check")

	// A GENUINE non-word character (not an invalid byte) in the same
	// position must still correctly satisfy the boundary, confirming
	// this fix did not overcorrect into rejecting valid non-word
	// adjacency too.
	writeFile(t, dir, "valid.txt", "ab!c\n")
	stdout, _, code := cmdRun(t, "rg -w -o 'ab' valid.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "ab\n", stdout)
}

// TestRgNewlineViaUnicodeEscapeRejected is a regression test: a
// Unicode code point escape denoting a newline (\u000A, \u{A},
// \U0000000A — all decoding to U+000A LINE FEED) must be rejected with
// the SAME "the literal \"\\n\" is not allowed in a regex" error real
// ripgrep 15.1.0 gives for a raw newline byte or the plain \n escape,
// not silently accepted. requiresNewlineMatch's pre-translation check
// runs on the ORIGINAL pattern text (it must, per its own doc comment,
// to correctly defer to the LATER, more specific backreference
// rejection for a \<digit> pattern) — but \u/\U is not valid Go regex
// syntax on its own, so syntax.Parse fails outright on it and
// requiresNewlineMatch's own err!=nil branch silently returns false,
// before translateUnicodeClasses has ever converted it to Go's \x{A}
// form (which DOES compile successfully as a literal newline). Without
// a SECOND newline check after translation, such a pattern would
// bypass the newline rejection entirely and reach regexp.Compile
// successfully instead.
func TestRgNewlineViaUnicodeEscapeRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "test\n")

	for _, p := range []string{`\u000A`, `\u{A}`, `\U0000000A`} {
		_, stderr, code := cmdRun(t, "rg '"+p+"' f.txt", dir)
		assert.Equal(t, 2, code, "pattern %q", p)
		assert.Contains(t, stderr, "the literal", "pattern %q", p)
		assert.Contains(t, stderr, "not allowed", "pattern %q", p)
	}

	// An ordinary Unicode escape not denoting a newline still works.
	writeFile(t, dir, "g.txt", "A\n")
	stdout, _, code := cmdRun(t, `rg '\u0041' g.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "A\n", stdout)
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

// TestRgWordRegexpHalfBoundaryUsesFullUnicodeWordSet is a regression
// test: -w/--word-regexp's half-boundary check (isWordRune/
// hasWordBoundaries) must use the exact same complete Unicode
// word-character definition as \w's own translation (wordCharMembers),
// not a narrower unicode.IsLetter/IsDigit/M/Pc-only predicate — verified
// directly against real ripgrep 15.1.0: "printf 'x\u2167\n' | rg -w x -"
// exits 1 (no match), since U+2167 ROMAN NUMERAL EIGHT (Unicode category
// Nl, which unicode.IsLetter does not cover) continues the word after
// 'x' rather than ending it there, so 'x' alone is not a whole word. A
// genuine boundary (a following space) still matches normally.
func TestRgWordRegexpHalfBoundaryUsesFullUnicodeWordSet(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "noboundary.txt", "x\u2167\n")
	writeFile(t, dir, "realboundary.txt", "x y\n")

	_, _, code := cmdRun(t, `rg -w x noboundary.txt`, dir)
	assert.Equal(t, 1, code)

	stdout, _, code := cmdRun(t, `rg -w x realboundary.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "x y\n", stdout)
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

// TestRgUnicodeClassExpansionAggregateCapRejected is a DoS regression
// test: translateUnicodeClasses' expansion factor (e.g. \w's ~3.9 KiB
// wordCharMembers per 2-byte \w token) means the raw-input
// MaxAggregatePatternBytes cap alone does not bound the size of the
// TRANSLATED text — a raw pattern well under that cap can still expand
// to many times its own size once every shorthand class is substituted.
// A large number of \w tokens (well under MaxAggregatePatternBytes in
// raw form) must be rejected once the EXPANDED text exceeds
// MaxAggregateExpandedPatternBytes, and quickly (before the full
// expansion/compilation cost is paid for every remaining pattern).
func TestRgUnicodeClassExpansionAggregateCapRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")

	// rg.MaxAggregateExpandedPatternBytes / len(wordCharMembers-ish ~3.9KiB)
	// comfortably exceeded by enough \w tokens; each \w is 2 raw bytes, so
	// this stays far under MaxAggregatePatternBytes (256 KiB) in raw form
	// while still exceeding the 16 MiB expanded cap. translateUnicodeClasses
	// aborts INCREMENTALLY as soon as its own builder would exceed the
	// budget passed to it (not after fully expanding the whole pattern
	// first — see TestRgUnicodeClassExpansionAbortsIncrementallyWithoutFullyExpanding
	// for a direct check of that bound), so this single accepted-looking
	// pattern rejects quickly rather than allocating hundreds of MiB
	// first.
	const numTokens = 120_000 // ~240 KiB raw, but ~450+ MiB once expanded
	pattern := strings.Repeat(`\w`, numTokens)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, stderr, code := cmdRunCtx(ctx, t, "rg '"+pattern+"' f.txt", dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "expands to more than")
}

// TestRgUnicodeClassExpansionAbortsIncrementallyWithoutFullyExpanding is a
// DoS regression test for the SAME scenario as
// TestRgUnicodeClassExpansionAggregateCapRejected, but verifying the
// ACTUAL MECHANISM directly via runtime.MemStats rather than only the
// end-to-end exit code/error message: translateUnicodeClasses must abort
// as soon as its own internal builder would exceed the budget passed to
// it, not after fully expanding the whole oversized pattern into memory
// first and only checking the RESULT afterward. Without the incremental
// check (i.e. checking only after translateUnicodeClasses returns), this
// exact pattern (120,000 \\w tokens) allocates 500+ MiB of retained heap
// (2.7+ GiB of runtime.MemStats.TotalAlloc, which — unlike HeapAlloc —
// accumulates EVERY allocation made during the call including ordinary
// GC churn from intermediate strings.Builder growth, not just live/
// retained memory) before ever being rejected; with the fix, the SAME
// pattern measures roughly 150-200 MiB of TotalAlloc (a small, bounded
// multiple of MaxAggregateExpandedPatternBytes, 16 MiB, plus ordinary
// allocator/GC overhead) regardless of how large the untranslated
// pattern is. 512 MiB is a generous, non-flaky upper bound for this
// assertion: comfortably above the observed fixed-code figure (leaving
// headroom for GC/allocator variance across Go versions and platforms)
// while still more than 5x below the multi-GiB figure the bug produced,
// so a regression back to "fully expand first, check after" would still
// fail this assertion clearly.
func TestRgUnicodeClassExpansionAbortsIncrementallyWithoutFullyExpanding(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "f.txt", "a\n")

	const numTokens = 120_000
	pattern := strings.Repeat(`\w`, numTokens)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, stderr, code := cmdRunCtx(ctx, t, "rg '"+pattern+"' f.txt", dir)

	runtime.ReadMemStats(&after)

	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "expands to more than")

	const maxAcceptableDeltaBytes = 512 * 1024 * 1024
	delta := after.TotalAlloc - before.TotalAlloc
	assert.Less(t, delta, uint64(maxAcceptableDeltaBytes),
		"translateUnicodeClasses allocated %d bytes (%.1f MiB); expected it to abort incrementally well under %d MiB, not fully expand the pattern first",
		delta, float64(delta)/(1024*1024), maxAcceptableDeltaBytes/(1024*1024))
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

// TestRgAfterContextLargerThanMaxContextBytesStillPrinted is a
// regression test: a SINGLE after-context line larger than
// MaxContextBytes (512 KiB) — but still within the separate, larger
// MaxLineBytes cap (1 MiB) — must still be printed in full, not
// silently dropped with no error at all. Before this fix, the after-
// context print sites checked "would ADDING this line's own length
// push the running group total over MaxContextBytes", which a single
// line larger than the whole cap always fails on its own, regardless of
// how little context had been printed so far in this group — silently
// skipping the line (but still consuming its slot in afterRemaining)
// with no warning or error whatsoever, and exit 0 as if nothing had
// gone wrong. The fix checks only the ALREADY-accumulated group total
// (afterGroupBytes alone, not afterGroupBytes+len(lineBytes)), mirroring
// -B's own before-context sliding window, which has never had this bug:
// it evicts OLDER lines to make room but never refuses to buffer the
// newest one, regardless of that line's own size. Verified directly
// against real ripgrep 15.1.0, which has no such cap at all and always
// emits a large context line in full (a 600 KiB line following a
// match, with -A1, exits 0 with the FULL line printed).
func TestRgAfterContextLargerThanMaxContextBytesStillPrinted(t *testing.T) {
	dir := t.TempDir()
	bigLine := strings.Repeat("a", 600*1024)
	writeFile(t, dir, "file.txt", "needle\n"+bigLine+"\n")

	stdout, stderr, code := cmdRun(t, "rg -A1 needle file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "needle\n"+bigLine+"\n", stdout)
}

// TestRgMatchInsideWindowAlwaysPrintedDespiteAccumulatedContextBudget
// is a regression test, DISTINCT from the single-oversized-line fix
// above: a genuine MATCH line falling inside an already-open trailing-
// context window must always be printed, even when an EARLIER, smaller
// (individually fitting) context line in the SAME window has ALREADY
// exhausted the accumulated afterGroupBytes budget on its own. The fix
// above only handled a single line that is, by itself, larger than the
// whole budget; it did not handle the accumulated-budget case, where no
// single line is individually oversized but the RUNNING TOTAL across
// several ordinary-sized lines already exceeds the cap before a LATER
// match line is reached. Verified directly against real ripgrep 15.1.0:
// with -A2 -m1, a match on line 1, a 700 KiB non-matching line 2 (which
// alone exhausts the accumulated budget), and another match on line 3,
// ripgrep emits all three lines; the pre-fix afterGroupBytes<=
// MaxContextBytes check at the match-line print site silently dropped
// line 3 entirely, since afterGroupBytes was already pushed over the
// cap by line 2's own length before line 3 was ever reached -- a
// genuine MATCH, not merely context, which must never be silently
// dropped regardless of accumulated budget (only ORDINARY context lines
// are gated by that budget, which this fix leaves unchanged for the
// sibling, non-match context print site).
func TestRgMatchInsideWindowAlwaysPrintedDespiteAccumulatedContextBudget(t *testing.T) {
	dir := t.TempDir()
	bigLine := strings.Repeat("x", 700*1024)
	writeFile(t, dir, "file.txt", "match1\n"+bigLine+"\nmatch2\n")

	stdout, stderr, code := cmdRun(t, "rg -A2 -m1 match file.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "match1\n"+bigLine+"\nmatch2\n", stdout, "match2 must still be printed despite the preceding big line exhausting the accumulated context budget")
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

// blockingStdinReader is an io.Reader that never returns any data (and
// never errors) until closed via its done channel, simulating a pipe
// or other blocking stdin source that remains open with no data
// available — used by TestRgStdinBinaryProbeRespectsContextCancellation
// below.
type blockingStdinReader struct {
	done chan struct{}
}

func (b *blockingStdinReader) Read(p []byte) (int, error) {
	<-b.done
	return 0, io.EOF
}

// TestRgStdinBinaryProbeRespectsContextCancellation is a regression
// test: when stdin is a pipe (or other blocking reader) supplying
// fewer than the 64 KiB binary-detection probe window's worth of data
// and remaining open, searchFile's initial io.ReadFull call against it
// must still be interruptible by the shell's own execution deadline,
// not block indefinitely regardless of ctx's own cancellation. Before
// cancellableReaderFor existed, this blocked forever: the runner has no
// way to interrupt a blocked syscall from OUTSIDE the builtin (it only
// observes cancellation once the builtin itself returns), so a plain
// io.ReadFull against a never-completing stdin reader hung well past
// any configured deadline. Verified as a real, reproducible hang
// directly (not merely a slow return): running the equivalent scenario
// against the pre-fix code with a 4-second Go test timeout let the test
// runner kill it without the read ever having returned at all.
func TestRgStdinBinaryProbeRespectsContextCancellation(t *testing.T) {
	dir := t.TempDir()
	br := &blockingStdinReader{done: make(chan struct{})}
	defer close(br.done)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, _, code := testutil.RunScriptCtx(ctx, t, "rg foo -", dir, interp.AllowedPaths([]string{dir}), interp.StdIO(br, nil, nil))
	// The key assertion is that this call returns at all within the
	// test's own timeout, rather than hanging forever; the exact exit
	// code (a cancellation-flavored error) is secondary.
	_ = code
}

// TestRgOnlyMatchingRespectsContextCancellationWithinASingleLine is a
// regression test for -o's own per-match streaming: a single line that
// matches at (or near) every byte position ("rg -o ”" against a long
// line) must let ctx cancellation interrupt the PRINT LOOP mid-line, not
// just between lines. Before forEachMatchIndex existed, -o materialized
// every match index into a slice via matchIndices before the print loop
// even started, and neither that materialization nor the print loop
// itself checked ctx — so a single sufficiently long, densely-matching
// line could overshoot a short deadline by many times regardless of how
// tightly the deadline was set relative to the OUTER per-line scan
// loop's own ctx check. This test does not assert a specific timing
// (that would be flaky in CI); like TestRgRespectsContextCancellation
// above, the test's own timeout is the real assertion: the command must
// return promptly rather than hang or grossly overshoot.
func TestRgOnlyMatchingRespectsContextCancellationWithinASingleLine(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "big.txt", strings.Repeat("a", 1<<20)+"\n")

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	_, _, code := cmdRunCtx(ctx, t, "rg -o '' big.txt", dir)
	_ = code
}

// TestRgQuietStopsSearchingButStillValidatesLaterOperandsOnceMatchFound
// is a regression test: "-q" must stop CONTENT-SEARCHING files under
// later path operands once an earlier operand already produced a
// match (performance short-circuit, mirroring the analogous --files -q
// short-circuit in TestRgFilesQuietStopsAfterFirstEligibleFile above),
// but every later operand's own EXISTENCE/readability must still be
// VALIDATED and reported — verified directly against real ripgrep
// 15.1.0: "rg -q needle quick missing" (where "quick" matches and
// "missing" does not exist) still reports "missing: No such file or
// directory" on stderr even though exit code 0 (the match) wins;
// ripgrep's own --help only promises -q suppresses stdout and stops
// SEARCHING after a match, not that later operands' own path errors go
// unreported. (An EARLIER version of this test wrongly asserted the
// opposite — that the later operand's error is suppressed entirely —
// based on an incomplete verification; this is the corrected version,
// confirmed directly against the real binary in both operand orders
// below.) The CONTENT-search short-circuit itself (not merely the
// error-reporting correctness) is covered separately by
// TestRgQuietLaterOperandNotContentSearched below, via timing against a
// huge later file that real ripgrep also does not scan once -q has
// already matched.
func TestRgQuietStopsSearchingButStillValidatesLaterOperandsOnceMatchFound(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "quick/f", "needle\n")

	_, stderr, code := cmdRun(t, "rg -q needle quick missing", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr, "missing", "an operand given AFTER the matched operand must still have its own existence validated and reported, matching real ripgrep")

	_, stderr, code = cmdRun(t, "rg -q needle missing quick", dir)
	assert.Equal(t, 0, code, "a match anywhere still wins over an error on an earlier operand")
	assert.Contains(t, stderr, "missing", "an operand processed BEFORE the match is found still reports its own error")

	_, stderr, code = cmdRun(t, "rg -q noneedle quick missing", dir)
	assert.Equal(t, 2, code, "without a match anywhere, an error on any operand still forces exit 2")
	assert.Contains(t, stderr, "missing")
}

// TestRgQuietLaterOperandNotContentSearched is a regression test for
// the performance half of the fix above: although a later operand's
// own EXISTENCE must still be validated (see the test above), its
// CONTENT must not be searched once an earlier operand already
// matched — verified directly against real ripgrep 15.1.0: timing "rg
// -q needle good huge.txt" (good matches immediately; huge.txt is a
// large non-matching file given afterward) completes in single-digit
// milliseconds, confirming huge.txt's content is never scanned.
// Asserted here via a generous test timeout (not a strict millisecond
// budget, to avoid CI flakiness) that would still catch a gross
// regression back to actually scanning the whole file.
func TestRgQuietLaterOperandNotContentSearched(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "good", "needle\n")
	writeFile(t, dir, "huge.txt", strings.Repeat("no match here\n", 2_000_000))

	done := make(chan struct{})
	var code int
	go func() {
		_, _, code = cmdRun(t, "rg -q needle good huge.txt", dir)
		close(done)
	}()
	select {
	case <-done:
		assert.Equal(t, 0, code)
	case <-time.After(5 * time.Second):
		t.Fatal("rg -q took too long, suggesting huge.txt was content-searched despite an earlier operand already matching")
	}
}

// TestRgQuietStreamsDirectoryTraversalNotJustOperands is a regression
// test: -q must stream SEARCH into directory DISCOVERY itself (searching
// each file the moment walkDir finds it), not merely interleave whole
// path OPERANDS (see TestRgQuietStopsDiscoveringLaterOperandsOnceMatchFound
// above, which only covers the operand-level case). Before walkDir grew
// its onDiscover streaming callback, a match inside one directory operand
// still required the ENTIRE tree beneath that operand to be fully
// discovered (and sorted) before the search loop calling searchFile ever
// ran — so a huge sibling subtree past the matching one, within the SAME
// directory operand, still had to be fully walked first. This is
// asserted the same non-flaky way as TestRgRespectsContextCancellation
// (via a test timeout that would catch a hang/gross overshoot, not a
// hard millisecond assertion): walkDir traverses a sorted-children stack
// LIFO (last-sorted child popped first), so the matching subdirectory is
// named to sort LAST, guaranteeing it is reached almost immediately,
// while many more sibling subdirectories are pushed — but never popped,
// once the match is found — underneath the same directory operand.
func TestRgQuietStreamsDirectoryTraversalNotJustOperands(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "d/zzzmatch/f", "needle\n")
	for i := 0; i < 5000; i++ {
		writeFile(t, dir, fmt.Sprintf("d/a%05d/f", i), "nothing\n")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, code := cmdRunCtx(ctx, t, "rg -q needle d", dir)
	assert.Equal(t, 0, code)
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
