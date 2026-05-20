// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Tripwire tests added by vuln-hunt campaign 2026-05-20-gpt-5.5-cyber-3 /
// builtin-import-allowlist.

package analysis

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestVulnHuntSubsystemBuiltinImportAllowlist_AliasedUnlistedSymbolRejected(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	writeGoFile(t,
		filepath.Join(tmp, "builtins", "echo", "alias_symbol_rejected.go"),
		"echo",
		[]string{`o "os"`},
		"var _ = o.Setenv\n",
	)

	var globalErrs []string
	checkAllowedSymbols(t, builtinsVerifyCfg(tmp, &globalErrs))
	if !errContains(globalErrs, "os.Setenv") || !errContains(globalErrs, "not in the allowlist") {
		t.Fatalf("expected aliased os.Setenv to be rejected by global builtin allowlist, got: %v", globalErrs)
	}

	var perCmdErrs []string
	cfg := builtinsPerCmdVerifyCfg(tmp, &perCmdErrs)
	checkPerBuiltinAllowedSymbols(t, cfg)
	if !errContains(perCmdErrs, "os") || !errContains(perCmdErrs, "not in the allowlist") {
		t.Fatalf("expected aliased os.Setenv to be rejected by per-command allowlist, got: %v", perCmdErrs)
	}
}

func TestVulnHuntSubsystemBuiltinImportAllowlist_DotImportRejected(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	writeGoFile(t,
		filepath.Join(tmp, "builtins", "echo", "dot_import_rejected.go"),
		"echo",
		[]string{`. "fmt"`},
		"var _ = Sprintf\n",
	)

	var errs []string
	checkAllowedSymbols(t, builtinsVerifyCfg(tmp, &errs))
	if !errContains(errs, "blank/dot import") {
		t.Fatalf("expected dot import to be rejected, got: %v", errs)
	}
}

func TestVulnHuntSubsystemBuiltinImportAllowlist_ParseErrorsFailClosed(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	badPath := filepath.Join(tmp, "builtins", "echo", "parse_error.go")
	if err := os.WriteFile(badPath, []byte("package echo\nfunc broken(\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var errs []string
	checkAllowedSymbols(t, builtinsVerifyCfg(tmp, &errs))
	if !errContains(errs, "parse error") || !errContains(errs, "parse_error.go") {
		t.Fatalf("expected parse error to fail closed, got: %v", errs)
	}
}

func TestVulnHuntSubsystemBuiltinImportAllowlist_PlatformSpecificInternalFilesChecked(t *testing.T) {
	root := repoRoot(t)
	cfg := internalCheckConfig()
	files, err := cfg.CollectFiles(filepath.Join(root, "builtins", "internal"))
	if err != nil {
		t.Fatal(err)
	}

	seen := make(map[string]bool, len(files))
	for _, path := range files {
		rel, err := filepath.Rel(filepath.Join(root, "builtins", "internal"), path)
		if err != nil {
			t.Fatal(err)
		}
		seen[filepath.ToSlash(rel)] = true
	}

	required := []string{
		"diskstats/diskstats_darwin.go",
		"procnetsocket/procnetsocket_linux.go",
		"winnet/winnet_windows.go",
		"winpoll/winpoll_windows.go",
	}
	for _, rel := range required {
		if !seen[rel] {
			t.Fatalf("platform-specific internal file %s was not collected; got %s", rel, strings.Join(mapKeys(seen), ", "))
		}
	}
}

func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
