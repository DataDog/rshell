// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

import (
	"strings"
	"testing"
)

// timecompCheckConfig returns the allowedSymbolsConfig used to enforce
// symbol-level import restrictions on timecomp/. Verification tests reuse
// this function to ensure they test the exact same configuration.
func timecompCheckConfig() allowedSymbolsConfig {
	return allowedSymbolsConfig{
		Symbols:   timecompAllowedSymbols,
		TargetDir: "timecomp",
		CollectFiles: func(dir string) ([]string, error) {
			return collectFlatGoFiles(dir)
		},
		ExemptImport: func(importPath string) bool {
			return strings.HasPrefix(importPath, "github.com/DataDog/rshell/")
		},
		ListName: "timecompAllowedSymbols",
		MinFiles: 1,
	}
}

// TestTimecompAllowedSymbols enforces symbol-level import restrictions on
// non-test Go files in timecomp/. Every imported symbol must be explicitly
// listed in timecompAllowedSymbols. Internal module imports
// (github.com/DataDog/rshell/*) are auto-allowed.
func TestTimecompAllowedSymbols(t *testing.T) {
	checkAllowedSymbols(t, timecompCheckConfig())
}
