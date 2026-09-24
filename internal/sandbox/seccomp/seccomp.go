// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package seccomp installs the syscall restrictions used by the privileged
// rshell worker.
package seccomp

import (
	"errors"
	"slices"
)

// ErrUnsupported is returned when syscall filtering is unavailable on the
// current operating system.
var ErrUnsupported = errors.New("seccomp is not supported on this platform")

// defaultDenylist is deliberately a blocklist, rather than an allowlist. The
// rshell worker and Go runtime need a broad set of ordinary syscalls, while the
// operations below are not part of executing rshell builtins.
//
// Keep this list explicit and reviewed. In particular:
//   - clone is denied unless its flags exactly match the Go runtime's thread
//     creation flags; clone3 is denied unconditionally;
//   - setresuid remains allowed for the helper's controlled UID elevation;
//   - prctl and ioctl are denied only after no_new_privs, parent-death signal,
//     and other worker setup is complete.
var defaultDenylist = []string{
	// Create processes or replace the worker image. clone is filtered by flags.
	"clone",
	"clone3",
	"fork",
	"vfork",
	"execve",
	"execveat",
	"kill",

	// Change credentials or capabilities. setresuid is intentionally omitted.
	"setuid",
	"setgid",
	"setreuid",
	"setregid",
	"setresgid",
	"setgroups",
	"setfsuid",
	"setfsgid",
	"capset",
	"prctl",

	// Change namespaces, roots, or mount topology, including the new mount API.
	"unshare",
	"setns",
	"mount",
	"umount2",
	"pivot_root",
	"chroot",
	"open_tree",
	"move_mount",
	"fsopen",
	"fsconfig",
	"fsmount",
	"mount_setattr",
	"sethostname",
	"setdomainname",
	"settimeofday",
	"adjtimex",
	"clock_settime",
	"clock_adjtime",

	// Reach privileged kernel instrumentation or another process's memory.
	"bpf",
	"perf_event_open",
	"ptrace",
	"process_vm_readv",
	"process_vm_writev",
	"process_madvise",
	"process_mrelease",
	"pidfd_getfd",
	"pidfd_send_signal",
	"kcmp",

	// Access the kernel keyring.
	"keyctl",
	"add_key",
	"request_key",

	// Load, unload, or replace kernel code.
	"init_module",
	"finit_module",
	"delete_module",
	"create_module",
	"query_module",
	"get_kernel_syms",
	"kexec_load",
	"kexec_file_load",
	"reboot",

	// Create device nodes or modify global kernel/filesystem configuration.
	"mknod",
	"mknodat",
	"ioctl",
	"chmod",
	"fchmod",
	"fchmodat",
	"fchmodat2",
	"chown",
	"fchown",
	"lchown",
	"fchownat",
	"setxattr",
	"lsetxattr",
	"fsetxattr",
	"removexattr",
	"lremovexattr",
	"fremovexattr",
	"utime",
	"utimes",
	"futimesat",
	"utimensat",
	"swapon",
	"swapoff",
	"acct",
	"quotactl",
	"quotactl_fd",
	"setpriority",
	"sched_setparam",
	"sched_setscheduler",
	"sched_setattr",
	"sched_setaffinity",
	"ioprio_set",

	// APIs that expose broad kernel attack surface or bypass normal path opens.
	"io_uring_setup",
	"io_uring_enter",
	"io_uring_register",
	"userfaultfd",
	"open_by_handle_at",
	"name_to_handle_at",
	"fanotify_init",
	"fanotify_mark",
	"iopl",
	"ioperm",
	"syslog",
	"lookup_dcookie",
	"vhangup",
}

// DefaultDenylist returns a copy of the reviewed privileged-worker denylist.
// Callers may safely modify the returned slice.
func DefaultDenylist() []string {
	return append([]string(nil), defaultDenylist...)
}

// RestrictDefault installs the reviewed privileged-worker denylist.
func RestrictDefault() error {
	return Restrict(DefaultDenylist())
}

// aclWriteSyscalls are the syscalls setfacl needs to write POSIX ACL
// extended attributes (system.posix_acl_access / system.posix_acl_default).
//
// Classic seccomp-bpf (and the go-seccomp-bpf library wrapping it here) can
// only compare *immediate* argument values, such as the clone flags below.
// The xattr name is a pointer to a string in the traced process's memory, and
// raw seccomp-bpf has no ability to dereference and compare pointer
// arguments, so there is no way to build a filter that allows setxattr only
// when its name argument is exactly "system.posix_acl_access" or
// "system.posix_acl_default". Argument-level scoping to those two attribute
// names specifically is therefore not achievable at this layer.
//
// Instead these syscalls are allowed only in the one-shot privileged-worker
// process handling a verified rshell:setfacl invocation. That verification
// already comes from a backend-issued, unsigned-but-intersected policy
// (privilegedhelper.VerifiedCommand.AllowedCommands), and this mirrors the
// existing pattern of varying the Landlock policy per verified command in
// trustedPathsForPolicy. removexattr/lremovexattr/fremovexattr are
// deliberately left denied in every case: the setfacl builtin only ever
// creates or replaces ACL entries via -m, never removes the ACL xattr
// outright (-x is out of scope; see AGENTS.md), so there is no legitimate
// caller for the xattr-removal syscalls yet.
var aclWriteSyscalls = []string{"setxattr", "lsetxattr", "fsetxattr"}

// DenylistForCommand returns the reviewed privileged-worker denylist,
// narrowed for the verified command's effective allowlist. When
// allowedCommands contains "rshell:setfacl" (the only builtin that
// legitimately needs to write POSIX ACL xattrs), the setxattr family is
// removed from the denylist so setfacl's ACL writes succeed as real root;
// every other syscall, including the xattr-removal family, remains denied
// exactly as in DefaultDenylist. For every other allowlist the returned list
// is identical to DefaultDenylist.
//
// allowedCommands is the verified, backend-intersected command list
// (privilegedhelper.VerifiedCommand.AllowedCommands), not raw shell input:
// the one-shot worker process runs exactly one command per invocation, and
// this mirrors trustedPathsForCommands scoping Landlock grants the same way.
func DenylistForCommand(allowedCommands []string) []string {
	denylist := DefaultDenylist()
	if !slices.Contains(allowedCommands, "rshell:setfacl") {
		return denylist
	}
	narrowed := make([]string, 0, len(denylist))
	for _, name := range denylist {
		if !slices.Contains(aclWriteSyscalls, name) {
			narrowed = append(narrowed, name)
		}
	}
	return narrowed
}

// RestrictForCommand installs the denylist appropriate for the verified
// command's effective allowlist in the one-shot privileged worker. See
// DenylistForCommand.
func RestrictForCommand(allowedCommands []string) error {
	return Restrict(DenylistForCommand(allowedCommands))
}
