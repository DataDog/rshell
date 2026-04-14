// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package version

import (
	"os/exec"
	"strings"
	"testing"
)

func TestVersionConstantNotEmpty(t *testing.T) {
	if version == "" {
		t.Fatal("version constant must not be empty")
	}
}

func TestVersionDefaultMatchesConstant(t *testing.T) {
	// When ldflags haven't overridden Version, it should equal the constant.
	// In tests, ldflags are not set, so this always holds.
	if Version != version {
		t.Errorf("Version = %q, want source constant %q (was it overridden by ldflags in a test?)", Version, version)
	}
}

// TestVersionMatchesGitTag verifies that the source constant matches the
// latest git tag. This catches forgotten version bumps before a release.
//
// Skipped when:
//   - git is not available
//   - there are no tags (new clone / shallow clone)
//   - HEAD is not exactly on a tag (development builds between releases)
func TestVersionMatchesGitTag(t *testing.T) {
	// Check if HEAD is exactly a tag (git describe --exact-match fails otherwise).
	out, err := exec.Command("git", "describe", "--tags", "--exact-match", "HEAD").CombinedOutput()
	if err != nil {
		t.Skipf("HEAD is not on a tag (expected during development): %v", err)
	}
	tag := strings.TrimSpace(string(out))
	tag = strings.TrimPrefix(tag, "v")

	if version != tag {
		t.Errorf("version constant %q does not match git tag %q — update the constant in version.go before tagging", version, tag)
	}
}
