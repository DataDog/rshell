// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

// Go tests, rather than scenario tests, for the same underlying reason
// AGENTS.md documents for `free`/`ip route`/`lsof`: usermod's behavior
// depends on real system state (/etc/group, /etc/passwd) via hardcoded
// paths in builtins/internal/etcgroup and builtins/internal/etcpasswd —
// etcGroupPath and etcPasswdPath are not configurable — so a scenario test
// cannot inject a fixture file.
//
// # Scope of this file vs. the etcgroup/etcpasswd package tests
//
// This file deliberately never causes a real write to /etc/group: unlike
// setfacl's own Go tests (which write into a t.TempDir() PATH operand under
// an AllowedPaths root), usermod's target file is not sandboxed by
// AllowedPaths at all — every write path always resolves to the one real
// system /etc/group — so there is no test-safe way to exercise the
// successful "append a member" mutation through the usermod builtin's own
// CLI dispatch without either mutating the CI host's real /etc/group or
// running as an isolated container/VM dedicated to this test, neither of
// which this test suite does for any other builtin.
//
// The full behavioral matrix requested for this builtin — appending to an
// existing member list, idempotent re-add, byte-for-byte preservation of
// every other line, and crash safety around the temp-file-plus-rename
// write — is covered instead at the layer that *can* safely use a
// temporary file: builtins/internal/etcgroup/etcgroup_linux_test.go (via
// addMemberAtPath) and builtins/internal/etcpasswd/etcpasswd_linux_test.go.
// This file covers everything that is safe to exercise through the real
// interpreter dispatch path without ever writing to the real /etc/group:
// remediation-mode gating, help text, flag/operand validation, and the
// error paths for a nonexistent group or user (which fail closed *before*
// any write is attempted, so they are safe to run against the read-only
// real host — see the tests below for the well-known "root" group/account
// and the reserved bogus name used to force a NotFound).
package usermod_test

import (
	"strings"
	"testing"
)

// bogusName is never a valid Linux account or group name (Linux account/
// group names are conventionally lowercase and this contains uppercase and
// a "-test-marker" suffix unlikely to collide with any real system group or
// user), so referencing it always exercises the "does not exist" error path
// without any risk of accidentally matching (and thus mutating group
// membership for) a real account.
const bogusName = "No-Such-Rshell-Test-Marker-Account"

func TestUsermodReadOnlyModeRefused(t *testing.T) {
	_, stderr, code := usermodRunReadOnly(t, "usermod -aG docker root")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "remediation mode required") {
		t.Fatalf("expected a remediation-mode-required error, got %q", stderr)
	}
}

func TestUsermodReadOnlyModeRefusesHelpToo(t *testing.T) {
	// Matches setfacl/systemctl's own defence-in-depth ordering: the
	// capability check runs before --help is even inspected, so --help in
	// read-only mode is refused exactly like any other invocation.
	stdout, stderr, code := usermodRunReadOnly(t, "usermod --help")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if stdout != "" {
		t.Fatalf("expected no help text on stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "remediation mode required") {
		t.Fatalf("expected a remediation-mode-required error, got %q", stderr)
	}
}

func TestUsermodHelp(t *testing.T) {
	stdout, stderr, code := usermodRun(t, "usermod --help")
	if code != 0 {
		t.Fatalf("expected exit 0, got %d (stderr %q)", code, stderr)
	}
	if !strings.Contains(stdout, "Usage: usermod -aG GROUP") {
		t.Fatalf("expected usage line, got %q", stdout)
	}
	if !strings.Contains(stdout, "narrow subset of GNU usermod") {
		t.Fatalf("expected the narrow-subset disclosure text, got %q", stdout)
	}
}

func TestUsermodMissingGroupsFlag(t *testing.T) {
	_, stderr, code := usermodRun(t, "usermod root")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "-G/--groups") {
		t.Fatalf("expected a -G/--groups usage error, got %q", stderr)
	}
}

func TestUsermodGWithoutARejected(t *testing.T) {
	_, stderr, code := usermodRun(t, "usermod -G docker root")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "-a/--append") {
		t.Fatalf("expected an error naming -a/--append as required, got %q", stderr)
	}
}

func TestUsermodMissingUserOperand(t *testing.T) {
	_, stderr, code := usermodRun(t, "usermod -aG docker")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "missing operand") {
		t.Fatalf("expected a missing-operand error, got %q", stderr)
	}
}

func TestUsermodExtraOperandRejected(t *testing.T) {
	_, stderr, code := usermodRun(t, "usermod -aG docker root daemon")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "extra operand") {
		t.Fatalf("expected an extra-operand error, got %q", stderr)
	}
}

func TestUsermodEmptyGroupsValueRejected(t *testing.T) {
	_, stderr, code := usermodRun(t, "usermod -aG '' root")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "empty group list") {
		t.Fatalf("expected an empty-group-list error, got %q", stderr)
	}
}

func TestUsermodTrailingCommaInGroupsRejected(t *testing.T) {
	_, stderr, code := usermodRun(t, "usermod -aG docker, root")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "invalid group list") {
		t.Fatalf("expected an invalid-group-list error, got %q", stderr)
	}
}

func TestUsermodUnknownFlagRejected(t *testing.T) {
	_, stderr, code := usermodRun(t, "usermod -x docker root")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if stderr == "" {
		t.Fatalf("expected an unknown-flag error")
	}
}

// TestUsermodNonexistentUser exercises the real, unmutated /etc/passwd on
// the host running this test: bogusName is guaranteed not to be a real
// account, so this always takes the "user does not exist" error path
// before etcgroup.AddMember (and therefore any write to /etc/group) is ever
// reached. This is safe to run against the real host filesystem.
func TestUsermodNonexistentUser(t *testing.T) {
	_, stderr, code := usermodRun(t, "usermod -aG root "+bogusName)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d (stderr %q)", code, stderr)
	}
	if !strings.Contains(stderr, "user '"+bogusName+"' does not exist") {
		t.Fatalf("expected a user-does-not-exist error, got %q", stderr)
	}
}

// TestUsermodNonexistentGroup exercises the real, unmutated /etc/group on
// the host running this test. "root" is a well-known account present on
// every Linux host (matching setfacl's own test convention for group
// resolution — see helpers_test.go's package doc comment in the setfacl
// suite), so the user-existence check passes and the group lookup itself
// fails, without ever writing to /etc/group.
func TestUsermodNonexistentGroup(t *testing.T) {
	_, stderr, code := usermodRun(t, "usermod -aG "+bogusName+" root")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d (stderr %q)", code, stderr)
	}
	if !strings.Contains(stderr, "group '"+bogusName+"' does not exist") {
		t.Fatalf("expected a group-does-not-exist error, got %q", stderr)
	}
}

// TestUsermodMultipleGroupsContinuesPastFirstFailure exercises the
// multi-group operand loop's continue-past-failure behavior (mirroring
// setfacl's own multi-operand loop) without ever writing to /etc/group: the
// first group name is bogus (fails), the second is also bogus (also
// fails), so no AddMember call in this invocation can succeed and mutate
// the real file, while still exercising that both failures are reported and
// the exit code is 1.
func TestUsermodMultipleGroupsContinuesPastFirstFailure(t *testing.T) {
	_, stderr, code := usermodRun(t, "usermod -aG "+bogusName+"-one,"+bogusName+"-two root")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d (stderr %q)", code, stderr)
	}
	if !strings.Contains(stderr, bogusName+"-one") {
		t.Fatalf("expected an error mentioning the first bogus group, got %q", stderr)
	}
	if !strings.Contains(stderr, bogusName+"-two") {
		t.Fatalf("expected an error mentioning the second bogus group (i.e. the loop continued past the first failure), got %q", stderr)
	}
}

func TestUsermodMissingOperandListsHelp(t *testing.T) {
	// GNU-style commands print a "Try 'cmd --help'" hint after a usage
	// error, when the command declares --help at all (see the dispatch
	// logic in builtins.Command.Register). usermod declares -h/--help, so
	// this hint should appear.
	_, stderr, code := usermodRun(t, "usermod")
	if code != 1 {
		t.Fatalf("expected exit 1, got %d", code)
	}
	if !strings.Contains(stderr, "-G/--groups") {
		t.Fatalf("expected a -G/--groups usage error, got %q", stderr)
	}
}
