// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedpaths

import "errors"

// ErrACLNotSupported is returned by Sandbox.GetACL and Sandbox.SetACL on
// platforms without a POSIX ACL xattr backend (everything but Linux).
var ErrACLNotSupported = errors.New("ACLs are not supported on this platform")
