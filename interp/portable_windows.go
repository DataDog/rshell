// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import "github.com/DataDog/rshell/allowedpaths"

// isErrIsDirectory checks if the error is the Windows equivalent of EISDIR.
func isErrIsDirectory(err error) bool {
	return allowedpaths.IsErrIsDirectory(err)
}
