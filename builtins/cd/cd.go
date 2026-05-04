// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package cd implements the cd builtin command.
//
// cd — change the working directory
//
// Usage: cd [-L|-P] [DIR]
//
// Change the shell's working directory to DIR. With no argument, cd
// changes to $HOME. The special argument "-" switches to $OLDPWD and
// prints the new working directory.
//
// Accepted flags:
//
//	-L, --logical
//	    Treat symbolic links logically: the literal path is preserved in
//	    $PWD even when components are symlinks. This is the default.
//
//	-P, --physical
//	    Resolve symbolic links physically: each symlink in DIR is followed
//	    so that $PWD reflects the real underlying directory. Resolution is
//	    bounded by maxSymlinkHops to defeat circular-symlink loops.
//
//	-h, --help
//	    Print usage to stdout and exit 0.
//
// Behaviour notes:
//
//	cd updates two shell variables on success: it copies the previous
//	working directory into $OLDPWD and writes the new directory into
//	$PWD. On failure, neither the runner directory nor those variables
//	are touched.
//
//	`cd -` is exclusive: it is equivalent to `cd "$OLDPWD"` followed by
//	echoing the resolved directory to stdout. If $OLDPWD is unset, the
//	command fails with a non-zero exit and no state changes.
//
//	Path validation goes exclusively through callCtx.StatFile and
//	callCtx.ReadlinkFile, both of which honour the AllowedPaths sandbox.
//	Targets that exist on the host filesystem but are outside the
//	sandbox are rejected with the same "no such file or directory"
//	message used for missing entries — the failure mode never reveals
//	whether a denied directory exists.
//
// Exit codes:
//
//	0  Directory was changed successfully.
//	1  Argument missing/invalid, target not found, target not a directory,
//	   $HOME or $OLDPWD unset when needed, or symlink loop detected.
//
// Memory safety:
//
//	Inputs are bounded: paths longer than maxPathBytes are rejected
//	without ever being passed to the sandbox or to filepath.Clean. The
//	-P symlink walk is capped at maxSymlinkHops to prevent infinite
//	loops on circular symlinks.
package cd

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the cd builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "cd",
	Description: "change the working directory",
	MakeFlags:   registerFlags,
}

// maxPathBytes caps the length of any path cd will look at. The bound is
// generous (well above the historic PATH_MAX of 4096) but finite, so
// runaway concatenation of $HOME, $OLDPWD, or symlink targets cannot tie
// the sandbox up in arbitrarily long resolution work.
const maxPathBytes = 1 << 16 // 64 KiB

// maxSymlinkHops caps the number of symlink expansions performed during
// `cd -P`. Capped at 40 to prevent unbounded work on deep or circular
// symlink chains while remaining well above any realistic directory tree depth.
const maxSymlinkHops = 40

// cdMode encodes the current -L vs -P selection. Using a typed enum (vs.
// a magic string) keeps the precedence rule below readable.
type cdMode int

const (
	modeUnset cdMode = iota
	modeLogical
	modePhysical
)

// reservedWindowsNames lists the basenames Windows treats as reserved
// device files. Touching any of these by name can hang processes (RULES.md
// cross-platform requirement). The check applies only on Windows; other
// platforms allow these as ordinary directory names.
var reservedWindowsNames = map[string]struct{}{
	"CON": {}, "PRN": {}, "AUX": {}, "NUL": {},
	"COM1": {}, "COM2": {}, "COM3": {}, "COM4": {}, "COM5": {}, "COM6": {}, "COM7": {}, "COM8": {}, "COM9": {},
	"LPT1": {}, "LPT2": {}, "LPT3": {}, "LPT4": {}, "LPT5": {}, "LPT6": {}, "LPT7": {}, "LPT8": {}, "LPT9": {},
}

// isReservedWindowsPath reports whether any segment of path is a Windows
// reserved device name. Returns false on non-Windows platforms.
func isReservedWindowsPath(path string) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
		if seg == "" {
			continue
		}
		// Reserved names are matched without the extension and case-insensitively.
		base := seg
		if i := strings.IndexByte(base, '.'); i > 0 {
			base = base[:i]
		}
		if _, ok := reservedWindowsNames[strings.ToUpper(base)]; ok {
			return true
		}
	}
	return false
}

func registerFlags(flags *builtins.FlagSet) builtins.HandlerFunc {
	// lastMode records which of -L / -P was parsed most recently.
	// pflag's Visit iterates in lexical order, so we use a custom flag
	// value that remembers parse order. The pointer is shared between
	// both flag values.
	var lastMode cdMode
	flags.VarP(newLPFlag(&lastMode, modeLogical), "logical", "L", "follow symbolic links logically (default)")
	flags.VarP(newLPFlag(&lastMode, modePhysical), "physical", "P", "resolve symbolic links to their targets")
	flags.Lookup("logical").NoOptDefVal = "true"
	flags.Lookup("physical").NoOptDefVal = "true"
	help := flags.BoolP("help", "h", false, "print usage and exit")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: cd [-L|-P] [DIR]\n")
			callCtx.Out("Change the shell working directory.\n\n")
			flags.SetOutput(callCtx.Stdout)
			flags.PrintDefaults()
			return builtins.Result{}
		}

		// -L is the default; if both are given the last-on-the-command-line
		// wins, matching bash. lastMode tracks which flag appeared last.
		usePhysical := lastMode == modePhysical

		if len(args) > 1 {
			callCtx.Errf("cd: too many arguments\n")
			return builtins.Result{Code: 1}
		}

		var (
			target string
			// printValue is set when `cd -` succeeds: bash prints the
			// pre-cleaning OLDPWD value verbatim to stdout (so trailing
			// slashes etc. survive). filepath.Clean would normalise that
			// away, hence the separate field.
			printValue string
		)
		switch {
		case len(args) == 0:
			home, ok := lookupVar(callCtx, "HOME")
			if !ok {
				callCtx.Errf("cd: HOME not set\n")
				return builtins.Result{Code: 1}
			}
			// Bash distinguishes unset (error) from set-but-empty
			// (silent no-op success). Match that: if HOME is set
			// to "", `cd` is a no-op that returns 0 without
			// touching $PWD/$OLDPWD.
			if home == "" {
				return builtins.Result{}
			}
			target = home
		case args[0] == "-":
			oldpwd, ok := lookupVar(callCtx, "OLDPWD")
			if !ok {
				callCtx.Errf("cd: OLDPWD not set\n")
				return builtins.Result{Code: 1}
			}
			// Match bash: empty-but-set OLDPWD makes `cd -` a
			// silent no-op success (rather than the "OLDPWD not
			// set" error reserved for the unset case).
			if oldpwd == "" {
				return builtins.Result{}
			}
			target = oldpwd
			printValue = oldpwd
		default:
			target = args[0]
		}

		if target == "" {
			// Match bash: cd "" is a no-op success (stays in $PWD, exits 0).
			return builtins.Result{}
		}
		if len(target) > maxPathBytes {
			callCtx.Errf("cd: path too long\n")
			return builtins.Result{Code: 1}
		}
		// Windows reserved device names (CON, NUL, COM1, ...) hang on
		// Stat — refuse them up front. RULES.md cross-platform mandate.
		if isReservedWindowsPath(target) {
			callCtx.Errf("cd: %s: no such file or directory\n", target)
			return builtins.Result{Code: 1}
		}

		// Resolve to absolute against the current working directory. We use
		// the displayed value (the caller's argument) for error messages so
		// that bash-style "cd: foo: no such file or directory" is preserved.
		display := target
		absPath := target
		if !filepath.IsAbs(absPath) {
			cwd := ""
			if callCtx.WorkDir != nil {
				cwd = callCtx.WorkDir()
			}
			absPath = filepath.Join(cwd, absPath)
		}
		absPath = filepath.Clean(absPath)

		if usePhysical {
			resolved, err := resolvePhysical(ctx, callCtx, absPath)
			if err != nil {
				callCtx.Errf("cd: %s: %s\n", display, formatErr(callCtx, err))
				return builtins.Result{Code: 1}
			}
			absPath = resolved
		}

		if len(absPath) > maxPathBytes {
			callCtx.Errf("cd: %s: path too long\n", display)
			return builtins.Result{Code: 1}
		}

		info, err := callCtx.StatFile(ctx, absPath)
		if err != nil {
			callCtx.Errf("cd: %s: %s\n", display, formatErr(callCtx, err))
			return builtins.Result{Code: 1}
		}
		if !info.IsDir() {
			callCtx.Errf("cd: %s: not a directory\n", display)
			return builtins.Result{Code: 1}
		}

		if printValue != "" {
			callCtx.Out(printValue)
			callCtx.Out("\n")
		}

		return builtins.Result{NewWorkDir: absPath}
	}
}

// resolvePhysical resolves every symlink component in absPath — including
// intermediate ones — so the returned path is fully canonical, matching
// bash's `cd -P` behaviour. For `dir/symlink/sub` where `symlink → real`,
// bash sets $PWD to `dir/real/sub`; a single Lstat on the leaf cannot
// detect this because the kernel transparently follows intermediate
// symlinks (only the final component is exempt under O_NOFOLLOW). To
// catch intermediate symlinks we resolve the leaf first and then walk
// back up the path looking for any ancestor that is itself a symlink,
// substitute its target, and repeat from the leaf.
//
// All filesystem access goes through callCtx.LstatFile and
// callCtx.ReadlinkFile, both of which honour the AllowedPaths sandbox.
// Ancestors that fall outside the sandbox return a permission error from
// LstatFile; we treat that as "this prefix is opaque to us" and stop the
// upward walk — there is nothing the user can do to make the sandbox
// reveal more, and the leaf-side resolution is still applied.
//
// Bounded by maxSymlinkHops across both leaf and intermediate hops; ctx
// is checked between hops so cancellation is honoured.
func resolvePhysical(ctx context.Context, callCtx *builtins.CallContext, absPath string) (string, error) {
	if callCtx.LstatFile == nil || callCtx.ReadlinkFile == nil {
		return absPath, nil
	}
	current := absPath
	hops := 0
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		// 1) Resolve the leaf if it is itself a symlink.
		info, err := callCtx.LstatFile(ctx, current)
		if err != nil {
			return "", err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			hops++
			if hops > maxSymlinkHops {
				return "", errors.New("too many levels of symbolic links")
			}
			target, err := callCtx.ReadlinkFile(ctx, current)
			if err != nil {
				return "", err
			}
			if filepath.IsAbs(target) {
				current = filepath.Clean(target)
			} else {
				current = filepath.Clean(filepath.Join(filepath.Dir(current), target))
			}
			if len(current) > maxPathBytes {
				return "", errors.New("path too long")
			}
			continue
		}
		// 2) Leaf is regular. Walk parents looking for the deepest
		//    ancestor that is itself a symlink. If we find one, splice in
		//    its target and re-enter the outer loop so the new path is
		//    re-checked from the leaf.
		rebuilt, replaced, err := substituteIntermediateSymlink(ctx, callCtx, current, &hops)
		if err != nil {
			return "", err
		}
		if !replaced {
			return filepath.Clean(current), nil
		}
		if len(rebuilt) > maxPathBytes {
			return "", errors.New("path too long")
		}
		current = rebuilt
	}
}

// substituteIntermediateSymlink walks parents of absPath from deepest to
// shallowest. The first parent that is itself a symlink is resolved and
// the path is rebuilt with the target substituted (the suffix below the
// symlink is preserved). On success returns (newPath, true, nil); when
// no parent is a symlink (or LstatFile rejects a parent — typically the
// sandbox boundary), returns ("", false, nil) to signal the outer loop
// that absPath is fully canonical for the visible portion of the tree.
func substituteIntermediateSymlink(ctx context.Context, callCtx *builtins.CallContext, absPath string, hops *int) (string, bool, error) {
	current := absPath
	suffix := ""
	for {
		if err := ctx.Err(); err != nil {
			return "", false, err
		}
		parent := filepath.Dir(current)
		if parent == current {
			// Reached the volume root — no symlink in any ancestor.
			return "", false, nil
		}
		// Extract the trailing component of `current` without using
		// filepath.Base (which is not in cd's symbol allowlist). After
		// normalising both sides to forward slashes the basename is the
		// final segment of the slash-joined form.
		baseSegments := strings.Split(filepath.ToSlash(current), "/")
		base := baseSegments[len(baseSegments)-1]
		if suffix == "" {
			suffix = base
		} else {
			suffix = filepath.Join(base, suffix)
		}
		info, err := callCtx.LstatFile(ctx, parent)
		if err != nil {
			// Parent is opaque to us (sandbox boundary, permission
			// denied, etc.). We cannot resolve symlinks above this
			// point; treat the path as canonical from here up.
			return "", false, nil
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			*hops++
			if *hops > maxSymlinkHops {
				return "", false, errors.New("too many levels of symbolic links")
			}
			target, err := callCtx.ReadlinkFile(ctx, parent)
			if err != nil {
				return "", false, err
			}
			if len(target) > maxPathBytes {
				return "", false, errors.New("path too long")
			}
			var rebuilt string
			if filepath.IsAbs(target) {
				rebuilt = filepath.Clean(filepath.Join(target, suffix))
			} else {
				rebuilt = filepath.Clean(filepath.Join(filepath.Dir(parent), target, suffix))
			}
			return rebuilt, true, nil
		}
		current = parent
	}
}

// lookupVar reads name from the caller's shell environment. When LookupVar
// is not wired (older callers, tests that build CallContext directly) the
// variable is reported as unset rather than panicking.
func lookupVar(callCtx *builtins.CallContext, name string) (string, bool) {
	if callCtx.LookupVar == nil {
		return "", false
	}
	return callCtx.LookupVar(name)
}

// formatErr maps a sandbox/IO error onto a stable, POSIX-style message
// suitable for "cd: <path>: <msg>" formatting. The sandbox returns
// *os.PathError values whose String() form includes the syscall name and
// path (e.g. "statat foo: no such file or directory"); we strip those so
// that callers can format the path themselves and the message is stable
// across platforms.
func formatErr(callCtx *builtins.CallContext, err error) string {
	if err == nil {
		return ""
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		if callCtx.PortableErr != nil {
			if msg := callCtx.PortableErr(pe.Err); msg != "" {
				return msg
			}
		}
		return pe.Err.Error()
	}
	if errors.Is(err, fs.ErrNotExist) {
		return "no such file or directory"
	}
	if errors.Is(err, fs.ErrPermission) {
		return "permission denied"
	}
	if callCtx.PortableErr != nil {
		if msg := callCtx.PortableErr(err); msg != "" {
			return msg
		}
	}
	return err.Error()
}

// lpFlag is a boolean-shaped pflag.Value that writes its own mode into a
// shared *cdMode each time pflag invokes Set. This is how cd matches
// bash's "last one wins" rule for -L/-P: pflag's own Visit iterates in
// lexical order and so cannot tell us which of -L or -P was supplied last
// on the command line.
type lpFlag struct {
	last *cdMode
	mode cdMode
}

func newLPFlag(last *cdMode, mode cdMode) *lpFlag {
	return &lpFlag{last: last, mode: mode}
}

// String reports whether this flag is currently the active one. pflag
// only consults this for `--help` formatting; the runtime decision
// reads `*f.last` directly.
func (f *lpFlag) String() string {
	if f.last != nil && *f.last == f.mode {
		return "true"
	}
	return "false"
}

func (f *lpFlag) Set(s string) error {
	switch s {
	case "true", "":
		// Only "this flag is now active" should update last-wins precedence;
		// `--logical=false` does not mean the user wants logical handling.
		*f.last = f.mode
	case "false":
		// Explicit deactivation: if this flag was the active one, clear it.
		if *f.last == f.mode {
			*f.last = modeUnset
		}
	default:
		return errors.New("invalid boolean value: " + s)
	}
	return nil
}

func (f *lpFlag) Type() string { return "bool" }

func (f *lpFlag) IsBoolFlag() bool { return true }
