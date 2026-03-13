// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allowedSymbolsConfig configures a single run of the allowed-symbols check.
type allowedSymbolsConfig struct {
	// Symbols is the allowlist to enforce (e.g. builtinAllowedSymbols).
	Symbols []string
	// TargetDir is the directory to scan, relative to the repo root.
	TargetDir string
	// CollectFiles walks TargetDir and returns the absolute paths of Go files
	// to check. It receives the absolute path to TargetDir.
	CollectFiles func(dir string) ([]string, error)
	// ExemptImport returns true for import paths that are auto-allowed and
	// should not be checked against the allowlist.
	ExemptImport func(importPath string) bool
	// ListName is used in error messages (e.g. "builtinAllowedSymbols").
	ListName string
	// MinFiles is the minimum number of files expected (sanity check).
	MinFiles int
	// RepoRootOverride, if set, is used instead of auto-detecting the repo
	// root from os.Getwd(). Used by verification tests that operate on a
	// temp copy.
	RepoRootOverride string
	// Errors, if non-nil, collects error messages instead of calling t.Errorf.
	// Used by verification tests to inspect specific errors.
	Errors *[]string
}

// checkAllowedSymbols enforces symbol-level import restrictions on a set of
// Go source files. It verifies that every imported symbol is in the allowlist,
// that no permanently banned packages are imported, and that every symbol in
// the allowlist is actually used.
func checkAllowedSymbols(t *testing.T, cfg allowedSymbolsConfig) {
	t.Helper()

	// Build lookup sets from the allowlist.
	allowedSymbols := make(map[string]bool, len(cfg.Symbols))
	usedSymbols := make(map[string]bool, len(cfg.Symbols))
	allowedPackages := make(map[string]bool)
	for _, entry := range cfg.Symbols {
		dot := strings.LastIndexByte(entry, '.')
		if dot <= 0 {
			t.Fatalf("malformed allowlist entry (no dot): %q", entry)
		}
		allowedSymbols[entry] = true
		allowedPackages[entry[:dot]] = true
	}

	// reportErr collects errors into cfg.Errors when set, otherwise calls t.Errorf.
	reportErr := func(format string, args ...any) {
		msg := fmt.Sprintf(format, args...)
		if cfg.Errors != nil {
			*cfg.Errors = append(*cfg.Errors, msg)
		} else {
			t.Errorf("%s", msg)
		}
	}

	// Determine the repo root.
	var root string
	if cfg.RepoRootOverride != "" {
		root = cfg.RepoRootOverride
	} else {
		// This package lives in _allowedsymbols/, so the repo root is one level up.
		dir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		root = filepath.Dir(dir)
	}
	targetDir := filepath.Join(root, cfg.TargetDir)

	goFiles, err := cfg.CollectFiles(targetDir)
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	checked := 0
	for _, path := range goFiles {
		rel, _ := filepath.Rel(targetDir, path)
		checked++

		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			reportErr("%s: parse error: %v", rel, err)
			continue
		}

		// Build a map from local package name → import path and validate each import.
		localToPath := make(map[string]string)
		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)

			banned := false
			for key, reason := range permanentlyBanned {
				if strings.HasSuffix(key, "/") {
					if strings.HasPrefix(importPath, key) {
						reportErr("%s: import of %q is permanently banned (%s)", rel, importPath, reason)
						banned = true
						break
					}
				} else if importPath == key {
					reportErr("%s: import of %q is permanently banned (%s)", rel, importPath, reason)
					banned = true
					break
				}
			}
			if banned {
				continue
			}

			if cfg.ExemptImport(importPath) {
				continue
			}

			// Determine the local name used to reference this package.
			var localName string
			if imp.Name != nil {
				localName = imp.Name.Name
			} else {
				parts := strings.Split(importPath, "/")
				localName = parts[len(parts)-1]
			}

			if localName == "_" || localName == "." {
				reportErr("%s: blank/dot import of %q is not allowed", rel, importPath)
				continue
			}

			if !allowedPackages[importPath] {
				reportErr("%s: import of %q is not in the allowlist", rel, importPath)
				continue
			}

			localToPath[localName] = importPath
		}

		// Walk all selector expressions and verify each pkg.Symbol is allowed.
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			importPath, ok := localToPath[ident.Name]
			if !ok {
				return true // not a package-level selector
			}
			key := importPath + "." + sel.Sel.Name
			if !allowedSymbols[key] {
				pos := fset.Position(sel.Pos())
				reportErr("%s:%d: %s is not in the allowlist", rel, pos.Line, key)
			} else {
				usedSymbols[key] = true
			}
			return true
		})
	}
	if checked < cfg.MinFiles {
		t.Fatalf("expected at least %d files in %s, found %d", cfg.MinFiles, cfg.TargetDir, checked)
	}

	// Verify every symbol in the allowlist is actually used by at least one
	// file. Unused entries should be removed to keep the allowlist minimal.
	for _, entry := range cfg.Symbols {
		if !usedSymbols[entry] {
			reportErr("allowlist symbol %q is not used by any file in %s — remove it from %s", entry, cfg.TargetDir, cfg.ListName)
		}
	}
}

// collectSubdirGoFiles walks a directory tree and returns all non-test .go
// files that are inside subdirectories (not at the top level). Optionally
// skips directories by name.
func collectSubdirGoFiles(dir string, skipDirs map[string]bool, skipTopLevel func(rel string) bool) ([]string, error) {
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") || strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		if skipTopLevel != nil && skipTopLevel(rel) {
			return nil
		}
		// Only check files inside subdirectories.
		if !strings.Contains(rel, string(filepath.Separator)) {
			return nil
		}
		files = append(files, path)
		return nil
	})
	return files, err
}

// collectFlatGoFiles returns all non-test .go files directly in dir (not
// in subdirectories).
func collectFlatGoFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, e.Name()))
	}
	return files, nil
}
