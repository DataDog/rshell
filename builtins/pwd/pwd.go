// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package pwd implements the pwd builtin command.
//
// pwd — print the absolute pathname of the current working directory
//
// Usage: pwd [-LP] [--help]
//
// Print the absolute pathname of the shell's current working directory
// to standard output. Stdin is not used. Extra positional arguments are
// ignored to match GNU coreutils and bash behavior.
//
// Flags:
//
//	-L, --logical   Use the shell's tracked logical path (default).
//	                The path may contain symbolic links, exactly as the
//	                user reached the directory. This matches the POSIX
//	                default and the bash builtin default.
//	-P, --physical  Resolve all symbolic links so the printed path contains
//	                no symlinks and no "." or ".." components.
//	-h, --help      Print usage to stdout and exit 0.
//
// If both -L and -P are given on the same invocation, the last one wins
// (matches POSIX and bash, implemented via pflag's declaration-order
// Visit traversal).
//
// Symlink resolution for -P is sandbox-safe: it walks the absolute path
// component-by-component using callCtx.LstatFile and callCtx.ReadlinkFile,
// never calling os.Lstat / os.Readlink directly. The total number of
// symlink expansions is capped at maxSymlinkHops (40) to defeat cycles,
// matching the Linux ELOOP limit.
//
// Sandbox best-effort for -P: if a path component lies outside the
// AllowedPaths sandbox (a common case when the working directory is
// itself the sandbox root), the resolver cannot walk that component and
// gives up gracefully — falling back to the logical path. This means
// "pwd -P" never produces a hard error from the sandbox; it returns the
// best canonical path it can compute.
//
// Exit codes:
//
//	0  Success — a working directory was written (logical or physical).
//	1  Error — invalid flag, or the runner exposes no working directory.
package pwd

import (
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"path/filepath"
	"strings"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the pwd builtin command descriptor. Help is intentionally not
// set: pwd registers flags, so the `help` builtin invokes `pwd --help`
// to produce its description, keeping `help pwd` and `pwd --help`
// identical.
var Cmd = builtins.Command{
	Name:        "pwd",
	Description: "print working directory",
	MakeFlags:   makeFlags,
}

// maxSymlinkHops caps the number of symlink expansions during -P
// resolution. Matches the Linux ELOOP limit (40) so that pathological
// or cyclic chains terminate with a clear error.
const maxSymlinkHops = 40

// errSymlinkLoop is returned when -P resolution exceeds maxSymlinkHops.
var errSymlinkLoop = errors.New("too many levels of symbolic links")

func makeFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	helpFlag := fs.BoolP("help", "h", false, "print usage and exit")
	// -L and -P are deliberately not bound to local pointers: the
	// command-line ordering matters (last-wins per POSIX), so the
	// handler consults pflag.FlagSet.Visit in pickPhysical(fs)
	// rather than reading the bool values directly.
	fs.BoolP("logical", "L", false, "use the shell's working directory, even if it contains symlinks (default)")
	fs.BoolP("physical", "P", false, "resolve all symlinks before printing the path")

	return func(ctx context.Context, callCtx *builtins.CallContext, _ []string) builtins.Result {
		if *helpFlag {
			printHelp(callCtx)
			return builtins.Result{}
		}

		if callCtx.WorkDir == nil {
			callCtx.Errf("pwd: no working directory available\n")
			return builtins.Result{Code: 1}
		}

		cwd := callCtx.WorkDir()
		if cwd == "" {
			callCtx.Errf("pwd: working directory is empty\n")
			return builtins.Result{Code: 1}
		}

		if pickPhysical(fs) {
			// Best-effort: if symlink resolution fails (typically because
			// the cwd is the sandbox root and we cannot walk above it),
			// fall back to the logical path silently. Cycles still error
			// because they indicate corrupt input, not a sandbox limit.
			if resolved, err := resolveSymlinks(ctx, callCtx, cwd); err == nil {
				cwd = resolved
			} else if errors.Is(err, errSymlinkLoop) {
				callCtx.Errf("pwd: %s\n", err)
				return builtins.Result{Code: 1}
			}
		}

		callCtx.Outf("%s\n", cwd)
		return builtins.Result{}
	}
}

// pickPhysical reports whether to resolve symlinks (-P). Walks fs.Visit
// in declaration order so that when both -L and -P appear, the last one
// wins (POSIX). When neither is set, returns false (logical default).
func pickPhysical(fs *builtins.FlagSet) bool {
	usePhysical := false
	fs.Visit(func(f *builtins.Flag) {
		switch f.Name {
		case "logical":
			usePhysical = false
		case "physical":
			usePhysical = true
		}
	})
	return usePhysical
}

func printHelp(callCtx *builtins.CallContext) {
	callCtx.Out("Usage: pwd [-LP] [--help]\n")
	callCtx.Out("Print the absolute pathname of the current working directory.\n\n")
	callCtx.Out("  -L, --logical   use the shell's tracked working directory, even if it\n")
	callCtx.Out("                  contains symlinks (default)\n")
	callCtx.Out("  -P, --physical  resolve all symlinks before printing the path\n")
	callCtx.Out("  -h, --help      display this help and exit\n")
}

// resolveSymlinks returns the canonical (no-symlinks, no "." or "..")
// absolute form of path. It walks path component-by-component using the
// sandbox-safe LstatFile and ReadlinkFile from callCtx, so it never
// touches the filesystem directly. The number of symlink expansions is
// capped at maxSymlinkHops to defeat cycles.
//
// Algorithm: maintain `out` (the already-resolved prefix) and `rest`
// (the remaining unresolved suffix, always relative). Pop one component
// off rest at a time. If the component is a symlink, prepend the link
// target back onto rest (and reset out to the absolute root if the
// target is absolute). Continue until rest is empty.
func resolveSymlinks(ctx context.Context, callCtx *builtins.CallContext, path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("not an absolute path: %s", path)
	}
	if callCtx.LstatFile == nil || callCtx.ReadlinkFile == nil {
		return "", errors.New("sandbox does not support symlink resolution")
	}

	cleaned := filepath.Clean(path)
	out := rootPrefix(cleaned)
	rest := strings.TrimPrefix(cleaned, out)

	hops := 0
	for rest != "" {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		// Consume any leading separator(s) before extracting the next
		// component. filepath.Clean collapses runs of separators, but
		// expanded symlink targets (which we splice back into rest)
		// can reintroduce them.
		for len(rest) > 0 && rest[0] == filepath.Separator {
			rest = rest[1:]
		}
		if rest == "" {
			break
		}

		var comp string
		if i := strings.IndexByte(rest, filepath.Separator); i >= 0 {
			comp = rest[:i]
			rest = rest[i:]
		} else {
			comp = rest
			rest = ""
		}

		switch comp {
		case ".":
			continue
		case "..":
			out = parentDir(out)
			continue
		}

		candidate := joinPath(out, comp)
		info, err := callCtx.LstatFile(ctx, candidate)
		if err != nil {
			// Cannot stat through the sandbox — typically because the
			// path is above the AllowedPaths root. Treat the component
			// as opaque (not a symlink) and continue. This is what makes
			// -P resolution work when the cwd is somewhere under the
			// sandbox root: components above the root pass through, and
			// only the symlinks we can actually inspect get resolved.
			out = candidate
			continue
		}

		if info.Mode()&iofs.ModeSymlink == 0 {
			out = candidate
			continue
		}

		hops++
		if hops > maxSymlinkHops {
			return "", errSymlinkLoop
		}

		target, err := callCtx.ReadlinkFile(ctx, candidate)
		if err != nil {
			// Lstat said it's a symlink but readlink failed. Treat the
			// component as opaque rather than aborting resolution.
			out = candidate
			continue
		}

		if filepath.IsAbs(target) {
			cleanedTarget := filepath.Clean(target)
			out = rootPrefix(cleanedTarget)
			rest = strings.TrimPrefix(cleanedTarget, out) + rest
		} else {
			// filepath.Clean normalizes the relative target — collapsing
			// "." segments and converting forward slashes to the host
			// separator on Windows. Without it, a target like "./real"
			// keeps its forward slash on Windows and the walking loop
			// (which splits on filepath.Separator) treats "./real" as a
			// single opaque component.
			rest = string(filepath.Separator) + filepath.Clean(target) + rest
		}
	}

	if out == "" {
		out = string(filepath.Separator)
	}
	return out, nil
}

// rootPrefix returns the leading absolute-path root for path. On Unix
// this is always "/". On Windows it preserves the drive letter or UNC
// volume so subsequent component walking does not mistake "C:" for a
// path component.
func rootPrefix(path string) string {
	vol := filepath.VolumeName(path)
	if vol != "" {
		if len(path) > len(vol) && path[len(vol)] == filepath.Separator {
			return vol + string(filepath.Separator)
		}
		return vol
	}
	return string(filepath.Separator)
}

// parentDir returns the directory containing dir, preserving the volume
// root. If dir is already at the root, parentDir returns dir unchanged.
func parentDir(dir string) string {
	root := rootPrefix(dir)
	if dir == root {
		return dir
	}
	parent := filepath.Dir(dir)
	if parent == "." {
		return root
	}
	return parent
}

// joinPath joins dir and a single non-empty component without using
// filepath.Join (which collapses ".." and may erase volume roots).
func joinPath(dir, comp string) string {
	if dir == "" {
		return comp
	}
	if dir[len(dir)-1] == filepath.Separator {
		return dir + comp
	}
	return dir + string(filepath.Separator) + comp
}
