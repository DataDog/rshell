// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !linux && !darwin

package writeopen

import "os"

func OpenRoot(*os.Root) (*os.File, error) {
	return nil, nil
}

func CloseRoot(*os.File) {}

func OpenFile(_ *os.File, root *os.Root, relPath string, flag int, perm os.FileMode) (*os.File, error) {
	return root.OpenFile(relPath, flag, perm)
}
