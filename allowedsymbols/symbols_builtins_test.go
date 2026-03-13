// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

import (
	"strings"
	"testing"
)

// builtinsCheckConfig returns the allowedSymbolsConfig used to enforce
// symbol-level import restrictions on builtins/. Verification tests reuse
// this function to ensure they test the exact same configuration.
func builtinsCheckConfig() allowedSymbolsConfig {
	return allowedSymbolsConfig{
		Symbols:   builtinAllowedSymbols,
		TargetDir: "builtins",
		CollectFiles: func(dir string) ([]string, error) {
			return collectSubdirGoFiles(dir, map[string]bool{"testutil": true}, func(rel string) bool {
				// builtins.go is the package framework and is exempt.
				return rel == "builtins.go"
			})
		},
		ExemptImport: func(importPath string) bool {
			return importPath == "github.com/DataDog/rshell/builtins" ||
				strings.HasPrefix(importPath, "github.com/DataDog/rshell/builtins/internal/")
		},
		ListName: "builtinAllowedSymbols",
		MinFiles: 1,
	}
}

// TestBuiltinAllowedSymbols enforces symbol-level import restrictions on
// command implementation files in builtins/. builtins.go is exempt as
// the package framework. Every other file's imports and pkg.Symbol references
// must be explicitly listed in builtinAllowedSymbols.
func TestBuiltinAllowedSymbols(t *testing.T) {
	checkAllowedSymbols(t, builtinsCheckConfig())
}
