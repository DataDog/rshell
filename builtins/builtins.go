// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/pflag"
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
		// Honor `--help` once it's reached in argv to match GNU coreutils:
		// `cmd --no-arg-flag --help --bogus` should print help and exit 0.
		// We trim args at the first `--help` only when every preceding
		// token is a registered no-argument flag. Value-takers (`-n 5`),
		// explicit-value forms (`--foo=bar`), positionals, and unknown
		// flags all preclude the trim — those tokens may still fail
		// later validation (e.g. `head -n nope`'s numeric check), and
		// trimming would silently swallow that error by short-circuiting
		// on `--help`. GNU's left-to-right semantics require the earlier
		// failure to surface even when `--help` follows it.
		if hasHelp {
			if idx, ok := safeHelpTrimIndex(fs, args); ok {
				args = args[:idx+1]
			}
		}
		if err := fs.Parse(args); err != nil {
			callCtx.Errf("%s: %s\n", name, rewritePflagError(err, args))
			if hasHelp {
				callCtx.Errf("Try '%s --help' for more information.\n", name)
			}
			return Result{Code: 1}
		}
		return handler(ctx, callCtx, fs.Args())
	})
}

// rewritePflagError translates pflag's default error messages to the
// GNU-getopt-style wording that matches GNU coreutils byte-for-byte.
// It returns the rewritten message without a trailing newline or any
// "cmd:" prefix; callers prepend the builtin name themselves.
//
// args is the argv that was handed to fs.Parse. It is consulted only
// for the unknown-long-flag case, where pflag strips the `=value`
// suffix from its error string but GNU getopt reports the full token
// (`--no-such=value`, not just `--no-such`).
//
// Patterns covered:
//
//	pflag                                       → GNU
//	"unknown flag: --foo"                       → "unrecognized option '--foo'"
//	                                              ("--foo=value" if the original argv used =value)
//	"unknown shorthand flag: 'X' in -..."       → "invalid option -- 'X'"
//	"flag needs an argument: --foo"             → "option '--foo' requires an argument"
//	"flag needs an argument: 'X' in -Y"         → "option requires an argument -- 'X'"
//	`invalid argument "..." for "DESC" flag:    → "option '--LONG' doesn't allow
//	   flag does not allow an argument`              an argument"
//
// Unknown messages are returned unchanged.
func rewritePflagError(err error, args []string) string {
	msg := err.Error()

	// pflag wraps errors returned by a Var's Set(value) as
	//   `invalid argument "VALUE" for "DESC" flag: INNER`
	// where DESC is e.g. `-h, --human-readable` or `--total`. df's
	// noArgBool / unitFlag.Set returns the literal "flag does not
	// allow an argument" so users (and tests) see GNU's "doesn't"
	// instead of the wrapped pflag verbosity.
	if strings.HasPrefix(msg, "invalid argument ") &&
		strings.HasSuffix(msg, "flag does not allow an argument") {
		if d, ok := extractFlagDescriptor(msg); ok {
			// `-X=value` for a no-arg shorthand: GNU getopt iterates
			// shorthand chars and treats `=` as an unknown shorthand,
			// emitting `invalid option -- '='`. pflag instead routes
			// the value through Set, hitting our no-arg guard. Match
			// GNU when the original argv used the shorthand=value form.
			if shortFlagEqualsValueIn(d, args) {
				return "invalid option -- '='"
			}
			return "option '" + longFlagName(d) + "' doesn't allow an argument"
		}
	}

	if rest, ok := strings.CutPrefix(msg, "unknown flag: "); ok {
		// pflag's error carries only the flag name; GNU's reports the
		// full token, so we recover `--foo=value` from argv when the
		// user wrote it that way.
		return "unrecognized option '" + recoverLongFlagToken(rest, args) + "'"
	}

	const shortPrefix = "unknown shorthand flag: '"
	if rest, ok := strings.CutPrefix(msg, shortPrefix); ok {
		if char, _, ok := strings.Cut(rest, "'"); ok {
			return "invalid option -- '" + char + "'"
		}
	}

	if rest, ok := strings.CutPrefix(msg, "flag needs an argument: "); ok {
		// pflag emits two distinct payloads depending on the form:
		//   long form:  `--foo`
		//   short form: `'X' in -Y` (X is the char, Y is the run
		//                            of shorthand letters)
		// GNU getopt uses different wording for each: long flags get
		// `option '--foo' requires an argument`, short flags get
		// `option requires an argument -- 'X'`.
		if char, found := shortMissingArg(rest); found {
			return "option requires an argument -- '" + char + "'"
		}
		return "option '" + rest + "' requires an argument"
	}

	return msg
}

// safeHelpTrimIndex returns the index of the first `--help` in args if
// trimming at it is provably safe — i.e. every preceding token is a
// registered no-argument flag and the scan does not cross a `--`
// end-of-flags marker. Returns false otherwise, signalling the caller
// to leave args alone and let pflag report any earlier validation
// error.
func safeHelpTrimIndex(fs *pflag.FlagSet, args []string) (int, bool) {
	for i, a := range args {
		if a == "--" {
			return 0, false
		}
		if a == "--help" {
			return i, true
		}
		if !isNoArgFlagToken(fs, a) {
			return 0, false
		}
	}
	return 0, false
}

// isNoArgFlagToken reports whether a is a registered flag (long or
// shorthand cluster) whose every component takes no value. Tokens
// containing `=` (explicit-value form) or matching value-taking flags
// return false — both can fail at parse time. Non-ASCII bytes in a
// shorthand cluster also return false: pflag's ShorthandLookup panics
// on inputs longer than one byte and `string(byte)` for any byte ≥
// 0x80 produces a 2-byte UTF-8 encoding.
func isNoArgFlagToken(fs *pflag.FlagSet, a string) bool {
	if len(a) < 2 || a[0] != '-' || a == "-" {
		return false
	}
	if strings.Contains(a, "=") {
		return false
	}
	if strings.HasPrefix(a, "--") {
		f := fs.Lookup(a[2:])
		return f != nil && f.NoOptDefVal != ""
	}
	for i := 1; i < len(a); i++ {
		c := a[i]
		if c > 0x7E || c <= ' ' {
			return false
		}
		f := fs.ShorthandLookup(string(c))
		if f == nil || f.NoOptDefVal == "" {
			return false
		}
	}
	return true
}

// recoverLongFlagToken returns the original argv token for an unknown
// long flag whose stripped name (e.g. `--no-such`) appears in pflag's
// error. If the user wrote `--no-such=value`, the full token is
// returned; otherwise the bare flag is returned. Stops at `--` so a
// later positional like `-- --no-such=foo` is never misclassified.
func recoverLongFlagToken(flag string, args []string) string {
	prefix := flag + "="
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == flag || strings.HasPrefix(a, prefix) {
			return a
		}
	}
	return flag
}

// shortFlagEqualsValueIn reports whether args contains a token of the
// form `-X=...` whose shorthand char X matches the shorthand encoded
// in descriptor (e.g. `-h, --human-readable` → X=`h`). Used to detect
// the GNU-getopt shorthand=value error class.
func shortFlagEqualsValueIn(descriptor string, args []string) bool {
	short, ok := shortFlagFromDescriptor(descriptor)
	if !ok {
		return false
	}
	for _, a := range args {
		if a == "--" {
			break
		}
		if len(a) >= 3 && a[0] == '-' && a[1] != '-' && a[1] == short && a[2] == '=' {
			return true
		}
	}
	return false
}

// shortFlagFromDescriptor extracts the single shorthand char from a
// pflag descriptor like `-h, --human-readable`. Returns false for
// long-only descriptors (`--total`).
func shortFlagFromDescriptor(d string) (byte, bool) {
	if len(d) >= 2 && d[0] == '-' && d[1] != '-' {
		return d[1], true
	}
	return 0, false
}

// shortMissingArg parses pflag's short-form payload for
// `flag needs an argument: 'X' in -Y` and returns the bare char
// (`X`). The second return is false when payload is in the long-form
// (`--foo`), letting the caller fall through.
func shortMissingArg(payload string) (string, bool) {
	if !strings.HasPrefix(payload, "'") {
		return "", false
	}
	char, _, found := strings.Cut(payload[1:], "'")
	if !found {
		return "", false
	}
	return char, true
}

// extractFlagDescriptor parses pflag's `invalid argument "..." for
// "DESC" flag: ...` wrapper and returns the DESC segment.
func extractFlagDescriptor(msg string) (string, bool) {
	_, after, found := strings.Cut(msg, ` for "`)
	if !found {
		return "", false
	}
	desc, _, found := strings.Cut(after, `" flag:`)
	if !found {
		return "", false
	}
	return desc, true
}

// longFlagName returns the long-form (--name) flag name from a pflag
// descriptor like `-X, --LONG` or `--LONG`. For shorthand-only flags
// (rare; e.g. df's hidden `-k`) the descriptor is just `-X` and we
// return it unchanged.
func longFlagName(descriptor string) string {
	if i := strings.LastIndex(descriptor, ", "); i >= 0 {
		return descriptor[i+2:]
	}
	return descriptor
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

// Errf writes a formatted string to stderr.
func (c *CallContext) Errf(format string, a ...any) {
	fmt.Fprintf(c.Stderr, format, a...)
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
