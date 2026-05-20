// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"
	"sort"
	"syscall"
	"time"

	"github.com/spf13/pflag"

	"github.com/DataDog/rshell/builtins/internal/flagparser"
)

// ErrVarStorageExceeded is returned by CallContext.SetVar when the
// assignment would push the runner's total variable storage past its
// cap. This is a script-aborting condition: AST-level assignments
// treat it the same way (see interp/vars.go), so state-mutating
// builtins should propagate it via Result.Exiting=true rather than
// continuing with status 1, matching bash's resource-cap DoS guard.
var ErrVarStorageExceeded = errors.New("variable storage limit exceeded")

// FlagSet is a type alias for pflag.FlagSet. Command files receive a *FlagSet
// from the framework without needing to import pflag directly (the builtins
// package is always allowed by the import allowlist).
type FlagSet = pflag.FlagSet

// Flag is a type alias for pflag.Flag, exposed so command files can use
// FlagSet.Visit without importing pflag directly.
type Flag = pflag.Flag

// HandlerFunc is the bound handler called by the framework after flags are
// parsed. args contains only the positional (non-flag) arguments.
type HandlerFunc func(ctx context.Context, callCtx *CallContext, args []string) Result

// CapturedHostCommand is the output from a guarded host command run with
// stdout/stderr captured instead of streamed to the shell's current fds.
type CapturedHostCommand struct {
	Code            uint8
	Stdout          string
	Stderr          string
	StdoutTruncated bool
	StderrTruncated bool
}

// Command pairs a builtin name with its flag-declaring factory. MakeFlags
// registers any flags on the provided FlagSet and returns the bound handler.
// Commands that accept no flags may ignore fs via NoFlags.
type Command struct {
	Name        string
	Description string
	Help        string
	MakeFlags   func(*FlagSet) HandlerFunc

	// NormalizeArgs, if non-nil, rewrites raw argument slices before pflag
	// parsing. This allows commands to support legacy flag syntax that pflag
	// cannot handle natively (e.g. head/tail -5 → -n 5).
	NormalizeArgs func(args []string) []string
}

// NoFlags wraps a HandlerFunc in the MakeFlags format for commands that
// declare no flags.
func NoFlags(fn HandlerFunc) func(*FlagSet) HandlerFunc {
	return func(_ *FlagSet) HandlerFunc { return fn }
}

// Register adds the Command to the builtin registry. For each invocation the
// framework creates a fresh *FlagSet, passes it to MakeFlags so the command
// can register its flags, parses the raw args, writes any error to stderr
// (exit 1), and then calls the bound handler with positional args only.
//
// If MakeFlags registers no flags (e.g. via NoFlags), the framework skips
// parsing entirely and passes all raw args to the handler unchanged. This
// lets commands like echo treat flag-shaped literals (e.g. -n) correctly.
func (c Command) Register() {
	name := c.Name
	factory := c.MakeFlags
	normalize := c.NormalizeArgs
	if _, exists := featureByName[name]; exists {
		panic("builtin name conflicts with rshell feature: " + name)
	}

	// Probe whether the command registers any flags so we can record it
	// in metadata (used by tests to enforce help-consistency invariants).
	probe := pflag.NewFlagSet(name, pflag.ContinueOnError)
	probe.SetOutput(io.Discard)
	factory(probe)
	hasFlags := probe.HasFlags()

	metaRegistry[name] = CommandMeta{Name: name, Description: c.Description, Help: c.Help, HasFlags: hasFlags}
	addToRegistry(name, func(ctx context.Context, callCtx *CallContext, args []string) Result {
		fs := pflag.NewFlagSet(name, pflag.ContinueOnError)
		fs.SetOutput(io.Discard) // handler formats errors itself
		handler := factory(fs)
		if !fs.HasFlags() {
			// No flags declared: pass all args through unchanged.
			return handler(ctx, callCtx, args)
		}
		if normalize != nil {
			args = normalize(args)
		}
		hasHelp := fs.Lookup("help") != nil
		// Honor `--help` once it's reached in argv to match GNU coreutils.
		// flagparser.TrialHelpTrimIndex trial-parses the prefix; if every
		// preceding option parses cleanly the suffix is safely discardable
		// and the builtin's handler short-circuits on `--help`. Builtins
		// with handler-time validation (e.g. head/tail's numeric -n/-c
		// checks) must validate BEFORE the `--help` short-circuit fires,
		// otherwise an invalid value followed by `--help` would silently
		// print help.
		if hasHelp {
			if idx, ok := flagparser.TrialHelpTrimIndex(name, func(trial *pflag.FlagSet) {
				_ = factory(trial)
			}, args); ok {
				args = args[:idx+1]
			}
		}
		if err := fs.Parse(args); err != nil {
			callCtx.Errf("%s: %s\n", name, flagparser.RewriteError(err, args))
			if hasHelp {
				callCtx.Errf("Try '%s --help' for more information.\n", name)
			}
			return Result{Code: 1}
		}
		return handler(ctx, callCtx, fs.Args())
	})
}

// CallContext provides the capabilities available to builtin commands.
// It is created by the Runner for each builtin invocation.
type CallContext struct {
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader

	// InLoop is true when the builtin runs inside a for loop.
	InLoop bool

	// LastExitCode is the exit code from the previous command.
	LastExitCode uint8

	// OpenFile opens a file within the shell's path restrictions.
	OpenFile func(ctx context.Context, path string, flags int, mode os.FileMode) (io.ReadWriteCloser, error)

	// OpenFileForWrite opens a file for guarded write-style builtins within
	// the shell's path restrictions. The open happens before any host
	// delegation so the host command can receive a stable fd instead of a raw
	// pathname.
	OpenFileForWrite func(ctx context.Context, path string, append bool) (*os.File, error)

	// OpenExistingFileForWrite opens an existing file for guarded host
	// mutations that must not create, truncate, or append during validation.
	OpenExistingFileForWrite func(ctx context.Context, path string) (*os.File, error)

	// ReadDir reads a directory within the shell's path restrictions.
	// Entries are returned sorted by name. Used by builtins like ls
	// that need deterministic sorted output.
	ReadDir func(ctx context.Context, path string) ([]fs.DirEntry, error)

	// OpenDir opens a directory within the shell's path restrictions for
	// incremental reading via ReadDir(n). Caller must close the handle.
	OpenDir func(ctx context.Context, path string) (fs.ReadDirFile, error)

	// IsDirEmpty checks whether a directory is empty by reading at most
	// one entry. More efficient than reading all entries.
	IsDirEmpty func(ctx context.Context, path string) (bool, error)

	// ReadDirLimited reads directory entries, skipping the first offset entries
	// and returning up to maxRead entries sorted by name within the read window.
	// Returns (entries, truncated, error). When truncated is true, the directory
	// contained more entries beyond the returned set.
	ReadDirLimited func(ctx context.Context, path string, offset, maxRead int) ([]fs.DirEntry, bool, error)

	// StatFile returns file info within the shell's path restrictions (follows symlinks).
	StatFile func(ctx context.Context, path string) (fs.FileInfo, error)

	// LstatFile returns file info within the shell's path restrictions (does not follow symlinks).
	LstatFile func(ctx context.Context, path string) (fs.FileInfo, error)

	// ReadlinkFile returns the destination of a symbolic link within the
	// shell's path restrictions.
	ReadlinkFile func(ctx context.Context, path string) (string, error)

	// AccessFile checks whether the file at path is accessible with the given mode
	// within the shell's path restrictions. Mode: 0x04=read, 0x02=write, 0x01=execute.
	AccessFile func(ctx context.Context, path string, mode uint32) error

	// PortableErr normalizes an OS error to a POSIX-style message.
	PortableErr func(err error) string

	// Now is the time captured at the start of each Run() call. Builtins
	// should use this instead of calling time.Now() directly, so the time
	// source is consistent across all commands in a single run.
	//
	// Note: this means all builtins within one Run() share the same reference
	// time, whereas bash evaluates each command against its own invocation
	// time. This is an intentional trade-off for consistency within a script
	// run.
	//
	// Run() always sets this before dispatching any builtin; Reset() clears
	// it, so it is always re-set by the next Run() call. The zero value
	// (time.Time{}) is reserved as the unset sentinel; callers constructing
	// CallContext directly (e.g. in tests) must set this to a non-zero value
	// before invoking builtins that use time predicates (find -mmin/-mtime,
	// ls -l).
	Now time.Time

	// FileIdentity extracts canonical file identity from FileInfo.
	// On Unix: dev+inode from Stat_t. On Windows: volume serial + file index
	// via GetFileInformationByHandle. The path parameter is needed on Windows
	// where FileInfo.Sys() lacks identity fields; Unix ignores it.
	FileIdentity func(path string, info fs.FileInfo) (FileID, bool)

	// CommandAllowed reports whether a command name is permitted under the
	// current shell policy. Used by the help builtin to list only executable
	// commands.
	CommandAllowed func(name string) bool

	// AllowedPathsList returns the resolved absolute paths of the
	// configured AllowedPaths sandbox roots. An empty/nil slice means no
	// allowed paths are configured, which blocks all filesystem access.
	// Used by the help builtin to surface the active sandbox roots.
	AllowedPathsList func() []string

	// WorkDir returns the shell's current working directory (absolute path).
	// Used by builtins that need to compute absolute paths for sub-operations.
	WorkDir func() string

	// HostPrefix returns the configured host-mount prefix used by
	// container-style sandboxes to translate host-absolute paths
	// (e.g. /var/log/pods/...) into the prefixed paths the sandbox can
	// open (e.g. /mnt/host/var/log/pods/...). Returns "" when no prefix
	// is configured. Builtins that resolve absolute symlink targets
	// (e.g. pwd -P) use this to keep their output consistent with what
	// the sandbox itself accepts.
	HostPrefix func() string

	// CanonicalizeRootPrefix translates a configured AllowedPaths root
	// prefix in absPath to that root's canonical (symlink-resolved)
	// form. Used by `pwd -P` so that when the sandbox root is itself a
	// symlink (e.g. /tmp/link -> /tmp/real), the printed path reflects
	// the resolution that os.Root has already followed implicitly. If
	// absPath is outside every root or the matching root is not a
	// symlink, the input is returned unchanged.
	CanonicalizeRootPrefix func(absPath string) string

	// ChangeDir mutates the shell's working directory. The supplied path
	// must be absolute. Implementations validate that the target exists,
	// is a directory, and lies inside AllowedPaths; on any failure the
	// previous working directory is preserved and an error is returned.
	// On success, $OLDPWD is set to the previous directory and $PWD is
	// set to absDir. Used exclusively by the cd builtin.
	ChangeDir func(absDir string) error

	// LookupEnvVar reads an environment variable from the shell's
	// overlay environment. Returns (value, true) if the variable is
	// set, ("", false) otherwise. The cd builtin uses this to resolve
	// $HOME (no-arg form) and $OLDPWD (the `cd -` form) without
	// requiring a full WriteEnviron handle on every CallContext.
	LookupEnvVar func(name string) (string, bool)

	// RunCommand executes a builtin command within the shell's sandbox.
	// dir overrides the working directory for path resolution.
	// Returns the command's exit code.
	RunCommand func(ctx context.Context, dir string, name string, args []string) (uint8, error)

	// RunCommandWithStdin is like RunCommand but lets the caller supply a
	// stdin reader for the child. Used by xargs to give children empty
	// stdin (matching POSIX behavior of redirecting child stdin from
	// /dev/null) while still reading items from the parent's stdin itself.
	// If nil, callers should fall back to RunCommand.
	RunCommandWithStdin func(ctx context.Context, dir string, name string, args []string, stdin io.Reader) (uint8, error)

	// RunHostCommand executes a host command after the builtin has validated
	// its restricted contract. This is intentionally separate from RunCommand,
	// which only dispatches other rshell builtins.
	RunHostCommand func(ctx context.Context, name string, args []string) (uint8, error)

	// HostCommandAvailable reports whether a guarded host command has an
	// execution path configured. Builtins that must open write targets before
	// delegation use this to avoid create/truncate side effects when the host
	// command would only be rejected by the default no-exec handler.
	HostCommandAvailable func(name string) bool

	// RunHostCommandWithFiles is like RunHostCommand but passes additional
	// sandbox-opened files through HandlerContext.ExtraFiles. Host handlers
	// that execute via os/exec should wire these to Cmd.ExtraFiles so paths
	// returned by HostExtraFilePath refer to the opened files.
	RunHostCommandWithFiles func(ctx context.Context, name string, args []string, extraFiles []*os.File) (uint8, error)

	// RunHostCommandWithFilesCapture is like RunHostCommandWithFiles, but
	// captures stdout and stderr for builtins that need to return structured
	// command receipts. It is optional so older direct CallContext tests can
	// keep constructing only the capabilities they need.
	RunHostCommandWithFilesCapture func(ctx context.Context, name string, args []string, extraFiles []*os.File) (CapturedHostCommand, error)

	// SetVar assigns a value to a shell variable in the calling shell's
	// scope. Returns an error if the value exceeds the per-variable size
	// limit or if the total variable-storage cap would be exceeded.
	// Used by builtins that mutate parent-shell state, such as read.
	SetVar func(name, value string) error

	// GetVar returns the value of a shell variable. The bool reports
	// whether the variable was set; an unset variable returns ("", false).
	// Used by builtins that need to consult shell state, such as read
	// reading IFS for field-splitting.
	GetVar func(name string) (value string, ok bool)

	// Proc provides access to the proc filesystem for the ps builtin.
	// The path is fixed at construction time and cannot be overridden by callers.
	Proc *ProcProvider
}

// Out writes a string to stdout.
func (c *CallContext) Out(s string) {
	io.WriteString(c.Stdout, s)
}

// Outf writes a formatted string to stdout.
func (c *CallContext) Outf(format string, a ...any) {
	fmt.Fprintf(c.Stdout, format, a...)
}

// OutJSON writes v as a single compact JSON line to stdout.
func (c *CallContext) OutJSON(v any) Result {
	data, err := json.Marshal(v)
	if err != nil {
		c.Errf("json: %s\n", err)
		return Result{Code: 1}
	}
	c.Out(string(data))
	c.Out("\n")
	return Result{}
}

// Errf writes a formatted string to stderr.
func (c *CallContext) Errf(format string, a ...any) {
	fmt.Fprintf(c.Stderr, format, a...)
}

const hostExtraFileBaseFD = 3

// HostExtraFilePath returns the argv path for an ExtraFiles entry. The first
// extra file is exposed to host commands as /dev/fd/3, matching os/exec's
// Cmd.ExtraFiles fd numbering on Unix-like platforms. Callers must only use
// this when HostExtraFilesSupported reports true.
func HostExtraFilePath(index int) string {
	return fmt.Sprintf("/dev/fd/%d", hostExtraFileBaseFD+index)
}

// HostExtraFilesSupported reports whether host commands can receive files via
// HandlerContext.ExtraFiles and address them with HostExtraFilePath.
func HostExtraFilesSupported() bool {
	return runtime.GOOS != "windows"
}

// InvokeHostCommand runs a guarded host command and converts failures into a
// shell Result suitable for builtins.
func (c *CallContext) InvokeHostCommand(ctx context.Context, name string, args []string) Result {
	return c.InvokeHostCommandWithFiles(ctx, name, args, nil)
}

// InvokeHostCommandWithFiles runs a guarded host command with additional
// sandbox-opened files and converts failures into a shell Result.
func (c *CallContext) InvokeHostCommandWithFiles(ctx context.Context, name string, args []string, extraFiles []*os.File) Result {
	for _, f := range extraFiles {
		defer f.Close()
	}
	if len(extraFiles) > 0 && !HostExtraFilesSupported() {
		c.Errf("%s: host file descriptor handoff is not supported on this platform\n", name)
		return Result{Code: 1}
	}

	var (
		code uint8
		err  error
	)
	switch {
	case c.RunHostCommandWithFiles != nil:
		code, err = c.RunHostCommandWithFiles(ctx, name, args, extraFiles)
	case len(extraFiles) == 0 && c.RunHostCommand != nil:
		code, err = c.RunHostCommand(ctx, name, args)
	default:
		c.Errf("%s: host command execution not available\n", name)
		return Result{Code: 127}
	}
	if err != nil {
		c.Errf("%s: %s\n", name, err)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Result{Code: 1, Exiting: true}
		}
		return Result{Code: 1}
	}
	return Result{Code: code}
}

// CaptureHostCommandWithFiles runs a guarded host command with additional
// sandbox-opened files and captures stdout/stderr for structured receipts.
func (c *CallContext) CaptureHostCommandWithFiles(ctx context.Context, name string, args []string, extraFiles []*os.File) (CapturedHostCommand, Result, bool) {
	for _, f := range extraFiles {
		defer f.Close()
	}
	if len(extraFiles) > 0 && !HostExtraFilesSupported() {
		c.Errf("%s: host file descriptor handoff is not supported on this platform\n", name)
		return CapturedHostCommand{}, Result{Code: 1}, false
	}
	if c.RunHostCommandWithFilesCapture == nil {
		c.Errf("%s: host command capture is not available\n", name)
		return CapturedHostCommand{}, Result{Code: 127}, false
	}
	output, err := c.RunHostCommandWithFilesCapture(ctx, name, args, extraFiles)
	if err != nil {
		c.Errf("%s: %s\n", name, err)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return CapturedHostCommand{}, Result{Code: 1, Exiting: true}, false
		}
		return CapturedHostCommand{}, Result{Code: 1}, false
	}
	return output, Result{}, true
}

// CaptureHostCommand is CaptureHostCommandWithFiles without extra files.
func (c *CallContext) CaptureHostCommand(ctx context.Context, name string, args []string) (CapturedHostCommand, Result, bool) {
	return c.CaptureHostCommandWithFiles(ctx, name, args, nil)
}

// IsBrokenPipe reports whether err is a broken-pipe (EPIPE) error,
// which occurs when writing to a pipe whose read end has been closed.
// In bash this triggers SIGPIPE which silently terminates the writer;
// builtins should use this to suppress error messages on pipe closure.
func IsBrokenPipe(err error) bool {
	return err != nil && errors.Is(err, syscall.EPIPE)
}

// FileID is a comparable file identity for cycle detection.
// On Unix: device + inode. On Windows: volume serial + file index.
// Used as map key for visited-set tracking.
type FileID struct {
	Dev uint64
	Ino uint64
}

// Result captures the outcome of executing a builtin command.
type Result struct {
	// Code is the exit status code.
	Code uint8

	// Exiting signals that the shell should exit (set by the "exit" builtin).
	Exiting bool

	// BreakN > 0 means break out of N enclosing loops.
	BreakN int

	// ContinueN > 0 means continue from N enclosing loops.
	ContinueN int
}

var registry = map[string]HandlerFunc{}

// CommandMeta holds metadata about a registered builtin command.
type CommandMeta struct {
	Name        string
	Description string
	Help        string
	HasFlags    bool // true when MakeFlags registers at least one flag
}

var metaRegistry = map[string]CommandMeta{}

func addToRegistry(name string, fn HandlerFunc) {
	if _, exists := registry[name]; exists {
		panic("builtin already registered: " + name)
	}
	// Defense-in-depth: Register() already checks this before calling
	// addToRegistry, so this branch is unreachable from current callers.
	// Kept to guard against future callers that bypass Register().
	if _, exists := featureByName[name]; exists {
		panic("builtin name conflicts with rshell feature: " + name)
	}
	registry[name] = fn
}

// Lookup returns the handler for a builtin command.
func Lookup(name string) (HandlerFunc, bool) {
	fn, ok := registry[name]
	return fn, ok
}

// Names returns a sorted list of all registered builtin command names.
func Names() []string {
	names := make([]string, 0, len(metaRegistry))
	for name := range metaRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Meta returns the metadata for a registered builtin command.
func Meta(name string) (CommandMeta, bool) {
	m, ok := metaRegistry[name]
	return m, ok
}

// NormalizeBareNumberArg rewrites legacy -N shorthand (e.g. -5) to -n N so
// that pflag can parse it. Only a bare -<digits> token in the first argument
// position is rewritten; -<digits> appearing later in the argument list is
// left unchanged (matching GNU head/tail behavior where the obsolete form is
// only accepted as the first option). Processing stops at "--".
//
// When the first argument is a value-taking flag (-n, -c, --lines, --bytes),
// the second argument is its value and must not be rewritten — even if it
// looks like -<digits> (e.g. "head -n -9223372036854775809").
//
// valueFlags lists the flags that consume the next argument as a value
// (e.g. []string{"-n", "-c", "--lines", "--bytes"}).
func NormalizeBareNumberArg(args []string, valueFlags []string) []string {
	if len(args) == 0 {
		return args
	}
	a := args[0]
	if a == "--" {
		return args
	}
	if len(a) >= 2 && a[0] == '-' && a[1] >= '0' && a[1] <= '9' {
		out := make([]string, 0, len(args)+1)
		out = append(out, "-n", a[1:])
		out = append(out, args[1:]...)
		return out
	}
	return args
}
