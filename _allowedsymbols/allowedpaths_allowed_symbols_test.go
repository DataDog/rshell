// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

import (
	"testing"
)

// TestAllowedPathsAllowedSymbols enforces symbol-level import restrictions on
// non-test Go files in allowedpaths/. Every imported symbol must be explicitly
// listed in allowedpathsAllowedSymbols. Internal module imports
// (github.com/DataDog/rshell/*) are auto-allowed.
func TestAllowedPathsAllowedSymbols(t *testing.T) {
	checkAllowedSymbols(t, allowedSymbolsConfig{
		Symbols:   allowedpathsAllowedSymbols,
		TargetDir: "allowedpaths",
		CollectFiles: func(dir string) ([]string, error) {
			return collectFlatGoFiles(dir)
		},
		ListName: "allowedpathsAllowedSymbols",
		MinFiles: 1,
	})
}
