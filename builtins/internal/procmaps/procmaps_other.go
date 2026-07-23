// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !linux && !windows

package procmaps

import "context"

// readImpl returns ErrNotSupported on platforms without a backend
// (currently everything but Linux and Windows — see the package doc
// comment for why macOS is not implemented).
func readImpl(_ context.Context, _ string, _ int, _ bool) (string, []Mapping, error) {
	return "", nil, ErrNotSupported
}
