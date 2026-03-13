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

func builtinsCfg(tempRoot string, errs *[]string) allowedSymbolsConfig {
	return allowedSymbolsConfig{
		Symbols:   builtinAllowedSymbols,
		TargetDir: "builtins",
		CollectFiles: func(dir string) ([]string, error) {
			return collectSubdirGoFiles(dir, map[string]bool{"testutil": true}, func(rel string) bool {
				return rel == "builtins.go"
			})
		},
		ExemptImport: func(importPath string) bool {
			return importPath == "github.com/DataDog/rshell/builtins" ||
				strings.HasPrefix(importPath, "github.com/DataDog/rshell/builtins/internal/")
		},
		ListName:         "builtinAllowedSymbols",
		MinFiles:         1,
		RepoRootOverride: tempRoot,
		Errors:           errs,
	}
}

func TestVerificationBuiltinsCleanPass(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	var errs []string
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

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
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

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
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

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
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

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
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

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
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

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
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

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
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

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
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

	if errContains(errs, "os/exec") {
		t.Errorf("testutil/ should be skipped, but got error: %v", errs)
	}
}
