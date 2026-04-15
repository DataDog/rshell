// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package version

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// TestBuildVersionAsDependency verifies that buildVersion returns a real
// version (not "dev") when rshell is imported as a dependency by another
// module. This is the primary use case (the Datadog Agent imports rshell).
//
// The test uses testdata/depcheck/ — a small Go program that imports rshell
// and prints the version from debug.ReadBuildInfo(). The test copies it to a
// temp dir, adds a replace directive pointing to the local rshell module,
// builds and runs it, and checks the output.
func TestBuildVersionAsDependency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: requires building an external module")
	}

	// Find the rshell module root (two levels up from internal/version/).
	modRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(modRoot, "go.mod")); err != nil {
		t.Fatalf("could not find rshell go.mod at %s: %v", modRoot, err)
	}

	tmp := t.TempDir()

	// Copy main.go from testdata.
	mainSrc, err := os.ReadFile("testdata/depcheck/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "main.go"), mainSrc, 0o644); err != nil {
		t.Fatal(err)
	}

	// Copy go.mod template and append the replace directive.
	goModSrc, err := os.ReadFile("testdata/depcheck/go.mod.test")
	if err != nil {
		t.Fatal(err)
	}
	goMod := string(goModSrc) + fmt.Sprintf("\nreplace github.com/DataDog/rshell => %s\n", modRoot)
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(goMod), 0o644); err != nil {
		t.Fatal(err)
	}

	// Resolve dependencies.
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = tmp
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy failed: %v\n%s", err, out)
	}

	// Build and run.
	run := exec.Command("go", "run", ".")
	run.Dir = tmp
	out, err := run.CombinedOutput()
	if err != nil {
		t.Fatalf("go run failed: %v\n%s", err, out)
	}

	got := strings.TrimSpace(string(out))
	if got == "NOT_FOUND" || got == "NO_BUILD_INFO" || got == "" {
		t.Fatalf("expected a version from build info deps, got %q", got)
	}
	t.Logf("rshell version from dependency build info: %s", got)
}
