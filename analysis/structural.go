// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package analysis

import (
	"go/ast"
	"go/token"

	"golang.org/x/tools/go/analysis"
)

// ScannerBufferAnalyzer checks that every bufio.NewScanner call in the
// analyzed package has a corresponding .Buffer() call on the returned value
// within the same function scope. Without Buffer(), the scanner uses a fixed
// 64 KiB internal buffer and fails on lines longer than that — a reliability
// and DoS risk for builtins that must handle arbitrary input.
var ScannerBufferAnalyzer = &analysis.Analyzer{
	Name: "scannerbuffer",
	Doc:  "checks that bufio.NewScanner results have Buffer() called to set a bounded read buffer",
	Run:  runScannerBuffer,
}

// OpenFileCloseAnalyzer checks that every callCtx.OpenFile call result that
// is assigned to a variable has a corresponding .Close() call (direct or via
// defer) within the same function scope. Unclosed file handles exhaust file
// descriptors over repeated script executions.
var OpenFileCloseAnalyzer = &analysis.Analyzer{
	Name: "openfileclose",
	Doc:  "checks that callCtx.OpenFile results are always closed within the same function",
	Run:  runOpenFileClose,
}

func runScannerBuffer(pass *analysis.Pass) (any, error) {
	for _, f := range pass.Files {
		checkFileScannerBuffer(f, func(pos token.Pos, format string, args ...any) {
			pass.Reportf(pos, format, args...)
		})
	}
	return nil, nil
}

func runOpenFileClose(pass *analysis.Pass) (any, error) {
	for _, f := range pass.Files {
		checkFileOpenFileClose(f, func(pos token.Pos, format string, args ...any) {
			pass.Reportf(pos, format, args...)
		})
	}
	return nil, nil
}

// checkFileScannerBuffer enforces the Scanner.Buffer() rule on a single file.
// It is also called directly by the test harness in symbols_test.go.
func checkFileScannerBuffer(f *ast.File, report func(pos token.Pos, format string, args ...any)) {
	forEachFuncBody(f, func(body *ast.BlockStmt) {
		type scannerVar struct {
			pos  token.Pos
			name string
		}
		var scanners []scannerVar
		buffered := make(map[string]bool)

		inspectBody(body, func(n ast.Node) {
			switch node := n.(type) {
			case *ast.AssignStmt:
				// Detect: x := bufio.NewScanner(...)
				for i, rhs := range node.Rhs {
					if !isCall(rhs, "bufio", "NewScanner") {
						continue
					}
					if i < len(node.Lhs) {
						if id, ok := node.Lhs[i].(*ast.Ident); ok && id.Name != "_" {
							scanners = append(scanners, scannerVar{pos: rhs.Pos(), name: id.Name})
						}
					}
				}
			case *ast.CallExpr:
				// Detect: x.Buffer(...)
				if sel, ok := node.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Buffer" {
					if id, ok := sel.X.(*ast.Ident); ok {
						buffered[id.Name] = true
					}
				}
			}
		})

		for _, sc := range scanners {
			if !buffered[sc.name] {
				report(sc.pos,
					"bufio.NewScanner result %q must have .Buffer() called to cap the maximum line size (see analysis/README.md §Structural Rules)",
					sc.name)
			}
		}
	})
}

// checkFileOpenFileClose enforces the OpenFile-must-be-closed rule on a
// single file. It is also called directly by the test harness.
//
// The rule accounts for the common hand-off pattern:
//
//	f, err := callCtx.OpenFile(...)
//	rc = f          // hand off to rc
//	defer rc.Close() // closes f transitively
func checkFileOpenFileClose(f *ast.File, report func(pos token.Pos, format string, args ...any)) {
	forEachFuncBody(f, func(body *ast.BlockStmt) {
		type openVar struct {
			pos  token.Pos
			name string
		}
		var opens []openVar
		closed := make(map[string]bool)
		// handOff maps a "holder" variable name to the original variable it was
		// assigned from. Closing the holder counts as closing the original.
		handOff := make(map[string]string) // holder → original
		// returned tracks variables that appear in a return statement. Returning
		// a file handle transfers ownership to the caller, so no Close() is
		// required in the current scope.
		returned := make(map[string]bool)

		inspectBody(body, func(n ast.Node) {
			switch node := n.(type) {
			case *ast.AssignStmt:
				for i, rhs := range node.Rhs {
					// Detect: f, err := <anything>.OpenFile(...)
					if call, ok := rhs.(*ast.CallExpr); ok {
						if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "OpenFile" {
							if i < len(node.Lhs) {
								if id, ok := node.Lhs[i].(*ast.Ident); ok && id.Name != "_" {
									opens = append(opens, openVar{pos: rhs.Pos(), name: id.Name})
								}
							}
						}
					}
					// Detect: rc = f (hand-off from OpenFile result to another var).
					// Works for both := and = assignments.
					if rhsId, ok := rhs.(*ast.Ident); ok && i < len(node.Lhs) {
						if lhsId, ok := node.Lhs[i].(*ast.Ident); ok && lhsId.Name != "_" {
							handOff[lhsId.Name] = rhsId.Name
						}
					}
				}
			case *ast.ReturnStmt:
				// Detect: return f, nil — caller takes ownership, no Close needed here.
				for _, result := range node.Results {
					if id, ok := result.(*ast.Ident); ok {
						returned[id.Name] = true
					}
				}
			case *ast.CallExpr:
				// Detect: f.Close() or defer f.Close() (the defer wrapper is
				// stripped by inspectBody before the CallExpr reaches here).
				if sel, ok := node.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Close" {
					if id, ok := sel.X.(*ast.Ident); ok {
						closed[id.Name] = true
					}
				}
			}
		})

		for _, ov := range opens {
			if !isClosedTransitive(ov.name, closed, handOff) && !returned[ov.name] {
				report(ov.pos,
					"OpenFile result %q must be closed via defer or explicit Close() call (see analysis/README.md §Structural Rules)",
					ov.name)
			}
		}
	})
}

// isClosedTransitive returns true if name is closed directly or if a variable
// that name was handed off to is closed.
func isClosedTransitive(name string, closed map[string]bool, handOff map[string]string) bool {
	if closed[name] {
		return true
	}
	for holder, orig := range handOff {
		if orig == name && closed[holder] {
			return true
		}
	}
	return false
}

// forEachFuncBody calls fn for every function body in f — both top-level
// FuncDecl bodies and FuncLit bodies — so each scope is checked independently.
func forEachFuncBody(f *ast.File, fn func(*ast.BlockStmt)) {
	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			if node.Body != nil {
				fn(node.Body)
			}
		case *ast.FuncLit:
			fn(node.Body)
		}
		return true
	})
}

// inspectBody walks body without recursing into nested FuncLit nodes (which
// are treated as independent scopes by forEachFuncBody). fn is called for
// every non-FuncLit node encountered.
func inspectBody(body *ast.BlockStmt, fn func(ast.Node)) {
	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if _, ok := n.(*ast.FuncLit); ok {
			return false // handled as its own scope by forEachFuncBody
		}
		fn(n)
		return true
	})
}

// isCall returns true if expr is a call to pkg.Name (using the local package
// alias name, e.g. "bufio" for import "bufio").
func isCall(expr ast.Expr, localPkg, name string) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == localPkg && sel.Sel.Name == name
}
