// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Tripwire tests added by vuln-hunt campaign 2026-05-20-gpt-5.5-cyber-3 /
// df-mount-enumeration.

package analysis

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVulnHuntSubsystemDfMountEnumeration_MountinfoPathHardcoded(t *testing.T) {
	fset, file := parseProductionFileVH(t, "builtins/internal/diskstats/diskstats_linux.go")

	constName := "mountInfoPath"
	constValue := ""
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok {
			return true
		}
		for i, name := range spec.Names {
			if name.Name != constName || i >= len(spec.Values) {
				continue
			}
			if lit, ok := spec.Values[i].(*ast.BasicLit); ok {
				constValue = strings.Trim(lit.Value, `"`)
			}
		}
		return true
	})
	if constValue != "/proc/self/mountinfo" {
		t.Fatalf("mountInfoPath const = %q, want /proc/self/mountinfo", constValue)
	}

	osOpenCalls := 0
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || receiverName(sel.X) != "os" {
			return true
		}
		if sel.Sel.Name != "Open" {
			t.Fatalf("%s: diskstats Linux may only use direct os.Open, got os.%s",
				fset.Position(sel.Pos()), sel.Sel.Name)
		}
		osOpenCalls++
		if len(call.Args) != 1 {
			t.Fatalf("%s: os.Open has %d args, want 1", fset.Position(call.Pos()), len(call.Args))
		}
		id, ok := call.Args[0].(*ast.Ident)
		if !ok || id.Name != constName {
			t.Fatalf("%s: os.Open argument = %s, want mountInfoPath const",
				fset.Position(call.Args[0].Pos()), exprString(call.Args[0]))
		}
		return true
	})
	if osOpenCalls != 1 {
		t.Fatalf("diskstats Linux os.Open calls = %d, want exactly 1", osOpenCalls)
	}
}

func TestVulnHuntSubsystemDfMountEnumeration_ScannerBufferUsesMountInfoCap(t *testing.T) {
	_, file := parseProductionFileVH(t, "builtins/internal/diskstats/diskstats_linux.go")
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Buffer" || len(call.Args) != 2 {
			return true
		}
		id, ok := call.Args[1].(*ast.Ident)
		if ok && id.Name == "maxMountInfoLine" {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("parseMountInfo must call scanner.Buffer(..., maxMountInfoLine)")
	}
}

func TestVulnHuntSubsystemDfMountEnumeration_StatfsErrorsSkippedAndCtxChecked(t *testing.T) {
	fset, file := parseProductionFileVH(t, "builtins/internal/diskstats/diskstats_linux.go")
	var statfsPos token.Pos
	var ctxErrBeforeStatfs bool
	var statfsErrorContinues bool

	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "listImpl" {
			return true
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if isSelectorCall(call, "ctx", "Err") && statfsPos == token.NoPos {
				ctxErrBeforeStatfs = true
			}
			if isSelectorCall(call, "unix", "Statfs") {
				statfsPos = call.Pos()
			}
			return true
		})
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			ifstmt, ok := n.(*ast.IfStmt)
			if !ok {
				return true
			}
			if !containsSelectorCallInNode(ifstmt.Init, "unix", "Statfs") &&
				!containsSelectorCallInNode(ifstmt.Cond, "unix", "Statfs") {
				return true
			}
			for _, stmt := range ifstmt.Body.List {
				if branch, ok := stmt.(*ast.BranchStmt); ok && branch.Tok == token.CONTINUE {
					statfsErrorContinues = true
				}
			}
			return true
		})
		return false
	})
	if statfsPos == token.NoPos {
		t.Fatal("unix.Statfs call not found in listImpl")
	}
	if !ctxErrBeforeStatfs {
		t.Fatalf("%s: listImpl must check ctx.Err before statfs loop work", fset.Position(statfsPos))
	}
	if !statfsErrorContinues {
		t.Fatal("listImpl must continue, not return, when unix.Statfs fails")
	}
}

func TestVulnHuntSubsystemDfMountEnumeration_PlatformBackendsFailClosed(t *testing.T) {
	other := readProductionFileVH(t, "builtins/internal/diskstats/diskstats_other.go")
	if !strings.Contains(other, "//go:build !linux && !darwin") {
		t.Fatal("diskstats_other.go must be restricted to non-Linux/non-Darwin platforms")
	}
	if !strings.Contains(other, "return nil, ErrNotSupported") {
		t.Fatal("unsupported diskstats backend must return ErrNotSupported")
	}

	darwin := readProductionFileVH(t, "builtins/internal/diskstats/diskstats_darwin.go")
	if strings.Count(darwin, "unix.MNT_NOWAIT") < 2 {
		t.Fatal("Darwin diskstats backend must use MNT_NOWAIT for both Getfsstat calls")
	}

	windowsTest := readProductionFileVH(t, "builtins/df/df_windows_test.go")
	if !strings.Contains(windowsTest, "TestDfNotSupportedOnWindows") ||
		!strings.Contains(windowsTest, "TestDfHelpAlwaysWorks") {
		t.Fatal("df Windows tests must cover fail-closed enumeration and --help availability")
	}
}

func parseProductionFileVH(t *testing.T, rel string) (*token.FileSet, *ast.File) {
	t.Helper()
	path := filepath.Join(repoRoot(t), filepath.FromSlash(rel))
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	return fset, file
}

func readProductionFileVH(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func isSelectorCall(call *ast.CallExpr, recv, name string) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && receiverName(sel.X) == recv && sel.Sel.Name == name
}

func containsSelectorCallInNode(node ast.Node, recv, name string) bool {
	if node == nil {
		return false
	}
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if ok && isSelectorCall(call, recv, name) {
			found = true
		}
		return !found
	})
	return found
}
