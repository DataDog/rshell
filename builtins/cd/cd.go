// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package cd implements the cd builtin command.
//
// cd — change the shell's working directory
//
// Usage: cd [-LP] [-|DIRECTORY]
//
// Change the shell's current working directory. The new directory must
// resolve to a path inside the AllowedPaths sandbox; targets outside the
// sandbox are rejected with exit 1 and the working directory is left
// unchanged.
//
// Operands:
//
//	(none)       Change to the directory named by $HOME. If HOME is not
//	             set, an error is printed and exit 1 is returned.
//	-            Change to the directory named by $OLDPWD. If OLDPWD is
//	             not set (no successful cd has happened yet in this
//	             session), an error is printed and exit 1 is returned.
//	             On success the new absolute path is written to stdout
//	             (matching POSIX / bash).
//	DIRECTORY    Change to DIRECTORY. Relative paths are resolved against
//	             the shell's current working directory.
//
// Flags:
//
//	-L, --logical   Resolve `..` lexically; symbolic links in the result
//	                are preserved. This is the default and matches POSIX.
//	-P, --physical  Resolve symbolic links before processing `..`. The
//	                resolution is sandbox-safe — components the sandbox
//	                cannot inspect (paths above the AllowedPaths root,
//	                or the root path itself if it is a symlink) pass
//	                through unresolved.
//	-h, --help      Print usage to stdout and exit 0.
//
// If both -L and -P are given on the same invocation, the last one wins
// (matches POSIX and bash). Multiple positional operands are rejected
// with exit 1.
//
// Side effects on success:
//
//	$OLDPWD is set to the previous working directory.
//	$PWD    is set to the new working directory.
//	The shell's tracked working directory is updated; subsequent file
//	operations resolve relative paths against the new directory.
//
// Exit codes:
//
//	0  Success — the working directory was changed.
//	1  Error — invalid flag, missing target, target outside AllowedPaths,
//	   target is not a directory, or HOME/OLDPWD was required but unset.
//
// Bash extensions intentionally not implemented:
//
//	-e          Only meaningful with -P when getcwd fails; not reachable
//	            here because rshell maintains the working directory
//	            internally via runner.Dir.
//	-@          AIX-only extended-attribute view.
//	CDPATH      Implicit directory search via $CDPATH is a path-confusion
//	            risk in a restricted shell.
//	~user       User-home expansion is a parser concern, not cd's.
//	cdable_vars Bash extension that treats unknown directories as variable
//	            names; off by default in bash too.
package cd

import (
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the cd builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "cd",
	Description: "change the working directory",
	MakeFlags:   makeFlags,
}

// maxSymlinkHops bounds the number of symlink expansions done in -P
// mode. Mirrors the Linux ELOOP limit and matches pwd -P. Cyclic
// chains terminate with a clear error rather than spinning.
const maxSymlinkHops = 40

// errSymlinkLoop is returned when -P resolution exceeds maxSymlinkHops.
var errSymlinkLoop = errors.New("too many levels of symbolic links")

// errBoolSeqValue is returned by boolSeqFlag.Set when the user passes
// an explicit value to -L / -P (e.g. `cd --physical=true`). Defined at
// package scope so each invocation reuses the same error value rather
// than allocating per-call.
var errBoolSeqValue = errors.New("option doesn't allow an argument")

// boolSeqSentinel is the NoOptDefVal we register for -L/-P. pflag passes
// this exact string to Set when the user types the bare flag form
// (`cd -P`). Any other value — including the literal "true" supplied via
// `--physical=true` — fails the equality check in Set and is rejected
// as "option doesn't allow an argument", matching bash.
const boolSeqSentinel = "\x00rshell:cd:bare\x00"

func makeFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	helpFlag := fs.BoolP("help", "h", false, "print usage and exit")

	// -L and -P share a sequence counter so that after parsing we can
	// compare their pos fields to determine which appeared last on the
	// command line. pflag calls Set() in parse order, so the last flag
	// Set gets the highest pos value. This mirrors pwd's pattern.
	var modeSeq int
	logicalFlag := newBoolSeqFlag(&modeSeq)
	physicalFlag := newBoolSeqFlag(&modeSeq)
	fs.VarPF(logicalFlag, "logical", "L", "treat .. lexically; preserve symbolic links in the result (default)").NoOptDefVal = boolSeqSentinel
	fs.VarPF(physicalFlag, "physical", "P", "resolve symbolic links before treating ..").NoOptDefVal = boolSeqSentinel

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *helpFlag {
			printHelp(callCtx)
			return builtins.Result{}
		}

		if callCtx.ChangeDir == nil || callCtx.WorkDir == nil {
			callCtx.Errf("cd: not supported in this runner\n")
			return builtins.Result{Code: 1}
		}

		if len(args) > 1 {
			callCtx.Errf("cd: too many arguments\n")
			return builtins.Result{Code: 1}
		}

		// Resolve the operand into a target path string.
		// printResult is true when the new directory must be printed
		// to stdout on success (the `cd -` form).
		target, printResult, err := resolveOperand(args, callCtx)
		if err != nil {
			callCtx.Errf("cd: %s\n", err)
			return builtins.Result{Code: 1}
		}

		// Convert the operand to an absolute candidate path. Relative
		// paths are joined with the current working directory.
		cwd := callCtx.WorkDir()
		absTarget := target
		if !filepath.IsAbs(absTarget) {
			if cwd == "" {
				callCtx.Errf("cd: cannot resolve relative path: no working directory\n")
				return builtins.Result{Code: 1}
			}
			absTarget = filepath.Join(cwd, absTarget)
		}

		physical := physicalFlag.pos > logicalFlag.pos
		resolved, err := resolvePath(ctx, callCtx, absTarget, physical)
		if err != nil {
			callCtx.Errf("cd: %s: %s\n", target, formatErr(callCtx, err))
			return builtins.Result{Code: 1}
		}

		if err := callCtx.ChangeDir(resolved); err != nil {
			callCtx.Errf("cd: %s: %s\n", target, formatErr(callCtx, err))
			return builtins.Result{Code: 1}
		}

		if printResult {
			callCtx.Outf("%s\n", resolved)
		}
		return builtins.Result{}
	}
}

// resolveOperand inspects the parsed positional arguments and returns the
// target path string. printResult is true when the caller must print the
// resolved path on success (the `cd -` form). An error is returned when
// the required env var (HOME / OLDPWD) is unset.
func resolveOperand(args []string, callCtx *builtins.CallContext) (target string, printResult bool, err error) {
	if len(args) == 0 {
		// No-arg form: change to $HOME.
		home, ok := lookupEnv(callCtx, "HOME")
		if !ok || home == "" {
			return "", false, errors.New("HOME not set")
		}
		return home, false, nil
	}

	operand := args[0]
	if operand == "" {
		// Empty operand is rejected by POSIX.
		return "", false, errors.New("invalid empty operand")
	}
	if operand == "-" {
		oldpwd, ok := lookupEnv(callCtx, "OLDPWD")
		if !ok || oldpwd == "" {
			return "", false, errors.New("OLDPWD not set")
		}
		return oldpwd, true, nil
	}
	return operand, false, nil
}

// lookupEnv reads name from the runner's environment via the LookupEnvVar
// callback. When the callback is unavailable (e.g. an exotic test
// configuration), it returns ("", false) — matching the unset case so
// callers do not need to handle a third state.
func lookupEnv(callCtx *builtins.CallContext, name string) (string, bool) {
	if callCtx.LookupEnvVar == nil {
		return "", false
	}
	return callCtx.LookupEnvVar(name)
}

// resolvePath canonicalises absPath to the form that ChangeDir expects.
// For -L (physical=false) the path is cleaned lexically: "." and ".." are
// resolved without consulting the filesystem, so symlinks in the result
// are preserved. For -P, intermediate symlinks are walked through the
// sandbox-safe Lstat / Readlink callbacks before any ".." processing.
//
// Note: unlike pwd -P, cd does not apply CanonicalizeRootPrefix to the
// resolved path. cd needs the working directory in the configured-root
// form (e.g. /tmp/link) so subsequent sandbox operations resolve under
// the same root; canonicalising would translate the path to the
// host-canonical form (e.g. /var/data) which the sandbox would reject
// as outside its configured roots. The trade-off is that cd -P cannot
// resolve a symlink at the AllowedPaths root itself, only symlinks at
// non-root components — a documented limitation.
func resolvePath(ctx context.Context, callCtx *builtins.CallContext, absPath string, physical bool) (string, error) {
	if !physical {
		return filepath.Clean(absPath), nil
	}
	return resolveSymlinks(ctx, callCtx, absPath)
}

// resolveSymlinks walks absPath component by component using callCtx's
// sandbox-safe Lstat and Readlink callbacks, expanding symlinks before
// processing dot-dot. The total number of symlink expansions is capped
// at maxSymlinkHops to defeat cycles.
//
// When LstatFile fails for a component (typically because the path lies
// above the AllowedPaths root and cannot be inspected through os.Root),
// the component is treated as opaque and walking continues. This is the
// same best-effort policy that pwd -P uses: components above the root
// pass through unresolved, and only the symlinks we can actually inspect
// get expanded.
//
// When LstatFile / ReadlinkFile are unavailable, the path is returned
// after lexical cleaning — the best canonicalisation possible without
// access to the filesystem.
func resolveSymlinks(ctx context.Context, callCtx *builtins.CallContext, absPath string) (string, error) {
	if !filepath.IsAbs(absPath) {
		return "", fmt.Errorf("not an absolute path: %s", absPath)
	}
	if callCtx.LstatFile == nil || callCtx.ReadlinkFile == nil {
		return filepath.Clean(absPath), nil
	}

	out := rootPrefix(absPath)
	rest := strings.TrimPrefix(absPath, out)

	hops := 0
	for rest != "" {
		if err := ctx.Err(); err != nil {
			return "", err
		}

		// Consume any leading separator(s) before extracting the next
		// component. filepath.Clean collapses runs of separators in
		// the input, but expanded symlink targets that we splice back
		// into rest can reintroduce them.
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
			// as opaque (not a symlink) and continue.
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
			// Lstat said it's a symlink but readlink failed. Treat it
			// as opaque rather than aborting resolution.
			out = candidate
			continue
		}

		if filepath.IsAbs(target) {
			cleanedTarget := filepath.Clean(target)
			// Container-style sandboxes mount the host filesystem at a
			// prefix (e.g. /mnt/host). Symlink targets stored on disk
			// often refer to host-absolute paths without that prefix,
			// so apply the prefix when set — otherwise the resolved
			// path would not be reachable through the sandbox.
			if callCtx.HostPrefix != nil {
				if hp := callCtx.HostPrefix(); hp != "" && !strings.HasPrefix(cleanedTarget, hp+string(filepath.Separator)) && cleanedTarget != hp {
					cleanedTarget = filepath.Join(hp, cleanedTarget)
				}
			}
			out = rootPrefix(cleanedTarget)
			rest = strings.TrimPrefix(cleanedTarget, out) + rest
		} else {
			// filepath.Clean normalises the relative target — collapsing
			// "." segments and converting forward slashes to the host
			// separator on Windows.
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
// root. If dir is already at the root, parentDir returns dir unchanged
// (matching POSIX: at "/", ".." stays at "/").
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

// formatErr renders err as a short, POSIX-style message. The cd builtin
// always prefixes its output with `cd: <operand>:`, so we strip the
// `op path:` prefix that os.PathError adds — otherwise the user sees
// `cd: foo: statat foo: no such file or directory`, with the operand
// quoted twice. Errors that are not *os.PathError fall through to
// PortableErr (when available) or Error().
func formatErr(callCtx *builtins.CallContext, err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) && pe.Err != nil {
		return pe.Err.Error()
	}
	if callCtx.PortableErr != nil {
		if msg := callCtx.PortableErr(err); msg != "" {
			return msg
		}
	}
	return err.Error()
}

// boolSeqFlag is a pflag.Value implementation for -L/-P. Two boolSeqFlag
// values share a *seq counter; each call to Set increments the counter
// and records the new value in pos. After parsing, comparing pos fields
// reveals which flag appeared last on the command line.
//
// Set also rejects explicit values: cd's mode flags are bare boolean
// switches and `cd --physical=false` should fail like bash rather than
// silently flip the mode.
type boolSeqFlag struct {
	seq *int
	pos int
}

func newBoolSeqFlag(seq *int) *boolSeqFlag {
	return &boolSeqFlag{seq: seq}
}

func (f *boolSeqFlag) String() string { return "false" }

func (f *boolSeqFlag) Set(s string) error {
	if s != boolSeqSentinel {
		return errBoolSeqValue
	}
	*f.seq++
	f.pos = *f.seq
	return nil
}

func (f *boolSeqFlag) Type() string { return "bool" }

func printHelp(callCtx *builtins.CallContext) {
	callCtx.Out(`Usage: cd [-LP] [-|DIRECTORY]
Change the shell's current working directory.

With no DIRECTORY, change to $HOME. With '-', change to $OLDPWD
and print the new directory.

  -L, --logical   treat .. lexically; preserve symbolic links
                  in the result (default)
  -P, --physical  resolve symbolic links before treating ..
  -h, --help      display this help and exit
`)
}
