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

// runShell is a minimal harness used by the GNU-compat tests, isolated from
// the package-level helpers in awk_test.go to keep this file self-contained.
func runShell(t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(script), "")
	require.NoError(t, err)
	var outBuf, errBuf bytes.Buffer
	opts := []interp.RunnerOption{
		interp.StdIO(nil, &outBuf, &errBuf),
		interpoption.AllowAllCommands().(interp.RunnerOption),
		interp.AllowedPaths([]string{dir}),
	}
	runner, err := interp.New(opts...)
	require.NoError(t, err)
	defer runner.Close()
	if dir != "" {
		runner.Dir = dir
	}
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

// gnuSetup mimics the gawk reference invocation: identical input file
// content, identical script, identical command-line flags.
func gnuSetup(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
	}
	return dir
}

// TestGNUCompatPrintField — gawk '{print $2}' two_cols.txt
// $ printf 'alpha beta gamma\nfoo bar baz\n' | gawk '{print $2}'
// Expected: "beta\nbar\n"
func TestGNUCompatPrintField(t *testing.T) {
	dir := gnuSetup(t, map[string]string{"a.txt": "alpha beta gamma\nfoo bar baz\n"})
	stdout, _, code := runShell(t, `awk '{print $2}' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "beta\nbar\n", stdout)
}

// TestGNUCompatBeginEndAggregation — classic sum-then-print idiom.
// $ printf '10\n20\n30\n' | gawk 'BEGIN{s=0} {s+=$1} END{print s}'
// Expected: "60\n"
func TestGNUCompatBeginEndAggregation(t *testing.T) {
	dir := gnuSetup(t, map[string]string{"n.txt": "10\n20\n30\n"})
	stdout, _, code := runShell(t, `awk 'BEGIN{s=0} {s+=$1} END{print s}' n.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "60\n", stdout)
}

// TestGNUCompatPrintfPercentSpec — printf format reuse with multiple args.
// $ gawk 'BEGIN { printf "%-8s%6.2f\n", "pi", 3.14159 }'
// Expected: "pi        3.14\n"
func TestGNUCompatPrintfPercentSpec(t *testing.T) {
	stdout, _, code := runShell(t, `awk 'BEGIN { printf "%-8s%6.2f\n", "pi", 3.14159 }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "pi        3.14\n", stdout)
}

// TestGNUCompatPrintfHex — printf integer format conversions.
// $ gawk 'BEGIN { printf "%d %o %x %X\n", 255, 255, 255, 255 }'
// Expected: "255 377 ff FF\n"
func TestGNUCompatPrintfHex(t *testing.T) {
	stdout, _, code := runShell(t, `awk 'BEGIN { printf "%d %o %x %X\n", 255, 255, 255, 255 }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "255 377 ff FF\n", stdout)
}

// TestGNUCompatRegexMatch — only matching lines are printed.
// $ printf 'apple\nbanana\ncherry\n' | gawk '/an/'
// Expected: "banana\n"
func TestGNUCompatRegexMatch(t *testing.T) {
	dir := gnuSetup(t, map[string]string{"a.txt": "apple\nbanana\ncherry\n"})
	stdout, _, code := runShell(t, `awk '/an/' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "banana\n", stdout)
}

// TestGNUCompatGsub — gsub returns count, modifies in place.
// $ gawk 'BEGIN { s="aXaXa"; n=gsub(/X/, "Y", s); print n, s }'
// Expected: "2 aYaYa\n"
func TestGNUCompatGsub(t *testing.T) {
	stdout, _, code := runShell(t, `awk 'BEGIN { s="aXaXa"; n=gsub(/X/, "Y", s); print n, s }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "2 aYaYa\n", stdout)
}

// TestGNUCompatFieldSeparator — -F sets FS to a literal string.
// $ printf 'a,b,c\n' | gawk -F, '{print $2}'
// Expected: "b\n"
func TestGNUCompatFieldSeparator(t *testing.T) {
	dir := gnuSetup(t, map[string]string{"csv.txt": "a,b,c\n"})
	stdout, _, code := runShell(t, `awk -F, '{print $2}' csv.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "b\n", stdout)
}

// TestGNUCompatNRPattern — NR pattern selects a single line.
// $ printf 'one\ntwo\nthree\n' | gawk 'NR==2'
// Expected: "two\n"
func TestGNUCompatNRPattern(t *testing.T) {
	dir := gnuSetup(t, map[string]string{"a.txt": "one\ntwo\nthree\n"})
	stdout, _, code := runShell(t, `awk 'NR==2' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "two\n", stdout)
}

// TestGNUCompatRangePattern — /a/,/b/ inclusive range.
// $ printf 'x\nA\nm\nB\ny\n' | gawk '/A/,/B/'
// Expected: "A\nm\nB\n"
func TestGNUCompatRangePattern(t *testing.T) {
	dir := gnuSetup(t, map[string]string{"a.txt": "x\nA\nm\nB\ny\n"})
	stdout, _, code := runShell(t, `awk '/A/,/B/' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "A\nm\nB\n", stdout)
}

// TestGNUCompatVarAssignment — -v var=value.
// $ gawk -v x=42 'BEGIN { print x }'
// Expected: "42\n"
func TestGNUCompatVarAssignment(t *testing.T) {
	stdout, _, code := runShell(t, `awk -v x=42 'BEGIN { print x }'`, t.TempDir())
	assert.Equal(t, 0, code)
	assert.Equal(t, "42\n", stdout)
}

// TestGNUCompatExitCode — exit propagates as the program's exit status.
// $ gawk 'BEGIN { exit 7 }'; echo $?
// Expected: 7
func TestGNUCompatExitCode(t *testing.T) {
	_, _, code := runShell(t, `awk 'BEGIN { exit 7 }'`, t.TempDir())
	assert.Equal(t, 7, code)
}

// TestGNUCompatNoTrailingNewline — final non-newline-terminated record counts.
// $ printf 'lonely' | gawk 'END { print NR }'
// Expected: "1\n"
func TestGNUCompatNoTrailingNewline(t *testing.T) {
	dir := gnuSetup(t, map[string]string{"a.txt": "lonely"})
	stdout, _, code := runShell(t, `awk 'END { print NR }' a.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "1\n", stdout)
}
