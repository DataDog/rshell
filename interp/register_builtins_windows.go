// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package interp

import (
	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/ntfsdu"
)

// platformBuiltins returns builtins that exist only on Windows and thus
// listed by `help` and runnable only on Windows.
func platformBuiltins() []builtins.Command {
	return []builtins.Command{ntfsdu.Cmd}
}
