// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package interp

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestNestedAwkCommandInputRejectsBlockingPipe(t *testing.T) {
	var descriptors [2]int
	require.NoError(t, unix.Pipe(descriptors[:]))
	stdin := os.NewFile(uintptr(descriptors[0]), "blocking-pipe-reader")
	writer := os.NewFile(uintptr(descriptors[1]), "blocking-pipe-writer")
	require.NotNil(t, stdin)
	require.NotNil(t, writer)
	t.Cleanup(func() { _ = stdin.Close() })
	t.Cleanup(func() { _ = writer.Close() })
	require.False(t, fileIsNonblocking(t, stdin))

	var stderr bytes.Buffer
	r, err := New(
		StdIO(stdin, io.Discard, &stderr),
		MaxExecutionTime(100*time.Millisecond),
		allowAllCommandsOpt(),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, r.Close()) })

	prog := parseScript(t, `awk 'BEGIN { "cat" | getline }'`)
	done := make(chan error, 1)
	go func() {
		done <- r.Run(context.Background(), prog)
	}()

	select {
	case err := <-done:
		require.Error(t, err)
	case <-time.After(time.Second):
		_ = writer.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		t.Fatal("nested command did not reject blocking pipe stdin promptly")
	}
	assert.Contains(t, stderr.String(), "nested command stdin does not support cancellable reads")
	assert.False(t, fileIsNonblocking(t, stdin))

	_, err = writer.WriteString("x")
	require.NoError(t, err)
	buf := make([]byte, 1)
	_, err = io.ReadFull(stdin, buf)
	require.NoError(t, err)
	assert.Equal(t, "x", string(buf))
}

func TestNestedStdinFileAllowsNullDevice(t *testing.T) {
	stdin, err := os.Open(os.DevNull)
	require.NoError(t, err)
	t.Cleanup(func() { _ = stdin.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	childStdin, owned, closeOnCancel, err := nestedStdinFile(ctx, stdin)
	require.NoError(t, err)
	require.True(t, owned)
	require.False(t, closeOnCancel)
	require.NotNil(t, childStdin)
	require.NoError(t, childStdin.Close())
}

func fileIsNonblocking(t *testing.T, file *os.File) bool {
	t.Helper()
	var flags int
	var controlErr error
	raw, err := file.SyscallConn()
	require.NoError(t, err)
	require.NoError(t, raw.Control(func(fd uintptr) {
		flags, controlErr = unix.FcntlInt(fd, unix.F_GETFL, 0)
	}))
	require.NoError(t, controlErr)
	return flags&unix.O_NONBLOCK != 0
}
