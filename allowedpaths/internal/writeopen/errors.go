// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package writeopen

import "errors"

// ErrSymlinkWriteTarget reports a symlink in a write target. Writes reject
// symlinks rather than following them so the target cannot change between
// resolution and open.
var ErrSymlinkWriteTarget = errors.New("symlinks are not supported as write targets")

// ErrIsDirectory reports that Unlink's target is a directory.
var ErrIsDirectory = errors.New("is a directory")
