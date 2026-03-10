// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/pflag"
)

// FlagSet is a type alias for pflag.FlagSet. Builtin command files that use
// FlaggedCommand receive a *FlagSet from the framework without needing to
// import pflag directly (the builtins package is always allowed by the import
// allowlist, so builtins.FlagSet is accessible in command implementation files).
type FlagSet = pflag.FlagSet

// HandlerFunc is the signature for a builtin command implementation.
type HandlerFunc func(ctx context.Context, callCtx *CallContext, args []string) Result

// BoundHandlerFunc is the run function returned by a FlaggedHandlerFunc. args
// contains only the positional (non-flag) arguments after the framework has
// parsed the FlagSet.
type BoundHandlerFunc func(ctx context.Context, callCtx *CallContext, args []string) Result

// FlaggedHandlerFunc is called once per invocation to register flags on the
// framework-provided FlagSet and return a BoundHandlerFunc whose flag variables
// are captured by closure. The framework calls Parse then invokes the bound
// handler with the remaining positional arguments.
type FlaggedHandlerFunc func(fs *FlagSet) BoundHandlerFunc

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

	// PortableErr normalizes an OS error to a POSIX-style message.
	PortableErr func(err error) string
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

// Command pairs a builtin name with its handler, used for explicit
// registration in the all package.
type Command struct {
	Name string
	Run  HandlerFunc
}

// FlaggedCommand pairs a builtin name with a flag-declaring factory. Use this
// instead of Command for builtins that accept flags. The framework creates a
// *FlagSet, passes it to MakeFlags so the command can register its flags, then
// calls Parse and invokes the returned handler with positional arguments only.
type FlaggedCommand struct {
	Name      string
	MakeFlags FlaggedHandlerFunc
}

// Registrable is implemented by both Command and FlaggedCommand so they can be
// collected in a single slice and registered uniformly.
type Registrable interface {
	Register()
}

// Register adds the Command to the builtin registry.
func (c Command) Register() { Register(c.Name, c.Run) }

// Register adds the FlaggedCommand to the builtin registry via the flagged
// adapter, which handles FlagSet creation, Parse, and error reporting.
func (c FlaggedCommand) Register() { RegisterFlagged(c.Name, c.MakeFlags) }

var registry = map[string]HandlerFunc{}

// Register adds a builtin command to the registry.
// It panics if name is already registered, catching duplicate registrations at startup.
func Register(name string, fn HandlerFunc) {
	if _, exists := registry[name]; exists {
		panic("builtin already registered: " + name)
	}
	registry[name] = fn
}

// RegisterFlagged registers a FlaggedHandlerFunc under name. For each
// invocation the adapter creates a fresh *FlagSet, calls factory to register
// flags and obtain the bound handler, parses the raw args, writes any error to
// stderr (exit 1), and then calls the handler with the positional args.
func RegisterFlagged(name string, factory FlaggedHandlerFunc) {
	Register(name, func(ctx context.Context, callCtx *CallContext, args []string) Result {
		fs := pflag.NewFlagSet(name, pflag.ContinueOnError)
		fs.SetOutput(io.Discard) // handler formats errors itself
		handler := factory(fs)
		if err := fs.Parse(args); err != nil {
			callCtx.Errf("%s: %v\n", name, err)
			return Result{Code: 1}
		}
		return handler(ctx, callCtx, fs.Args())
	})
}

// Lookup returns the handler for a builtin command.
func Lookup(name string) (HandlerFunc, bool) {
	fn, ok := registry[name]
	return fn, ok
}

