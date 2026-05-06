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
	allOpts := append([]interp.RunnerOption{interp.StdIO(nil, &outBuf, &errBuf), interpoption.AllowAllCommands().(interp.RunnerOption)}, opts...)
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0644))
}

func TestAwkPrintFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "input.txt", "alpha beta gamma\none two three\n")
	stdout, stderr, code := cmdRun(t, `awk '{ print $1, $3 }' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "alpha gamma\none three\n", stdout)
}

func TestAwkFieldSeparatorAndConcat(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "input.txt", "root:x:0\nagent:x:42\n")
	stdout, _, code := cmdRun(t, `awk -F: '{ print "user=" $1 ":" $3 }' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "user=root:0\nuser=agent:42\n", stdout)
}

func TestAwkBeginEndAndAggregation(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "input.txt", "a 2\nb 3\n")
	stdout, _, code := cmdRun(t, `awk 'BEGIN { print "start" } { sum += $2 } END { print "sum", sum }' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "start\nsum 5\n", stdout)
}

func TestAwkPatternsAndRegexMatch(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "input.txt", "ok 1\nerror 2\nwarn 3\n")
	stdout, _, code := cmdRun(t, `awk '$2 > 1 && $1 !~ /warn/ { print $1 }' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "error\n", stdout)
}

func TestAwkStringNumericSemantics(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "input.txt", "0\n10\n")
	stdout, stderr, code := cmdRun(t, `awk 'BEGIN { print ("10" < "2"), (x == 0), (x == ""), !"0" } $1 { print "truthy", $1 } { print ($1 == 0), ($1 < 2), ($1 < "2") }' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "1 1 1 0\n1 1 1\ntruthy 10\n0 0 1\n", stdout)
}

func TestAwkBeginOnlySkipsInputFiles(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `awk 'BEGIN { print "x" }' missing.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "x\n", stdout)
}

func TestAwkProgramFileAndDashStdin(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "prog.awk", `{ print NR ":" $2 }`)
	stdout, _, code := runScript(t, `printf 'a b\nc d\n' | awk -f prog.awk -`, dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 0, code)
	assert.Equal(t, "1:b\n2:d\n", stdout)
}

func TestAwkVariablesTabFSAndMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "one.tsv", "a\t1\n")
	writeFile(t, dir, "two.tsv", "b\t2\n")
	stdout, _, code := cmdRun(t, `awk -F '\t' -v prefix=row 'BEGIN { OFS=":" } { print prefix, FILENAME, FNR, NR, $2 }' one.tsv two.tsv`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "row:one.tsv:1:1:1\nrow:two.tsv:1:2:2\n", stdout)
}

func TestAwkRejectsUnsafeFeatures(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "input.txt", "a b\n")
	for _, script := range []string{
		`awk '{ system("sh") }' input.txt`,
		`awk '{ print $1 > "out" }' input.txt`,
		`awk '{ $1 = "x" }' input.txt`,
		`awk '{ next; print $1 }' input.txt`,
		`awk '{ exit 0 }' input.txt`,
		`awk '{ print 1 / 0 }' input.txt`,
		`awk -F '' '{ print $1 }' input.txt`,
	} {
		_, stderr, code := cmdRun(t, script, dir)
		assert.Equal(t, 1, code, script)
		assert.Contains(t, stderr, "awk:", script)
	}
}
