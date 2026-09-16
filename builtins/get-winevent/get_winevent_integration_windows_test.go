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

func TestGetWinEventHelpDocumentsMaxEventsAndMessageSanitization(t *testing.T) {
	stdout, stderr, code := testutil.RunScript(t,
		"get-winevent --help", "", interp.AllowedCommands([]string{"rshell:get-winevent"}))
	if code != 0 {
		t.Fatalf("get-winevent --help code = %d, stderr %q", code, stderr)
	}
	for _, want := range []string{
		"maximum events to return (default 256; capped at 1024)",
		"Formatted messages strip U+200E",
		"get-winevent --LogName System --FilterXPath \"*[System[(Level=2)]]\"",
		"Errors only (Level: 1=Critical, 2=Error, 3=Warning, 4=Information, 5=Verbose).",
		"get-winevent --LogName System --FilterXPath \"*[System[Provider[@Name='Microsoft-Windows-Kernel-Power']]]\"",
		"Only events from a specific provider.",
		"get-winevent --LogName System --FilterXPath \"*[System[TimeCreated[timediff(@SystemTime)<=3600000]]]\"",
		"Events from the last hour (timediff() takes milliseconds, no calendar math needed).",
		"get-winevent --LogName System --FilterXPath \"*[System[TimeCreated[@SystemTime>='2026-09-16T00:00:00.000Z' and @SystemTime<='2026-09-16T12:00:00.000Z']]]\"",
		"Events between two absolute UTC timestamps (SystemTime is always UTC, millisecond precision).",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("help output missing %q:\n%s", want, stdout)
		}
	}
}

func TestGetWinEventMaxEventsParseErrorIsUserFacing(t *testing.T) {
	_, stderr, code := testutil.RunScript(t,
		"get-winevent --LogName Application --MaxEvents abc", "", interp.AllowedCommands([]string{"rshell:get-winevent"}))
	if code != 1 {
		t.Fatalf("invalid --MaxEvents code = %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stderr, "--MaxEvents must be a whole number") {
		t.Errorf("stderr = %q, want user-facing integer error", stderr)
	}
	if strings.Contains(stderr, "strconv.ParseInt") {
		t.Errorf("stderr leaked strconv internals: %q", stderr)
	}
}
