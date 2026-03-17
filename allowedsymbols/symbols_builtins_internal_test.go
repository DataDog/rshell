// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

import (
	"testing"
)

// builtinsInternalCheckConfig returns the allowedSymbolsConfig used to enforce
// symbol-level restrictions on builtins/internal/ helper packages.
// These packages provide OS-specific implementations and use a broader
// allowlist than command files, but are still prohibited from importing
// permanently-banned packages (unsafe, reflect, os/exec, net, plugin).
//
// Third-party OS abstraction packages (golang.org/x/sys/unix,
// golang.org/x/sys/windows) are exempted via ExemptImport because they
// provide safe wrappers around OS-level process-inspection APIs.
func builtinsInternalCheckConfig() allowedSymbolsConfig {
	return allowedSymbolsConfig{
		Symbols:   builtinInternalAllowedSymbols,
		TargetDir: "builtins/internal",
		CollectFiles: func(dir string) ([]string, error) {
			return collectSubdirGoFiles(dir, nil, nil)
		},
		ExemptImport: func(importPath string) bool {
			return importPath == "github.com/DataDog/rshell/builtins" ||
				importPath == "golang.org/x/sys/unix" ||
				importPath == "golang.org/x/sys/windows"
		},
		ListName: "builtinInternalAllowedSymbols",
		MinFiles: 1,
	}
}

// TestBuiltinInternalAllowedSymbols enforces symbol-level restrictions on
// OS-specific helper packages under builtins/internal/. Permanently-banned
// packages (unsafe, reflect, os/exec, net, plugin) are still forbidden; only
// the read-only OS-inspection APIs listed in builtinInternalAllowedSymbols
// are permitted.
func TestBuiltinInternalAllowedSymbols(t *testing.T) {
	checkAllowedSymbols(t, builtinsInternalCheckConfig())
}
