// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.

// Package privilegedhelper defines the authenticated wire protocol shared by
// rshell's privileged helper and the Datadog Private Action Runner.
package privilegedhelper

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const (
	ProtocolVersion = 1
	MaxMessageBytes = 1 << 20
)

type KeyType string

const (
	KeyTypeX509RSA KeyType = "X509_RSA"
	KeyTypeED25519 KeyType = "ED25519"
)

type Signature struct {
	KeyType   KeyType `json:"keyType"`
	KeyID     string  `json:"keyId"`
	Signature []byte  `json:"signature"`
}

// SignedEnvelope contains the original backend-signed protobuf bytes. Trust
// roots are deliberately absent: they must come from the helper credential.
type SignedEnvelope struct {
	Data       []byte      `json:"data"`
	HashType   string      `json:"hashType"`
	Signatures []Signature `json:"signatures"`
}

type ExecuteRequest struct {
	Version  int            `json:"version"`
	Envelope SignedEnvelope `json:"envelope"`
}

type ExecuteResponse struct {
	Version         int      `json:"version"`
	ExitCode        int      `json:"exitCode"`
	Stdout          string   `json:"stdout,omitempty"`
	Stderr          string   `json:"stderr,omitempty"`
	SandboxWarnings []string `json:"sandboxWarnings,omitempty"`
	Error           string   `json:"error,omitempty"`
}

func writeMessage(w io.Writer, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	if len(payload) > MaxMessageBytes {
		return fmt.Errorf("message exceeds %d bytes", MaxMessageBytes)
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(payload)))
	if _, err := w.Write(size[:]); err != nil {
		return fmt.Errorf("write message size: %w", err)
	}
	if _, err := w.Write(payload); err != nil {
		return fmt.Errorf("write message: %w", err)
	}
	return nil
}

func readMessage(r io.Reader, value any) error {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return fmt.Errorf("read message size: %w", err)
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > MaxMessageBytes {
		return fmt.Errorf("invalid message size %d", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return fmt.Errorf("read message: %w", err)
	}
	dec := json.NewDecoder(io.LimitReader(&byteReader{data: payload}, int64(n)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		return fmt.Errorf("decode message: %w", err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return err
	}
	return nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("message contains trailing JSON data")
	}
	return nil
}

type byteReader struct{ data []byte }

func (r *byteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

type Client struct {
	SocketPath string
	Timeout    time.Duration
}

func (c Client) Execute(ctx context.Context, req ExecuteRequest) (*ExecuteResponse, error) {
	if c.SocketPath == "" {
		return nil, errors.New("socket path is required")
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	dialer := net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "unix", c.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("connect to privileged helper: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(timeout))
	}
	if err := writeMessage(conn, req); err != nil {
		return nil, err
	}
	var response ExecuteResponse
	if err := readMessage(conn, &response); err != nil {
		return nil, err
	}
	if response.Version != ProtocolVersion {
		return nil, fmt.Errorf("unsupported response version %d", response.Version)
	}
	if response.Error != "" {
		return nil, errors.New(response.Error)
	}
	return &response, nil
}
