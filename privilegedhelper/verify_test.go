// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.

package privilegedhelper

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
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

func TestVerifySignedRequest(t *testing.T) {
	credential, private := testCredential(t)
	verified, err := credential.Verify(signedRequest(t, private, nil), time.Now())
	require.NoError(t, err)
	require.Equal(t, "task-1", verified.TaskID)
	require.Equal(t, []string{"rshell:truncate"}, verified.AllowedCommands)
	require.Equal(t, []string{"rshell:truncate"}, verified.ElevatableCommands)
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
