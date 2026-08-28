// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package privilegedhelper

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func signedRequest(t *testing.T, private ed25519.PrivateKey, mutate func(*PrivateActionTask)) ExecuteRequest {
	t.Helper()
	inputs, err := structpb.NewStruct(map[string]any{
		"command":              "sudo truncate -s 0 /var/log/app.log",
		"effectivePermissions": EscalationAllowed,
		"elevatableCommands":   []any{"rshell:truncate"},
	})
	require.NoError(t, err)
	task := &PrivateActionTask{
		ActionName: remediationAction, BundleId: remediationBundle, OrgId: 42, TaskId: "task-1",
		Inputs: inputs, ConnectionInfo: &ConnectionInfo{RunnerId: "runner-1"},
		ExpirationTime: timestamppb.New(time.Now().Add(time.Minute)),
		SystemInputs: &SystemInputs{Input: &SystemInputs_RemoteAction{RemoteAction: &RemoteAction{
			AllowedCommands: []string{"rshell:truncate", "rshell:echo"}, AllowedPaths: []string{"/var/log"},
		}}},
	}
	if mutate != nil {
		mutate(task)
	}
	data, err := proto.Marshal(task)
	require.NoError(t, err)
	digest := sha256.Sum256(data)
	return ExecuteRequest{Version: ProtocolVersion, Envelope: SignedEnvelope{
		Data: data, HashType: "SHA256", Signatures: []Signature{{KeyType: KeyTypeED25519, KeyID: "key-1", Signature: ed25519.Sign(private, digest[:])}},
	}}
}

func testCredential(t *testing.T) (*Credential, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return &Credential{
		Version: ProtocolVersion, OrgID: 42, RunnerID: "runner-1",
		AllowedCommands: []string{"rshell:truncate"}, AllowedPaths: []string{"/var/log"}, ElevatableCommands: []string{"rshell:truncate"},
		decodedKeys: map[string]verificationKey{"key-1": ed25519Key{key: public}},
	}, private
}

func socketCredentialKey(t *testing.T, private ed25519.PrivateKey) CredentialKey {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(private.Public())
	require.NoError(t, err)
	return CredentialKey{
		ID: "key-1", Type: KeyTypeED25519,
		PEM: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})),
	}
}

func TestVerifySignedRequest(t *testing.T) {
	credential, private := testCredential(t)
	verified, err := credential.Verify(signedRequest(t, private, nil), time.Now())
	require.NoError(t, err)
	require.Equal(t, "task-1", verified.TaskID)
	require.Equal(t, []string{"rshell:truncate"}, verified.AllowedCommands)
	require.Equal(t, []string{"rshell:truncate"}, verified.ElevatableCommands)
}

func TestIntersectPathsCollapsesDuplicateModes(t *testing.T) {
	require.Equal(t,
		[]string{"/var/log:rw"},
		intersectPaths([]string{"/:ro", "/:rw"}, []string{"/var/log:rw"}),
	)
	require.Equal(t,
		[]string{"/var/log:rw"},
		intersectPaths([]string{"/:rw", "/:ro"}, []string{"/var/log:rw"}),
	)
}

type testExecutor struct {
	command *VerifiedCommand
}

func (e *testExecutor) Execute(_ context.Context, command *VerifiedCommand) (*ExecuteResponse, error) {
	e.command = command
	return &ExecuteResponse{ExitCode: 23}, nil
}

func TestServerUsesSocketVerificationKeyForOneRequest(t *testing.T) {
	credential, private := testCredential(t)
	credential.decodedKeys = map[string]verificationKey{}
	executor := &testExecutor{}
	server := &Server{Credential: credential, Executor: executor}
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	go server.handle(context.Background(), serverConn)

	request := signedRequest(t, private, nil)
	request.VerificationKeys = []CredentialKey{socketCredentialKey(t, private)}
	require.NoError(t, writeMessage(clientConn, request))
	var response ExecuteResponse
	require.NoError(t, readMessage(clientConn, &response))

	require.Empty(t, response.Error)
	require.Equal(t, 23, response.ExitCode)
	require.Equal(t, "task-1", executor.command.TaskID)
	_, err := credential.Verify(request, time.Now())
	require.EqualError(t, err, "no trusted signature found")
}

func TestServerLogsAuthorizationPolicyIntersection(t *testing.T) {
	credential, private := testCredential(t)
	credential.ElevatableCommands = nil
	executor := &testExecutor{}
	var diagnostics bytes.Buffer
	server := &Server{Credential: credential, Executor: executor, LogWriter: &diagnostics}
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	go server.handle(context.Background(), serverConn)

	require.NoError(t, writeMessage(clientConn, signedRequest(t, private, nil)))
	var response ExecuteResponse
	require.NoError(t, readMessage(clientConn, &response))

	require.Empty(t, response.Error)
	require.Empty(t, executor.command.ElevatableCommands)
	logged := diagnostics.String()
	require.Contains(t, logged, `"event":"authorization_context"`)
	require.Contains(t, logged, `"taskId":"task-1"`)
	require.Contains(t, logged, `"orgId":42`)
	require.Contains(t, logged, `"runnerId":"runner-1"`)
	require.Contains(t, logged, `"effectivePermissions":"EscalationAllowed"`)
	require.Contains(t, logged, `"signed":{"allowedCommands":["rshell:truncate","rshell:echo"],"allowedPaths":["/var/log"],"elevatableCommands":["rshell:truncate"]}`)
	require.Contains(t, logged, `"local":{"allowedCommands":["rshell:truncate"],"allowedPaths":["/var/log"],"elevatableCommands":null}`)
	require.Contains(t, logged, `"effective":{"allowedCommands":["rshell:truncate"],"allowedPaths":["/var/log"],"elevatableCommands":[]}`)
	require.Contains(t, logged, `"event":"execution_completed"`)
	require.NotContains(t, logged, "sudo truncate")
	require.NotContains(t, logged, "BEGIN PUBLIC KEY")
}

func TestDecodeX509RSAAcceptsAgentPublicKeyPEM(t *testing.T) {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(&private.PublicKey)
	require.NoError(t, err)

	key, err := decodeKey(CredentialKey{
		ID: "rsa-key", Type: KeyTypeX509RSA,
		PEM: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})),
	})

	require.NoError(t, err)
	require.Equal(t, KeyTypeX509RSA, key.keyType())
}

func TestVerifySignedInputTypesFailClosed(t *testing.T) {
	credential, private := testCredential(t)
	tests := []struct {
		name       string
		mutateTask func(*PrivateActionTask)
		wantError  string
	}{
		{
			name: "missing inputs",
			mutateTask: func(task *PrivateActionTask) {
				task.Inputs = nil
			},
			wantError: "signed task inputs are required",
		},
		{
			name: "command has wrong type",
			mutateTask: func(task *PrivateActionTask) {
				task.Inputs.Fields["command"] = structpb.NewNumberValue(1)
			},
			wantError: "signed task command must be a non-empty string",
		},
		{
			name: "permissions are missing",
			mutateTask: func(task *PrivateActionTask) {
				delete(task.Inputs.Fields, "effectivePermissions")
			},
			wantError: "signed task effectivePermissions is required",
		},
		{
			name: "elevatable commands have wrong type",
			mutateTask: func(task *PrivateActionTask) {
				task.Inputs.Fields["elevatableCommands"] = structpb.NewStringValue("rshell:truncate")
			},
			wantError: "signed task elevatableCommands must be an array",
		},
		{
			name: "elevatable command is empty",
			mutateTask: func(task *PrivateActionTask) {
				task.Inputs.Fields["elevatableCommands"] = structpb.NewListValue(&structpb.ListValue{Values: []*structpb.Value{structpb.NewStringValue("")}})
			},
			wantError: "signed task elevatableCommands must contain non-empty strings",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := credential.Verify(signedRequest(t, private, tc.mutateTask), time.Now())
			require.EqualError(t, err, tc.wantError)
		})
	}
}

func TestVerifyFailsClosed(t *testing.T) {
	credential, private := testCredential(t)
	tests := []struct {
		name          string
		mutateTask    func(*PrivateActionTask)
		mutateRequest func(*ExecuteRequest)
	}{
		{name: "expired", mutateTask: func(task *PrivateActionTask) { task.ExpirationTime = timestamppb.New(time.Now().Add(-time.Second)) }},
		{name: "wrong org", mutateTask: func(task *PrivateActionTask) { task.OrgId++ }},
		{name: "wrong runner", mutateTask: func(task *PrivateActionTask) { task.ConnectionInfo.RunnerId = "other" }},
		{name: "wrong action", mutateTask: func(task *PrivateActionTask) { task.ActionName = "runCommand" }},
		{name: "root mode", mutateTask: func(task *PrivateActionTask) {
			task.Inputs.Fields["effectivePermissions"] = structpb.NewStringValue("Root")
		}},
		{name: "tampered", mutateRequest: func(req *ExecuteRequest) { req.Envelope.Data = append(req.Envelope.Data, 0) }},
		{name: "unknown key", mutateRequest: func(req *ExecuteRequest) { req.Envelope.Signatures[0].KeyID = "unknown" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := signedRequest(t, private, tc.mutateTask)
			if tc.mutateRequest != nil {
				tc.mutateRequest(&req)
			}
			_, err := credential.Verify(req, time.Now())
			require.Error(t, err)
		})
	}
}
