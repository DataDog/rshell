// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/DataDog/rshell/privilegedhelper"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestPrivilegedHelperRootIntegration launches the actual rshell binary with a
// systemd-style inherited socket. It is intentionally opt-in because it must
// run as root to exercise the real seteuid boundary.
func TestPrivilegedHelperRootIntegration(t *testing.T) {
	if os.Getenv("RSHELL_ROOT_INTEGRATION") != "1" {
		t.Skip("set RSHELL_ROOT_INTEGRATION=1 to run")
	}
	if os.Getuid() != 0 {
		t.Fatal("root integration test must run with real uid 0")
	}
	binary := os.Getenv("RSHELL_BINARY")
	if binary == "" {
		t.Fatal("RSHELL_BINARY is required")
	}

	// The helper deliberately opens sandbox roots only after dropping to the
	// service user, so the root directory itself must be traversable by that
	// user. Keep the file root-only to test the actual selective elevation.
	dir, err := os.MkdirTemp("", "rshell-privileged-helper-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(dir)) })
	require.NoError(t, os.Chmod(dir, 0o755))
	target := filepath.Join(dir, "root-group-writable.log")
	// Group write is deliberate: this catches a helper that changes only EUID
	// while retaining root as its effective or supplementary group.
	require.NoError(t, os.WriteFile(target, []byte("data that must be truncated"), 0o660))

	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	require.NoError(t, err)
	publicPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	credential := privilegedhelper.Credential{
		Version: privilegedhelper.ProtocolVersion, OrgID: 42, RunnerID: "runner-1",
		Keys: []privilegedhelper.CredentialKey{{ID: "key-1", Type: privilegedhelper.KeyTypeED25519, PEM: string(publicPEM)}},
		AllowedCommands: []string{
			"rshell:df", "rshell:echo", "rshell:ip", "rshell:ps",
			"rshell:ss", "rshell:truncate", "rshell:uname",
		},
		AllowedPaths:       []string{dir + ":rw"},
		ElevatableCommands: []string{"rshell:truncate"},
	}
	credentialJSON, err := json.Marshal(credential)
	require.NoError(t, err)
	credentialPath := filepath.Join(dir, "credential.json")
	require.NoError(t, os.WriteFile(credentialPath, credentialJSON, 0o600))

	socketPath := filepath.Join(dir, "helper.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	require.NoError(t, err)
	defer listener.Close()
	listenerFile, err := listener.File()
	require.NoError(t, err)
	defer listenerFile.Close()

	command := exec.Command("/bin/sh", "-c", `LISTEN_PID=$$; export LISTEN_PID; exec "$RSHELL_BINARY" privileged-helper --user=nobody --credential="$RSHELL_CREDENTIAL" --idle-timeout=500ms`)
	command.Env = append(os.Environ(), "LISTEN_FDS=1", "RSHELL_BINARY="+binary, "RSHELL_CREDENTIAL="+credentialPath)
	command.ExtraFiles = []*os.File{listenerFile}
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	require.NoError(t, command.Start())
	defer func() {
		if command.ProcessState == nil {
			_ = command.Process.Kill()
		}
	}()

	client := privilegedhelper.Client{SocketPath: socketPath, Timeout: 3 * time.Second}
	for _, test := range []struct {
		command        string
		allowedCommand string
		wantOutput     bool
	}{
		{command: "ps -p 1", allowedCommand: "rshell:ps", wantOutput: true},
		{command: "ss -l", allowedCommand: "rshell:ss", wantOutput: true},
		{command: "ip route show", allowedCommand: "rshell:ip", wantOutput: true},
		{command: "df -P", allowedCommand: "rshell:df", wantOutput: true},
		{command: "uname -a", allowedCommand: "rshell:uname", wantOutput: true},
		{command: "echo discarded >/dev/null", allowedCommand: "rshell:echo"},
	} {
		response, err := client.Execute(context.Background(), signedIntegrationRequest(t, privateKey, test.command, dir, test.allowedCommand))
		require.NoError(t, err, "command: %s", test.command)
		require.Zero(t, response.ExitCode, "command: %s; stderr: %s", test.command, response.Stderr)
		if test.wantOutput {
			require.NotEmpty(t, response.Stdout, "command: %s", test.command)
		}
	}

	nonElevated := signedIntegrationRequest(t, privateKey, "truncate -s 0 "+target, dir, "rshell:truncate")
	response, err := client.Execute(context.Background(), nonElevated)
	require.NoError(t, err)
	require.NotZero(t, response.ExitCode, "non-elevated command unexpectedly modified a root-only file")
	info, err := os.Stat(target)
	require.NoError(t, err)
	require.NotZero(t, info.Size())

	elevated := signedIntegrationRequest(t, privateKey, "sudo truncate -s 0 "+target, dir, "rshell:truncate")
	response, err = client.Execute(context.Background(), elevated)
	require.NoError(t, err)
	require.Zero(t, response.ExitCode, "stderr: %s", response.Stderr)
	info, err = os.Stat(target)
	require.NoError(t, err)
	require.Zero(t, info.Size())
	require.NoError(t, command.Wait())
}

func signedIntegrationRequest(t *testing.T, privateKey ed25519.PrivateKey, command, allowedDir, allowedCommand string) privilegedhelper.ExecuteRequest {
	t.Helper()
	inputs, err := structpb.NewStruct(map[string]any{
		"command": command, "effectivePermissions": privilegedhelper.EscalationAllowed,
		"elevatableCommands": []any{"rshell:truncate"},
	})
	require.NoError(t, err)
	task := &privilegedhelper.PrivateActionTask{
		ActionName: "runRemediationCommand", BundleId: "com.datadoghq.remoteaction.rshell",
		OrgId: 42, TaskId: fmt.Sprintf("task-%x", sha256.Sum256([]byte(command))), Inputs: inputs,
		ConnectionInfo: &privilegedhelper.ConnectionInfo{RunnerId: "runner-1"},
		ExpirationTime: timestamppb.New(time.Now().Add(time.Minute)),
		SystemInputs: &privilegedhelper.SystemInputs{Input: &privilegedhelper.SystemInputs_RemoteAction{
			RemoteAction: &privilegedhelper.RemoteAction{AllowedCommands: []string{allowedCommand}, AllowedPaths: []string{allowedDir + ":rw"}},
		}},
	}
	data, err := proto.Marshal(task)
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	return privilegedhelper.ExecuteRequest{Version: privilegedhelper.ProtocolVersion, Envelope: privilegedhelper.SignedEnvelope{
		Data: data, HashType: "SHA256", Signatures: []privilegedhelper.Signature{{
			KeyType: privilegedhelper.KeyTypeED25519, KeyID: "key-1", Signature: ed25519.Sign(privateKey, digest[:]),
		}},
	}}
}
