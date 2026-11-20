// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package wineventlog

import (
	"context"
	"errors"
)

// ErrUnsupported is returned by every exported entry point on non-Windows
// platforms. The get-winevent builtin prints its own platform-specific error
// before reaching these stubs, so this error is only visible to callers that
// use the package directly on the wrong platform.
var ErrUnsupported = errors.New("wineventlog: only supported on Windows")

// Run is the non-Windows stub.
func Run(_ context.Context, _ Query, _ OnEvent, _ OnWarn) error {
	return ErrUnsupported
}

// ListChannels is the non-Windows stub.
func ListChannels(_ context.Context) ([]string, error) {
	return nil, ErrUnsupported
}

// ListProviders is the non-Windows stub.
func ListProviders(_ context.Context) ([]string, error) {
	return nil, ErrUnsupported
}
