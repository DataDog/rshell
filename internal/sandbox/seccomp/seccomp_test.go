// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package seccomp

import (
	"slices"
	"testing"
)

func TestDefaultDenylistReturnsCopy(t *testing.T) {
	first := DefaultDenylist()
	second := DefaultDenylist()
	if len(first) == 0 {
		t.Fatal("default denylist must not be empty")
	}

	first[0] = "changed"
	if second[0] == "changed" {
		t.Fatal("DefaultDenylist returned shared mutable storage")
	}
}

func TestDefaultDenylistReviewedExceptions(t *testing.T) {
	denied := DefaultDenylist()

	for _, name := range []string{
		"clone", "clone3", "fork", "vfork", "execve", "execveat",
		"setuid", "setgid", "setgroups", "capset", "prctl",
		"unshare", "setns", "mount", "umount2", "pivot_root",
		"bpf", "perf_event_open", "ptrace", "process_vm_readv", "process_vm_writev",
		"keyctl", "add_key", "request_key",
		"init_module", "finit_module", "delete_module",
		"reboot", "mknod", "mknodat", "ioctl",
		"chmod", "fchmodat2", "chown", "setxattr", "removexattr", "utimensat",
		"kill", "settimeofday", "clock_settime", "setpriority", "sched_setattr",
		"io_uring_setup", "userfaultfd", "open_by_handle_at",
	} {
		if !slices.Contains(denied, name) {
			t.Errorf("default denylist is missing %q", name)
		}
	}

	for _, name := range []string{"setresuid"} {
		if slices.Contains(denied, name) {
			t.Errorf("worker-required syscall %q must not be denied", name)
		}
	}
}

func TestDefaultDenylistHasNoDuplicates(t *testing.T) {
	seen := make(map[string]struct{})
	for _, name := range DefaultDenylist() {
		if _, exists := seen[name]; exists {
			t.Fatalf("duplicate syscall %q", name)
		}
		seen[name] = struct{}{}
	}
}

func TestDenylistForCommandWithoutSetfaclMatchesDefault(t *testing.T) {
	for _, allowed := range [][]string{
		nil,
		{},
		{"rshell:cat"},
		{"rshell:ps", "rshell:df"},
	} {
		if !slices.Equal(DenylistForCommand(allowed), DefaultDenylist()) {
			t.Errorf("DenylistForCommand(%v) diverged from DefaultDenylist", allowed)
		}
	}
}

func TestDenylistForCommandWithSetfaclAllowsOnlyACLWriteSyscalls(t *testing.T) {
	denied := DenylistForCommand([]string{"rshell:setfacl"})

	for _, name := range aclWriteSyscalls {
		if slices.Contains(denied, name) {
			t.Errorf("setfacl denylist still contains %q", name)
		}
	}

	// The removal family stays denied: setfacl only ever adds/replaces ACL
	// entries (-m), never removes the ACL xattr outright.
	for _, name := range []string{"removexattr", "lremovexattr", "fremovexattr"} {
		if !slices.Contains(denied, name) {
			t.Errorf("setfacl denylist must still deny %q", name)
		}
	}

	// Every other reviewed exception (credentials, namespaces, module
	// loading, etc.) must remain denied; only the three ACL-write syscalls
	// may be removed.
	defaultDenied := DefaultDenylist()
	if len(defaultDenied)-len(denied) != len(aclWriteSyscalls) {
		t.Fatalf("DenylistForCommand removed %d entries, want exactly %d (aclWriteSyscalls)",
			len(defaultDenied)-len(denied), len(aclWriteSyscalls))
	}
	for _, name := range defaultDenied {
		if slices.Contains(aclWriteSyscalls, name) {
			continue
		}
		if !slices.Contains(denied, name) {
			t.Errorf("DenylistForCommand(setfacl) unexpectedly removed unrelated syscall %q", name)
		}
	}
}

func TestDenylistForCommandReturnsIndependentCopy(t *testing.T) {
	first := DenylistForCommand([]string{"rshell:setfacl"})
	second := DenylistForCommand([]string{"rshell:setfacl"})
	if len(first) == 0 {
		t.Fatal("denylist for setfacl must not be empty")
	}
	first[0] = "changed"
	if second[0] == "changed" {
		t.Fatal("DenylistForCommand shares mutable storage across calls")
	}
}
