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
	"time"

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

func runScriptRestricted(t *testing.T, script, dir string, opts ...interp.RunnerOption) (string, string, int) {
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

func TestAwkHelpDescribesSupportedAndUnsupportedProfile(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `awk --help`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Contains(t, stdout, "Usage: awk [OPTION]... 'program' [FILE]...")
	assert.Contains(t, stdout, "This is a practical rshell awk profile, not a full GNU awk clone.")
	assert.Contains(t, stdout, "Supported profile:")
	assert.Contains(t, stdout, "Output command pipes such as print x | \"sort\"")
	assert.Contains(t, stdout, "getline, getline var, getline var < file, and \"cmd\" | getline var")
	assert.Contains(t, stdout, "Not supported:")
	assert.Contains(t, stdout, "system(). Use supported awk command pipes/getline pipes instead")
	assert.Contains(t, stdout, "print/printf file output redirection to file targets")
	assert.Contains(t, stdout, "ARGV/ARGC mutation")
	assert.Contains(t, stdout, "PROCINFO, SYMTAB, FUNCTAB")
	assert.Contains(t, stdout, "gensub, match, strtonum, asorti")
	assert.Contains(t, stdout, "asort, patsplit")
}

func TestAwkCompoundAssignmentReadsCurrentTargetAfterRightSide(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `awk 'BEGIN { print b += b += 1; b = 6; print b += b++; print b }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "2\n13\n13\n", stdout)
}

func TestAwkAssociativeArrayElements(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "input.txt", "api 200\napi 500\nworker 200\n")
	stdout, stderr, code := cmdRun(t, `awk '{ count[$1]++; status[$2] += 1 } END { print count["api"], count["worker"], status[200], status[500], missing["x"] }' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "2 1 2 1 \n", stdout)
}

func TestAwkArrayMembershipDeleteForInAndSplit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "input.txt", "api 200\napi 500\nworker 200\n")
	stdout, stderr, code := cmdRun(t, `awk '{ count[$1]++; split($0, fields); status[fields[2]]++ } END { delete status[500]; print ("api" in count), ("500" in status); for (k in count) print k, count[k]; delete count; print ("api" in count) }' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "1 0\napi 2\nworker 1\n0\n", stdout)
}

func TestAwkSplitRegexAndCharacterSeparator(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `awk 'BEGIN { n = split("a,b:c", fields, /[,:]/); print n, fields[1], fields[2], fields[3]; m = split("xy", chars, ""); print m, chars[1], chars[2]; print split("a  b", special, " "), split("a  b", literal, / /); print split("abc", dotLiteral, "."), split("a.b", dotted, "."), split("a|b", pipeLiteral, "|"), split("abc", dotRegex, /./); print split("abc", nullRegex, //), nullRegex[1], nullRegex[2], nullRegex[3]; print split(" a b ", starRegex, / */), "[" starRegex[1] "]", "[" starRegex[2] "]", "[" starRegex[3] "]", "[" starRegex[4] "]"; print split("aaa", longest, /a|aa/), "[" longest[1] "]", "[" longest[2] "]", "[" longest[3] "]" }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "3 a b c\n2 x y\n2 3\n1 2 2 4\n3 a b c\n4 [] [a] [b] []\n3 [] [] []\n", stdout)
}

func TestAwkMatchCapturesGensubStrtonumAndAsorti(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `printf 'cached_tables=31\n' | awk 'match($0, /cached_tables=([0-9]+)/, m) { print m[0], m[1] }'; awk 'BEGIN { print strtonum("0x1538"), strtonum("010"); print strtonum("123abc"), strtonum("-12.5ms"), strtonum("1e3rows"); print strtonum("012.3"), strtonum("012e2"), strtonum("0128"), strtonum("010"); print gensub(/.*trace_id=([0-9]+).*/, "\\1", 1, "trace_id=42"); a["b"] = 2; a["a"] = 1; print asorti(a, k), k[1], k[2]; a[1] = "abc"; print match(a[1], /(b)/, a), RSTART, RLENGTH, a[0], a[1] }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "cached_tables=31 31\n5432 8\n123 -12.5 1000\n12.3 1200 128 8\n42\n2 a b\n2 2 1 b b\n", stdout)
}

func TestAwkIgnoreCaseAffectsRegexOperations(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `printf 'TypeError\nok\n' | awk 'BEGIN { IGNORECASE = 1 } /typeerror/ { c++ } END { print c + 0 }'; awk 'BEGIN { IGNORECASE = 1; s = "TypeError"; sub(/type/, "Schema", s); print s; print split("AxxB", a, /X+/), a[1], a[2] }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "1\nSchemaError\n2 A B\n", stdout)
}

func TestAwkByteModeMatchOffsetsUseRunePositions(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `awk 'BEGIN { s = "\303\251"; print length(s), "[" s "]"; print match(s, /\251/), RSTART, RLENGTH, "[" substr(s, RSTART, RLENGTH) "]" }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "1 [\303\251]\n1 1 1 [\303\251]\n", stdout)
}

func TestAwkExitRunsEndAndPreservesStatus(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "input.txt", "1\n2\n3\n")
	stdout, stderr, code := cmdRun(t, `awk '{ if ($1 == 2) exit 7; print $1 } END { print "end", NR }' input.txt`, dir)
	assert.Equal(t, 7, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "1\nend 2\n", stdout)

	stdout, stderr, code = cmdRun(t, `awk 'BEGIN { print "begin"; exit } { print } END { print "end" }' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "begin\nend\n", stdout)
}

func TestAwkLoopsObserveContextCancellation(t *testing.T) {
	for _, script := range []string{
		`awk 'BEGIN { while (1) {} }'`,
		`awk 'BEGIN { for (i = 1; 1; i++) {} }'`,
	} {
		t.Run(script, func(t *testing.T) {
			parser := syntax.NewParser()
			prog, err := parser.Parse(strings.NewReader(script), "")
			require.NoError(t, err)
			var outBuf, errBuf bytes.Buffer
			runner, err := interp.New(
				interp.StdIO(nil, &outBuf, &errBuf),
				interpoption.AllowAllCommands().(interp.RunnerOption),
				// Expire before awk's independent explicit-loop iteration limit.
				interp.MaxExecutionTime(10*time.Millisecond),
			)
			require.NoError(t, err)
			defer runner.Close()

			done := make(chan error, 1)
			go func() {
				done <- runner.Run(context.Background(), prog)
			}()

			select {
			case runErr := <-done:
				var exitStatus interp.ExitStatus
				if errors.As(runErr, &exitStatus) {
					assert.NotEqual(t, 0, int(exitStatus))
					assert.Contains(t, errBuf.String(), "context deadline exceeded")
					return
				}
				assert.ErrorIs(t, runErr, context.DeadlineExceeded)
			case <-time.After(2 * time.Second):
				t.Fatal("awk loop did not observe context cancellation")
			}
		})
	}
}

func TestAwkBlockedStdinReadObservesParentCancellation(t *testing.T) {
	stdin, writer, err := os.Pipe()
	require.NoError(t, err)
	defer stdin.Close()
	defer writer.Close()
	_, err = writer.WriteString("partial")
	require.NoError(t, err)

	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(`awk '{ print }'`), "")
	require.NoError(t, err)
	var stdout, stderr bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(stdin, &stdout, &stderr),
		interpoption.AllowAllCommands().(interp.RunnerOption),
	)
	require.NoError(t, err)
	defer runner.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx, prog) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case runErr := <-done:
		var status interp.ExitStatus
		assert.True(t, errors.Is(runErr, context.Canceled) || errors.As(runErr, &status), runErr)
		assert.Empty(t, stdout.String())
		assert.Contains(t, stderr.String(), "context canceled")
	case <-time.After(2 * time.Second):
		t.Fatal("awk did not interrupt its blocked stdin read")
	}

	require.NoError(t, writer.Close())
}

func TestAwkRejectsScalarArrayNameConflicts(t *testing.T) {
	dir := t.TempDir()
	for _, script := range []string{
		`awk 'BEGIN { x = 1; print x[1] }'`,
		`awk 'BEGIN { print x; x[1] = 1 }'`,
		`awk 'BEGIN { a[1] = 2; print a }'`,
		`awk 'BEGIN { for (k in a) {}; print a }'`,
		`awk 'BEGIN { print ("x" in a); print a }'`,
		`awk 'BEGIN { delete a; print a }'`,
		`awk 'BEGIN { print ENVIRON }'`,
		`awk 'BEGIN { FS[1] = 2 }'`,
		`awk 'BEGIN { NF[1] = 2 }'`,
		`awk 'function f(x){ x = 1; x[1] = 2 } BEGIN { f(a) }'`,
		`awk 'function f(a,b){ a = 2; b[1] = 1 } BEGIN { f(x,x) }'`,
		`awk 'function f(x){ x = 1 } BEGIN { f(a); a[1] = 2 }'`,
		`awk 'function f(x){ print x; x[1] = 2 } BEGIN { f(a) }'`,
		`awk 'function f(x){ print x } BEGIN { f(a); a[1] = 2 }'`,
		`awk 'function f(x){ print x; x[1] = 2 } BEGIN { f() }'`,
	} {
		_, stderr, code := cmdRun(t, script, dir)
		assert.Equal(t, 1, code, script)
		assert.Contains(t, stderr, "awk:", script)
	}
}

func TestAwkRejectsSpecialVariableFunctionNames(t *testing.T) {
	dir := t.TempDir()
	for _, script := range []string{
		`awk 'function FS(){ return 1 } BEGIN { print FS() }'`,
		`awk 'function OFS(){ return 1 } BEGIN { print OFS() }'`,
		`awk 'function ORS(){ return 1 } BEGIN { print ORS() }'`,
		`awk 'function SUBSEP(){ return 1 } BEGIN { print SUBSEP() }'`,
		`awk 'function RSTART(){ return 1 } BEGIN { print RSTART() }'`,
		`awk 'function RLENGTH(){ return 1 } BEGIN { print RLENGTH() }'`,
	} {
		_, stderr, code := cmdRun(t, script, dir)
		assert.Equal(t, 1, code, script)
		assert.Contains(t, stderr, "reserved awk variable name", script)
	}
}

func TestAwkRejectsSpecialVariableFunctionParameters(t *testing.T) {
	dir := t.TempDir()
	for _, script := range []string{
		`awk 'function f(FS){ return FS } BEGIN { print f(1) }'`,
		`awk 'function f(OFS){ return OFS } BEGIN { print f(1) }'`,
		`awk 'function f(ORS){ return ORS } BEGIN { print f(1) }'`,
		`awk 'function f(SUBSEP){ return SUBSEP } BEGIN { print f(1) }'`,
		`awk 'function f(RSTART){ return RSTART } BEGIN { print f(1) }'`,
		`awk 'function f(RLENGTH){ return RLENGTH } BEGIN { print f(1) }'`,
	} {
		_, stderr, code := cmdRun(t, script, dir)
		assert.Equal(t, 1, code, script)
		assert.Contains(t, stderr, "reserved awk variable name", script)
	}
}

func TestAwkRejectsUserFunctionNamesAsVariables(t *testing.T) {
	dir := t.TempDir()
	for _, script := range []string{
		`awk 'function f(){ return 1 } BEGIN { f = 3; print f }'`,
		`awk 'function f(){ return 1 } BEGIN { print f }'`,
		`awk 'function f(){ return 1 } BEGIN { print $f }'`,
		`awk 'function f(){ return 1 } BEGIN { f[1] = 2 }'`,
		`awk 'function f(){ return 1 } BEGIN { delete f }'`,
		`awk 'function f(){ return 1 } BEGIN { for (f in a) print f }'`,
		`awk 'function f(){ return 1 } BEGIN { for (k in f) print k }'`,
		`awk 'BEGIN { f = 3 } function f(){ return 1 }'`,
		`awk 'function g(){ f = 1 } function f(){ return 1 } BEGIN { g() }'`,
	} {
		_, stderr, code := cmdRun(t, script, dir)
		assert.Equal(t, 1, code, script)
		assert.Contains(t, stderr, "cannot be used as a variable or array", script)
	}
}

func TestAwkFunctionParametersMayShadowOtherFunctionNames(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `awk 'function f(g){ print g } function g(){ return 1 } BEGIN { f(2); print g() }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "2\n1\n", stdout)
}

func TestAwkRejectsCallsThroughShadowingParameters(t *testing.T) {
	dir := t.TempDir()
	for _, script := range []string{
		`awk 'function f(g){ return g() } function g(){ return 1 } BEGIN { print f(2) }'`,
		`awk 'function f(g){ print g(1) } function g(x){ return x } BEGIN { f(2) }'`,
	} {
		_, stderr, code := cmdRun(t, script, dir)
		assert.Equal(t, 1, code, script)
		assert.Contains(t, stderr, "cannot be called as a function", script)
	}
}

func TestAwkRejectsLoopControlOutsideLexicalLoops(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		script string
		err    string
	}{
		{`awk 'BEGIN { break }'`, "break is not allowed outside a loop"},
		{`awk 'BEGIN { continue }'`, "continue is not allowed outside a loop"},
		{`awk 'function f(){ break } BEGIN { for (i = 0; i < 2; i++) f() }'`, "break is not allowed outside a loop"},
		{`awk 'function f(){ continue } BEGIN { for (i = 0; i < 2; i++) f() }'`, "continue is not allowed outside a loop"},
		{`awk 'function f(){ if (1) { break } } BEGIN { print "unused" }'`, "break is not allowed outside a loop"},
		{`awk 'function f(){ if (1) { continue } } BEGIN { print "unused" }'`, "continue is not allowed outside a loop"},
	} {
		_, stderr, code := cmdRun(t, tc.script, dir)
		assert.Equal(t, 1, code, tc.script)
		assert.Contains(t, stderr, tc.err, tc.script)
	}
}

func TestAwkAllowsLoopControlInsideFunctionLexicalLoops(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `awk 'function f(){ out = ""; for (i = 0; i < 4; i++) { if (i == 1) continue; if (i == 3) break; out = out i }; return out } BEGIN { print f() }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "02\n", stdout)
}

func TestAwkRegexLiteralCanContainRepeatedEquals(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `printf '=== WARM-UP ===\nplain\n' | awk '$0 ~ /===/ { print }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "=== WARM-UP ===\n", stdout)
}

func TestAwkRegexUnknownEscapesBecomeLiterals(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "input.txt", "5\nd\n")
	stdout, stderr, code := cmdRun(t, `awk '/\d/ { print }' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "d\n", stdout)
}

func TestAwkCompoundStatementsSeparateBeforeNextStatement(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `awk 'BEGIN { if (1) { x = 1 } print x; for (i = 1; i <= 1; i++) { if (1) y = 2 } print y }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "1\n2\n", stdout)
}

func TestAwkRecordAssignmentRespectsRecordLimit(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "large.txt", strings.Repeat("x", 1<<20)+"\n")
	for _, script := range []string{
		`awk 'BEGIN { $0 = "x"; for (i = 0; i < 21; i++) $0 = $0 $0; print "unreachable" }'`,
		`awk '{ $1 = $0; $2 = $0; print "unreachable" }' large.txt`,
		`awk '{ $1 = $0; NF = 2; print "unreachable" }' large.txt`,
	} {
		stdout, stderr, code := cmdRun(t, script, dir)
		assert.Equal(t, 1, code, script)
		assert.Equal(t, "", stdout, script)
		assert.Contains(t, stderr, "record exceeds 1048576 bytes", script)
	}
}

func TestAwkBoundsIntermediateResources(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name   string
		script string
		err    string
	}{
		{"print", `awk 'BEGIN { x = sprintf("%1048576s", ""); print x, x }'`, "print output exceeds 1048576 bytes"},
		{"buffered stdout", `awk 'BEGIN { print "" | "cat"; for (i = 0; i < 6; i++) printf "%1048576s", ""; print "" | "cat" }'`, "buffered output exceeds 5242880 bytes"},
		{"concatenation", `awk 'BEGIN { x = sprintf("%1048576s", ""); print length(x x x x x x) }'`, "string expression exceeds 5242880 bytes"},
		{"split", `awk 'BEGIN { x = sprintf("%16385s", ""); split(x, a, "") }'`, "split result exceeds 16384 fields"},
		{"redirection count", `awk 'BEGIN { p = "missing"; for (i = 0; i < 65; i++) { getline x < p; p = p "x" } }'`, "too many tracked redirections (maximum 64)"},
		{"pipe payload", `awk 'BEGIN { for (i = 0; i < 3; i++) printf "%1048576s", "" | "cat"; for (i = 0; i < 3; i++) printf "%1048576s", "" | "sort" }'`, "command pipe input storage exceeds 5242880 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, code := cmdRun(t, tc.script, dir)
			assert.Equal(t, 1, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, tc.err)
		})
	}
}

func TestAwkFlushesBufferedStdoutAfterFinalPipeWrite(t *testing.T) {
	dir := t.TempDir()
	script := `awk 'BEGIN { print "" | "cat"; printf "x"; print "" | "cat"; for (i = 0; i < 5; i++) printf "%1048576s", "" }'`
	stdout, stderr, code := cmdRun(t, script, dir)
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Len(t, stdout, 5*(1<<20)+3)
}

func TestAwkGetlineRespectsAllowedPaths(t *testing.T) {
	allowedDir := t.TempDir()
	restrictedDir := t.TempDir()
	restrictedFile := filepath.Join(restrictedDir, "secret")
	writeFile(t, restrictedDir, "secret", "do-not-read\n")

	direct := `awk 'BEGIN { status = getline x < "` + restrictedFile + `"; print status "|" x "|" (ERRNO != "") }'`
	stdout, stderr, code := runScriptRestricted(t, direct, allowedDir,
		interp.AllowedCommands([]string{"rshell:awk"}),
		interp.AllowedPaths([]string{allowedDir}),
	)
	assert.Equal(t, 0, code)
	assert.Equal(t, "-1||1\n", stdout)
	assert.Empty(t, stderr)

	nested := `awk 'BEGIN { cmd = "cat ` + restrictedFile + `"; status = (cmd | getline x); print status "|" x; print close(cmd) }'`
	stdout, stderr, code = runScriptRestricted(t, nested, allowedDir,
		interp.AllowedCommands([]string{"rshell:awk", "rshell:cat"}),
		interp.AllowedPaths([]string{allowedDir}),
	)
	assert.Equal(t, 0, code)
	assert.Equal(t, "0|\n1\n", stdout)
	assert.NotEmpty(t, stderr)
	assert.NotContains(t, stdout+stderr, "do-not-read")
}

func TestAwkEnvironUsesRshellEnvironment(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := runScript(t, `FOO=script; awk 'BEGIN { print ENVIRON["FROM_ENV"], ("FOO" in ENVIRON); print ENVIRON["NUMERIC_ENV"] < 2, ENVIRON["NUMERIC_ENV"] + 0, ENVIRON["NUMERIC_ENV"] == 10 }'; FOO=inline awk 'BEGIN { print ENVIRON["FOO"] }'`, dir, interp.Env("FROM_ENV=provided", "NUMERIC_ENV=10"))
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "provided 0\n0 10 1\ninline\n", stdout)
}

func TestAwkLargeEnvironDoesNotConsumeVariableBudget(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", 1<<20)
	stdout, stderr, code := runScript(t, `awk 'BEGIN { print 1; print length(ENVIRON["BIG"]) }'`, dir, interp.Env("BIG="+big))
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "1\n1048576\n", stdout)
}

func TestAwkIfNextPrintfAndScalarBuiltins(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "input.txt", "a 1\nb 22\nskip 5\n")
	stdout, stderr, code := cmdRun(t, `awk '{ if ($1 == "skip") next; if ($2 > 9) { printf "%s:%03d:%u\n", toupper($1), $2, 42 } else printf "small:%s:%d:%d:%d:%d:%s\n", tolower($1), int($2 + .9), length, index($0, $2), index($0, ""), substr($0, 2, 2) }' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "small:a:1:3:3:1: 1\nB:022:42\n", stdout)

	stdout, stderr, code = cmdRun(t, "awk '{ if ($1 == \"skip\")\nnext\nelse\nprintf \"%s:%x\\n\", $1, -1 }' input.txt", dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "a:ffffffffffffffff\nb:ffffffffffffffff\n", stdout)

	stdout, stderr, code = cmdRun(t, `awk 'BEGIN { printf "%d|%u|%x|%o\n", 18446744073709551615, 18446744073709551615, 18446744073709551615, 18446744073709551615 }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "18446744073709551616|18446744073709551616|10000000000000000|2000000000000000000000\n", stdout)
}

func TestAwkSingleCharacterRecordSeparator(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nul.txt"), []byte("alpha\x00beta\x00"), 0o644))
	writeFile(t, dir, "comma.txt", "x,y,z")
	stdout, stderr, code := cmdRun(t, `awk -v RS='\0' '{ print NR ":" $0 }' nul.txt; awk -v RS=, '{ print NR ":" $0 }' comma.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "1:alpha\n2:beta\n1:x\n2:y\n3:z\n", stdout)
}

func TestAwkCommandPipes(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `awk 'BEGIN { print "b" | "sort"; print "a" | "sort"; close("sort"); printf "%s\n", "pipe payload" | "cat"; close("cat") }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "a\nb\npipe payload\n", stdout)

	stdout, stderr, code = cmdRun(t, `awk 'BEGIN { print "auto-close" | "cat" }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "auto-close\n", stdout)

	stdout, stderr, code = cmdRun(t, `awk 'BEGIN { print "a" | "wc -l"; printf ""; print "b" | "wc -l"; close("wc -l") }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "2\n", stdout)

	stdout, stderr, code = cmdRun(t, `awk 'BEGIN { print "x" | "false" }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "", stdout)

	stdout, stderr, code = cmdRun(t, `awk 'BEGIN { print "x" | "false"; print "after"; print close("false") }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "after\n1\n", stdout)

	stdout, stderr, code = cmdRun(t, `awk 'BEGIN { print "x" | "false"; print close("false") }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "1\n", stdout)

	stdout, stderr, code = cmdRun(t, `awk 'BEGIN { print close("missing") }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "-1\n", stdout)
}

func TestAwkCommandPipesRunNestedRshellScripts(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `awk 'BEGIN { cmd = "cat | sort"; print "b" | cmd; print "a" | cmd; print close(cmd) }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "a\nb\n0\n", stdout)
}

func TestAwkCommandInputPipesUseNestedRshellScripts(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `awk 'BEGIN { cmd = "printf \"b\\na\\n\" | sort"; print (cmd | getline first), first; print (cmd | getline second), second; print (cmd | getline third), "[" third "]"; print close(cmd); print (cmd | getline again), again }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "1 a\n1 b\n0 []\n0\n1 a\n", stdout)
}

func TestAwkCommandInputPipesInheritUnopenedStdin(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `printf "outer\n" | awk 'BEGIN { "cat" | getline x; print "x=" x; getline y; print "y=" y }'`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "x=outer\ny=\n", stdout)
}

func TestAwkCommandInputPipesKeepStdinWhileReadingFiles(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	require.NoError(t, os.WriteFile(input, []byte("file-record\n"), 0o644))
	quotedInput := "'" + strings.ReplaceAll(input, "'", `'\''`) + "'"

	stdout, stderr, code := cmdRun(t, `printf "s\n" | awk '{ "cat" | getline x; print "x=" x; print "rec=" $0 }' `+quotedInput, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "x=s\nrec=file-record\n", stdout)
}

func TestAwkCommandPipesRespectAllowedCommands(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := runScriptRestricted(t, `awk 'BEGIN { print "x" | "sort" }'`, dir,
		interp.AllowedCommands([]string{"rshell:awk"}),
		interp.AllowedPaths([]string{dir}),
	)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, `rshell: sort: command not allowed`)
}

func TestAwkNestedCommandPipesRespectAllowedCommands(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := runScriptRestricted(t, `awk 'BEGIN { print "x" | "cat | sort" }'`, dir,
		interp.AllowedCommands([]string{"rshell:awk", "rshell:cat"}),
		interp.AllowedPaths([]string{dir}),
	)
	assert.Equal(t, 1, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, `rshell: sort: command not allowed`)
}

func TestAwkOperandAssignments(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "one.txt", "a\n")
	writeFile(t, dir, "two.txt", "b\n")
	stdout, stderr, code := cmdRun(t, `awk '{ print x ":" $0 }' one.txt x=foo two.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, ":a\nfoo:b\n", stdout)

	stdout, stderr, code = runScript(t, `awk '{ print x, $0 }' x=foo < one.txt`, dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "foo a\n", stdout)

	writeFile(t, dir, "1=x", "c\n")
	stdout, stderr, code = cmdRun(t, `awk '{ print $0 }' 1=x`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "c\n", stdout)
}

func TestAwkMissingInputFileIsFatal(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `awk '{ print }' missing.txt`, dir)
	assert.Equal(t, 2, code)
	assert.Equal(t, "", stdout)
	assert.Contains(t, stderr, "awk: fatal: cannot open file `missing.txt' for reading:")
}

func TestAwkRejectsNaNAndInfNumericStrings(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "input.txt", "NaN Inf Infinity\n")
	stdout, stderr, code := cmdRun(t, `awk '{ print $1 + 1, $2 + 1, $3 + 1, ($1 == $1), ($2 == $2) }' input.txt`, dir)
	assert.Equal(t, 0, code)
	assert.Equal(t, "", stderr)
	assert.Equal(t, "1 1 1 1 1\n", stdout)
}

func TestAwkRejectsUnsafeFeatures(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "input.txt", "a b\n")
	writeFile(t, dir, "empty.txt", "")
	for _, script := range []string{
		`awk '{ system("sh") }' input.txt`,
		`awk '{ print $1 > "out" }' input.txt`,
		`awk '{ printf "%s", $1 > "out" }' input.txt`,
		`awk '{ x = next }' input.txt`,
		`awk 'BEGIN { next }' input.txt`,
		`awk 'BEGIN { print tolower(), toupper(), int() }' input.txt`,
		`awk '{ print int() }' empty.txt`,
		`awk '$1 == "missing" { print length(1, 2) }' input.txt`,
		`awk 'BEGIN { printf "%1000000000s", "x" }' input.txt`,
		`awk 'BEGIN { printf "%.1000000000s", "x" }' input.txt`,
		`awk 'BEGIN { printf "%1048576s%1048576s", "x", "y" }' input.txt`,
		`awk 'BEGIN { BEGIN=1; print BEGIN }' input.txt`,
		`awk 'BEGIN { END=1; print END }' input.txt`,
		`awk '{ print $BEGIN }' input.txt`,
		`awk '{ print $if }' input.txt`,
		`awk '{ print $length }' input.txt`,
		`awk -v BEGIN=x 'BEGIN { print 1 }' input.txt`,
		`awk '{ print $0 }' BEGIN=x input.txt`,
		`awk 'BEGIN { print 1 < 2 < 3 }' input.txt`,
		`awk '{ print 1 / 0 }' input.txt`,
		`awk -F '' '{ print $1 }' input.txt`,
	} {
		_, stderr, code := cmdRun(t, script, dir)
		assert.Equal(t, 1, code, script)
		assert.Contains(t, stderr, "awk:", script)
	}
}

func TestAwkRejectsUnsupportedBuiltinWithoutParens(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "input.txt", "abc\n")
	_, stderr, code := cmdRun(t, `awk '{ print split }' input.txt`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "awk: function calls are not supported")
}
