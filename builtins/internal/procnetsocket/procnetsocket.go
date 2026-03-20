// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package procnetsocket reads Linux socket state from /proc/net/.
//
// This package is in builtins/internal/ and is therefore exempt from the
// builtinAllowedSymbols allowlist check. It may use OS-specific APIs freely.
//
// # Sandbox bypass
//
// All Read* functions intentionally bypass the AllowedPaths sandbox
// (callCtx.OpenFile) and call os.Open directly. This is safe because procPath
// is always a kernel-managed pseudo-filesystem root (/proc by default) that is
// hardcoded by the caller — it is never derived from user-supplied input and
// cannot be redirected by a shell script. The caller is responsible for
// ensuring that procPath remains a safe, non-user-controlled path.
package procnetsocket

import (
	"context"
	"fmt"
	"strings"

	"github.com/DataDog/rshell/builtins/internal/procpath"
)

// DefaultProcPath is the default proc filesystem root.
const DefaultProcPath = procpath.Default

// MaxLineBytes is the per-line buffer cap for the /proc/net/ scanner.
const MaxLineBytes = 1 << 20 // 1 MiB

// SocketKind identifies the protocol family of a parsed socket entry.
type SocketKind int

const (
	KindTCP4 SocketKind = iota
	KindTCP6
	KindUDP4
	KindUDP6
	KindUnix
)

// SocketEntry holds a parsed socket entry from /proc/net/.
type SocketEntry struct {
	Kind        SocketKind
	State       string
	RecvQ       uint64
	SendQ       uint64
	LocalAddr   string
	LocalPort   string
	PeerAddr    string
	PeerPort    string
	UID         uint32
	Inode       uint64
	HasExtended bool
}

// validateProcPath rejects any procPath that contains ".." components.
// Defence-in-depth: procPath is always a hardcoded kernel pseudo-filesystem
// root in production and never derived from user input, so this check should
// never trigger. It mirrors the equivalent guard in procnetroute.ReadRoutes
// and ensures the invariant is enforced consistently across both packages.
func validateProcPath(procPath string) error {
	if strings.Contains(procPath, "..") {
		return fmt.Errorf("procnetsocket: unsafe procPath %q (must not contain \"..\" components)", procPath)
	}
	return nil
}

// ReadTCP4 reads procPath/net/tcp and returns IPv4 TCP socket entries.
//
// Sandbox bypass: os.Open is used directly; path is derived from procPath, a
// hardcoded kernel pseudo-filesystem root never supplied by user input.
//
// Defence-in-depth: ".." components are always rejected regardless of context.
func ReadTCP4(ctx context.Context, procPath string) ([]SocketEntry, error) {
	if err := validateProcPath(procPath); err != nil {
		return nil, err
	}
	return readTCP4(ctx, procPath)
}

// ReadTCP6 reads procPath/net/tcp6 and returns IPv6 TCP socket entries.
//
// Sandbox bypass: same rationale as ReadTCP4.
// Defence-in-depth: same ".." guard as ReadTCP4.
func ReadTCP6(ctx context.Context, procPath string) ([]SocketEntry, error) {
	if err := validateProcPath(procPath); err != nil {
		return nil, err
	}
	return readTCP6(ctx, procPath)
}

// ReadUDP4 reads procPath/net/udp and returns IPv4 UDP socket entries.
//
// Sandbox bypass: same rationale as ReadTCP4.
// Defence-in-depth: same ".." guard as ReadTCP4.
func ReadUDP4(ctx context.Context, procPath string) ([]SocketEntry, error) {
	if err := validateProcPath(procPath); err != nil {
		return nil, err
	}
	return readUDP4(ctx, procPath)
}

// ReadUDP6 reads procPath/net/udp6 and returns IPv6 UDP socket entries.
//
// Sandbox bypass: same rationale as ReadTCP4.
// Defence-in-depth: same ".." guard as ReadTCP4.
func ReadUDP6(ctx context.Context, procPath string) ([]SocketEntry, error) {
	if err := validateProcPath(procPath); err != nil {
		return nil, err
	}
	return readUDP6(ctx, procPath)
}

// ReadUnix reads procPath/net/unix and returns Unix domain socket entries.
//
// Sandbox bypass: same rationale as ReadTCP4.
// Defence-in-depth: same ".." guard as ReadTCP4.
func ReadUnix(ctx context.Context, procPath string) ([]SocketEntry, error) {
	if err := validateProcPath(procPath); err != nil {
		return nil, err
	}
	return readUnix(ctx, procPath)
}
