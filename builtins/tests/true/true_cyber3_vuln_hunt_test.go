package true_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
)

func runScript(t *testing.T, script string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()

	prog, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	runner, err := interp.New(append([]interp.RunnerOption{
		interp.StdIO(nil, &stdout, &stderr),
	}, opts...)...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runner.Close() })

	err = runner.Run(context.Background(), prog)
	code := 0
	if err != nil {
		var exit interp.ExitStatus
		if errors.As(err, &exit) {
			code = int(exit)
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	return stdout.String(), stderr.String(), code
}

func TestVulnHuntBuiltinTrueDoesNotReadBlockedStdin(t *testing.T) {
	stdin, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = stdin.Close() })
	t.Cleanup(func() { _ = writer.Close() })

	prog, err := syntax.NewParser().Parse(strings.NewReader("true\n"), "")
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(stdin, &stdout, &stderr),
		interp.AllowedCommands([]string{"rshell:true"}),
		interp.MaxExecutionTime(25*time.Millisecond),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runner.Close() })

	start := time.Now()
	err = runner.Run(context.Background(), prog)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, 500*time.Millisecond)
	assert.Empty(t, stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntBuiltinTrueHelpMetadataDoesNotExecuteTrue(t *testing.T) {
	stdout, stderr, code := runScript(t, "true --help\necho true_status=$?\nhelp true\necho help_status=$?\n",
		interp.AllowedCommands([]string{"rshell:true", "rshell:help", "rshell:echo"}))

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "true_status=0\n")
	assert.Contains(t, stdout, "true: true\n")
	assert.Contains(t, stdout, "help_status=0\n")
	assert.NotContains(t, stdout, "Usage:")
}

func TestVulnHuntBuiltinTrueCommandPolicyGatesDispatch(t *testing.T) {
	stdout, stderr, code := runScript(t, "true\necho status=$?\n",
		interp.AllowedCommands([]string{"rshell:echo", "rshell:help"}))

	assert.Equal(t, 0, code)
	assert.Equal(t, "status=127\n", stdout)
	assert.Contains(t, stderr, "rshell: true: command not allowed")
	assert.Contains(t, stderr, "Run 'help' to see allowed commands.")
}

func TestVulnHuntBuiltinTrueDoesNotMutateShellState(t *testing.T) {
	stdout, stderr, code := runScript(t, `X=parent
X=inline true
echo after_inline=$X
( X=subshell; true; echo sub=$X )
echo parent=$X
for X in a b; do true; done
echo loop=$X
`, interp.AllowedCommands([]string{"rshell:true", "rshell:echo"}))

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "after_inline=parent\nsub=subshell\nparent=parent\nloop=b\n", stdout)
}
