// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedpaths

import (
	"os"

	"github.com/DataDog/rshell/allowedpaths/internal/writeopen"
)

const invalidWriteFD = writeopen.InvalidFD

func openWriteRoot(path string) (int, error) {
	return writeopen.OpenRoot(path)
}

func closeWriteRoot(fd int) {
	writeopen.CloseRoot(fd)
}

func (r *root) openWriteFile(relPath string, flag int, perm os.FileMode) (*os.File, error) {
	return writeopen.OpenFile(r.writeFD, r.root, relPath, flag, perm)
}
