// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import "github.com/DataDog/rshell/allowedpaths"

// portableErrMsg delegates to allowedpaths.PortableErrMsg.
func portableErrMsg(err error) string {
	return allowedpaths.PortableErrMsg(err)
}

// portablePathError delegates to allowedpaths.PortablePathError.
func portablePathError(err error) error {
	return allowedpaths.PortablePathError(err)
}
