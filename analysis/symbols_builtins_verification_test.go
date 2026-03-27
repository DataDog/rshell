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

// builtinsVerifyCfg returns a builtinsCheckConfig with RepoRootOverride and
// Errors set for verification testing.
func builtinsVerifyCfg(tempRoot string, errs *[]string) allowedSymbolsConfig {
	cfg := builtinsCheckConfig()
	cfg.RepoRootOverride = tempRoot
	cfg.Errors = errs
	return cfg
}

// builtinsPerCmdVerifyCfg returns a perBuiltinConfig with overrides for
// verification testing.
func builtinsPerCmdVerifyCfg(tempRoot string, errs *[]string) perBuiltinConfig {
	cfg := builtinsPerCommandCheckConfig()
	cfg.RepoRootOverride = tempRoot
	cfg.Errors = errs
	return cfg
}

func TestVerificationBuiltinsCleanPass(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	var errs []string
	checkAllowedSymbols(t, builtinsVerifyCfg(tmp, &errs))

	if len(errs) > 0 {
		t.Errorf("expected no errors on clean copy, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestVerificationBuiltinsBannedPackageExact(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	target := findFirstSubdirGoFile(t, filepath.Join(tmp, "builtins"))
	injectImport(t, target, `"os/exec"`, "var _ = exec.Command")

	var errs []string
	checkAllowedSymbols(t, builtinsVerifyCfg(tmp, &errs))

	if !errContains(errs, "permanently banned") {
		t.Errorf("expected 'permanently banned' error for os/exec, got: %v", errs)
	}
}

func TestVerificationBuiltinsBannedPackagePrefix(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	target := findFirstSubdirGoFile(t, filepath.Join(tmp, "builtins"))
	injectImport(t, target, `"net/http"`, "var _ = http.Get")

	var errs []string
	checkAllowedSymbols(t, builtinsVerifyCfg(tmp, &errs))

	if !errContains(errs, "permanently banned") {
		t.Errorf("expected 'permanently banned' error for net/http, got: %v", errs)
	}
}

func TestVerificationBuiltinsUnlistedSymbol(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	target := findFirstSubdirGoFile(t, filepath.Join(tmp, "builtins"))
	injectUnlistedSymbol(t, target)

	var errs []string
	checkAllowedSymbols(t, builtinsVerifyCfg(tmp, &errs))

	if !errContains(errs, "os.Setenv") || !errContains(errs, "not in the allowlist") {
		t.Errorf("expected 'not in the allowlist' error for os.Setenv, got: %v", errs)
	}
}

func TestVerificationBuiltinsUnlistedPackage(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	target := findFirstSubdirGoFile(t, filepath.Join(tmp, "builtins"))
	injectImport(t, target, `"crypto/rand"`, "var _ = rand.Read")

	var errs []string
	checkAllowedSymbols(t, builtinsVerifyCfg(tmp, &errs))

	if !errContains(errs, "not in the allowlist") {
		t.Errorf("expected 'not in the allowlist' error for crypto/rand, got: %v", errs)
	}
}

func TestVerificationBuiltinsBlankImport(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	target := findFirstSubdirGoFile(t, filepath.Join(tmp, "builtins"))
	injectImport(t, target, `_ "encoding/json"`, "")

	var errs []string
	checkAllowedSymbols(t, builtinsVerifyCfg(tmp, &errs))

	if !errContains(errs, "blank/dot import") {
		t.Errorf("expected 'blank/dot import' error, got: %v", errs)
	}
}

func TestVerificationBuiltinsExemptImport(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	target := findFirstSubdirGoFile(t, filepath.Join(tmp, "builtins"))
	// builtins/internal/* imports are exempt — should not trigger an error.
	injectImport(t, target, `internalfoo "github.com/DataDog/rshell/builtins/internal/fakepkg"`, "var _ = internalfoo.Foo")

	var errs []string
	checkAllowedSymbols(t, builtinsVerifyCfg(tmp, &errs))

	if errContains(errs, "github.com/DataDog/rshell/builtins/internal/fakepkg") {
		t.Errorf("exempt import should not be flagged, got: %v", errs)
	}
}

func TestVerificationBuiltinsSkipsTopLevel(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	// Inject a banned import into builtins.go (which is skipped by skipTopLevel).
	target := filepath.Join(tmp, "builtins", "builtins.go")
	injectImport(t, target, `"os/exec"`, "var _ = exec.Command")

	var errs []string
	checkAllowedSymbols(t, builtinsVerifyCfg(tmp, &errs))

	if errContains(errs, "os/exec") {
		t.Errorf("builtins.go should be skipped, but got error: %v", errs)
	}
}

func TestVerificationBuiltinsSkipsTestutilDir(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	// Create a violating file inside testutil/ (which is skipped by skipDirs).
	writeGoFile(t,
		filepath.Join(tmp, "builtins", "testutil", "bad.go"),
		"testutil",
		[]string{`"os/exec"`},
		"var _ = exec.Command\n",
	)

	var errs []string
	checkAllowedSymbols(t, builtinsVerifyCfg(tmp, &errs))

	if errContains(errs, "os/exec") {
		t.Errorf("testutil/ should be skipped, but got error: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// Per-command verification tests
// ---------------------------------------------------------------------------

func TestVerificationPerCmdCleanPass(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	var errs []string
	checkPerBuiltinAllowedSymbols(t, builtinsPerCmdVerifyCfg(tmp, &errs))

	if len(errs) > 0 {
		t.Errorf("expected no errors on clean copy, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestVerificationPerCmdSymbolNotInCommonList(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	// Override the per-command config to inject a symbol not in the common list.
	cfg := builtinsPerCmdVerifyCfg(tmp, nil)
	cfg.PerCommandSymbols = copyPerCommandMap(cfg.PerCommandSymbols)
	cfg.PerCommandSymbols["echo"] = append(cfg.PerCommandSymbols["echo"], "os.Remove")

	var errs []string
	cfg.Errors = &errs
	checkPerBuiltinAllowedSymbols(t, cfg)

	if !errContains(errs, "os.Remove") || !errContains(errs, "not in builtinAllowedSymbols") {
		t.Errorf("expected error about os.Remove not in common list, got: %v", errs)
	}
}

func TestVerificationPerCmdSymbolNotInPerCommandList(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	// Find a builtin that uses fmt.Sprintf (e.g. "ls") and remove it from its per-command list.
	cfg := builtinsPerCmdVerifyCfg(tmp, nil)
	cfg.PerCommandSymbols = copyPerCommandMap(cfg.PerCommandSymbols)
	// Remove "fmt.Sprintf" from ls's list.
	filtered := make([]string, 0, len(cfg.PerCommandSymbols["ls"]))
	for _, s := range cfg.PerCommandSymbols["ls"] {
		if s != "fmt.Sprintf" {
			filtered = append(filtered, s)
		}
	}
	cfg.PerCommandSymbols["ls"] = filtered

	var errs []string
	cfg.Errors = &errs
	checkPerBuiltinAllowedSymbols(t, cfg)

	// When fmt.Sprintf is the only fmt symbol, removing it makes the entire
	// fmt package unlisted, so the error may mention the package or the symbol.
	if !errContains(errs, "fmt") || !errContains(errs, "not in the allowlist") {
		t.Errorf("expected error about fmt not allowed for ls, got: %v", errs)
	}
}

func TestVerificationPerCmdUnusedSymbolFlagged(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	// Add an unused (but common-list-valid) symbol to echo's per-command list.
	cfg := builtinsPerCmdVerifyCfg(tmp, nil)
	cfg.PerCommandSymbols = copyPerCommandMap(cfg.PerCommandSymbols)
	cfg.PerCommandSymbols["echo"] = append(cfg.PerCommandSymbols["echo"], "regexp.Compile")

	var errs []string
	cfg.Errors = &errs
	checkPerBuiltinAllowedSymbols(t, cfg)

	if !errContains(errs, "regexp.Compile") || !errContains(errs, "not used") {
		t.Errorf("expected error about unused regexp.Compile in echo, got: %v", errs)
	}
}

func TestVerificationPerCmdMissingBuiltinEntry(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	// Remove "echo" from the per-command map.
	cfg := builtinsPerCmdVerifyCfg(tmp, nil)
	cfg.PerCommandSymbols = copyPerCommandMap(cfg.PerCommandSymbols)
	delete(cfg.PerCommandSymbols, "echo")

	var errs []string
	cfg.Errors = &errs
	checkPerBuiltinAllowedSymbols(t, cfg)

	if !errContains(errs, "echo") || !errContains(errs, "no entry in builtinPerCommandSymbols") {
		t.Errorf("expected error about missing echo entry, got: %v", errs)
	}
}

// copyPerCommandMap returns a shallow copy of a per-command symbols map so
// that verification tests can mutate it without affecting the original.
func copyPerCommandMap(m map[string][]string) map[string][]string {
	cp := make(map[string][]string, len(m))
	for k, v := range m {
		dup := make([]string, len(v))
		copy(dup, v)
		cp[k] = dup
	}
	return cp
}
