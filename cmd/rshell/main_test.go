// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runCLI(t *testing.T, args ...string) (exitCode int, stdout, stderr string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := run(args, strings.NewReader(""), &out, &errBuf)
	return code, out.String(), errBuf.String()
}

func runCLIWithStdin(t *testing.T, stdin string, args ...string) (exitCode int, stdout, stderr string) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errBuf)
	return code, out.String(), errBuf.String()
}

func TestEcho(t *testing.T) {
	code, stdout, _ := runCLI(t, "--allow-all-commands", "-s", `echo hello world`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\n", stdout)
}

func TestShortFlag(t *testing.T) {
	code, stdout, _ := runCLI(t, "--allow-all-commands", "-s", `echo short`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "short\n", stdout)
}

func TestLongFlag(t *testing.T) {
	code, stdout, _ := runCLI(t, "--allow-all-commands", "--script", `echo long`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "long\n", stdout)
}

func TestMissingScriptAndFiles(t *testing.T) {
	code, _, stderr := runCLI(t)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "requires either --script or file arguments")
}

func TestEmptyScript(t *testing.T) {
	code, stdout, stderr := runCLI(t, "-s", "")
	assert.Equal(t, 0, code, "empty script should exit 0 (matching bash -c '')")
	assert.Empty(t, stdout)
	assert.Empty(t, stderr)
}

func TestExitCode(t *testing.T) {
	code, _, _ := runCLI(t, "--allow-all-commands", "-s", `exit 42`)
	assert.Equal(t, 42, code)
}

func TestParseError(t *testing.T) {
	code, _, stderr := runCLI(t, "-s", `echo "unterminated`)
	assert.Equal(t, 2, code, "parse errors should return exit code 2 (matching bash)")
	assert.Contains(t, stderr, "without closing quote")
}

func TestParseErrorSyntax(t *testing.T) {
	code, _, stderr := runCLI(t, "-s", `if; then`)
	assert.Equal(t, 2, code, "syntax errors should return exit code 2 (matching bash)")
	assert.Contains(t, stderr, "must be followed by")
}

func TestParseErrorUnclosed(t *testing.T) {
	code, _, stderr := runCLI(t, "-s", "if true; then\n  echo hello")
	assert.Equal(t, 2, code, "unclosed blocks should return exit code 2 (matching bash)")
	assert.Contains(t, stderr, "must end with")
}

func setupTestFile(t *testing.T) (dir, filePath string) {
	t.Helper()
	dir = t.TempDir()
	filePath = filepath.Join(dir, "testfile.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("hello from testfile\n"), 0o644))
	if runtime.GOOS == "windows" {
		filePath = filepath.ToSlash(filePath)
		dir = filepath.ToSlash(dir)
	}
	return dir, filePath
}

func TestFileAccessDeniedByDefault(t *testing.T) {
	_, filePath := setupTestFile(t)
	code, _, stderr := runCLI(t, "--allow-all-commands", "-s", `cat `+filePath)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "permission denied")
}

func TestAllowedPathGrantsAccess(t *testing.T) {
	dir, filePath := setupTestFile(t)
	code, stdout, _ := runCLI(t, "--allow-all-commands", "-s", `cat `+filePath, "-a", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "hello from testfile")
}

func TestAllowedPathCommaSeparated(t *testing.T) {
	dir, filePath := setupTestFile(t)
	extraDir := t.TempDir()
	if runtime.GOOS == "windows" {
		extraDir = filepath.ToSlash(extraDir)
	}
	code, stdout, _ := runCLI(t, "--allow-all-commands", "-s", `cat `+filePath, "--allowed-path", dir+","+extraDir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "hello from testfile")
}

func TestMultipleStatements(t *testing.T) {
	code, stdout, _ := runCLI(t, "--allow-all-commands", "-s", "echo first\necho second")
	assert.Equal(t, 0, code)
	assert.Equal(t, "first\nsecond\n", stdout)
}

func TestVariableExpansion(t *testing.T) {
	code, stdout, _ := runCLI(t, "--allow-all-commands", "-s", `FOO=bar; echo $FOO`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "bar\n", stdout)
}

func TestHelp(t *testing.T) {
	code, stdout, _ := runCLI(t, "--help")
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "--script")
	assert.Contains(t, stdout, "--allowed-path")
	assert.Contains(t, stdout, "--allowed-commands")
	assert.Contains(t, stdout, "--allow-all-commands")
}

func TestFileArg(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "test.sh")
	require.NoError(t, os.WriteFile(script, []byte("echo from-file\n"), 0o644))

	code, stdout, _ := runCLI(t, "--allow-all-commands", script)
	assert.Equal(t, 0, code)
	assert.Equal(t, "from-file\n", stdout)
}

func TestMultipleFileArgs(t *testing.T) {
	dir := t.TempDir()
	script1 := filepath.Join(dir, "a.sh")
	script2 := filepath.Join(dir, "b.sh")
	require.NoError(t, os.WriteFile(script1, []byte("echo first\n"), 0o644))
	require.NoError(t, os.WriteFile(script2, []byte("echo second\n"), 0o644))

	code, stdout, _ := runCLI(t, "--allow-all-commands", script1, script2)
	assert.Equal(t, 0, code)
	assert.Equal(t, "first\nsecond\n", stdout)
}

func TestStdinDash(t *testing.T) {
	code, stdout, _ := runCLIWithStdin(t, "echo from-stdin\n", "--allow-all-commands", "-")
	assert.Equal(t, 0, code)
	assert.Equal(t, "from-stdin\n", stdout)
}

func TestScriptAndFileArgsMutuallyExclusive(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "test.sh")
	require.NoError(t, os.WriteFile(script, []byte("echo hi\n"), 0o644))

	code, _, stderr := runCLI(t, "--allow-all-commands", "-s", "echo hi", script)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "cannot use --script with file arguments")
}

func TestFileNotFound(t *testing.T) {
	code, _, stderr := runCLI(t, "/nonexistent/path/script.sh")
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "reading /nonexistent/path/script.sh")
}

func TestFileArgWithAllowedPath(t *testing.T) {
	dir := t.TempDir()
	dataDir := t.TempDir()
	dataFile := filepath.Join(dataDir, "data.txt")
	require.NoError(t, os.WriteFile(dataFile, []byte("secret data\n"), 0o644))

	if runtime.GOOS == "windows" {
		dataFile = filepath.ToSlash(dataFile)
		dataDir = filepath.ToSlash(dataDir)
	}

	script := filepath.Join(dir, "test.sh")
	require.NoError(t, os.WriteFile(script, []byte("cat "+dataFile+"\n"), 0o644))

	code, stdout, _ := runCLI(t, "--allow-all-commands", "-a", dataDir, script)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "secret data")
}

func TestDefaultNoCommandsAllowed(t *testing.T) {
	code, _, stderr := runCLI(t, "-s", `echo hello`)
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr, "command not allowed")
}

func TestAllowedCommandsFlag(t *testing.T) {
	code, stdout, _ := runCLI(t, "--allowed-commands", "echo", "-s", `echo hello`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
}

func TestAllowedCommandsBlocksOther(t *testing.T) {
	code, _, stderr := runCLI(t, "--allowed-commands", "echo", "-s", `cat /dev/null`)
	assert.Equal(t, 127, code)
	assert.Contains(t, stderr, "command not allowed")
}

func TestAllowAllCommandsFlag(t *testing.T) {
	code, stdout, _ := runCLI(t, "--allow-all-commands", "-s", `echo hello`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello\n", stdout)
}
