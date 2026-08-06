// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package ntfsmft

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unsafe"
)

// TestNTFSVolumeDataLayout pins the ABI layout of ntfsVolumeData against the
// Windows NTFS_VOLUME_DATA_BUFFER the FSCTL_GET_NTFS_VOLUME_DATA control code
// fills. Cross-compilation and `go vet` cannot catch a field type/order change
// that silently mis-decodes the volume geometry (which would corrupt every
// record read), and the real scan test skips in CI when raw NTFS access is
// unavailable — so assert the size and offsets deterministically here.
func TestNTFSVolumeDataLayout(t *testing.T) {
	if got := unsafe.Sizeof(ntfsVolumeData{}); got != 96 {
		t.Fatalf("sizeof(ntfsVolumeData) = %d, want 96", got)
	}
	var d ntfsVolumeData
	offsets := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"VolumeSerialNumber", unsafe.Offsetof(d.VolumeSerialNumber), 0},
		{"NumberSectors", unsafe.Offsetof(d.NumberSectors), 8},
		{"TotalClusters", unsafe.Offsetof(d.TotalClusters), 16},
		{"FreeClusters", unsafe.Offsetof(d.FreeClusters), 24},
		{"TotalReserved", unsafe.Offsetof(d.TotalReserved), 32},
		{"BytesPerSector", unsafe.Offsetof(d.BytesPerSector), 40},
		{"BytesPerCluster", unsafe.Offsetof(d.BytesPerCluster), 44},
		{"BytesPerFileRecordSegment", unsafe.Offsetof(d.BytesPerFileRecordSegment), 48},
		{"ClustersPerFRS", unsafe.Offsetof(d.ClustersPerFRS), 52},
		{"MftValidDataLength", unsafe.Offsetof(d.MftValidDataLength), 56},
		{"MftStartLcn", unsafe.Offsetof(d.MftStartLcn), 64},
		{"Mft2StartLcn", unsafe.Offsetof(d.Mft2StartLcn), 72},
		{"MftZoneStart", unsafe.Offsetof(d.MftZoneStart), 80},
		{"MftZoneEnd", unsafe.Offsetof(d.MftZoneEnd), 88},
	}
	for _, o := range offsets {
		if o.got != o.want {
			t.Errorf("offsetof(ntfsVolumeData.%s) = %d, want %d", o.name, o.got, o.want)
		}
	}
}

// TestFileIDDescriptorLayout pins the ABI layout of fileIDDescriptor against
// the Windows FILE_ID_DESCRIPTOR passed to OpenFileById. A wrong Size or a
// mis-placed FileID would make every top-file path resolution fail silently
// (falling back to "?\basename"), which the opportunistic scan test would not
// reliably catch in CI.
func TestFileIDDescriptorLayout(t *testing.T) {
	if got := unsafe.Sizeof(fileIDDescriptor{}); got != 24 {
		t.Fatalf("sizeof(fileIDDescriptor) = %d, want 24", got)
	}
	var f fileIDDescriptor
	if got := unsafe.Offsetof(f.Size); got != 0 {
		t.Errorf("offsetof(fileIDDescriptor.Size) = %d, want 0", got)
	}
	if got := unsafe.Offsetof(f.Type); got != 4 {
		t.Errorf("offsetof(fileIDDescriptor.Type) = %d, want 4", got)
	}
	if got := unsafe.Offsetof(f.FileID); got != 8 {
		t.Errorf("offsetof(fileIDDescriptor.FileID) = %d, want 8", got)
	}
}

// TestSystemDLLProcsArePinned guards the single System32 entry point this
// package uses. The symbol analyzer only sees NewLazySystemDLL — it does not
// pin the DLL name or the specific procedures — so a future edit could reach a
// different kernel32/System32 API (e.g. one that writes) without tripping the
// allowlist. This scans every non-test source file in the package and fails if
// the set of NewLazySystemDLL / NewProc string literals drifts from the
// audited set. Widening it must be a deliberate change to this test.
func TestSystemDLLProcsArePinned(t *testing.T) {
	wantDLLs := []string{"kernel32.dll"}
	wantProcs := []string{"GetFinalPathNameByHandleW", "OpenFileById"}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	var dlls, procs []string
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			lit, ok := firstStringLit(call)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "NewLazySystemDLL":
				dlls = append(dlls, lit)
			case "NewProc":
				procs = append(procs, lit)
			}
			return true
		})
	}

	sort.Strings(dlls)
	sort.Strings(procs)
	if !equalStrings(dlls, wantDLLs) {
		t.Errorf("System DLLs = %v, want %v (a new/changed DLL must be reviewed here)", dlls, wantDLLs)
	}
	if !equalStrings(procs, wantProcs) {
		t.Errorf("System32 procedures = %v, want %v (a new/changed proc must be reviewed here)", procs, wantProcs)
	}
}

// firstStringLit returns the value of a call's first argument when it is a
// string literal.
func firstStringLit(call *ast.CallExpr) (string, bool) {
	if len(call.Args) == 0 {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
