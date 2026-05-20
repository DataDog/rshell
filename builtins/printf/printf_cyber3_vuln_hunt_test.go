package printf_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
)

func TestVulnHuntBuiltinPrintfLengthModifiersDoNotSmuggleUnsupportedSpecifiers(t *testing.T) {
	for _, script := range []string{
		`printf "%ln" foo`,
		`printf "%lln" foo`,
		`printf "%zn" foo`,
		`printf "%hq" foo`,
		`printf "%la" 1.0`,
		`printf "%lA" 1.0`,
	} {
		stdout, stderr, code := cmdRun(t, script)
		assert.Equal(t, 1, code, script)
		assert.Empty(t, stdout, script)
		assert.Contains(t, stderr, "printf:", script)
	}
}

func TestVulnHuntBuiltinPrintfDoesNotReadBlockedStdin(t *testing.T) {
	stdin, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = stdin.Close() })
	t.Cleanup(func() { _ = writer.Close() })

	prog, err := syntax.NewParser().Parse(strings.NewReader(`printf "%s\n" ok`), "")
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(stdin, &stdout, &stderr),
		interp.AllowedCommands([]string{"rshell:printf"}),
		interp.MaxExecutionTime(25*time.Millisecond),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runner.Close() })

	start := time.Now()
	err = runner.Run(context.Background(), prog)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 500*time.Millisecond)
	assert.Equal(t, "ok\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntBuiltinPrintfInputRedirectSandboxedBeforeNoStdinCommand(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside")
	require.NoError(t, os.MkdirAll(allowed, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o644))

	stdout, stderr, code := runScript(t, "printf ok < ../outside/secret.txt\n", allowed,
		interp.AllowedCommands([]string{"rshell:printf"}),
		interp.AllowedPaths([]string{allowed}))

	assert.NotEqual(t, 0, code)
	assert.Empty(t, stdout)
	assert.NotEmpty(t, stderr)
}

func TestVulnHuntBuiltinPrintfHelpDoesNotApplyTrailingDangerousArgs(t *testing.T) {
	stdout, stderr, code := runScript(t, `printf --help -v PWNED "%n"; echo status=$?; echo PWNED=$PWNED`, "",
		interp.AllowedCommands([]string{"rshell:printf", "rshell:echo"}))

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "printf: usage: printf")
	assert.Contains(t, stdout, "status=2\n")
	assert.Contains(t, stdout, "PWNED=\n")
}

func TestVulnHuntBuiltinPrintfExpansionRemainsData(t *testing.T) {
	stdout, stderr, code := runScript(t, `PAYLOAD='; echo PWNED'; printf '[%s]\n' "$PAYLOAD"`, "",
		interp.AllowedCommands([]string{"rshell:printf"}))

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "[; echo PWNED]\n", stdout)
}
