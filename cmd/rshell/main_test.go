package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestFileAccessDeniedByDefault(t *testing.T) {
	code, _, stderr := runCLI(t, "-s", `cat /etc/hosts`)
	assert.NotEqual(t, 0, code)
	assert.Contains(t, stderr, "permission denied")
}

func TestAllowedPathGrantsAccess(t *testing.T) {
	code, stdout, _ := runCLI(t, "-s", `cat /etc/hosts`, "-a", "/etc")
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "localhost")
}

func TestAllowedPathCommaSeparated(t *testing.T) {
	code, stdout, _ := runCLI(t, "-s", `cat /etc/hosts`, "--allowed-path", "/etc,/tmp")
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "localhost")
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
