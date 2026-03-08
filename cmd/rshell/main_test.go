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

func TestEcho(t *testing.T) {
	code, stdout, _ := runCLI(t, "-s", `echo hello world`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "hello world\n", stdout)
}

func TestShortFlag(t *testing.T) {
	code, stdout, _ := runCLI(t, "-s", `echo short`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "short\n", stdout)
}

func TestLongFlag(t *testing.T) {
	code, stdout, _ := runCLI(t, "--script", `echo long`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "long\n", stdout)
}

func TestMissingScript(t *testing.T) {
	code, _, stderr := runCLI(t)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "script")
}

func TestExitCode(t *testing.T) {
	code, _, _ := runCLI(t, "-s", `exit 42`)
	assert.Equal(t, 42, code)
}

func TestParseError(t *testing.T) {
	code, _, stderr := runCLI(t, "-s", `echo "unterminated`)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "parse error")
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
	code, _, stderr := runCLI(t, "-s", `cat `+filePath)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "permission denied")
}

func TestAllowedPathGrantsAccess(t *testing.T) {
	dir, filePath := setupTestFile(t)
	code, stdout, _ := runCLI(t, "-s", `cat `+filePath, "-a", dir)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "hello from testfile")
}

func TestAllowedPathCommaSeparated(t *testing.T) {
	dir, filePath := setupTestFile(t)
	code, stdout, _ := runCLI(t, "-s", `cat `+filePath, "--allowed-path", dir+",/tmp")
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "hello from testfile")
}

func TestMultipleStatements(t *testing.T) {
	code, stdout, _ := runCLI(t, "-s", "echo first\necho second")
	assert.Equal(t, 0, code)
	assert.Equal(t, "first\nsecond\n", stdout)
}

func TestVariableExpansion(t *testing.T) {
	code, stdout, _ := runCLI(t, "-s", `FOO=bar; echo $FOO`)
	assert.Equal(t, 0, code)
	assert.Equal(t, "bar\n", stdout)
}

func TestHelp(t *testing.T) {
	code, stdout, _ := runCLI(t, "--help")
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "--script")
	assert.Contains(t, stdout, "--allowed-path")
}
