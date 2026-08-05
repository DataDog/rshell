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
// Remove each FILE. Directories are never removed (there is no recursive or
// empty-directory mode). Symlinks are removed without following them: the
// link itself is deleted, the target is left untouched. This command is
// only available in remediation mode.
//
// All file operations go through the AllowedPaths sandbox. Targets outside
// the sandbox are rejected with a permission error before any syscall is
// issued.
//
// Accepted flags:
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
//	-d, --dir             remove empty directories
//	-f, --force           ignore nonexistent files, never prompt
//	-i, --interactive     prompt before every removal
//	-I                    prompt once before removing >3 files
//	--preserve-root       no-op given AllowedPaths; rejected
//	--no-preserve-root    unsafe; rejected
//	--one-file-system     complex semantics; not needed
//
// File-count limit:
//
//	At most MaxRemoveFiles operands (after glob expansion) are accepted per
//	invocation. Exceeding the limit rejects the entire command before any
//	file is removed, so a single mistaken glob cannot delete an unbounded
//	number of files. Split larger cleanups into multiple rm invocations.
//
// Exit codes:
//
//	0  All files processed successfully.
//	1  Missing operand, too many operands, or at least one file failed
//	   (permission denied, is a directory, missing file, etc.). Processing
//	   continues across all operands so a single failure does not abort the
//	   run; exit 1 is returned at the end if any operand failed.
package rm

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/flagparser"
)

// MaxRemoveFiles is the maximum number of file operands accepted by a single
// rm invocation (after glob expansion). Kept small and named so the cap is
// easy to find and adjust.
const MaxRemoveFiles = 10

// Cmd is the rm builtin command descriptor.
var Cmd = builtins.Command{
	Name:            "rm",
	Description:     "remove files",
	MakeFlags:       registerFlags,
	RemediationOnly: true,
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	// --help/--verbose use RegisterNoArgBool rather than fs.BoolP so that
	// an explicit value (rm --verbose=false file, rm --help=false file) is
	// rejected with GNU's "doesn't allow an argument" error instead of
	// silently parsing — a bare fs.BoolP flag accepts --flag=value and
	// would let a malformed no-argument option fall through to deleting
	// the file, since GNU rm's own --help/--verbose take no argument.
	help := flagparser.RegisterNoArgBool(fs, "help", "h", "print usage and exit")
	verbose := flagparser.RegisterNoArgBool(fs, "verbose", "v", "print a message for each removed file")

	return func(ctx context.Context, callCtx *builtins.CallContext, files []string) builtins.Result {
		// Capability check before everything else — including --help — so that
		// rm --help behaves the same as invoking a disallowed command: it
		// fails immediately without showing help text.
		if callCtx.Remove == nil {
			callCtx.Errf("rm: filesystem capability not available (remediation mode required)\n")
			return builtins.Result{Code: 1}
		}

		if *help {
			callCtx.Out("Usage: rm [OPTION]... FILE...\n")
			callCtx.Out("Remove each FILE. Directories are never removed.\n")
			callCtx.Out("Symlinks are removed without following them.\n")
			callCtx.Outf("At most %d files may be removed per invocation.\n\n", MaxRemoveFiles)

			// RegisterNoArgBool uses an unforgeable NUL sentinel for bare
			// flags. Clear it while rendering defaults so help output
			// contains no NUL byte.
			var saved []*builtins.Flag
			fs.VisitAll(func(flag *builtins.Flag) {
				if flag.NoOptDefVal == flagparser.NoArgSentinel {
					saved = append(saved, flag)
					flag.NoOptDefVal = ""
				}
			})
			defer func() {
				for _, flag := range saved {
					flag.NoOptDefVal = flagparser.NoArgSentinel
				}
			}()

			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}

		if len(files) == 0 {
			callCtx.Errf("rm: missing operand\n")
			return builtins.Result{Code: 1}
		}
		if len(files) > MaxRemoveFiles {
			callCtx.Errf("rm: refusing to remove %d files: exceeds the %d-file limit per invocation\n", len(files), MaxRemoveFiles)
			return builtins.Result{Code: 1}
		}

		var failed bool
		for _, file := range files {
			if ctx.Err() != nil {
				return builtins.Result{Code: 1}
			}
			if err := removeFile(ctx, callCtx, file, *verbose); err != nil {
				failed = true
			}
		}

		if failed {
			return builtins.Result{Code: 1}
		}
		return builtins.Result{}
	}
}

// removeFile removes a single file, applying the verbose flag. It returns
// nil on success and a non-nil error (already printed to stderr) on failure.
//
// Design note — why LstatFile is used for the directory guard:
//
// On macOS, os.Root.Remove (unlinkat) returns nil when removing an empty
// directory rather than EISDIR. Relying on Remove's error alone to detect
// "target is a directory" would silently delete empty directories on macOS
// while correctly rejecting them on Linux. The LstatFile pre-check makes the
// rejection consistent across platforms. LstatFile does not follow symlinks,
// so a symlink-to-a-directory argument is treated as a removable symlink,
// not a directory — matching rm's expected behavior.
func removeFile(ctx context.Context, callCtx *builtins.CallContext, path string, verbose bool) error {
	info, err := callCtx.LstatFile(ctx, path)
	if err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		callCtx.Errf("rm: cannot remove '%s': Is a directory\n", builtins.SafeOperand(path))
		return errors.New("is a directory")
	}
	if hasTrailingDirSyntax(path) {
		// path syntactically demands directory semantics (a trailing
		// separator, or a final "." / ".." component). A POSIX trailing
		// slash forces the target to be dereferenced, so re-check with
		// StatFile (follows symlinks) before deciding between "Is a
		// directory" (e.g. a symlink-to-directory operand like "linkdir/")
		// and "Not a directory" (e.g. "file/" or "symlink-to-file/").
		// Without this, path cleaning earlier in the pipeline would
		// silently drop the trailing separator and let rm remove the wrong
		// kind of target.
		//
		// This check does not depend on the LstatFile call above
		// succeeding: LstatFile itself dereferences the trailing slash and
		// fails with ENOENT/ELOOP for a dangling or self-referential
		// symlink, which would otherwise skip straight to callCtx.Remove
		// below and leak an unwrapped, non-GNU-style error message instead
		// of the normalized one StatFile's error produces here.
		target, serr := callCtx.StatFile(ctx, path)
		switch {
		case serr == nil && target.IsDir():
			callCtx.Errf("rm: cannot remove '%s': Is a directory\n", builtins.SafeOperand(path))
			return errors.New("is a directory")
		case serr == nil:
			callCtx.Errf("rm: cannot remove '%s': Not a directory\n", builtins.SafeOperand(path))
			return errors.New("not a directory")
		default:
			// StatFile failed for a reason unrelated to the target's type —
			// e.g. a dangling symlink (No such file or directory) or a
			// symlink whose target escapes every configured AllowedPaths
			// root (permission denied). Propagate the real error instead of
			// misreporting it as "Not a directory".
			callCtx.Errf("rm: cannot remove '%s': %s\n", builtins.SafeOperand(path), callCtx.PortableErr(serr))
			return serr
		}
	}

	if err := callCtx.Remove(ctx, path); err != nil {
		callCtx.Errf("rm: cannot remove '%s': %s\n", builtins.SafeOperand(path), callCtx.PortableErr(err))
		return err
	}

	if verbose {
		callCtx.Outf("removed '%s'\n", path)
	}
	return nil
}

// hasTrailingDirSyntax reports whether path, as literally given,
// syntactically requires its target to resolve as a directory: it ends in a
// path separator, or its final component is "." or "..". GNU/BSD rm reject
// such operands with "Not a directory" (or "Is a directory" if the target,
// dereferenced, actually is one) rather than operating on whatever remains
// after path cleaning drops the trailing separator. "/" is always checked
// since it is the shell's own path-separator syntax; "\" is only a
// separator on Windows — on Unix it is a valid filename character, so a
// path like "foo\" must remain a literal filename there.
func hasTrailingDirSyntax(path string) bool {
	if path == "" {
		return false
	}
	last := path[len(path)-1]
	if last == '/' || (filepath.Separator == '\\' && last == '\\') {
		return true
	}
	base := filepath.Base(path)
	return base == "." || base == ".."
}
