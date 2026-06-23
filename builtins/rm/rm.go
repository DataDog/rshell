// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package rm implements the rm builtin command.
//
// rm — remove files
//
// Usage: rm [OPTION]... FILE...
//
// Remove each FILE. Does not remove directories unless -d is specified (and
// only then if the directory is empty). Symlinks are removed without following
// them (the link itself is deleted; the target is not touched). This command
// is only available in remediation mode.
//
// All file operations go through the AllowedPaths sandbox. Targets outside
// the sandbox are rejected with a permission error before any syscall is
// issued.
//
// Accepted flags:
//
//	-f, --force
//	    Ignore nonexistent files. When a FILE does not exist and --force is
//	    given, the missing file is silently skipped and exit 0 is returned
//	    (assuming all other files succeeded). Without --force, a missing file
//	    produces an error and exit 1.
//
//	-d, --dir
//	    Allow removal of empty directories. Without this flag, rm rejects
//	    any argument that is a directory with "Is a directory". With --dir,
//	    rm attempts to remove the directory; the OS rejects it if it is not
//	    empty (ENOTEMPTY / directory not empty).
//
//	-v, --verbose
//	    Print "removed 'FILE'" to stdout for each file successfully removed.
//	    Useful for audit trails in remediation scripts.
//
//	-h, --help
//	    Print this usage message to stdout and exit 0.
//
// Out of scope (not implemented; rejected as unknown flags):
//
//	-r, -R, --recursive   remove directories and their contents recursively
//	-i, --interactive     prompt before every removal
//	-I                    prompt once before removing >3 files
//	--preserve-root       no-op given AllowedPaths; rejected
//	--no-preserve-root    unsafe; rejected
//	--one-file-system     complex semantics; not needed
//
// Exit codes:
//
//	0  All files processed successfully (including nonexistent files with -f).
//	1  At least one file failed (permission denied, directory without -d,
//	   non-empty directory with -d, missing file without -f, etc.).
//	   Processing continues across all operands; exit 1 is returned at the
//	   end if any operand failed.
package rm

import (
	"context"
	"errors"
	"os"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the rm builtin command descriptor.
var Cmd = builtins.Command{
	Name:            "rm",
	Description:     "remove files",
	MakeFlags:       registerFlags,
	RemediationOnly: true,
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := builtins.NoArgBool(fs, "help", "h", "print usage and exit")
	force := builtins.NoArgBool(fs, "force", "f", "ignore nonexistent files, never prompt")
	dir := builtins.NoArgBool(fs, "dir", "d", "remove empty directories")
	verbose := builtins.NoArgBool(fs, "verbose", "v", "print a message for each removed file")

	return func(ctx context.Context, callCtx *builtins.CallContext, files []string) builtins.Result {
		// Capability check before everything else — including --help — so that
		// rm --help behaves the same as invoking a disallowed command:
		// it fails immediately without showing help text.
		if callCtx.Remove == nil {
			callCtx.Errf("rm: filesystem capability not available (remediation mode required)\n")
			return builtins.Result{Code: 1}
		}

		if *help {
			callCtx.Out("Usage: rm [OPTION]... FILE...\n")
			callCtx.Out("Remove each FILE.\n")
			callCtx.Out("Does not remove directories unless -d is specified (and only then if empty).\n")
			callCtx.Out("Symlinks are removed without following them.\n\n")
			fs.SetOutput(callCtx.Stdout)
			builtins.PrintFlagDefaults(fs)
			return builtins.Result{}
		}

		if len(files) == 0 {
			// GNU rm: "rm -f" with no operands is defined to succeed silently.
			if *force {
				return builtins.Result{}
			}
			callCtx.Errf("rm: missing operand\n")
			return builtins.Result{Code: 1}
		}

		var failed bool
		for _, file := range files {
			if ctx.Err() != nil {
				return builtins.Result{Code: 1}
			}
			if err := removeFile(ctx, callCtx, file, *force, *dir, *verbose); err != nil {
				failed = true
			}
		}

		if failed {
			return builtins.Result{Code: 1}
		}
		return builtins.Result{}
	}
}

// removeFile removes a single file, applying the force, dir, and verbose flags.
// It returns nil on success (including force-suppressed missing-file errors),
// and a non-nil error (already printed to stderr) on failure.
//
// Design note — why LstatFile is used for the directory guard:
//
// On macOS, os.Root.Remove (unlinkat) returns nil when removing an empty
// directory, and ENOTEMPTY for a non-empty one. It never returns EISDIR.
// So relying on Remove's error alone to detect "target is a directory" would
// silently delete empty directories when -d is not given on macOS, which
// contradicts GNU rm behaviour. The LstatFile pre-check is therefore necessary
// for correct cross-platform semantics — not just an optimisation.
//
// Separately, LstatFile errors go through allowedpaths.PortablePathError which
// replaces the inner sentinel (e.g. syscall.ENOENT) with errors.New(…),
// breaking errors.Is. We therefore use LstatFile only for the type check
// (where we care about the FileInfo, not the error value) and rely on Remove's
// raw error chain for ENOENT detection under -f.
func removeFile(ctx context.Context, callCtx *builtins.CallContext, path string, force, allowDir, verbose bool) error {
	// Detect directories before attempting removal. LstatFile does not follow
	// symlinks, so a symlink argument is treated as a file even if its target
	// is a directory. We only care about the type here; stat errors are ignored
	// and Remove surfaces its own (more precise) error below.
	if !allowDir {
		if info, err := callCtx.LstatFile(ctx, path); err == nil && info.IsDir() {
			callCtx.Errf("rm: cannot remove '%s': Is a directory\n", path)
			return errors.New("is a directory")
		}
	}

	// Attempt removal. Sandbox.Remove preserves the raw syscall error chain,
	// so errors.Is(err, os.ErrNotExist) correctly detects ENOENT on all
	// platforms without the PortablePathError wrapping issue.
	if err := callCtx.Remove(ctx, path); err != nil {
		if force && errors.Is(err, os.ErrNotExist) {
			return nil
		}
		callCtx.Errf("rm: cannot remove '%s': %s\n", path, callCtx.PortableErr(err))
		return err
	}

	if verbose {
		callCtx.Outf("removed '%s'\n", path)
	}
	return nil
}
