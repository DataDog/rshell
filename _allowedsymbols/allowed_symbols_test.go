// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuiltinAllowedSymbols enforces symbol-level import restrictions on
// command implementation files in builtins/. builtins.go is exempt as
// the package framework. Every other file's imports and pkg.Symbol references
// must be explicitly listed in builtinAllowedSymbols.
func TestBuiltinAllowedSymbols(t *testing.T) {
	// Build lookup sets from the allowlist.
	allowedSymbols := make(map[string]bool, len(builtinAllowedSymbols))
	usedSymbols := make(map[string]bool, len(builtinAllowedSymbols))
	allowedPackages := make(map[string]bool)
	for _, entry := range builtinAllowedSymbols {
		dot := strings.LastIndexByte(entry, '.')
		if dot <= 0 {
			t.Fatalf("malformed allowlist entry (no dot): %q", entry)
		}
		allowedSymbols[entry] = true
		allowedPackages[entry[:dot]] = true
	}

	// This package lives in allowedsymbols/, so the repo root is one level up.
	dir, err2 := os.Getwd()
	if err2 != nil {
		t.Fatal(err2)
	}
	root := filepath.Dir(dir)
	builtinsDir := filepath.Join(root, "builtins")

	// Collect all .go files in builtin sub-packages (each builtin lives
	// in its own subdirectory, e.g. cat/cat.go, head/head.go). Internal
	// shared packages (internal/) are also checked.
	var goFiles []string
	err := filepath.Walk(builtinsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// testutil/ is a test-only helper package, not a command implementation.
			if info.Name() == "testutil" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") || strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(builtinsDir, path)
		// builtins.go is the package framework (CallContext, Result, Register,
		// Lookup) and is exempt. Only command implementation files are checked.
		if rel == "builtins.go" {
			return nil
		}
		// Only check files inside subdirectories (the per-builtin packages).
		if !strings.Contains(rel, string(filepath.Separator)) {
			return nil
		}
		goFiles = append(goFiles, path)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	checked := 0
	for _, path := range goFiles {
		rel, _ := filepath.Rel(builtinsDir, path)
		checked++

		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Errorf("%s: parse error: %v", rel, err)
			continue
		}

		// Build a map from local package name → import path and validate each import.
		localToPath := make(map[string]string)
		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)

			if reason, banned := permanentlyBanned[importPath]; banned {
				t.Errorf("%s: import of %q is permanently banned (%s)", rel, importPath, reason)
				continue
			}

			// The parent builtins package and sibling internal packages are
			// always allowed — they are part of the builtins module.
			if importPath == "github.com/DataDog/rshell/builtins" ||
				strings.HasPrefix(importPath, "github.com/DataDog/rshell/builtins/internal/") {
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
				t.Errorf("%s: blank/dot import of %q is not allowed", rel, importPath)
				continue
			}

			if !allowedPackages[importPath] {
				t.Errorf("%s: import of %q is not in the allowlist", rel, importPath)
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
				t.Errorf("%s:%d: %s is not in the allowlist", rel, pos.Line, key)
			} else {
				usedSymbols[key] = true
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no command implementation files found in builtins/ sub-packages")
	}

	// Verify every symbol in the allowlist is actually used by at least one
	// builtin. Unused entries should be removed to keep the allowlist minimal.
	for _, entry := range builtinAllowedSymbols {
		if !usedSymbols[entry] {
			t.Errorf("allowlist symbol %q is not used by any builtin — remove it from builtinAllowedSymbols", entry)
		}
	}
}
