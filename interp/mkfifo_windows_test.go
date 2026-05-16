// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package interp_test

import "errors"

func mkfifo(path string, mode uint32) error {
	return errors.New("mkfifo is not supported on Windows")
}
