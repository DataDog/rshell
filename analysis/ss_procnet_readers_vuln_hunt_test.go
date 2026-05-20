// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Tripwire tests added by vuln-hunt campaign 2026-05-20-gpt-5.5-cyber-3 /
// ss-procnet-readers.

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

// TestVulnHuntSubsystemSSProcnetReaders_ProcPathsNotUserMutable pins the
// threat-model boundary for the documented /proc/net/* direct-open exception:
// production code may declare the proc root globals at package init, but must
// not later assign to them from CLI flags, env vars, shell state, or any other
// user-facing path. Tests are allowed to mutate these globals for synthetic
// proc fixtures and are intentionally excluded here.
func TestVulnHuntSubsystemSSProcnetReaders_ProcPathsNotUserMutable(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()

	for _, dir := range []string{"cmd", "interp", "builtins"} {
		base := filepath.Join(root, dir)
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") {
				return nil
			}
			if isGoTestFile(path) {
				return nil
			}
			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				return parseErr
			}

			ast.Inspect(file, func(n ast.Node) bool {
				switch node := n.(type) {
				case *ast.AssignStmt:
					for _, lhs := range node.Lhs {
						if procnetPathName(lhs) != "" {
							t.Errorf("%s assigns to %s in production code; procnet roots must remain hardcoded outside tests",
								fset.Position(lhs.Pos()), procnetPathName(lhs))
						}
					}
				case *ast.ValueSpec:
					for _, name := range node.Names {
						if name.Name != "ProcPath" && name.Name != "ProcNetRoutePath" {
							continue
						}
						if !allowedProcnetPathDeclaration(root, path, name.Name) {
							t.Errorf("%s declares %s outside the approved procnet root declaration files",
								fset.Position(name.Pos()), name.Name)
						}
					}
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", base, err)
		}
	}
}

func procnetPathName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		if e.Name == "ProcPath" || e.Name == "ProcNetRoutePath" {
			return e.Name
		}
	case *ast.SelectorExpr:
		if e.Sel.Name == "ProcPath" || e.Sel.Name == "ProcNetRoutePath" {
			return e.Sel.Name
		}
	}
	return ""
}

func allowedProcnetPathDeclaration(root, path, name string) bool {
	switch name {
	case "ProcPath":
		return path == filepath.Join(root, "builtins", "ss", "ss_linux.go")
	case "ProcNetRoutePath":
		return path == filepath.Join(root, "builtins", "ip", "ip.go")
	default:
		return false
	}
}

func isGoTestFile(path string) bool {
	return strings.HasSuffix(path, "_test.go")
}
