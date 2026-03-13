// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

import (
	"testing"
)

// TestInterpAllowedSymbols enforces symbol-level import restrictions on
// non-test Go files in interp/. Every imported symbol must be explicitly
// listed in interpAllowedSymbols. Internal module imports
// (github.com/DataDog/rshell/*) are auto-allowed.
func TestInterpAllowedSymbols(t *testing.T) {
	checkAllowedSymbols(t, allowedSymbolsConfig{
		Symbols:   interpAllowedSymbols,
		TargetDir: "interp",
		CollectFiles: func(dir string) ([]string, error) {
			return collectFlatGoFiles(dir)
		},
		ListName: "interpAllowedSymbols",
		MinFiles: 1,
	})
}
