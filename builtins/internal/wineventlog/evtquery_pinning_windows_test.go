// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package wineventlog

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"
)

// TestEvtQueryPinsTargetFile pins the platform behaviour that the --Path
// sandbox mitigation depends on.
//
// get-winevent cannot hand EvtQuery an already-validated handle (EvtQuery
// takes a path string), so it instead re-checks the file's identity via
// Query.VerifyAfterOpen once EvtQuery has returned. That re-check is only
// sound if two things hold:
//
//  1. EvtQuery opens the target eagerly, at EvtQuery time — not lazily on
//     the first EvtNext. Otherwise "after open" would not yet have opened
//     anything.
//  2. The service holds the file in a share mode that denies rename and
//     delete of that name. Otherwise an attacker could restore the original
//     name before the re-check and evade it.
//
// Together these mean the name is pinned to the object the service opened,
// so re-resolving it yields that object's identity. If a future Windows
// release changes either property, the mitigation silently weakens — this
// test is here to fail loudly instead.
func TestEvtQueryPinsTargetFile(t *testing.T) {
	src := `C:\Windows\System32\winevt\Logs\Application.evtx`
	content, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("cannot read %s: %v", src, err)
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "pinned.evtx")
	if err := os.WriteFile(target, content, 0o644); err != nil {
		t.Fatal(err)
	}

	pathPtr, err := utf16Ptr(target)
	if err != nil {
		t.Fatal(err)
	}
	hQuery, _, callErr := procEvtQuery.Call(
		0,
		uintptr(unsafe.Pointer(pathPtr)),
		0,
		uintptr(evtQueryFilePath|evtQueryReverseDirection),
	)
	if hQuery == 0 {
		t.Fatalf("EvtQuery: %v", callErr)
	}
	defer evtClose(hQuery)

	// Property 2: the pinned name cannot be moved or removed.
	if err := os.Rename(target, filepath.Join(dir, "decoy.evtx")); err == nil {
		t.Error("rename of the query target succeeded; EvtQuery no longer pins the name, " +
			"so the --Path VerifyAfterOpen identity re-check can be evaded")
	}
	if err := os.Remove(target); err == nil {
		t.Error("delete of the query target succeeded; EvtQuery no longer pins the name, " +
			"so the --Path VerifyAfterOpen identity re-check can be evaded")
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("query target no longer resolves after EvtQuery: %v", err)
	}

	// Property 1: the result set is usable, i.e. EvtQuery really did open
	// the file rather than deferring to the first EvtNext.
	handles := make([]uintptr, 4)
	got, err := evtNextBatch(hQuery, handles)
	if err != nil {
		t.Fatalf("EvtNext: %v", err)
	}
	for i := 0; i < got; i++ {
		evtClose(handles[i])
	}
	if got == 0 {
		t.Skip("Application.evtx copy yielded no events; cannot confirm eager open")
	}
}
