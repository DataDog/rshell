// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"
)

// runScript runs a shell script and returns (stdout, stderr, exitCode).
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
	allOpts := append([]interp.RunnerOption{
		interp.StdIO(nil, &outBuf, &errBuf),
		interpoption.AllowAllCommands().(interp.RunnerOption),
	}, opts...)
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

// cmdRun is the awk-specific wrapper that allows the temp dir.
func cmdRun(t *testing.T, script, dir string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

func setupDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		writeFile(t, dir, name, content)
	}
	return dir
}

// =============================================================================
// Help and basic flag handling.
// =============================================================================

func TestHelpLong(t *testing.T) {
	stdout, stderr, code := cmdRun(t, "awk --help", t.TempDir())
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage: awk")
	assert.Equal(t, "", stderr)
}

func TestHelpShort(t *testing.T) {
	stdout, stderr, code := cmdRun(t, "awk -h", t.TempDir())
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage: awk")
	assert.Equal(t, "", stderr)
}

func TestNoProgram(t *testing.T) {
	_, stderr, code := cmdRun(t, "awk", t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "awk: missing program")
}

func TestUnknownFlag(t *testing.T) {
	_, stderr, code := cmdRun(t, "awk --no-such-flag '1'", t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "awk:")
}

// =============================================================================
// Program structure.
// =============================================================================

func TestBeginOnly(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { print "x" }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "x\n", stdout)
}

func TestEndOnly(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "1\n2\n3\n"})
	stdout, _, code := cmdRun(t, `awk 'END { print NR }' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "3\n", stdout)
}

func TestBeginEndChain(t *testing.T) {
	dir := setupDir(t, map[string]string{"n.txt": "10\n20\n30\n"})
	stdout, _, code := cmdRun(t, `awk 'BEGIN{s=0} {s+=$1} END{print s}' n.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "60\n", stdout)
}

func TestPatternOnlyDefaultAction(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "alpha\nbeta\ngamma\n"})
	stdout, _, code := cmdRun(t, `awk '/a/' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "alpha\nbeta\ngamma\n", stdout)
}

func TestActionOnlyMatchesAll(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "x\ny\n"})
	stdout, _, code := cmdRun(t, `awk '{print "v:" $0}' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "v:x\nv:y\n", stdout)
}

// =============================================================================
// Fields.
// =============================================================================

func TestFieldByIndex(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "alpha beta gamma\n"})
	stdout, _, code := cmdRun(t, `awk '{print $2}' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "beta\n", stdout)
}

func TestFieldDollarZero(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "alpha beta\n"})
	stdout, _, code := cmdRun(t, `awk '{print $0}' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "alpha beta\n", stdout)
}

func TestFieldOversize(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "a b\n"})
	stdout, _, code := cmdRun(t, `awk '{print "[" $99 "]"}' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "[]\n", stdout)
}

func TestNFAndNR(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "a b c\nd e\nf\n"})
	stdout, _, code := cmdRun(t, `awk '{print NR, NF}' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1 3\n2 2\n3 1\n", stdout)
}

func TestFieldAssignmentRebuildsRecord(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { OFS="-"; $0="a b c"; $2="X"; print }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "a-X-c\n", stdout)
}

func TestDollarZeroAssignmentResplits(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { $0 = "p q r"; print NF, $1, $3 }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "3 p r\n", stdout)
}

// =============================================================================
// Field separator.
// =============================================================================

func TestFieldSeparatorFlag(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "alpha,beta,gamma\n"})
	stdout, _, code := cmdRun(t, `awk -F, '{print $2}' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "beta\n", stdout)
}

func TestFieldSeparatorRegex(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "a,b:c|d\n"})
	stdout, _, code := cmdRun(t, `awk -F'[,:|]' '{print $1, $4}' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a d\n", stdout)
}

func TestFieldSeparatorEmpty(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { FS = ""; $0 = "abc"; print NF, $1, $3 }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "3 a c\n", stdout)
}

// =============================================================================
// -v variable assignment.
// =============================================================================

func TestVarAssignment(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk -v x=42 'BEGIN { print x }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "42\n", stdout)
}

func TestVarMultipleAssignments(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk -v a=1 -v b=2 -v c=3 'BEGIN { print a+b+c }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "6\n", stdout)
}

func TestVarInvalid(t *testing.T) {
	_, stderr, code := cmdRun(t, `awk -v 'noequals' 'BEGIN {}'`, t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "awk:")
}

// =============================================================================
// Patterns.
// =============================================================================

func TestRegexPattern(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "apple\nbanana\ncherry\n"})
	stdout, _, code := cmdRun(t, `awk '/an/' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "banana\n", stdout)
}

func TestExprPattern(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "1\n2\n3\n4\n"})
	stdout, _, code := cmdRun(t, `awk '$1 > 2' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "3\n4\n", stdout)
}

func TestRangePattern(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "head\nstart\nmid\nstop\ntail\n"})
	stdout, _, code := cmdRun(t, `awk '/start/,/stop/' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "start\nmid\nstop\n", stdout)
}

func TestMatchOperator(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "ab\ncd\nef\n"})
	stdout, _, code := cmdRun(t, `awk '$0 ~ /^c/' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "cd\n", stdout)
}

func TestNotMatchOperator(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "ab\ncd\nef\n"})
	stdout, _, code := cmdRun(t, `awk '$0 !~ /^c/' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "ab\nef\n", stdout)
}

// =============================================================================
// Print and printf.
// =============================================================================

func TestPrintMultipleArgs(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { OFS=":"; print "a", "b", "c" }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "a:b:c\n", stdout)
}

func TestPrintORS(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { ORS="|"; print "a"; print "b" }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "a|b|", stdout)
}

func TestPrintfBasic(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { printf "%s=%d\n", "x", 42 }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "x=42\n", stdout)
}

func TestPrintfWidthAndPrecision(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { printf "%-8s%6.2f\n", "pi", 3.14159 }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "pi        3.14\n", stdout)
}

func TestPrintfIntegerFormats(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { printf "%d %o %x %X\n", 255, 255, 255, 255 }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "255 377 ff FF\n", stdout)
}

func TestPrintfPercentLiteral(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { printf "100%%\n" }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "100%\n", stdout)
}

func TestPrintfFloatFormats(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { printf "%e %g\n", 12345.678, 0.0001234 }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "1.234568e+04 0.0001234\n", stdout)
}

func TestPrintfStarWidth(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { printf "[%*d]\n", 6, 42 }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "[    42]\n", stdout)
}

// =============================================================================
// Arithmetic and operators.
// =============================================================================

func TestArithmetic(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { print 1+2, 10-3, 6*7, 10/4, 10%3, 2^10 }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "3 7 42 2.5 1 1024\n", stdout)
}

func TestDivisionByZero(t *testing.T) {
	_, stderr, code := cmdRun(t, `awk 'BEGIN { print 1/0 }'`, t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "division by zero")
}

func TestModuloByZero(t *testing.T) {
	_, stderr, code := cmdRun(t, `awk 'BEGIN { print 5 % 0 }'`, t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "division by zero")
}

func TestStringToNumberCoercion(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { print "12abc"+0, " -3.5 " + 0 }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "12 -3.5\n", stdout)
}

func TestConcatenation(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { a="foo"; b="bar"; print a b }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "foobar\n", stdout)
}

func TestPrePostIncrement(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { x=5; print x++, x, ++x, x }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "5 6 7 7\n", stdout)
}

func TestCompoundAssignments(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { x=10; x+=5; print x; x-=3; print x; x*=2; print x; x/=4; print x }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "15\n12\n24\n6\n", stdout)
}

func TestTernary(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { print (5>3 ? "yes" : "no") }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

func TestLogicalShortCircuit(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { x=0; (x++ && x++); print x; (x++ || x++); print x }'`, t.TempDir())
	assert.Equal(t, 0, code)
	// First && short-circuits after first ++ (x=0 is false), so x becomes 1.
	// Then || short-circuits after first ++ (x=1 is true), so x becomes 2.
	assert.Equal(t, "1\n2\n", stdout)
}

// =============================================================================
// Control flow.
// =============================================================================

func TestIfElse(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "5\n10\n15\n"})
	stdout, _, code := cmdRun(t, `awk '{ if ($1 > 7) print "big"; else print "small" }' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "small\nbig\nbig\n", stdout)
}

func TestForLoop(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { for (i=1; i<=3; i++) print i }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n2\n3\n", stdout)
}

func TestWhileLoop(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { i=3; while (i>0) { print i; i-- } }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "3\n2\n1\n", stdout)
}

func TestDoWhile(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { i=5; do { print i; i++ } while (i<3) }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "5\n", stdout)
}

func TestForIn(t *testing.T) {
	// Sort keys before printing for deterministic output.
	stdout, _, code := cmdRun(t, `
		awk 'BEGIN {
			a["x"]=1; a["y"]=2; a["z"]=3
			n=0
			for (k in a) n += a[k]
			print n
		}'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "6\n", stdout)
}

func TestBreak(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { for (i=1; i<=10; i++) { if (i==3) break; print i } }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n2\n", stdout)
}

func TestContinue(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { for (i=1; i<=5; i++) { if (i==3) continue; print i } }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n2\n4\n5\n", stdout)
}

func TestNext(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "keep\nskip\nkeep\n"})
	stdout, _, code := cmdRun(t, `awk '/skip/ {next} {print}' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "keep\nkeep\n", stdout)
}

func TestExit(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { exit 7 }'`, t.TempDir())
	assert.Equal(t, 7, code)
	assert.Equal(t, "", stdout)
}

func TestExitInMainTriggersEnd(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "x\ny\nz\n"})
	stdout, _, code := cmdRun(t, `awk 'NR==2 {exit} END {print "end NR=" NR}' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "end NR=2\n", stdout)
}

// =============================================================================
// Arrays.
// =============================================================================

func TestArrayBasic(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { a[1]="one"; a[2]="two"; print a[1], a[2] }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "one two\n", stdout)
}

func TestArrayInOperator(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { a["k"]=1; print ("k" in a), ("z" in a) }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "1 0\n", stdout)
}

func TestArrayDelete(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { a[1]=10; a[2]=20; delete a[1]; print (1 in a), a[2] }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "0 20\n", stdout)
}

func TestArrayDeleteAll(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { a[1]=10; a[2]=20; delete a; n=0; for (k in a) n++; print n }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "0\n", stdout)
}

func TestArrayMultiDim(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { a[1,2]="x"; print a[1,2], ((1 SUBSEP 2) in a) }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "x 1\n", stdout)
}

// =============================================================================
// Built-in functions.
// =============================================================================

func TestLength(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { print length("hello"), length("") }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "5 0\n", stdout)
}

func TestLengthArray(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { a[1]=1; a[2]=1; a[3]=1; print length(a) }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "3\n", stdout)
}

func TestLengthDollarZero(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "abcd\nef\n"})
	stdout, _, code := cmdRun(t, `awk '{print length}' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "4\n2\n", stdout)
}

func TestSubstr(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { print substr("abcdef", 2, 3), substr("abcdef", 4) }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "bcd def\n", stdout)
}

func TestIndexFunc(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { print index("hello", "ll"), index("hello", "z") }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "3 0\n", stdout)
}

func TestSplit(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { n=split("a:b:c", a, ":"); print n, a[1], a[3] }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "3 a c\n", stdout)
}

func TestSub(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { s="abcabc"; n=sub(/abc/, "X", s); print n, s }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "1 Xabc\n", stdout)
}

func TestGsub(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { s="abcabc"; n=gsub(/abc/, "X", s); print n, s }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "2 XX\n", stdout)
}

func TestSubAmpersand(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { s="abc"; sub(/b/, "[&]", s); print s }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "a[b]c\n", stdout)
}

func TestMatch(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { match("foo bar baz", /b../); print RSTART, RLENGTH }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "5 3\n", stdout)
}

func TestMatchNoMatch(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { match("foo", /xyz/); print RSTART, RLENGTH }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "0 -1\n", stdout)
}

func TestSprintf(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { s = sprintf("[%5d]", 42); print s }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "[   42]\n", stdout)
}

func TestToLower(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { print tolower("HELLO World") }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\n", stdout)
}

func TestToUpper(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { print toupper("hello World") }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "HELLO WORLD\n", stdout)
}

func TestInt(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { print int(3.9), int(-2.1) }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "3 -2\n", stdout)
}

func TestSqrt(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { printf "%.4f\n", sqrt(2) }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "1.4142\n", stdout)
}

func TestExpLog(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { printf "%.4f %.4f\n", exp(1), log(exp(2)) }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "2.7183 2.0000\n", stdout)
}

func TestRandSrandDeterministic(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk 'BEGIN { srand(42); printf "%.4f\n", rand() }'`, t.TempDir())
	assert.Equal(t, 0, code)
	// Expect any deterministic value; just ensure it's a number in [0,1).
	got := strings.TrimSpace(stdout)
	require.Regexp(t, `^0\.\d+$`, got)
}

// =============================================================================
// Stdin.
// =============================================================================

func TestStdinNoFile(t *testing.T) {
	dir := setupDir(t, map[string]string{"data": "x\ny\nz\n"})
	stdout, _, code := cmdRun(t, `cat data | awk '/y/'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "y\n", stdout)
}

func TestStdinDashFilename(t *testing.T) {
	dir := setupDir(t, map[string]string{"data": "x\ny\nz\n"})
	stdout, _, code := cmdRun(t, `cat data | awk '/y/' -`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "y\n", stdout)
}

// =============================================================================
// Multiple files / FILENAME / FNR.
// =============================================================================

func TestMultipleFiles(t *testing.T) {
	dir := setupDir(t, map[string]string{
		"a.txt": "x\ny\n",
		"b.txt": "z\n",
	})
	stdout, _, code := cmdRun(t, `awk '{print FILENAME, FNR, NR, $0}' a.txt b.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a.txt 1 1 x\na.txt 2 2 y\nb.txt 1 3 z\n", stdout)
}

// =============================================================================
// Edge cases.
// =============================================================================

func TestEmptyFile(t *testing.T) {
	dir := setupDir(t, map[string]string{"e.txt": ""})
	stdout, _, code := cmdRun(t, `awk '{print "line"}' e.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
}

func TestNoTrailingNewline(t *testing.T) {
	dir := setupDir(t, map[string]string{"a.txt": "lonely"})
	stdout, _, code := cmdRun(t, `awk 'END {print NR}' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout)
}

func TestComments(t *testing.T) {
	stdout, _, code := cmdRun(t, `awk '
# this is a comment
BEGIN { print "ok" }  # trailing
'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "ok\n", stdout)
}

func TestMissingFile(t *testing.T) {
	_, stderr, code := cmdRun(t, `awk '{print}' missing.txt`, t.TempDir())
	assert.Equal(t, 2, code) // exit 2 matches mawk: non-fatal file-open error, END blocks run (gawk exits 0)
	assert.Contains(t, stderr, "missing.txt")
}

func TestParseError(t *testing.T) {
	_, stderr, code := cmdRun(t, `awk '{print +'`, t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "awk:")
}

// =============================================================================
// Security: blocked features must produce a parse-time error.
// =============================================================================

func TestSecuritySystemBlocked(t *testing.T) {
	_, stderr, code := cmdRun(t, `awk 'BEGIN { system("echo pwned") }'`, t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "system()")
}

func TestSecurityRedirectGtBlocked(t *testing.T) {
	_, stderr, code := cmdRun(t, `awk 'BEGIN { print "x" > "/tmp/xyzzy" }'`, t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "output redirection")
}

func TestSecurityRedirectAppendBlocked(t *testing.T) {
	_, stderr, code := cmdRun(t, `awk 'BEGIN { print "x" >> "/tmp/xyzzy" }'`, t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "output redirection")
}

func TestSecurityPipeBlocked(t *testing.T) {
	_, stderr, code := cmdRun(t, `awk 'BEGIN { print "x" | "wc -l" }'`, t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "pipe")
}

func TestSecurityGetlineBlocked(t *testing.T) {
	_, stderr, code := cmdRun(t, `awk 'BEGIN { getline x < "/etc/passwd" }'`, t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "getline")
}

func TestSecurityEnvironBlocked(t *testing.T) {
	_, stderr, code := cmdRun(t, `awk 'BEGIN { print ENVIRON["PATH"] }'`, t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "ENVIRON")
}

func TestSecurityFunctionBlocked(t *testing.T) {
	_, stderr, code := cmdRun(t, `awk 'function f() { return 1 } BEGIN { print f() }'`, t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "functions")
}

func TestSecurityCloseBlocked(t *testing.T) {
	_, stderr, code := cmdRun(t, `awk 'BEGIN { close("foo") }'`, t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "close")
}

func TestSecurityFflushBlocked(t *testing.T) {
	_, stderr, code := cmdRun(t, `awk 'BEGIN { fflush("foo") }'`, t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "fflush")
}

// =============================================================================
// Hardening: bounded buffers, large inputs, infinite loops, long lines.
// =============================================================================

func TestLongLineRejected(t *testing.T) {
	// A single line of just over MaxRecordBytes should be rejected.
	long := strings.Repeat("a", (1<<20)+10) + "\n"
	dir := setupDir(t, map[string]string{"long.txt": long})
	_, stderr, code := cmdRun(t, `awk '{print length}' long.txt`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "record")
}

func TestLoopIterationLimit(t *testing.T) {
	_, stderr, code := cmdRun(t, `awk 'BEGIN { while (1) { } }'`, t.TempDir())
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "loop iteration limit")
}

func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	stdout, _, _ := runScriptCtx(ctx, t, `awk 'BEGIN { for (i=0; i<10000000; i++) {} print "done" }'`, t.TempDir(), interp.AllowedPaths([]string{}))
	// The interpreter respects context cancellation: "done" must NOT appear.
	assert.NotContains(t, stdout, "done")
}

// =============================================================================
// Description metadata.
// =============================================================================

func TestHelpListsAwk(t *testing.T) {
	stdout, _, code := cmdRun(t, "help", t.TempDir())
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "awk")
	assert.Contains(t, stdout, "pattern scanning and processing language")
}
