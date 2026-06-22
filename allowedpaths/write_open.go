// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedpaths

import (
	"os"

	"github.com/DataDog/rshell/allowedpaths/internal/writeopen"
)

func openWriteRoot(root *os.Root) (*os.File, error) {
	return writeopen.OpenRoot(root)
}

func closeWriteRoot(file *os.File) {
	writeopen.CloseRoot(file)
}

func (r *root) openWriteFile(relPath string, flag int, perm os.FileMode) (*os.File, error) {
	return writeopen.OpenFile(r.writeRoot, r.root, relPath, flag, perm)
}
