// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// builtinAllowedSymbols lists every "importpath.Symbol" that may be used by
// files in interp/builtins/cmds/. Each entry must be in "importpath.Symbol"
// form, where importpath is the full Go import path.
//
// To use a new symbol, add a single line here.
//
// Permanently banned (cannot be added):
//   - reflect  — reflection defeats static safety analysis
//   - unsafe   — bypasses Go's type and memory safety guarantees
//
// All packages not listed here are implicitly banned, including third-party
// and other internal module packages.
var builtinAllowedSymbols = []string{
	"context.Context",
	"github.com/DataDog/rshell/interp/builtins.CallContext",
	"github.com/DataDog/rshell/interp/builtins.Register",
	"github.com/DataDog/rshell/interp/builtins.Result",
	"io.Copy",
	"io.NopCloser",
	"io.ReadCloser",
	"os.O_RDONLY",
	"strconv.Atoi",
}

// permanentlyBanned lists packages that may never be imported by builtins,
// regardless of what symbols they export.
var permanentlyBanned = map[string]string{
	"reflect": "reflection defeats static safety analysis",
	"unsafe":  "bypasses Go's type and memory safety guarantees",
}

// TestBuiltinImportAllowlist enforces symbol-level import restrictions on all
// .go files in interp/builtins/cmds/. Every imported package must appear in
// builtinAllowedSymbols, and every pkg.Symbol reference must be explicitly
// listed there.
func TestBuiltinImportAllowlist(t *testing.T) {
	// Build lookup sets from the allowlist.
	allowedSymbols := make(map[string]bool, len(builtinAllowedSymbols))
	allowedPackages := make(map[string]bool)
	for _, entry := range builtinAllowedSymbols {
		dot := strings.LastIndexByte(entry, '.')
		if dot <= 0 {
			t.Fatalf("malformed allowlist entry (no dot): %q", entry)
		}
		allowedSymbols[entry] = true
		allowedPackages[entry[:dot]] = true
	}

	root := repoRoot(t)
	cmdsDir := filepath.Join(root, "interp", "builtins", "cmds")

	entries, err := os.ReadDir(cmdsDir)
	if err != nil {
		t.Fatal(err)
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(cmdsDir, name)
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Errorf("%s: parse error: %v", name, err)
			continue
		}

		// Build a map from local package name → import path and validate each import.
		localToPath := make(map[string]string)
		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)

			if reason, banned := permanentlyBanned[importPath]; banned {
				t.Errorf("%s: import of %q is permanently banned (%s)", name, importPath, reason)
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
				t.Errorf("%s: blank/dot import of %q is not allowed", name, importPath)
				continue
			}

			if !allowedPackages[importPath] {
				t.Errorf("%s: import of %q is not in the allowlist", name, importPath)
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
				t.Errorf("%s:%d: %s is not in the allowlist", name, pos.Line, key)
			}
			return true
		})
	}
}
