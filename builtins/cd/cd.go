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
// prints the raw OLDPWD value to stdout.
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
//	`cd -` changes to $OLDPWD and echoes the raw OLDPWD value to stdout
//	(bash always prints the original OLDPWD string, even under -P, even
//	when it is the empty string — in which case a bare newline is emitted).
//	If $OLDPWD is unset the command fails with a non-zero exit. If
//	$OLDPWD is set but empty, cd - stays in place, prints a bare newline,
//	and updates $OLDPWD to the current directory.
//
//	Path validation goes exclusively through callCtx.StatFile,
//	callCtx.LstatFile, and callCtx.ReadlinkFile, all of which honour
//	the AllowedPaths sandbox. (LstatFile and ReadlinkFile are used only
//	during -P symlink resolution; StatFile is used for all modes.)
//	Targets outside the sandbox are rejected with a "permission denied"
//	error from the AllowedPaths sandbox (not "no such file or directory"),
//	so scripts can distinguish a denied path from a missing one.
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
	// Bash rejects `cd sub -P` (too many arguments). Disable interspersed
	// parsing so positional arguments are never re-interpreted as flags.
	// Without this, pflag would parse `cd sub -P` as `cd -P sub`, silently
	// changing behaviour compared to bash.
	flags.SetInterspersed(false)
	// -e is a bash extension: with -P, exit non-zero if the physical path
	// cannot be determined. Without -P it is a no-op. We accept but ignore
	// it so scripts using `cd -e dir` or `cd -Pe dir` do not fail with
	// "unknown flag". Matches bash ≥ 4.0 behaviour.
	_ = flags.BoolP("_e", "e", false, "(ignored) bash compat: exit non-zero if physical path cannot be determined")
	if f := flags.Lookup("_e"); f != nil {
		f.Hidden = true
	}
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

		// currentDir returns the runner's current working directory.
		// Used for no-op cases where bash still rotates OLDPWD.
		currentDir := func() string {
			if callCtx.WorkDir == nil {
				return ""
			}
			return callCtx.WorkDir()
		}

		var (
			target string
			// displayOverride, when non-empty, is used as the error-message
			// label instead of the derived target path.
			displayOverride string
			// printValue holds the value to print when cd - succeeds.
			// Bash prints the raw OLDPWD verbatim (preserving trailing
			// slashes etc. that filepath.Clean would strip), followed by a
			// newline — even when OLDPWD is the empty string (one bare
			// newline). printDash tracks whether to print at all.
			printValue string
			printDash  bool
		)
		switch {
		case len(args) == 0:
			home, ok := lookupVar(callCtx, "HOME")
			if !ok {
				callCtx.Errf("cd: HOME not set\n")
				return builtins.Result{Code: 1}
			}
			// Bash distinguishes unset (error) from set-but-empty. When HOME
			// is set but empty, `cd` stays in place AND updates OLDPWD to the
			// current directory (same as a no-move cd). Route through the
			// normal stat+NewWorkDir path so applyNewWorkDir fires.
			if home == "" {
				target = currentDir()
			} else {
				target = home
			}
		case args[0] == "-":
			oldpwd, ok := lookupVar(callCtx, "OLDPWD")
			if !ok {
				callCtx.Errf("cd: OLDPWD not set\n")
				return builtins.Result{Code: 1}
			}
			// Bash always prints OLDPWD (even "") followed by a newline on
			// successful cd -. Empty-but-set OLDPWD: stay in place and
			// update OLDPWD. Route through the normal path so applyNewWorkDir
			// fires.
			// Use oldpwd as the display value in error messages so that
			// errors read "cd: /no/exist: No such file or directory" (matching
			// bash) rather than "cd: -: ...". When OLDPWD is empty-but-set,
			// fall back to "-" so that any error message shows "cd: -: ..."
			// rather than exposing the runner's cwd path.
			if oldpwd == "" {
				displayOverride = "-"
				target = currentDir()
			} else {
				displayOverride = oldpwd
				target = oldpwd
				printValue = oldpwd
			}
			printDash = true
		default:
			target = args[0]
		}

		if target == "" {
			// cd "" or currentDir() returned "" (no working dir set): stay
			// in place and update OLDPWD to cwd. But if cwd itself is
			// unknown, there is nothing to stat — return no-op.
			cwd := currentDir()
			if cwd == "" {
				return builtins.Result{}
			}
			target = cwd
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
		if displayOverride != "" {
			display = displayOverride
		}
		absPath := target
		if !filepath.IsAbs(absPath) {
			cwd := ""
			if callCtx.WorkDir != nil {
				cwd = callCtx.WorkDir()
			}
			// In logical mode use filepath.Join which cleans the path
			// lexically (collapsing `..` etc.). In physical mode we must
			// NOT clean before symlink resolution: bash resolves symlinks
			// first and then applies `..` to the *real* directory, so
			// `cd -P link/..` lands in the parent of the symlink's target,
			// not the parent of the symlink itself. filepath.Join (which
			// calls filepath.Clean internally) would collapse `link/..` to
			// `.` before we ever see `link`, producing the wrong parent.
			// Physical-mode cleaning is deferred to resolvePhysical.
			if usePhysical {
				if cwd == "" {
					// No working directory: cannot resolve a relative path
					// in physical mode. Fail fast here so resolvePhysical
					// is not called with a non-absolute path (violating its
					// documented contract that absPath must be absolute).
					// The StatFile gate would catch this too, but explicit
					// rejection is cleaner and gives a more accurate error.
					callCtx.Errf("cd: %s: no such file or directory\n", display)
					return builtins.Result{Code: 1}
				} else {
					// Raw concatenation: cwd + separator + target, preserving
					// `..` for the component-by-component resolver.
					absPath = cwd + string(filepath.Separator) + target
				}
			} else {
				absPath = filepath.Join(cwd, absPath)
			}
		}
		if !usePhysical {
			absPath = filepath.Clean(absPath)
		}

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

		if callCtx.StatFile == nil {
			// StatFile is always wired in production (via runner_exec.go),
			// but guard against misconfigured CallContext in tests or
			// embedded use to avoid a nil-pointer panic.
			callCtx.Errf("cd: %s: stat not available\n", display)
			return builtins.Result{Code: 1}
		}
		info, err := callCtx.StatFile(ctx, absPath)
		if err != nil {
			callCtx.Errf("cd: %s: %s\n", display, formatErr(callCtx, err))
			return builtins.Result{Code: 1}
		}
		if !info.IsDir() {
			callCtx.Errf("cd: %s: Not a directory\n", display)
			return builtins.Result{Code: 1}
		}

		if printDash {
			callCtx.Out(printValue)
			callCtx.Out("\n")
		}

		return builtins.Result{NewWorkDir: absPath}
	}
}

// resolvePhysical resolves every symlink in absPath component by component,
// mirroring bash's `cd -P` behaviour. bash says: for physical mode, resolve
// each symlink in the path before applying `..`, so `cd -P link/..` lands in
// the *real* parent of the link's target, not in the lexical parent of the
// link itself.
//
// The algorithm walks the path left-to-right, accumulating a "resolved so
// far" prefix. For each component:
//   - `..` pops the last segment of the resolved prefix (after any symlinks
//     at that prefix are already resolved).
//   - any other component is appended, and if the resulting path is a
//     symlink its target is substituted (and the walk restarts at the new
//     leaf, still bounded by maxSymlinkHops).
//
// All filesystem access goes through callCtx.LstatFile and
// callCtx.ReadlinkFile, both of which honour the AllowedPaths sandbox.
// Ancestors that fall outside the sandbox return a permission error from
// LstatFile; we treat that as "this prefix is opaque — not a symlink" and
// advance resolved without following, preserving the correct semantics for
// paths within the sandbox while remaining bounded.
//
// Bounded by maxSymlinkHops total symlink hops; ctx is checked between
// hops so cancellation is honoured.
//
// absPath must be absolute (filepath.IsAbs true); it may contain `..`
// components that have not been cleaned.
func resolvePhysical(ctx context.Context, callCtx *builtins.CallContext, absPath string) (string, error) {
	if callCtx.LstatFile == nil || callCtx.ReadlinkFile == nil {
		return filepath.Clean(absPath), nil
	}

	// Split into components, skipping empty segments.
	parts := strings.Split(filepath.ToSlash(absPath), "/")
	// resolved accumulates the canonical prefix built so far.
	// Start with the root (volume root on Windows, "/" on Unix).
	volName := filepath.VolumeName(absPath)
	resolved := volName + string(filepath.Separator)
	// volNameParts holds the non-empty components that make up the volume name
	// after filepath.ToSlash normalisation. For a regular Windows drive "C:"
	// the set is {"C:"}. For a UNC path "\\\\server\\share", ToSlash produces
	// "//server/share" and the set is {"server", "share"}. The loop below
	// skips any segment in this set so that UNC host/share components are not
	// treated as ordinary path components. On Unix volName is "" so the set
	// is empty and there is no overhead.
	volNameParts := make(map[string]bool)
	for _, vp := range strings.Split(filepath.ToSlash(volName), "/") {
		if vp != "" {
			volNameParts[vp] = true
		}
	}
	hops := 0

	for i := 0; i < len(parts); i++ {
		seg := parts[i]
		// Skip empty segments, ".", and Windows volume-prefix components
		// (e.g. drive "C:" or UNC host+share "server","share") that are
		// already absorbed into resolved. volNameParts is built from the
		// ToSlash-split volume name so it matches both C: paths and UNC
		// paths correctly regardless of path separator used.
		if seg == "" || seg == "." || volNameParts[seg] {
			continue
		}
		if seg == ".." {
			// Pop the last segment off the resolved prefix (move to parent).
			parent := filepath.Dir(resolved)
			if parent != resolved { // guard against popping past root
				resolved = parent
			}
			continue
		}

		// Append this segment and check if it is a symlink.
		candidate := filepath.Join(resolved, seg)
		if len(candidate) > maxPathBytes {
			return "", errors.New("path too long")
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		info, err := callCtx.LstatFile(ctx, candidate)
		if err != nil {
			// Sandbox boundary or path outside AllowedPaths: treat as
			// a non-symlink regular entry. SECURITY: setting
			// resolved = candidate here does NOT grant access to this
			// path — it only influences how subsequent ".." components
			// are resolved. The mandatory StatFile call at the end of
			// the cd handler is the actual access-control gate and will
			// reject any out-of-sandbox final target.
			// NOTE: sandbox.LstatFile also returns ErrPermission for
			// real filesystem permission errors (e.g. a symlink with
			// mode 000). In that case we also skip following — the
			// final StatFile call rejects with permission denied
			// regardless. This means the component is treated as
			// non-symlink, which is correct: we cannot follow a
			// symlink we cannot read.
			// For any intermediate component opaque to the sandbox we
			// simply advance resolved without following it.
			if errors.Is(err, fs.ErrPermission) {
				resolved = candidate
				continue
			}
			// Propagate any other error (not-found, etc.).
			return "", err
		}
		if info.Mode()&fs.ModeSymlink == 0 {
			// Regular entry: advance resolved.
			resolved = candidate
			continue
		}
		// Symlink: follow it, count the hop.
		hops++
		if hops > maxSymlinkHops {
			return "", errors.New("too many levels of symbolic links")
		}
		target, err := callCtx.ReadlinkFile(ctx, candidate)
		if err != nil {
			return "", err
		}
		if len(target) > maxPathBytes {
			return "", errors.New("path too long")
		}
		var newBase string
		if filepath.IsAbs(target) {
			// Container-style sandboxes mount the host filesystem under a
			// prefix (e.g. /mnt/host). Symlink targets stored on disk are
			// often host-absolute paths without that prefix, so apply
			// HostPrefix when set — otherwise the resolved path would not
			// be reachable through the sandbox (mirrors pwd -P's handling).
			cleanedTarget := filepath.Clean(target)
			if callCtx.HostPrefix != nil {
				if hp := callCtx.HostPrefix(); hp != "" {
					sep := string(filepath.Separator)
					if !strings.HasPrefix(cleanedTarget, hp+sep) && cleanedTarget != hp {
						cleanedTarget = filepath.Join(hp, cleanedTarget)
					}
				}
			}
			newBase = cleanedTarget
		} else {
			// Relative symlink target is relative to the directory
			// containing the symlink (= resolved, not candidate).
			newBase = filepath.Join(resolved, target)
		}
		// Prepend the symlink's target to the remaining components and
		// restart the walk so we re-resolve any symlinks within it.
		rest := parts[i+1:]
		newParts := strings.Split(filepath.ToSlash(newBase), "/")
		parts = append(newParts, rest...)
		i = -1 // will be incremented to 0 by the loop
		// Reset resolved to the volume root so the new absolute path
		// starting from newBase's root is walked from scratch.
		// Also update volName so the volume-prefix skip (seg == volName)
		// stays in sync when the symlink target is on a different Windows
		// volume (e.g. resolving across drive letters).
		volName = filepath.VolumeName(newBase)
		resolved = volName + string(filepath.Separator)
		// Rebuild volNameParts for the new volume (needed when an absolute
		// symlink target is on a different Windows volume, e.g. cross-drive).
		volNameParts = make(map[string]bool)
		for _, vp := range strings.Split(filepath.ToSlash(volName), "/") {
			if vp != "" {
				volNameParts[vp] = true
			}
		}
	}
	return filepath.Clean(resolved), nil
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
