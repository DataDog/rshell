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

// interpVerifyCfg returns an interpCheckConfig with RepoRootOverride and
// Errors set for verification testing.
func interpVerifyCfg(tempRoot string, errs *[]string) allowedSymbolsConfig {
	cfg := interpCheckConfig()
	cfg.RepoRootOverride = tempRoot
	cfg.Errors = errs
	return cfg
}

func TestVerificationInterpCleanPass(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "interp"), filepath.Join(tmp, "interp"))

	var errs []string
	checkAllowedSymbols(t, interpVerifyCfg(tmp, &errs))

	if len(errs) > 0 {
		t.Errorf("expected no errors on clean copy, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestVerificationInterpUnlistedSymbol(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "interp"), filepath.Join(tmp, "interp"))

	target := findFirstFlatGoFile(t, filepath.Join(tmp, "interp"))
	injectUnlistedSymbol(t, target)

	var errs []string
	checkAllowedSymbols(t, interpVerifyCfg(tmp, &errs))

	if !errContains(errs, "os.Setenv") || !errContains(errs, "not in the allowlist") {
		t.Errorf("expected 'not in the allowlist' error for os.Setenv, got: %v", errs)
	}
}

func TestVerificationInterpExemptImport(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "interp"), filepath.Join(tmp, "interp"))

	target := findFirstFlatGoFile(t, filepath.Join(tmp, "interp"))
	// Internal module imports (github.com/DataDog/rshell/*) are exempt.
	injectImport(t, target, `fakepkg "github.com/DataDog/rshell/fakepkg"`, "var _ = fakepkg.Foo")

	var errs []string
	checkAllowedSymbols(t, interpVerifyCfg(tmp, &errs))

	if errContains(errs, "github.com/DataDog/rshell/fakepkg") {
		t.Errorf("exempt import should not be flagged, got: %v", errs)
	}
}

func TestVerificationInterpIgnoresSubdirs(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "interp"), filepath.Join(tmp, "interp"))

	// collectFlatGoFiles only checks top-level files. A violating file in a
	// subdirectory should be ignored.
	writeGoFile(t,
		filepath.Join(tmp, "interp", "subdir", "bad.go"),
		"subdir",
		[]string{`"os/exec"`},
		"var _ = exec.Command\n",
	)

	var errs []string
	checkAllowedSymbols(t, interpVerifyCfg(tmp, &errs))

	if errContains(errs, "os/exec") {
		t.Errorf("subdirectory files should be ignored by collectFlatGoFiles, but got error: %v", errs)
	}
}
