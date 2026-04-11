// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package python_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

func cmdRun(t *testing.T, script, dir string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return testutil.RunScript(t, script, dir, interp.AllowedPaths([]string{dir}))
}

func cmdRunCtx(ctx context.Context, t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, interp.AllowedPaths([]string{dir}))
}

// ---- Basic execution ----

func TestPrintInline(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `python -c "print('hello')"`, dir)
	assert.Equal(t, "hello\n", stdout)
	assert.Empty(t, stderr)
	assert.Equal(t, 0, code)
}

func TestArithmetic(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `python -c "print(2 + 3)"`, dir)
	assert.Equal(t, "5\n", stdout)
	assert.Empty(t, stderr)
	assert.Equal(t, 0, code)
}

func TestStringOps(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `python -c "print('hello' + ' world')"`, dir)
	assert.Equal(t, "hello world\n", stdout)
	assert.Empty(t, stderr)
	assert.Equal(t, 0, code)
}

func TestHelpFlag(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `python --help`, dir)
	assert.Contains(t, stdout, "Usage: python")
	assert.Empty(t, stderr)
	assert.Equal(t, 0, code)
}

func TestHelpShortFlag(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `python -h`, dir)
	assert.Contains(t, stdout, "Usage: python")
	assert.Equal(t, 0, code)
}

// ---- sys.exit ----

func TestSysExitZero(t *testing.T) {
	dir := t.TempDir()
	_, _, code := cmdRun(t, `python -c "import sys; sys.exit(0)"`, dir)
	assert.Equal(t, 0, code)
}

func TestSysExitNonzero(t *testing.T) {
	dir := t.TempDir()
	_, _, code := cmdRun(t, `python -c "import sys; sys.exit(42)"`, dir)
	assert.Equal(t, 42, code)
}

func TestSysExitOne(t *testing.T) {
	dir := t.TempDir()
	_, _, code := cmdRun(t, `python -c "import sys; sys.exit(1)"`, dir)
	assert.Equal(t, 1, code)
}

func TestSysExitPropagatesAsShellDollarQuestion(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `python -c "import sys; sys.exit(7)"; echo "code=$?"`, dir)
	assert.Equal(t, "code=7\n", stdout)
	assert.Equal(t, 0, code)
}

// ---- Script file execution ----

func TestRunScriptFile(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "hello.py"), []byte(`print("hello from script")`+"\n"), 0644)
	require.NoError(t, err)
	stdout, stderr, code := cmdRun(t, `python hello.py`, dir)
	assert.Equal(t, "hello from script\n", stdout)
	assert.Empty(t, stderr)
	assert.Equal(t, 0, code)
}

func TestRunScriptFileWithArgs(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "args.py"), []byte("import sys\nprint(sys.argv[1])\n"), 0644)
	require.NoError(t, err)
	stdout, _, code := cmdRun(t, `python args.py myarg`, dir)
	assert.Equal(t, "myarg\n", stdout)
	assert.Equal(t, 0, code)
}

func TestMissingScriptFile(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `python nonexistent.py`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "python:")
	assert.Contains(t, stderr, "nonexistent.py")
}

// ---- Stdin mode ----

func TestStdinDash(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `echo "print('from stdin')" | python -`, dir)
	assert.Equal(t, "from stdin\n", stdout)
	assert.Equal(t, 0, code)
}

// ---- File I/O via open() ----

func TestOpenReadFile(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("content\n"), 0644)
	require.NoError(t, err)
	stdout, stderr, code := cmdRun(t, `python -c "f = open('data.txt'); print(f.read().strip()); f.close()"`, dir)
	assert.Equal(t, "content\n", stdout)
	assert.Empty(t, stderr)
	assert.Equal(t, 0, code)
}

func TestWithStatementOpenClose(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("hello\n"), 0644)
	require.NoError(t, err)
	script := "python -c \"\nwith open('data.txt') as f:\n    print(f.read().strip())\n\""
	stdout, stderr, code := cmdRun(t, script, dir)
	assert.Equal(t, "hello\n", stdout)
	assert.Empty(t, stderr)
	assert.Equal(t, 0, code)
}

// ---- Security sandbox ----

func TestOsSystemBlocked(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `python -c "import os; os.system('id')"`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "AttributeError")
}

func TestOsPopenBlocked(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `python -c "import os; os.popen('id')"`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "AttributeError")
}

func TestOsRemoveBlocked(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `python -c "import os; os.remove('/tmp/x')"`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "AttributeError")
}

func TestOsMkdirBlocked(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `python -c "import os; os.mkdir('/tmp/x')"`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "AttributeError")
}

func TestOsExeclBlocked(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `python -c "import os; os.execl('/bin/sh', 'sh')"`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "AttributeError")
}

func TestOpenWriteModeBlocked(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `python -c "open('/tmp/evil.txt', 'w')"`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "PermissionError")
}

func TestOpenAppendModeBlocked(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `python -c "open('/tmp/evil.txt', 'a')"`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "PermissionError")
}

func TestOpenExclusiveCreateBlocked(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `python -c "open('/tmp/evil.txt', 'x')"`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "PermissionError")
}

func TestOpenReadWriteModeBlocked(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `python -c "open('/tmp/evil.txt', 'r+')"`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "PermissionError")
}

func TestOpenOutsideAllowedPaths(t *testing.T) {
	dir := t.TempDir()
	// Allowed paths is set to dir; /etc/passwd is outside it.
	_, stderr, code := cmdRun(t, `python -c "open('/etc/passwd')"`, dir)
	assert.Equal(t, 1, code)
	assert.NotEmpty(t, stderr)
}

func TestTempfileNeutered(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `python -c "import tempfile; tempfile.mkstemp()"`, dir)
	assert.Equal(t, 1, code)
	assert.NotEmpty(t, stderr)
}

func TestGlobNeutered(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `python -c "import glob; glob.glob('*')"`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "AttributeError")
}

// ---- Error handling ----

func TestSyntaxError(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `python -c "def foo("`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "SyntaxError")
}

func TestRuntimeException(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `python -c "raise ValueError('oops')"`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "ValueError")
	assert.Contains(t, stderr, "oops")
}

func TestDivisionByZero(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `python -c "x = 1/0"`, dir)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "ZeroDivisionError")
}

func TestUnknownFlag(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := cmdRun(t, `python --unknown-flag`, dir)
	assert.Equal(t, 1, code)
	assert.NotEmpty(t, stderr)
}

// ---- Context cancellation ----

func TestContextCancellation(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Infinite loop — should be killed by context deadline.
	_, _, code := cmdRunCtx(ctx, t, `python -c "while True: pass"`, dir)
	// After context cancellation the shell returns exit code 1.
	assert.Equal(t, 1, code)
}

// ---- Stdlib availability ----

func TestMathModule(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `python -c "import math; print(math.floor(3.7))"`, dir)
	assert.Equal(t, "3\n", stdout)
	assert.Equal(t, 0, code)
}

func TestSysArgv(t *testing.T) {
	dir := t.TempDir()
	stdout, _, code := cmdRun(t, `python -c "import sys; print(sys.argv[0])"`, dir)
	assert.Equal(t, "<string>\n", stdout)
	assert.Equal(t, 0, code)
}

// ---- Output to stderr ----

func TestStderrOutput(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := cmdRun(t, `python -c "import sys; sys.stderr.write('err msg\n')"`, dir)
	assert.Empty(t, stdout)
	assert.Equal(t, "err msg\n", stderr)
	assert.Equal(t, 0, code)
}

// ---- Memory safety ----

func TestLargeOutputDoesNotCrash(t *testing.T) {
	dir := t.TempDir()
	// Print 100 lines — small enough to complete quickly but exercises output path.
	stdout, _, code := testutil.RunScript(t, `python -c "
for i in range(100):
    print('line ' + str(i))
"`, dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 0, code)
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	assert.Equal(t, 100, len(lines))
}

func TestReadlineFromFile(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "lines.txt"), []byte("first\nsecond\nthird\n"), 0644)
	require.NoError(t, err)
	stdout, _, code := cmdRun(t, `python -c "f = open('lines.txt'); print(f.readline().strip())"`, dir)
	assert.Equal(t, "first\n", stdout)
	assert.Equal(t, 0, code)
}
