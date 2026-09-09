// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package setfacl implements the setfacl builtin command.
//
// setfacl — set a POSIX ACL group entry on a file or directory
//
// Usage: setfacl [OPTION]... PATH
//
// Adds or replaces a single named-group entry (g:GROUP:PERMS) in the access
// ACL of PATH, or (with -d) in its default ACL. This is a narrow subset of
// GNU setfacl: only one -m entry per invocation, only g: (named group)
// entries, and no -x/--remove, -b/--remove-all, --set, or multi-entry -m
// lists. u:/o:/m: entries are out of scope.
//
// setfacl is Linux-only: POSIX ACL extended attributes
// (system.posix_acl_access / system.posix_acl_default) are a Linux
// filesystem feature with no portable equivalent, and this command exits 1
// with "not supported on this platform" on macOS and Windows.
//
// All file operations go through the AllowedPaths sandbox via
// callCtx.GetACL/SetACL, which resolve PATH the same TOCTOU-safe,
// no-follow, hard-link-rejecting way Truncate does: a target whose *path*
// falls outside the sandbox, or whose underlying regular file has more than
// one hard link, is rejected before any xattr syscall is issued. See the
// hard link entry in AGENTS.md. This command is only available in
// remediation mode.
//
// Accepted flags:
//
//	-m ENTRY, --modify=ENTRY
//	    A single ACL entry of the form g:GROUP:PERMS, where PERMS is zero
//	    to three letters drawn from r, w, x (in any order/combination,
//	    e.g. "rx", "w", ""). GROUP is resolved to a numeric GID via
//	    /etc/group; an unknown group is rejected.
//
//	-R, --recursive
//	    Apply the entry to PATH and, if PATH is a directory, to every file
//	    and subdirectory beneath it. Symlinks are not followed and are
//	    skipped (POSIX ACLs are a property of the referenced inode, not
//	    the link, and this shell never traverses through a symlink for a
//	    write-adjacent operation).
//
//	-d, --default
//	    Apply the entry to the default ACL instead of the access ACL.
//	    Without -R, PATH must be a directory (default ACLs only exist on
//	    directories); with -R, non-directory entries found during the walk
//	    are silently skipped for the default ACL (matching GNU setfacl:
//	    -d is simply inapplicable to a file, not an error).
//
//	-h, --help
//	    Print this usage message to stdout and exit 0.
//
// Out of scope (not implemented; rejected as unknown flags):
//
//	-x, --remove          remove a specific entry
//	-b, --remove-all       remove every extended entry
//	-n, --no-mask          skip mask recomputation
//	--set                  replace the whole ACL from a spec
//	-M FILE, --modify-file=FILE  read entries from a file
//	--restore=FILE         restore ACLs from getfacl-style output
//	-L/-P                  symlink traversal mode (symlinks are never
//	                       followed here, matching every other write
//	                       primitive in this shell)
//
// Exit codes:
//
//	0  PATH (and, with -R, every entry beneath it) was updated
//	   successfully.
//	1  Missing/extra operand, malformed -m entry, unknown group,
//	   remediation mode off, no writable root, not running on Linux, -d
//	   used on a non-directory without -R, or at least one target failed
//	   (permission denied, hard-linked write target, etc.). Recursive
//	   traversal continues across failures so a single failure does not
//	   abort the walk; exit 1 is returned at the end if any target failed.
package setfacl

import (
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"regexp"
	"runtime"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/acl"
	"github.com/DataDog/rshell/builtins/internal/etcgroup"
	"github.com/DataDog/rshell/builtins/internal/flagparser"
)

// Cmd is the setfacl builtin command descriptor.
var Cmd = builtins.Command{
	Name:            "setfacl",
	Description:     "set a POSIX ACL group entry on a file or directory",
	MakeFlags:       registerFlags,
	RemediationOnly: true,
	// Preserve the historical read-only refusal wording; the dispatch gate
	// in interp emits this before flag parsing, and the in-handler check
	// below repeats it as defence in depth.
	RemediationDeniedMessage: readOnlyMessage,
}

const (
	readOnlyMessage    = "setfacl: filesystem capability not available (remediation mode required)\n"
	noWritableRootHint = "setfacl: no writable path is configured (remediation mode requires an AllowedPaths entry with :rw)\n"
)

// maxTraversalDepth bounds -R recursion depth, matching find's
// maxTraversalDepth safety limit for the same reason: an unbounded walk
// (e.g. driven by a symlink-free but arbitrarily deep directory tree) must
// not be able to exhaust the stack or run forever.
const maxTraversalDepth = 256

// entryPattern matches a single g:GROUP:PERMS ACL entry, the only entry
// kind this builtin supports. GROUP is any run of non-colon characters;
// PERMS is zero to three letters drawn from r, w, x, in any order or
// combination (matching GNU setfacl's own -m grammar, which does not
// require positional r/w/x order or a fixed length).
var entryPattern = regexp.MustCompile(`^g:([^:]+):([rwx]{0,3})$`)

// hasWritableRoot reports whether the sandbox has at least one AllowedPaths
// root configured with :rw access. callCtx.SetACL being non-nil only means
// a sandbox exists, not that it grants any writable root, since AllowedPaths
// wires the sandbox even for an empty list or read-only-only entries.
func hasWritableRoot(callCtx *builtins.CallContext) bool {
	if callCtx.AllowedPathsList == nil {
		return false
	}
	for _, p := range callCtx.AllowedPathsList() {
		if p.Access == builtins.AllowedPathReadWrite {
			return true
		}
	}
	return false
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := flagparser.RegisterNoArgBool(fs, "help", "h", "print usage and exit")
	recursive := flagparser.RegisterNoArgBool(fs, "recursive", "R", "apply the entry recursively")
	defaultACL := flagparser.RegisterNoArgBool(fs, "default", "d", "apply the entry to the default ACL")
	modify := fs.StringP("modify", "m", "", "ACL entry to add/replace: g:GROUP:PERMS")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		// Capability check before everything else — including --help — so
		// that setfacl --help behaves the same as invoking a disallowed
		// command: it fails immediately without showing help text.
		//
		// callCtx.SetACL is wired whenever remediation mode is on and any
		// AllowedPaths option was configured at all — even an explicit
		// empty list or read-only-only roots — so a nil check alone cannot
		// detect "remediation mode is on but no writable root exists".
		// Check AllowedPathsList directly so that case gets the same
		// guidance instead of falling through to a per-target "permission
		// denied".
		if !callCtx.RemediationMode {
			callCtx.Errf("%s", readOnlyMessage)
			return builtins.Result{Code: 1}
		}
		if callCtx.SetACL == nil || callCtx.GetACL == nil || !hasWritableRoot(callCtx) {
			callCtx.Errf("%s", noWritableRootHint)
			return builtins.Result{Code: 1}
		}

		if *help {
			callCtx.Out("Usage: setfacl [OPTION]... PATH\n")
			callCtx.Out("Add or replace a single named-group entry (g:GROUP:PERMS) in the\n")
			callCtx.Out("ACL of PATH.\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}

		if !fs.Changed("modify") {
			callCtx.Errf("setfacl: you must specify -m/--modify\n")
			return builtins.Result{Code: 1}
		}
		groupName, permBits, err := parseEntrySyntax(*modify)
		if err != nil {
			callCtx.Errf("setfacl: %s: Invalid argument\n", builtins.SafeOperand(*modify))
			return builtins.Result{Code: 1}
		}

		if len(args) == 0 {
			callCtx.Errf("setfacl: missing operand\n")
			return builtins.Result{Code: 1}
		}
		if len(args) > 1 {
			callCtx.Errf("setfacl: extra operand '%s'\n", builtins.SafeOperand(args[1]))
			return builtins.Result{Code: 1}
		}
		path := args[0]

		// Argument/flag validation above (operand counts, -m syntax) is
		// platform-independent and runs first, matching GNU tool
		// conventions (bad usage is always an error). The platform gate
		// runs next, before the group-name-to-GID lookup below: that
		// lookup goes through etcgroup, which is itself Linux-only and
		// would otherwise mask "not supported on this platform" behind a
		// misleading "Invalid argument" for every -m entry on macOS and
		// Windows, no matter how well-formed. Only the actual ACL work
		// (GID resolution and the xattr syscalls) is gated on platform
		// support, matching lsof's placement.
		if runtime.GOOS != "linux" {
			callCtx.Errf("setfacl: not supported on this platform\n")
			return builtins.Result{Code: 1}
		}

		gid, err := etcgroup.LookupGID(groupName)
		if err != nil {
			callCtx.Errf("setfacl: %s: Invalid argument\n", builtins.SafeOperand(*modify))
			return builtins.Result{Code: 1}
		}

		if *recursive {
			return runRecursive(ctx, callCtx, path, gid, permBits, *defaultACL)
		}
		return runSingle(ctx, callCtx, path, gid, permBits, *defaultACL)
	}
}

// parseEntrySyntax validates and decodes the syntax of a single -m ACL
// entry string, returning the group name and the permission bitmask
// (acl.PermRead|Write|Execute). It does not resolve the group name to a
// GID: that step is platform-specific (see etcgroup) and is deferred to the
// caller, which must run it only after the runtime.GOOS gate.
func parseEntrySyntax(entry string) (groupName string, permBits uint16, err error) {
	m := entryPattern.FindStringSubmatch(entry)
	if m == nil {
		return "", 0, fmt.Errorf("malformed entry")
	}
	groupName, permStr := m[1], m[2]

	for _, c := range permStr {
		switch c {
		case 'r':
			permBits |= acl.PermRead
		case 'w':
			permBits |= acl.PermWrite
		case 'x':
			permBits |= acl.PermExecute
		}
	}
	return groupName, permBits, nil
}

// errDefaultACLNotDir signals that -d was requested for a target that is
// not a directory. runSingle turns this into a fatal GNU-style error;
// runRecursive treats it as "silently inapplicable" for a non-root target,
// matching GNU setfacl's behaviour under -R -d.
var errDefaultACLNotDir = errors.New("setfacl: only directories can have default ACLs")

// applyEntry fetches the current ACL(s) of path, upserts the named-group
// entry, and writes the result back. When useDefault is true the entry is
// upserted into the default ACL (path must be a directory); otherwise it is
// upserted into the access ACL.
//
// The access xattr is always re-encoded and rewritten (even when only the
// default ACL changes), because Sandbox.SetACL writes access unconditionally
// and a nil/empty access buffer would corrupt the file's effective
// permissions. When GetACL reports no existing ACL (access/def == nil),
// acl.MinimalACLFromMode synthesizes the equivalent 3-entry ACL from the
// file's mode bits, matching what a fresh setfacl invocation on a
// mode-only file would otherwise materialize.
func applyEntry(ctx context.Context, callCtx *builtins.CallContext, path string, info iofs.FileInfo, gid uint32, permBits uint16, useDefault bool) error {
	access, def, err := callCtx.GetACL(ctx, path)
	if err != nil {
		return err
	}

	accessEntries, err := decodeOrMinimal(access, info.Mode())
	if err != nil {
		return err
	}

	if useDefault {
		if !info.IsDir() {
			return errDefaultACLNotDir
		}
		defEntries, err := decodeOrMinimal(def, info.Mode())
		if err != nil {
			return err
		}
		defEntries = acl.UpsertGroupEntry(defEntries, gid, permBits)
		return callCtx.SetACL(ctx, path, acl.EncodeACL(accessEntries), acl.EncodeACL(defEntries))
	}

	accessEntries = acl.UpsertGroupEntry(accessEntries, gid, permBits)
	return callCtx.SetACL(ctx, path, acl.EncodeACL(accessEntries), nil)
}

// decodeOrMinimal decodes an existing ACL xattr, or synthesizes the minimal
// 3-entry ACL from mode when no xattr is set (data == nil, i.e. ENODATA).
func decodeOrMinimal(data []byte, mode os.FileMode) ([]acl.Entry, error) {
	if data == nil {
		return acl.MinimalACLFromMode(mode), nil
	}
	return acl.DecodeACL(data)
}

// runSingle applies the entry to exactly one target (no -R).
func runSingle(ctx context.Context, callCtx *builtins.CallContext, path string, gid uint32, permBits uint16, useDefault bool) builtins.Result {
	info, err := callCtx.LstatFile(ctx, path)
	if err != nil {
		callCtx.Errf("setfacl: %s: %s\n", builtins.SafeOperand(path), callCtx.PortableErr(err))
		return builtins.Result{Code: 1}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		callCtx.Errf("setfacl: %s: Operation not supported\n", builtins.SafeOperand(path))
		return builtins.Result{Code: 1}
	}

	if err := applyEntry(ctx, callCtx, path, info, gid, permBits, useDefault); err != nil {
		if errors.Is(err, errDefaultACLNotDir) {
			callCtx.Errf("setfacl: %s: Only directories can have default ACLs\n", builtins.SafeOperand(path))
		} else {
			callCtx.Errf("setfacl: %s: %s\n", builtins.SafeOperand(path), callCtx.PortableErr(err))
		}
		return builtins.Result{Code: 1}
	}
	return builtins.Result{}
}

// runRecursive applies the entry to startPath and, if it is a directory,
// to every entry beneath it. The walk is an iterative stack of streamed
// directory handles (one ReadDir(1) at a time), modeled on find.go's
// walkPath, so memory stays proportional to tree depth rather than width.
// Symlinks are never followed: LstatFile is used throughout, so a symlink
// is visited as itself (and rejected, like runSingle does) rather than
// descended into.
func runRecursive(ctx context.Context, callCtx *builtins.CallContext, startPath string, gid uint32, permBits uint16, useDefault bool) builtins.Result {
	var failed bool

	apply := func(path string, info iofs.FileInfo) {
		if info.Mode()&os.ModeSymlink != 0 {
			callCtx.Errf("setfacl: %s: Operation not supported\n", builtins.SafeOperand(path))
			failed = true
			return
		}
		if err := applyEntry(ctx, callCtx, path, info, gid, permBits, useDefault); err != nil {
			if errors.Is(err, errDefaultACLNotDir) {
				// -R -d on a non-directory target: GNU setfacl silently
				// skips the default ACL for files found during the walk
				// rather than treating it as an error.
				return
			}
			callCtx.Errf("setfacl: %s: %s\n", builtins.SafeOperand(path), callCtx.PortableErr(err))
			failed = true
		}
	}

	startInfo, err := callCtx.LstatFile(ctx, startPath)
	if err != nil {
		callCtx.Errf("setfacl: %s: %s\n", builtins.SafeOperand(startPath), callCtx.PortableErr(err))
		return builtins.Result{Code: 1}
	}
	apply(startPath, startInfo)

	type dirIterator struct {
		dir        iofs.ReadDirFile
		parentPath string
		depth      int
		done       bool
	}
	var iterStack []*dirIterator

	if startInfo.Mode()&os.ModeSymlink == 0 && startInfo.IsDir() {
		dir, openErr := callCtx.OpenDir(ctx, startPath)
		if openErr != nil {
			callCtx.Errf("setfacl: %s: %s\n", builtins.SafeOperand(startPath), callCtx.PortableErr(openErr))
			return builtins.Result{Code: 1}
		}
		iterStack = append(iterStack, &dirIterator{dir: dir, parentPath: startPath, depth: 1})
	}

	for len(iterStack) > 0 {
		if ctx.Err() != nil {
			failed = true
			break
		}

		top := iterStack[len(iterStack)-1]
		if top.done {
			top.dir.Close() //nolint:errcheck
			iterStack = iterStack[:len(iterStack)-1]
			continue
		}

		entries, readErr := top.dir.ReadDir(1)
		if readErr != nil {
			if !errors.Is(readErr, iofs.ErrClosed) {
				callCtx.Errf("setfacl: %s: %s\n", builtins.SafeOperand(top.parentPath), callCtx.PortableErr(readErr))
			}
			top.done = true
			continue
		}
		if len(entries) == 0 {
			top.done = true
			continue
		}

		child := entries[0]
		childPath := joinPath(top.parentPath, child.Name())

		childInfo, err := callCtx.LstatFile(ctx, childPath)
		if err != nil {
			callCtx.Errf("setfacl: %s: %s\n", builtins.SafeOperand(childPath), callCtx.PortableErr(err))
			failed = true
			continue
		}

		apply(childPath, childInfo)

		if childInfo.Mode()&os.ModeSymlink == 0 && childInfo.IsDir() && top.depth < maxTraversalDepth {
			dir, openErr := callCtx.OpenDir(ctx, childPath)
			if openErr != nil {
				callCtx.Errf("setfacl: %s: %s\n", builtins.SafeOperand(childPath), callCtx.PortableErr(openErr))
				failed = true
				continue
			}
			iterStack = append(iterStack, &dirIterator{dir: dir, parentPath: childPath, depth: top.depth + 1})
		}
	}

	for _, it := range iterStack {
		it.dir.Close() //nolint:errcheck
	}

	if failed {
		return builtins.Result{Code: 1}
	}
	return builtins.Result{}
}

// joinPath joins a parent directory and a child name with '/', without the
// lexical cleaning filepath.Join performs (which would collapse a leading
// "./" the caller may have passed as the operand). setfacl is Linux-only,
// so '/' is always the correct separator.
func joinPath(dir, name string) string {
	if len(dir) == 0 {
		return name
	}
	if dir[len(dir)-1] == '/' {
		return dir + name
	}
	return dir + "/" + name
}
