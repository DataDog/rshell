// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.

package privilegedhelper

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

type Executor interface {
	Execute(context.Context, *VerifiedCommand) (*ExecuteResponse, error)
}

type Server struct {
	Credential  *Credential
	Executor    Executor
	Now         func() time.Time
	IdleTimeout time.Duration
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	if s.Credential == nil || s.Executor == nil {
		return errors.New("helper server requires credential and executor")
	}
	for {
		if s.IdleTimeout > 0 {
			if deadlineListener, ok := listener.(interface{ SetDeadline(time.Time) error }); ok {
				_ = deadlineListener.SetDeadline(time.Now().Add(s.IdleTimeout))
			}
		}
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				return nil
			}
			return fmt.Errorf("accept helper connection: %w", err)
		}
		// Deliberately process one connection synchronously. The privileged
		// helper is a single-task executor, matching the RFC's isolation model.
		s.handle(ctx, conn)
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	response := ExecuteResponse{Version: ProtocolVersion}
	var request ExecuteRequest
	if err := readMessage(conn, &request); err != nil {
		response.Error = err.Error()
		_ = writeMessage(conn, response)
		return
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	command, err := s.Credential.Verify(request, now)
	if err != nil {
		response.Error = err.Error()
		_ = writeMessage(conn, response)
		return
	}
	result, err := s.Executor.Execute(ctx, command)
	if err != nil {
		response.Error = err.Error()
	} else if result != nil {
		response = *result
		response.Version = ProtocolVersion
	}
	_ = writeMessage(conn, response)
}
