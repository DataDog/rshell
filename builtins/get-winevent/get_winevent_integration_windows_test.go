// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package get_winevent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

const applicationEventLog = `C:\Windows\System32\winevt\Logs\Application.evtx`

// copyApplicationEventLog creates a disposable fixture for a real EvtQuery.
// Access to the host log is policy-dependent (and often unavailable in CI), so
// callers skip instead of treating that environmental limitation as a failure.
func copyApplicationEventLog(t *testing.T, dst string) {
	t.Helper()
	contents, err := os.ReadFile(applicationEventLog)
	if err != nil {
		t.Skipf("cannot read %s: %v", applicationEventLog, err)
	}
	if err := os.WriteFile(dst, contents, 0o644); err != nil {
		t.Fatal(err)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func TestGetWinEventPathUsesWorkDirInsideAllowedPaths(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "work")
	if err := os.Mkdir(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	copyApplicationEventLog(t, filepath.Join(workDir, "copy.evtx"))

	stdout, stderr, code := testutil.RunScript(t,
		"get-winevent --Path copy.evtx --MaxEvents 1 --NoMessage",
		workDir, interp.AllowedPaths([]string{root}))
	if code != 0 {
		t.Fatalf("get-winevent in allowed WorkDir failed (stdout %q, stderr %q)", stdout, stderr)
	}
}

func TestGetWinEventPathOutsideAllowedPathsIsRejectedBeforeQuery(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	if err := os.Mkdir(allowed, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.evtx")
	copyApplicationEventLog(t, outside)

	stdout, stderr, code := testutil.RunScript(t,
		"get-winevent --Path "+shellQuote(outside)+" --MaxEvents 1 --NoMessage",
		allowed, interp.AllowedPaths([]string{allowed}))
	if code != 1 {
		t.Fatalf("get-winevent outside AllowedPaths code = %d, want 1 (stdout %q, stderr %q)", code, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("get-winevent wrote event data before the sandbox denial: %q", stdout)
	}
	if !strings.Contains(stderr, "permission denied") {
		t.Errorf("stderr = %q, want the sandbox permission denial", stderr)
	}
}
