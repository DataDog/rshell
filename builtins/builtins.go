// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"time"

	"github.com/spf13/pflag"
)

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
	MakeFlags   func(*FlagSet) HandlerFunc
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
	metaRegistry[name] = CommandMeta{Name: name, Description: c.Description}
	addToRegistry(name, func(ctx context.Context, callCtx *CallContext, args []string) Result {
		fs := pflag.NewFlagSet(name, pflag.ContinueOnError)
		fs.SetOutput(io.Discard) // handler formats errors itself
		handler := factory(fs)
		if !fs.HasFlags() {
			// No flags declared: pass all args through unchanged.
			return handler(ctx, callCtx, args)
		}
		if err := fs.Parse(args); err != nil {
			callCtx.Errf("%s: %v\n", name, err)
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
	// run. If Now is the zero value, callers should treat it as time.Now().
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
}

var metaRegistry = map[string]CommandMeta{}

func addToRegistry(name string, fn HandlerFunc) {
	if _, exists := registry[name]; exists {
		panic("builtin already registered: " + name)
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
