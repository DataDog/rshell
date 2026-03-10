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
// command implementation files in interp/builtins/. Each entry must be in
// "importpath.Symbol" form, where importpath is the full Go import path.
//
// Each symbol must have a comment explaining what it does and why it is safe
// to use inside a sandboxed builtin (e.g. pure function, constant, interface,
// no filesystem/network/exec side effects).
//
// To use a new symbol, add a single line here with its safety justification.
//
// Permanently banned (cannot be added):
//   - reflect  — reflection defeats static safety analysis
//   - unsafe   — bypasses Go's type and memory safety guarantees
//
// All packages not listed here are implicitly banned, including all
// third-party packages and other internal module packages.
var builtinAllowedSymbols = []string{
	// bufio.NewScanner — line-by-line input reading (e.g. head, cat); no write or exec capability.
	"bufio.NewScanner",
	// context.Context — deadline/cancellation plumbing; pure interface, no side effects.
	"context.Context",
	// errors.Is — error comparison; pure function, no I/O.
	"errors.Is",
	// errors.New — creates a simple error value; no I/O or side effects.
	"errors.New",
	// io.Copy — stream data between reader and writer; builtins receive sandboxed streams.
	"io.Copy",
	// io.EOF — sentinel error value; pure constant.
	"io.EOF",
	// io.NopCloser — wraps a Reader with a no-op Close; no side effects.
	"io.NopCloser",
	// io.ReadCloser — interface type; no side effects.
	"io.ReadCloser",
	// io.Reader — interface type; no side effects.
	"io.Reader",
	// os.FileInfo — file metadata interface returned by Stat; no I/O side effects.
	"os.FileInfo",
	// os.O_RDONLY — read-only file flag constant; cannot open files by itself.
	"os.O_RDONLY",
	// regexp.Compile — compiles a regular expression; pure function, no I/O. Uses RE2 engine (linear-time, no backtracking).
	"regexp.Compile",
	// regexp.QuoteMeta — escapes all special regex characters in a string; pure function, no I/O.
	"regexp.QuoteMeta",
	// regexp.Regexp — compiled regular expression type; no I/O side effects. All matching methods are linear-time (RE2).
	"regexp.Regexp",
	// strings.Builder — efficient string concatenation; pure in-memory buffer, no I/O.
	"strings.Builder",
	// strings.Join — concatenates a slice of strings with a separator; pure function, no I/O.
	"strings.Join",
	// strconv.Atoi — string-to-int conversion; pure function, no I/O.
	"strconv.Atoi",
	// strconv.Itoa — int-to-string conversion; pure function, no I/O.
	"strconv.Itoa",
	// strconv.ParseInt — string-to-int conversion with base/bit-size; pure function, no I/O.
	"strconv.ParseInt",
	// strconv.FormatInt — int-to-string conversion; pure function, no I/O.
	"strconv.FormatInt",
	// unicode.Cc — control character category range table; pure data, no I/O.
	"unicode.Cc",
	// unicode.Cf — format character category range table; pure data, no I/O.
	"unicode.Cf",
	// unicode.Is — checks if rune belongs to a range table; pure function, no I/O.
	"unicode.Is",
	// unicode.Me — enclosing mark category range table; pure data, no I/O.
	"unicode.Me",
	// unicode.Mn — nonspacing mark category range table; pure data, no I/O.
	"unicode.Mn",
	// unicode.Range16 — struct type for 16-bit Unicode ranges; pure data.
	"unicode.Range16",
	// unicode.Range32 — struct type for 32-bit Unicode ranges; pure data.
	"unicode.Range32",
	// unicode.RangeTable — struct type for Unicode range tables; pure data.
	"unicode.RangeTable",
	// unicode/utf8.DecodeRune — decodes first UTF-8 rune from a byte slice; pure function, no I/O.
	"unicode/utf8.DecodeRune",
	// unicode/utf8.RuneCount — counts UTF-8 runes in a byte slice; pure function, no I/O.
	"unicode/utf8.RuneCount",
	// unicode/utf8.UTFMax — maximum number of bytes in a UTF-8 encoding; constant, no I/O.
	"unicode/utf8.UTFMax",
	// unicode/utf8.Valid — checks if a byte slice is valid UTF-8; pure function, no I/O.
	"unicode/utf8.Valid",
}

// permanentlyBanned lists packages that may never be imported by builtin
// command implementations, regardless of what symbols they export.
var permanentlyBanned = map[string]string{
	"reflect": "reflection defeats static safety analysis",
	"unsafe":  "bypasses Go's type and memory safety guarantees",
}

// TestBuiltinImportAllowlist enforces symbol-level import restrictions on
// command implementation files in interp/builtins/. builtins.go is exempt as
// the package framework. Every other file's imports and pkg.Symbol references
// must be explicitly listed in builtinAllowedSymbols.
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
	builtinsDir := filepath.Join(root, "interp", "builtins")

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
			if importPath == "github.com/DataDog/rshell/interp/builtins" ||
				strings.HasPrefix(importPath, "github.com/DataDog/rshell/interp/builtins/internal/") {
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
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no command implementation files found in interp/builtins/ sub-packages")
	}
}
