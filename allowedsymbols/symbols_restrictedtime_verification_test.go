// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

import (
	"path/filepath"
	"strings"
	"testing"
)

// restrictedtimeVerifyCfg returns a restrictedtimeCheckConfig with RepoRootOverride and
// Errors set for verification testing.
func restrictedtimeVerifyCfg(tempRoot string, errs *[]string) allowedSymbolsConfig {
	cfg := restrictedtimeCheckConfig()
	cfg.RepoRootOverride = tempRoot
	cfg.Errors = errs
	return cfg
}

func TestVerificationTimecompCleanPass(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "restrictedtime"), filepath.Join(tmp, "restrictedtime"))

	var errs []string
	checkAllowedSymbols(t, restrictedtimeVerifyCfg(tmp, &errs))

	if len(errs) > 0 {
		t.Errorf("expected no errors on clean copy, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestVerificationTimecompUnlistedSymbol(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "restrictedtime"), filepath.Join(tmp, "restrictedtime"))

	target := findFirstFlatGoFile(t, filepath.Join(tmp, "restrictedtime"))
	injectUnlistedSymbol(t, target)

	var errs []string
	checkAllowedSymbols(t, restrictedtimeVerifyCfg(tmp, &errs))

	if !errContains(errs, "os") || !errContains(errs, "not in the allowlist") {
		t.Errorf("expected 'not in the allowlist' error for os import, got: %v", errs)
	}
}

func TestVerificationTimecompExemptImport(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "restrictedtime"), filepath.Join(tmp, "restrictedtime"))

	target := findFirstFlatGoFile(t, filepath.Join(tmp, "restrictedtime"))
	// Internal module imports (github.com/DataDog/rshell/*) are exempt.
	injectImport(t, target, `fakepkg "github.com/DataDog/rshell/fakepkg"`, "var _ = fakepkg.Foo")

	var errs []string
	checkAllowedSymbols(t, restrictedtimeVerifyCfg(tmp, &errs))

	if errContains(errs, "github.com/DataDog/rshell/fakepkg") {
		t.Errorf("exempt import should not be flagged, got: %v", errs)
	}
}
