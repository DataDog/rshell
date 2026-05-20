package false_test

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

func TestVulnHuntBuiltinFalseDoesNotReadBlockedStdin(t *testing.T) {
	stdin, writer, err := os.Pipe()
	require.NoError(t, err)
	t.Cleanup(func() { _ = stdin.Close() })
	t.Cleanup(func() { _ = writer.Close() })

	prog, err := syntax.NewParser().Parse(strings.NewReader("false\n"), "")
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(stdin, &stdout, &stderr),
		interp.AllowedCommands([]string{"rshell:false"}),
		interp.MaxExecutionTime(25*time.Millisecond),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runner.Close() })

	start := time.Now()
	err = runner.Run(context.Background(), prog)
	elapsed := time.Since(start)

	var exit interp.ExitStatus
	require.ErrorAs(t, err, &exit)
	assert.Equal(t, interp.ExitStatus(1), exit)
	assert.Less(t, elapsed, 500*time.Millisecond)
	assert.Empty(t, stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntBuiltinFalseHelpMetadataDoesNotExecuteFalse(t *testing.T) {
	stdout, stderr, code := runScript(t, "false --help\necho false_status=$?\nhelp false\necho help_status=$?\n",
		interp.AllowedCommands([]string{"rshell:false", "rshell:help", "rshell:echo"}))

	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Contains(t, stdout, "false_status=1\n")
	assert.Contains(t, stdout, "false: false\n")
	assert.Contains(t, stdout, "help_status=0\n")
	assert.NotContains(t, stdout, "Usage:")
}
