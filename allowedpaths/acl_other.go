// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !linux

package allowedpaths

import "os"

// GetACL returns ErrACLNotSupported on platforms without a POSIX ACL xattr
// backend. Nothing calls this outside Linux today: setfacl, the only
// consumer, does not register on other platforms.
func (s *Sandbox) GetACL(path, _ string) (access, def []byte, err error) {
	return nil, nil, &os.PathError{Op: "getxattr", Path: path, Err: ErrACLNotSupported}
}

// SetACL returns ErrACLNotSupported on platforms without a POSIX ACL xattr
// backend.
func (s *Sandbox) SetACL(path, _ string, _, _ []byte) error {
	return &os.PathError{Op: "setxattr", Path: path, Err: ErrACLNotSupported}
}
