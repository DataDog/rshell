// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: tail (builtin)

package tail_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	tail "github.com/DataDog/rshell/builtins/tail"
	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
)

type repeatByteReader byte

func (r repeatByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

func TestVulnHuntBuiltinFlagDrivenExploit_DangerousFollowAliasesRejected(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello\n"), 0o644))

	for _, script := range []string{
		"tail -F f.txt",
		"tail --follow f.txt",
		"tail --follow=name f.txt",
		"tail --pid=1 f.txt",
		"tail --retry f.txt",
		`flag="--pid=1"; tail $flag f.txt`,
	} {
		t.Run(script, func(t *testing.T) {
			_, stderr, code := tailRun(t, script, dir)
			assert.Equal(t, 1, code)
			assert.Contains(t, stderr, "tail:")
		})
	}
}

func TestVulnHuntBuiltinFlagDrivenExploit_DoubleDashProtectsBareNumberFilenames(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "-5"), []byte("flag-shaped\n"), 0o644))

	stdout, stderr, code := tailRun(t, "tail -- -5", dir)

	assert.Equal(t, 0, code)
	assert.Equal(t, "flag-shaped\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinDeclaredVsImplemented_InvalidCountsReportBeforeHelp(t *testing.T) {
	dir := t.TempDir()

	_, stderr, code := tailRun(t, "tail -n nope --help", dir)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "tail: invalid number of lines: 'nope'")
}

func TestVulnHuntBuiltinResourceExhaustion_ByteBufferLimitFailsClosed(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "big.bin"))
	require.NoError(t, err)
	_, err = io.CopyN(f, repeatByteReader('A'), int64(tail.MaxBytesBuffer)+1)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	_, stderr, code := tailRun(t, fmt.Sprintf("tail -c %d big.bin", tail.MaxBytesBuffer+1), dir)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "byte buffer limit exceeded")
}

func TestVulnHuntBuiltinSpecialFiles_ByteOffsetDevZeroHonorsConfiguredTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/zero on Windows")
	}

	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader("tail -c +1 /dev/zero"), "")
	require.NoError(t, err)

	var stderr bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(nil, io.Discard, &stderr),
		interpoption.AllowAllCommands().(interp.RunnerOption),
		interp.AllowedPaths([]string{"/dev"}),
		interp.MaxExecutionTime(25*time.Millisecond),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = runner.Close() })

	start := time.Now()
	err = runner.Run(context.Background(), prog)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, time.Since(start), 2*time.Second)
}
