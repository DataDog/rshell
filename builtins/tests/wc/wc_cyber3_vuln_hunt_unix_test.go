//go:build unix

package wc_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
)

func TestVulnHuntBuiltinWcDevZeroRespectsMaxExecutionTime(t *testing.T) {
	prog, err := syntax.NewParser().Parse(strings.NewReader("wc -c /dev/zero\n"), "")
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	r, err := interp.New(
		interp.StdIO(nil, &stdout, &stderr),
		interp.AllowedCommands([]string{"rshell:wc"}),
		interp.AllowedPaths([]string{"/dev"}),
		interp.MaxExecutionTime(25*time.Millisecond),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	start := time.Now()
	err = r.Run(context.Background(), prog)
	elapsed := time.Since(start)

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 2*time.Second)
}
