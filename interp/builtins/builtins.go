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
	Name      string
	MakeFlags func(*FlagSet) HandlerFunc
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
	// Entries are returned sorted by name. This is an intentional design
	// choice for deterministic output, but means builtins that walk
	// directories (ls -R, find) produce sorted output rather than the
	// filesystem-dependent order used by GNU coreutils/findutils.
	ReadDir func(ctx context.Context, path string) ([]fs.DirEntry, error)

	// StatFile returns file info within the shell's path restrictions (follows symlinks).
	StatFile func(ctx context.Context, path string) (fs.FileInfo, error)

	// LstatFile returns file info within the shell's path restrictions (does not follow symlinks).
	LstatFile func(ctx context.Context, path string) (fs.FileInfo, error)

	// AccessFile checks whether the file at path is accessible with the given mode
	// within the shell's path restrictions. Mode: 0x04=read, 0x02=write, 0x01=execute.
	AccessFile func(ctx context.Context, path string, mode uint32) error

	// PortableErr normalizes an OS error to a POSIX-style message.
	PortableErr func(err error) string

	// Now returns the current time. Builtins should use this instead of
	// calling time.Now() directly, so the time source is consistent and
	// testable.
	Now func() time.Time
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
