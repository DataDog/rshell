// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package version

import "testing"

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
	// In a test binary, we expect "dev" since rshell is the main module.
	if v != "dev" {
		t.Logf("buildVersion() = %q (expected 'dev' in test, got something else — ldflags?)", v)
	}
}
