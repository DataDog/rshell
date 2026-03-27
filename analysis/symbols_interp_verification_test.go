// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package analysis

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

// ---------------------------------------------------------------------------
// Per-internal-package verification tests
// ---------------------------------------------------------------------------

// internalPerPkgVerifyCfg returns a perBuiltinConfig with overrides for
// verification testing of builtins/internal/ packages.
func internalPerPkgVerifyCfg(tempRoot string, errs *[]string) perBuiltinConfig {
	cfg := internalPerPackageCheckConfig()
	cfg.RepoRootOverride = tempRoot
	cfg.Errors = errs
	return cfg
}

func TestVerificationInternalPerPkgCleanPass(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins", "internal"), filepath.Join(tmp, "builtins", "internal"))

	var errs []string
	checkPerBuiltinAllowedSymbols(t, internalPerPkgVerifyCfg(tmp, &errs))

	if len(errs) > 0 {
		t.Errorf("expected no errors on clean copy, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestVerificationInternalPerPkgSymbolNotInCommonList(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins", "internal"), filepath.Join(tmp, "builtins", "internal"))

	// Override the per-package config to inject a symbol not in the common list.
	cfg := internalPerPkgVerifyCfg(tmp, nil)
	cfg.PerCommandSymbols = copyPerCommandMap(cfg.PerCommandSymbols)
	cfg.PerCommandSymbols["loopctl"] = append(cfg.PerCommandSymbols["loopctl"], "os.Remove")

	var errs []string
	cfg.Errors = &errs
	checkPerBuiltinAllowedSymbols(t, cfg)

	if !errContains(errs, "os.Remove") || !errContains(errs, "not in builtinAllowedSymbols") {
		t.Errorf("expected error about os.Remove not in common list, got: %v", errs)
	}
}

func TestVerificationInternalPerPkgSymbolNotInPerPackageList(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins", "internal"), filepath.Join(tmp, "builtins", "internal"))

	// Remove "strconv.Atoi" from loopctl's per-package list — loopctl uses it.
	cfg := internalPerPkgVerifyCfg(tmp, nil)
	cfg.PerCommandSymbols = copyPerCommandMap(cfg.PerCommandSymbols)
	filtered := make([]string, 0, len(cfg.PerCommandSymbols["loopctl"]))
	for _, s := range cfg.PerCommandSymbols["loopctl"] {
		if s != "strconv.Atoi" {
			filtered = append(filtered, s)
		}
	}
	cfg.PerCommandSymbols["loopctl"] = filtered

	var errs []string
	cfg.Errors = &errs
	checkPerBuiltinAllowedSymbols(t, cfg)

	if !errContains(errs, "strconv") || !errContains(errs, "not in the allowlist") {
		t.Errorf("expected error about strconv not allowed for loopctl, got: %v", errs)
	}
}

func TestVerificationInternalPerPkgUnusedSymbolFlagged(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins", "internal"), filepath.Join(tmp, "builtins", "internal"))

	// Add an unused (but common-list-valid) symbol to loopctl's per-package list.
	cfg := internalPerPkgVerifyCfg(tmp, nil)
	cfg.PerCommandSymbols = copyPerCommandMap(cfg.PerCommandSymbols)
	cfg.PerCommandSymbols["loopctl"] = append(cfg.PerCommandSymbols["loopctl"], "fmt.Sprintf")

	var errs []string
	cfg.Errors = &errs
	checkPerBuiltinAllowedSymbols(t, cfg)

	if !errContains(errs, "fmt.Sprintf") || !errContains(errs, "not used") {
		t.Errorf("expected error about unused fmt.Sprintf in loopctl, got: %v", errs)
	}
}

func TestVerificationInternalPerPkgMissingPackageEntry(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins", "internal"), filepath.Join(tmp, "builtins", "internal"))

	// Remove "loopctl" from the per-package map.
	cfg := internalPerPkgVerifyCfg(tmp, nil)
	cfg.PerCommandSymbols = copyPerCommandMap(cfg.PerCommandSymbols)
	delete(cfg.PerCommandSymbols, "loopctl")

	var errs []string
	cfg.Errors = &errs
	checkPerBuiltinAllowedSymbols(t, cfg)

	if !errContains(errs, "loopctl") || !errContains(errs, "no entry in builtinPerCommandSymbols") {
		t.Errorf("expected error about missing loopctl entry, got: %v", errs)
	}
}
