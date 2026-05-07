// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !linux

// DEMO ONLY: see host_exec_linux.go. The host-binary entry-point is a
// Linux-only demo; on darwin/windows we simply refuse to dispatch.

package interp

import "context"

// runHostCommand is a stub for non-Linux platforms. It writes an explanatory
// error to the runner's stderr and returns 127 (shell-conventional "command
// not found / not executable") so callers can rely on a non-zero exit.
func (r *Runner) runHostCommand(_ context.Context, _ string, args []string) uint8 {
	r.errf("rshell: %s: host execution not supported on this platform\n", args[0])
	return 127
}
