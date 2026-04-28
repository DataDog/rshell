// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package version

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

func TestVersionNotEmpty(t *testing.T) {
	// In normal test runs (go test), buildVersion returns "dev" because
	// the module is the main module built as (devel). That's fine — we
	// just verify it's never empty.
	if Version == "" {
		t.Fatal("Version must not be empty")
	}
}

func TestBuildVersionFallback(t *testing.T) {
	// When running tests, the module is the main module so ReadBuildInfo
	// returns (devel) for Main.Version. buildVersion should return "dev".
	v := buildVersion()
	if v == "" {
		t.Fatal("buildVersion() must not return empty string")
	}
	if v != "dev" {
		t.Logf("buildVersion() = %q (expected 'dev' in test, got something else — ldflags?)", v)
	}
}

// TestBuildVersionAsDependency verifies that debug.ReadBuildInfo() reports
// the correct version when rshell is imported as a dependency by another
// module. This is the primary use case (the Datadog Agent imports rshell).
//
// The test uses testdata/depcheck/ — a standalone Go module that depends on
// a published version of rshell. The depcheck program doesn't use rshell's
// version package — it only blank-imports rshell/interp so that rshell appears
// in the binary's dependency list, then calls ReadBuildInfo() to verify the
// version is present. This tests the Go embedding mechanism that our
// buildVersion() relies on, not rshell's code itself, so the specific version
// imported doesn't matter.
func TestBuildVersionAsDependency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: requires building an external module")
	}

	depDir := "testdata/depcheck"

	list := exec.Command("go", "list", "-m", "-f", "{{.Version}}", modulePath)
	list.Dir = depDir
	var listStderr bytes.Buffer
	list.Stderr = &listStderr
	wantOut, err := list.Output()
	if err != nil {
		t.Fatalf("go list failed: %v\n%s", err, listStderr.String())
	}
	want := strings.TrimSpace(string(wantOut))
	if want == "" {
		t.Fatal("go list returned an empty rshell module version")
	}

	// Build and run the depcheck program directly from testdata.
	run := exec.Command("go", "run", ".")
	run.Dir = depDir
	var runStderr bytes.Buffer
	run.Stderr = &runStderr
	out, err := run.Output()
	if err != nil {
		t.Fatalf("go run failed: %v\n%s", err, runStderr.String())
	}

	got := strings.TrimSpace(string(out))
	if got != want {
		t.Fatalf("expected version %q from build info deps, got %q", want, got)
	}
}
