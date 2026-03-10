// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/interp"
	"github.com/DataDog/rshell/interp/builtins/testutil"
)

func runScriptCtx(ctx context.Context, t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, opts...)
}

func runScript(t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	return testutil.RunScript(t, script, dir, opts...)
}

func cmdRun(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return runScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
	return name
}

// --- Basic behavior ---

func TestAwkPrintAll(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "alpha\nbeta\ngamma\n")
	stdout, stderr, code := cmdRun(t, `awk '{print}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "alpha\nbeta\ngamma\n", stdout)
	assert.Empty(t, stderr)
}

func TestAwkPatternMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello world\nfoo bar\nhello again\n")
	stdout, stderr, code := cmdRun(t, `awk '/hello/' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\nhello again\n", stdout)
	assert.Empty(t, stderr)
}

func TestAwkEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "empty.txt", "")
	stdout, stderr, code := cmdRun(t, `awk '{print}' empty.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stdout)
	assert.Empty(t, stderr)
}

func TestAwkNoTrailingNewline(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello")
	stdout, _, code := cmdRun(t, `awk '{print}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
}

// --- Field operations ---

func TestAwkPrintFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "one two three\nfour five six\n")
	stdout, _, code := cmdRun(t, `awk '{print $1, $3}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "one three\nfour six\n", stdout)
}

func TestAwkFieldSeparator(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.csv", "a:b:c\nd:e:f\n")
	stdout, _, code := cmdRun(t, `awk -F: '{print $2}' file.csv`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "b\ne\n", stdout)
}

func TestAwkNFVariable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "one two three\nfour five\n")
	stdout, _, code := cmdRun(t, `awk '{print NF}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "3\n2\n", stdout)
}

func TestAwkLastField(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "one two three\n")
	stdout, _, code := cmdRun(t, `awk '{print $NF}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "three\n", stdout)
}

func TestAwkFieldAssign(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a b c\n")
	stdout, _, code := cmdRun(t, `awk '{$2 = "X"; print}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a X c\n", stdout)
}

// --- Variable assignment (-v) ---

func TestAwkVFlag(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello\n")
	stdout, _, code := cmdRun(t, `awk -v greeting=hi '{print greeting, $0}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hi hello\n", stdout)
}

func TestAwkVFlagMultiple(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "x\n")
	stdout, _, code := cmdRun(t, `awk -v a=1 -v b=2 'BEGIN {print a + b}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "3\n", stdout)
}

// --- NR and FNR ---

func TestAwkNRVariable(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a\nb\nc\n")
	stdout, _, code := cmdRun(t, `awk '{print NR, $0}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1 a\n2 b\n3 c\n", stdout)
}

func TestAwkFNR(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "x\ny\n")
	writeFile(t, dir, "b.txt", "z\n")
	stdout, _, code := cmdRun(t, `awk '{print FNR, $0}' a.txt b.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1 x\n2 y\n1 z\n", stdout)
}

// --- BEGIN/END ---

func TestAwkBeginBlock(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "data\n")
	stdout, _, code := cmdRun(t, `awk 'BEGIN {print "start"} {print $0}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "start\ndata\n", stdout)
}

func TestAwkEndBlock(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a\nb\nc\n")
	stdout, _, code := cmdRun(t, `awk '{count++} END {print count}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "3\n", stdout)
}

func TestAwkBeginOnly(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {print "hello"}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
}

// --- Printf ---

func TestAwkPrintf(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello\nworld\n")
	stdout, _, code := cmdRun(t, `awk '{printf "%d: %s\n", NR, $0}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1: hello\n2: world\n", stdout)
}

func TestAwkPrintfNoNewline(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {printf "ab"; printf "cd"}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "abcd", stdout)
}

// --- Control flow ---

func TestAwkIfElse(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "1\n2\n3\n4\n")
	stdout, _, code := cmdRun(t, `awk '{if ($1 % 2 == 0) print "even"; else print "odd"}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "odd\neven\nodd\neven\n", stdout)
}

func TestAwkForLoop(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {for (i=1; i<=3; i++) print i}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n2\n3\n", stdout)
}

func TestAwkWhileLoop(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {i=1; while (i<=3) {print i; i++}}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n2\n3\n", stdout)
}

func TestAwkDoWhile(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {i=1; do {print i; i++} while (i<=3)}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n2\n3\n", stdout)
}

func TestAwkBreak(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {for (i=1; i<=10; i++) {if (i > 3) break; print i}}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n2\n3\n", stdout)
}

func TestAwkContinue(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {for (i=1; i<=5; i++) {if (i == 3) continue; print i}}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n2\n4\n5\n", stdout)
}

func TestAwkNext(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "1\n2\n3\n")
	stdout, _, code := cmdRun(t, `awk '{if ($1 == 2) next; print}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n3\n", stdout)
}

func TestAwkExit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a\nb\nc\n")
	stdout, _, code := cmdRun(t, `awk '{print; if (NR == 2) exit}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\n", stdout)
}

func TestAwkExitCode(t *testing.T) {
	dir := t.TempDir()
	_, _, code := cmdRun(t, `awk 'BEGIN {exit 42}'`, dir)
	assert.Equal(t, 42, code)
}

// --- String functions ---

func TestAwkLength(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello\nhi\n")
	stdout, _, code := cmdRun(t, `awk '{print length($0)}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "5\n2\n", stdout)
}

func TestAwkSubstr(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello world\n")
	stdout, _, code := cmdRun(t, `awk '{print substr($0, 1, 5)}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
}

func TestAwkIndex(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {print index("hello world", "world")}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "7\n", stdout)
}

func TestAwkSplit(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {n = split("a:b:c", arr, ":"); for (i=1; i<=n; i++) print arr[i]}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\nc\n", stdout)
}

func TestAwkSub(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello world hello\n")
	stdout, _, code := cmdRun(t, `awk '{sub(/hello/, "hi"); print}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hi world hello\n", stdout)
}

func TestAwkGsub(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello world hello\n")
	stdout, _, code := cmdRun(t, `awk '{gsub(/hello/, "hi"); print}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hi world hi\n", stdout)
}

func TestAwkMatch(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {print match("hello world", /wor/)}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "7\n", stdout)
}

func TestAwkTolower(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {print tolower("HELLO")}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
}

func TestAwkToupper(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {print toupper("hello")}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "HELLO\n", stdout)
}

func TestAwkSprintf(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {print sprintf("%05d", 42)}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "00042\n", stdout)
}

// --- Math functions ---

func TestAwkInt(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {print int(3.9)}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "3\n", stdout)
}

func TestAwkSqrt(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {print sqrt(16)}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "4\n", stdout)
}

// --- Arrays ---

func TestAwkArrays(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a\nb\na\nc\nb\na\n")
	stdout, _, code := cmdRun(t, `awk '{count[$1]++} END {for (k in count) print k, count[k]}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a 3\nb 2\nc 1\n", stdout)
}

func TestAwkDeleteArray(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {a[1]=1; a[2]=2; delete a[1]; for (k in a) print k, a[k]}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2 2\n", stdout)
}

func TestAwkInArray(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {a["x"]=1; if ("x" in a) print "yes"; if ("y" in a) print "no"}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "yes\n", stdout)
}

// --- Regex match operators ---

func TestAwkMatchOperator(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello\nworld\nhelpful\n")
	stdout, _, code := cmdRun(t, `awk '$0 ~ /hel/ {print}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\nhelpful\n", stdout)
}

func TestAwkNotMatchOperator(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "hello\nworld\nhelpful\n")
	stdout, _, code := cmdRun(t, `awk '$0 !~ /hel/ {print}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "world\n", stdout)
}

// --- Arithmetic ---

func TestAwkArithmetic(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {print 2 + 3 * 4}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "14\n", stdout)
}

func TestAwkModulo(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {print 17 % 5}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "2\n", stdout)
}

func TestAwkPower(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {print 2 ^ 10}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1024\n", stdout)
}

func TestAwkTernary(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {x = 5; print (x > 3 ? "big" : "small")}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "big\n", stdout)
}

func TestAwkIncrement(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {x = 0; print x++; print x; print ++x; print x}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "0\n1\n2\n2\n", stdout)
}

// --- String concatenation ---

func TestAwkConcat(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {print "hello" " " "world"}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\n", stdout)
}

// --- Assignment operators ---

func TestAwkAssignOps(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'BEGIN {x=10; x+=5; print x; x-=3; print x; x*=2; print x; x/=4; print x; x%=5; print x}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "15\n12\n24\n6\n1\n", stdout)
}

// --- Comparison pattern ---

func TestAwkComparisonPattern(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "1\n2\n3\n4\n5\n")
	stdout, _, code := cmdRun(t, `awk '$1 > 3' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "4\n5\n", stdout)
}

// --- Stdin ---

func TestAwkStdin(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "data.txt", "hello\nworld\n")
	stdout, _, code := cmdRun(t, `cat data.txt | awk '{print toupper($0)}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "HELLO\nWORLD\n", stdout)
}

// --- Multiple files ---

func TestAwkMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "a.txt", "one\n")
	writeFile(t, dir, "b.txt", "two\n")
	stdout, _, code := cmdRun(t, `awk '{print FILENAME, $0}' a.txt b.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a.txt one\nb.txt two\n", stdout)
}

// --- Help ---

func TestAwkHelp(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := runScript(t, `awk --help`, dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage:")
	assert.Empty(t, stderr)
}

func TestAwkHelpShort(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := runScript(t, `awk -h`, dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "Usage:")
}

// --- Errors ---

func TestAwkNoProgram(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := runScript(t, `awk`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "awk:")
}

func TestAwkMissingFile(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `awk '{print}' nonexistent.txt`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "awk:")
}

func TestAwkSyntaxError(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `awk '{print'`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "awk:")
}

func TestAwkUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := runScript(t, `awk --unknown '{print}'`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "awk:")
}

func TestAwkInvalidVAssignment(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := runScript(t, `awk -v badassign '{print}'`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "awk:")
}

func TestAwkDivisionByZero(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `awk 'BEGIN {print 1/0}'`, dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "awk:")
}

// --- Safety: blocked features ---

func TestAwkBlockedSystem(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `awk 'BEGIN {system("echo bad")}'`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "blocked")
}

func TestAwkBlockedOutputRedirect(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `awk 'BEGIN {print "bad" > "/tmp/evil"}'`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "blocked")
}

func TestAwkBlockedPipeRedirect(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `awk 'BEGIN {print "bad" | "cat"}'`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "blocked")
}

func TestAwkBlockedClose(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `awk 'BEGIN {close("file")}'`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "not supported")
}

// --- RULES.md compliance ---

func TestAwkContextCancellation(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _, _ = runScriptCtx(ctx, t, `awk 'BEGIN {while (1) {x++}}'`, dir, interp.AllowedPaths([]string{dir}))
}

func TestAwkOutsideAllowedPaths(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `awk '{print}' /etc/passwd`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "awk:")
}

// --- -f flag ---

func TestAwkProgFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "prog.awk", `{print toupper($0)}`)
	writeFile(t, dir, "data.txt", "hello\n")
	stdout, _, code := cmdRun(t, `awk -f prog.awk data.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "HELLO\n", stdout)
}

// --- User-defined functions ---

func TestAwkUserFunction(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `awk 'function double(x) {return x*2} BEGIN {print double(21)}'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "42\n", stdout)
}

// --- OFS ---

func TestAwkOFS(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "file.txt", "a b c\n")
	stdout, _, code := cmdRun(t, `awk -v OFS="," '{$1=$1; print}' file.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a,b,c\n", stdout)
}
