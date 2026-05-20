// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Tripwire tests added by vuln-hunt campaign 2026-05-20-gpt-5.5-cyber-3 /
// callctx-openfile.

package analysis

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestVulnHuntSubsystemCallCtxOpenFile_AllOpenFileCallsReadOnly(t *testing.T) {
	walkProductionBuiltins(t, func(path string, fset *token.FileSet, file *ast.File) {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "OpenFile" {
				return true
			}
			if receiverName(sel.X) == "os" {
				return true
			}
			if len(call.Args) < 3 {
				t.Errorf("%s: OpenFile call has fewer than 3 arguments", fset.Position(call.Pos()))
				return true
			}
			if !isOSReadonly(call.Args[2]) {
				t.Errorf("%s: builtin OpenFile capability must be called with os.O_RDONLY, got %s",
					fset.Position(call.Args[2].Pos()), exprString(call.Args[2]))
			}
			return true
		})
	})
}

func TestVulnHuntSubsystemCallCtxOpenFile_OpenFileResultsClosed(t *testing.T) {
	walkProductionBuiltins(t, func(path string, fset *token.FileSet, file *ast.File) {
		rel := productionBuiltinRelPath(t, path)
		reporter := fileLineReporter(fset, rel, func(format string, args ...any) {
			t.Errorf(format, args...)
		})
		checkFileOpenFileClose(file, reporter)
	})
}

func TestVulnHuntSubsystemCallCtxOpenFile_DirectFileAPIsAreAllowlisted(t *testing.T) {
	directFileAPIs := map[string]bool{
		"Open":     true,
		"OpenFile": true,
		"ReadDir":  true,
		"ReadFile": true,
		"Readlink": true,
		"Stat":     true,
		"Lstat":    true,
	}
	allowedDirectFileAPIs := map[string]map[string]bool{
		"builtins/internal/diskstats/diskstats_linux.go": {
			"Open": true,
		},
		"builtins/internal/procinfo/procinfo_linux.go": {
			"Open": true, "ReadDir": true, "ReadFile": true, "Stat": true,
		},
		"builtins/internal/procnetroute/procnetroute_linux.go": {
			"Open": true,
		},
		"builtins/internal/procnetsocket/procnetsocket_linux.go": {
			"Open": true,
		},
		"builtins/internal/procsyskernel/procsyskernel.go": {
			"OpenFile": true,
		},
	}

	walkProductionBuiltins(t, func(path string, fset *token.FileSet, file *ast.File) {
		rel := productionBuiltinRelPath(t, path)
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || receiverName(sel.X) != "os" || !directFileAPIs[sel.Sel.Name] {
				return true
			}
			if !allowedDirectFileAPIs[rel][sel.Sel.Name] {
				t.Errorf("%s: direct os.%s file API in production builtins must be routed through CallContext or added as a documented hardcoded internal exception",
					fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
	})
}

func walkProductionBuiltins(t *testing.T, visit func(path string, fset *token.FileSet, file *ast.File)) {
	t.Helper()

	root := repoRoot(t)
	builtinsRoot := filepath.Join(root, "builtins")
	fset := token.NewFileSet()
	err := filepath.WalkDir(builtinsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(fset, path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		visit(path, fset, file)
		return nil
	})
	if err != nil {
		t.Fatalf("walk production builtins: %v", err)
	}
}

func productionBuiltinRelPath(t *testing.T, path string) string {
	t.Helper()
	rel, err := filepath.Rel(repoRoot(t), path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.ToSlash(rel)
}

func receiverName(expr ast.Expr) string {
	if id, ok := expr.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func isOSReadonly(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && receiverName(sel.X) == "os" && sel.Sel.Name == "O_RDONLY"
}

func exprString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprString(e.X) + "." + e.Sel.Name
	case *ast.BinaryExpr:
		return exprString(e.X) + " " + e.Op.String() + " " + exprString(e.Y)
	default:
		return "<expr>"
	}
}
