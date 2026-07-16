// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package systemd

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDialPinnedJournalControlIgnoresPathReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "journal.sock")
	original := listenUnixSocket(t, path)

	socket, err := (&Client{}).openJournalControlSocket(path)
	require.NoError(t, err)
	t.Cleanup(func() { _ = socket.Close() })

	require.NoError(t, os.Rename(path, filepath.Join(dir, "original.sock")))
	attacker := listenUnixSocket(t, path)

	conn, err := dialPinnedJournalControl(context.Background(), int(socket.Fd()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	require.NoError(t, original.SetDeadline(time.Now().Add(time.Second)))
	accepted, err := original.AcceptUnix()
	require.NoError(t, err)
	require.NoError(t, accepted.Close())

	require.NoError(t, attacker.SetDeadline(time.Now().Add(100*time.Millisecond)))
	_, err = attacker.AcceptUnix()
	require.Error(t, err)
	var netErr net.Error
	require.ErrorAs(t, err, &netErr)
	assert.True(t, netErr.Timeout())
}

func TestRotateJournalResolvesAbsoluteSocketSymlinkWithinTargetRoot(t *testing.T) {
	root := shortSocketTempDir(t)
	realDirectory := filepath.Join(root, "custom", "systemd", "journal")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "etc"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "run"), 0o700))
	require.NoError(t, os.MkdirAll(realDirectory, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "etc", "machine-id"), []byte("0123456789abcdef0123456789abcdef\n"), 0o600))
	require.NoError(t, os.Symlink(filepath.FromSlash("/custom/systemd"), filepath.Join(root, "run", "systemd")))
	listener := listenUnixSocket(t, filepath.Join(realDirectory, "io.systemd.journal"))
	serverFinished := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			serverFinished <- err
			return
		}
		_, finished := serveVarlinkResponse(conn, []byte(`{"parameters":{}}`), nil)
		serverFinished <- <-finished
	}()

	target, err := ResolveTarget(Target{Root: root})
	require.NoError(t, err)
	require.NoError(t, NewClient(target).RotateJournal(context.Background()))
	require.NoError(t, <-serverFinished)
}

func TestDialJournalControlKeepsAbsoluteParentSymlinkWithinTargetRoot(t *testing.T) {
	parent := shortSocketTempDir(t)
	root := filepath.Join(parent, "host")
	outsideDirectory := filepath.Join(parent, "outside-systemd", "journal")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "run"), 0o700))
	require.NoError(t, os.MkdirAll(outsideDirectory, 0o700))
	require.NoError(t, os.Symlink(filepath.Dir(outsideDirectory), filepath.Join(root, "run", "systemd")))
	listenUnixSocket(t, filepath.Join(outsideDirectory, "io.systemd.journal"))

	target, err := ResolveTarget(Target{Root: root})
	require.NoError(t, err)
	_, err = NewClient(target).dialJournalControl(context.Background(), target.JournalControlSocket)
	require.Error(t, err)
	assert.ErrorContains(t, err, "inspect journal control socket")
}

func shortSocketTempDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "rsock-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(directory)) })
	return directory
}

func listenUnixSocket(t *testing.T, path string) *net.UnixListener {
	t.Helper()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	require.NoError(t, err)
	listener.SetUnlinkOnClose(false)
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}
