// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package usermod implements a narrow subset of usermod: adding a user to
// one or more supplementary groups.
//
// Usage: usermod -aG GROUP[,GROUP...] USER
//
// This is not a general-purpose usermod. It exists to run exactly one
// remediation: the Docker log-permission fix for the
// docker_file_tailing_disabled Agent Health issue, which needs
//
//	sudo usermod -aG docker dd-agent
//	sudo systemctl restart datadog-agent
//
// (see DataDog/datadog-agent's
// comp/healthplatform/issues/dockerpermissions/fix-docker-socket-permissions.sh
// and issue.go's buildLinux for the canonical remediation text). Only the
// -a (append) plus -G (supplementary groups) combination is supported, with
// exactly one USER operand; every other usermod flag and every other
// operand shape is out of scope and rejected as an unknown flag or a wrong
// number of operands, matching setfacl's documented "deliberately narrow"
// pattern (see builtins/setfacl/setfacl.go).
//
// Deliberately out of scope (not implemented; rejected as unknown
// flags/usage, never silently accepted with different semantics):
//
//   - -G without -a: GNU usermod's bare -G *replaces* a user's entire
//     supplementary group list, dropping every group not named on the
//     command line. That is a materially more destructive operation than
//     -aG's append-only semantics, and no known remediation needs it. This
//     builtin rejects -G unless -a is also given, rather than either
//     implementing replace semantics or (worse) silently treating -G alone
//     as -aG.
//   - -d/--home, -m/--move-home, -s/--shell, -u/--uid, -g/--gid,
//     -p/--password, -L/--lock, -U/--unlock, -e/--expiredate,
//     -f/--inactive, -c/--comment, -l/--login, -o/--non-unique,
//     -R/--root, -P/--prefix, -Z/--selinux-user, --add-subuids,
//     --add-subgids, and every other usermod flag: none of these are
//     needed by the target remediation, and several (password changes,
//     UID/GID changes, home directory moves) are considerably higher-risk
//     operations this shell's sandbox model is not designed to authorize.
//
// # Multi-group syntax
//
// Real GNU usermod's -aG accepts a comma-separated list of groups
// (`usermod -aG docker,adm dd-agent`), appending the user to every one.
// The target remediation only ever needs one group (docker), but
// supporting the comma-separated form costs nothing extra given the
// underlying implementation (etcgroup.AddMember is already called once per
// group operand-adjacent GROUP token), so it is supported for parity with
// real usermod rather than deferred.
//
// # Platform support and privilege model
//
// usermod is Linux-only, gated purely on remediation mode (RemediationOnly
// = true) rather than on the AllowedPaths sandbox: /etc/group is a fixed
// system file, never a caller-supplied path, so there is no PATH operand
// for AllowedPaths to scope (contrast setfacl, whose PATH operand is a
// user-chosen file that must live under an AllowedPaths :rw root). The
// entire capability is "can this verified command touch /etc/group and
// /etc/passwd at all", which is exactly what RemediationOnly plus the
// privileged worker's Landlock trusted-path grant (see
// trustedPathsForCommands in cmd/rshell/privileged_worker_linux.go) decide
// for the rshell:usermod command name — the same "no writable root
// required" reasoning setfacl documents does not apply here because no
// path ever flows through the AllowedPaths sandbox at all. See
// builtins/internal/etcgroup's package doc comment for the sandbox-bypass
// rationale and the write-path (atomic rename, no /etc/.pwd.lock)
// crash-safety design.
//
// # Seccomp
//
// Unlike setfacl (which needs the setxattr family to write POSIX ACL
// xattrs — see internal/sandbox/seccomp.DenylistForCommand), usermod needs
// no seccomp denylist carve-out. The underlying etcgroup.AddMember write
// path uses only open/openat, read, write, fsync, close, rename, unlink,
// and umask — none of which are in the default privileged-worker denylist
// — and deliberately avoids chmod/fchmod/chown/fchown by matching the
// temp file's permission bits at creation time via a temporary umask reset
// instead of a post-creation chmod (see etcgroup_linux.go's writeAtomic).
package usermod

import (
	"context"
	"errors"
	"runtime"
	"strings"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/etcgroup"
	"github.com/DataDog/rshell/builtins/internal/etcpasswd"
	"github.com/DataDog/rshell/builtins/internal/flagparser"
)

// Cmd is the usermod builtin command descriptor.
var Cmd = builtins.Command{
	Name:            "usermod",
	Description:     "append a user to one or more supplementary groups (usermod -aG GROUP USER only)",
	MakeFlags:       registerFlags,
	RemediationOnly: true,
	// Preserve the historical read-only refusal wording; the dispatch gate
	// in interp emits this before flag parsing, and the in-handler check
	// below repeats it as defence in depth.
	RemediationDeniedMessage: readOnlyMessage,
}

const readOnlyMessage = "usermod: filesystem capability not available (remediation mode required)\n"

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := flagparser.RegisterNoArgBool(fs, "help", "h", "print usage and exit")
	appendMode := flagparser.RegisterNoArgBool(fs, "append", "a", "append to the supplementary group(s) given by -G, rather than replacing them (required; -G without -a is not supported)")
	groups := fs.StringP("groups", "G", "", "comma-separated list of supplementary GROUP names to append USER to (requires -a)")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		// Capability check before everything else — including --help — so
		// that usermod --help behaves the same as invoking a disallowed
		// command: it fails immediately without showing help text. Matches
		// setfacl and systemctl's placement of this check.
		if !callCtx.RemediationMode {
			callCtx.Errf("%s", readOnlyMessage)
			return builtins.Result{Code: 1}
		}

		if *help {
			printHelp(callCtx, fs)
			return builtins.Result{}
		}

		if !fs.Changed("groups") {
			callCtx.Errf("usermod: you must specify -G/--groups (with -a/--append)\n")
			return builtins.Result{Code: 1}
		}
		if !*appendMode {
			callCtx.Errf("usermod: -G/--groups is only supported together with -a/--append; replacing a user's entire supplementary group list (-G without -a) is not supported by this builtin\n")
			return builtins.Result{Code: 1}
		}

		groupNames, err := parseGroupList(*groups)
		if err != nil {
			callCtx.Errf("usermod: %s: %s\n", builtins.SafeOperand(*groups), err)
			return builtins.Result{Code: 1}
		}

		if len(args) == 0 {
			callCtx.Errf("usermod: missing operand (USER)\n")
			return builtins.Result{Code: 1}
		}
		if len(args) > 1 {
			callCtx.Errf("usermod: extra operand %s\n", builtins.SafeOperand(args[1]))
			return builtins.Result{Code: 1}
		}
		user := args[0]

		// Argument/flag validation above (operand counts, -G syntax) is
		// platform-independent and runs first, matching setfacl's ordering
		// rationale: bad usage is always an error, checked before the
		// platform gate, so a malformed invocation gets a consistent error
		// on every platform rather than a misleading "not supported" that
		// would mask the real problem. Only the actual /etc/passwd and
		// /etc/group work is gated on platform support.
		if runtime.GOOS != "linux" {
			callCtx.Errf("usermod: not supported on this platform\n")
			return builtins.Result{Code: 1}
		}

		if ctx.Err() != nil {
			return builtins.Result{Code: 1}
		}

		userExists, err := etcpasswd.UserExists(user)
		if err != nil {
			callCtx.Errf("usermod: %s: %s\n", builtins.SafeOperand(user), callCtx.PortableErr(err))
			return builtins.Result{Code: 1}
		}
		if !userExists {
			callCtx.Errf("usermod: user '%s' does not exist\n", builtins.SafeOperand(user))
			return builtins.Result{Code: 1}
		}

		// Apply every GROUP operand in turn, continuing past a failure on
		// one group so the remaining groups still get processed — matching
		// setfacl's own operand loop (and this shell's rm builtin) for a
		// command that accepts more than one target in a single
		// invocation. The overall exit code is 1 if any group failed.
		var failed bool
		for _, groupName := range groupNames {
			if ctx.Err() != nil {
				return builtins.Result{Code: 1}
			}
			if err := etcgroup.AddMember(groupName, user); err != nil {
				if errors.Is(err, etcgroup.ErrGroupNotFound) {
					callCtx.Errf("usermod: group '%s' does not exist\n", builtins.SafeOperand(groupName))
				} else {
					callCtx.Errf("usermod: %s: %s\n", builtins.SafeOperand(groupName), callCtx.PortableErr(err))
				}
				failed = true
				continue
			}
		}

		if failed {
			return builtins.Result{Code: 1}
		}
		return builtins.Result{}
	}
}

// parseGroupList validates and splits a -G value into its comma-separated
// group names, rejecting an empty list or any empty group name (e.g. a
// leading/trailing/doubled comma), matching GNU usermod's own rejection of
// a malformed -G list.
func parseGroupList(value string) ([]string, error) {
	if value == "" {
		return nil, errNoGroups
	}
	parts := strings.Split(value, ",")
	names := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			return nil, errEmptyGroupName
		}
		names = append(names, p)
	}
	return names, nil
}

var (
	errNoGroups       = groupListError("empty group list")
	errEmptyGroupName = groupListError("invalid group list (empty group name)")
)

// groupListError is a trivial string error type so parseGroupList's
// sentinel errors format cleanly via %s in the caller's Errf call, without
// pulling in the "errors" package for just two fixed messages.
type groupListError string

func (e groupListError) Error() string { return string(e) }

func printHelp(callCtx *builtins.CallContext, fs *builtins.FlagSet) {
	callCtx.Out("Usage: usermod -aG GROUP[,GROUP...] USER\n")
	callCtx.Out("Append USER to the supplementary member list of each GROUP in /etc/group.\n\n")
	callCtx.Out("This is a narrow subset of GNU usermod: only -a/--append combined with\n")
	callCtx.Out("-G/--groups is supported, appending USER to one or more existing groups.\n")
	callCtx.Out("-G without -a (which replaces a user's entire supplementary group list)\n")
	callCtx.Out("is not supported, and no other usermod flag is supported.\n\n")
	fs.SetOutput(callCtx.Stdout)
	fs.PrintDefaults()
}
