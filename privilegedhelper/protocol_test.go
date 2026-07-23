// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.

package privilegedhelper

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewExecuteRequestKeepsAuthorizationInsideSignedEnvelope(t *testing.T) {
	envelope := SignedEnvelope{
		Data:       []byte("signed task containing command and permissions"),
		HashType:   "SHA256",
		Signatures: []Signature{{KeyType: KeyTypeED25519, KeyID: "key-1", Signature: []byte("signature")}},
	}

	request := NewExecuteRequest(envelope)

	require.Equal(t, ProtocolVersion, request.Version)
	require.Equal(t, envelope, request.Envelope)
	wire, err := json.Marshal(request)
	require.NoError(t, err)
	var outerFields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(wire, &outerFields))
	require.ElementsMatch(t, []string{"version", "envelope"}, mapKeys(outerFields))
}

func mapKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func TestExecuteSignedTaskBuildsVersionedRequest(t *testing.T) {
	dir := t.TempDir()
	socketPath := dir + "/helper.sock"
	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	defer listener.Close()

	received := make(chan ExecuteRequest, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		var request ExecuteRequest
		if readErr := readMessage(conn, &request); readErr != nil {
			return
		}
		received <- request
		_ = writeMessage(conn, ExecuteResponse{Version: ProtocolVersion, ExitCode: 23})
	}()

	envelope := SignedEnvelope{Data: []byte("signed"), HashType: "SHA256"}
	response, err := (Client{SocketPath: socketPath, Timeout: time.Second}).ExecuteSignedTask(context.Background(), envelope)
	require.NoError(t, err)
	require.Equal(t, 23, response.ExitCode)
	require.Equal(t, NewExecuteRequest(envelope), <-received)
}
