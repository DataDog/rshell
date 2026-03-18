// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package interp implements a restricted shell interpreter designed for
// safe, sandboxed execution. It supports a subset of Bash syntax with
// many features intentionally blocked (see [validateNode]).
//
// The interpreter behaves like a non-interactive shell. External command
// execution and filesystem access are denied by default and must be
// explicitly enabled via [RunnerOption] functions.
package interp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/allowedpaths"
)

// runnerConfig holds the immutable configuration of a [Runner].
// These fields are set during construction ([New]) and first [Runner.Reset],
// and never change afterwards.
type runnerConfig struct {
	// Env specifies the initial environment for the interpreter, which must
	// not be nil. It can only be set via [Env].
	Env expand.Environ

	// execHandler is responsible for executing programs. It must not be nil.
	execHandler ExecHandlerFunc

	// openHandler is a function responsible for opening files. It must not be nil.
	openHandler OpenHandlerFunc

	// readDirHandler is a function responsible for reading directories during
	// glob expansion. It must be non-nil.
	readDirHandler ReadDirHandlerFunc

	// sandbox restricts file/directory access to allowed directories.
	// nil (default) blocks all file access; populate via AllowedPaths option.
	sandbox *allowedpaths.Sandbox

	// allowedCommands is the set of command names (builtins or external) that
	// the interpreter is permitted to execute. If nil and allowAllCommands is
	// false, no commands are allowed.
	allowedCommands map[string]bool

	// allowAllCommands bypasses the allowedCommands check and permits any
	// command. Intended for testing convenience.
	allowAllCommands bool

	// procPath is the path to the proc filesystem used by the ps builtin.
	// Defaults to "/proc" when empty.
	procPath string

	// usedNew is set by New() and checked in Reset() to ensure a Runner
	// was properly constructed rather than zero-initialized.
	usedNew bool

	// origDir, origParams, and origStd* preserve the initial values
	// set during construction so that [Runner.Reset] can restore them.
	origDir    string
	origParams []string
	origStdin  *os.File
	origStdout io.Writer
	origStderr io.Writer
}

// runnerState holds the per-execution mutable state of a [Runner].
// [Runner.Reset] reinitializes this entire struct from [runnerConfig].
type runnerState struct {
	// writeEnv overlays [runnerConfig.Env] so that we can write environment
	// variables as an overlay.
	writeEnv expand.WriteEnviron

	// Dir specifies the working directory of the command, which must be an
	// absolute path.
	Dir string

	// Params are the current shell parameters, e.g. from running a shell
	// file. Note: positional parameter expansion ($@, $*, $1, etc.) is
	// blocked by the AST validator in this restricted interpreter.
	Params []string

	stdin  *os.File // e.g. the read end of a pipe
	stdout io.Writer
	stderr io.Writer

	ecfg *expand.Config
	ectx context.Context // just so that subshell can use it again

	// didReset remembers whether the runner has ever been reset. This is
	// used so that Reset is automatically called when running any program
	// or node for the first time on a Runner.
	didReset bool

	filename string // only if Node was a File

	// >0 to break or continue out of N enclosing loops
	breakEnclosing, contnEnclosing int

	inLoop bool

	// The current and last exit statuses. They can only be different if
	// the interpreter is in the middle of running a statement. In that
	// scenario, 'exit' is the status for the current statement being run,
	// and 'lastExit' corresponds to the previous statement that was run.
	exit     exitStatus
	lastExit exitStatus

	lastExpandExit exitStatus // used to surface exit statuses while expanding fields

	// startTime is captured once at the beginning of Run() and passed to
	// all builtin invocations so they share a consistent time reference.
	startTime time.Time
}

// A Runner interprets shell programs. It can be reused, but it is not safe for
// concurrent use. Use [New] to build a new Runner.
//
// Runner's exported fields are meant to be configured via [RunnerOption];
// once a Runner has been created, the fields should be treated as read-only.
type Runner struct {
	runnerConfig
	runnerState
}

// exitStatus holds the state of the shell after running one command.
// Beyond the exit status code, it also holds whether the shell should return or exit,
// as well as any Go error values that should be given back to the user.
type exitStatus struct {
	// code is the exit status code.
	code uint8

	exiting   bool // whether the current shell is exiting
	fatalExit bool // whether the current shell is exiting due to a fatal error; err below must not be nil

	// err is a fatal error if fatal is true, or a non-fatal custom error from a handler.
	// Used so that running a single statement with a custom handler
	// which returns a non-fatal Go error, such as a Go error wrapping [NewExitStatus],
	// can be returned by [Runner.Run] without being lost entirely.
	err error
}

func (e *exitStatus) ok() bool { return e.code == 0 }

func (e *exitStatus) oneIf(b bool) {
	if b {
		e.code = 1
	} else {
		e.code = 0
	}
}

func (e *exitStatus) fatal(err error) {
	if !e.fatalExit && err != nil {
		e.exiting = true
		e.fatalExit = true
		e.err = err
		if e.code == 0 {
			e.code = 1
		}
	}
}

func (e *exitStatus) fromHandlerError(err error) {
	if err != nil {
		var es ExitStatus
		if errors.As(err, &es) {
			e.err = err
			e.code = uint8(es)
		} else {
			e.fatal(err) // handler's custom fatal error
		}
	} else {
		e.code = 0
	}
}

// New creates a new Runner, applying a number of options. If applying any of
// the options results in an error, it is returned.
//
// Any unset options fall back to their defaults. For example, not supplying the
// environment defaults to an empty environment (no host env inherited), and not
// supplying the standard output writer means that the output will be discarded.
func New(opts ...RunnerOption) (*Runner, error) {
	registerBuiltins()
	r := &Runner{
		runnerConfig: runnerConfig{usedNew: true},
	}
	for _, opt := range opts {
		if err := opt(r); err != nil {
			_ = r.Close()
			return nil, err
		}
	}

	// Set the default fallbacks, if necessary.
	// Default to an empty environment to avoid propagating parent env vars.
	if r.Env == nil {
		r.Env = expand.ListEnviron()
	}
	if r.Dir == "" {
		dir, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("could not get current dir: %w", err)
		}
		r.Dir = dir
	}
	if r.stdout == nil || r.stderr == nil {
		StdIO(r.stdin, r.stdout, r.stderr)(r)
	}
	return r, nil
}

// RunnerOption can be passed to [New] to alter a [Runner]'s behaviour.
type RunnerOption func(*Runner) error

func stdinFile(r io.Reader) (*os.File, error) {
	switch r := r.(type) {
	case *os.File:
		return r, nil
	case nil:
		return nil, nil
	default:
		pr, pw, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		go func() {
			io.Copy(pw, r)
			pw.Close()
		}()
		return pr, nil
	}
}

// Env sets the initial environment for the interpreter. Each pair must be in
// "KEY=value" format. If this option is not used, the interpreter starts with
// an empty environment (no host environment variables are inherited).
func Env(pairs ...string) RunnerOption {
	return func(r *Runner) error {
		r.Env = expand.ListEnviron(pairs...)
		return nil
	}
}

// StdIO configures an interpreter's standard input, standard output, and
// standard error. If out or err are nil, they default to a writer that discards
// the output.
//
// Note that providing a non-nil standard input other than [*os.File] will require
// an [os.Pipe] and spawning a goroutine to copy into it,
// as an [os.File] is the only way to share a reader with subprocesses.
// This may cause the interpreter to consume the entire reader.
// See [os/exec.Cmd.Stdin].
//
// When providing an [*os.File] as standard input, consider using an [os.Pipe]
// as it has the best chance to support cancellable reads via [os.File.SetReadDeadline],
// so that cancelling the runner's context can stop a blocked standard input read.
func StdIO(in io.Reader, out, err io.Writer) RunnerOption {
	return func(r *Runner) error {
		stdin, _err := stdinFile(in)
		if _err != nil {
			return _err
		}
		r.stdin = stdin
		if out == nil {
			out = io.Discard
		}
		r.stdout = out
		if err == nil {
			err = io.Discard
		}
		r.stderr = err
		return nil
	}
}

// Reset returns a runner to its initial state, right before the first call to
// Run or Reset.
//
// Typically, this function only needs to be called if a runner is reused to run
// multiple programs non-incrementally. Not calling Reset between each run will
// mean that the shell state will be kept, including variables, options, and the
// current directory.
func (r *Runner) Reset() {
	if !r.usedNew {
		r.exit.fatal(fmt.Errorf("use interp.New to construct a Runner"))
		return
	}
	if !r.didReset {
		r.origDir = r.Dir
		r.origParams = r.Params
		r.origStdin = r.stdin
		r.origStdout = r.stdout
		r.origStderr = r.stderr

		// Install sandbox-backed handlers. AllowedPaths opens os.Root handles
		// eagerly during construction, so there is no filesystem race here.
		// Default: block all file access (nil sandbox).
		if r.openHandler == nil {
			r.openHandler = func(ctx context.Context, path string, flag int, perm os.FileMode) (io.ReadWriteCloser, error) {
				return r.sandbox.Open(path, HandlerCtx(ctx).Dir, flag, perm)
			}
			r.readDirHandler = func(ctx context.Context, path string) ([]os.DirEntry, error) {
				return r.sandbox.ReadDirForGlob(path, HandlerCtx(ctx).Dir)
			}
			r.execHandler = noExecHandler()
		}
		if r.execHandler == nil {
			r.execHandler = noExecHandler()
		}
	}
	// Reset only the mutable state; config is preserved.
	// startTime is intentionally zeroed here by the struct literal; it will
	// be set again by Run() before any builtin is invoked.
	r.runnerState = runnerState{
		Dir:    r.origDir,
		Params: r.origParams,
		stdin:  r.origStdin,
		stdout: r.origStdout,
		stderr: r.origStderr,
	}
	r.writeEnv = &overlayEnviron{parent: r.Env}
	r.setVarString("PWD", r.Dir)
	// IFS is intentionally mutable: scripts may set it to customise field splitting,
	// which is standard POSIX behaviour. Callers that provide a custom ExecHandler
	// should be aware that a script can set IFS to a non-whitespace value (e.g.
	// IFS=/) to manipulate how unquoted variable expansions are split before being
	// passed to executed commands (argument smuggling). The default noExecHandler
	// blocks all external execution, limiting the practical impact of this vector.
	r.setVarString("IFS", " \t\n")
	r.setVarString("OPTIND", "1")

	r.didReset = true
}

// ExitStatus is a non-zero status code resulting from running a shell node.
type ExitStatus uint8

func (s ExitStatus) Error() string { return fmt.Sprintf("exit status %d", s) }

// Run interprets a node, which can be a [*File], [*Stmt], or [Command]. If a non-nil
// error is returned, it will typically contain a command's exit status, which
// can be retrieved with [errors.As] and [ExitStatus].
//
// Run can be called multiple times synchronously to interpret programs
// incrementally. To reuse a [Runner] without keeping the internal shell state,
// call Reset.
func (r *Runner) Run(ctx context.Context, node syntax.Node) (retErr error) {
	defer func() {
		if rec := recover(); rec != nil {
			panicOut := io.Writer(io.Discard)
			if r != nil && r.stderr != nil {
				panicOut = r.stderr
			}
			func() {
				defer func() { recover() }()
				fmt.Fprintf(panicOut, "rshell: internal panic: %v\n", rec)
			}()
			retErr = fmt.Errorf("internal error")
		}
	}()
	if !r.didReset {
		r.Reset()
		if r.exit.fatalExit {
			return r.exit.err
		}
	}
	r.startTime = time.Now()
	r.fillExpandConfig(ctx)
	if err := validateNode(node); err != nil {
		fmt.Fprintln(r.stderr, err)
		return ExitStatus(2)
	}
	r.exit = exitStatus{}
	r.filename = ""
	switch node := node.(type) {
	case *syntax.File:
		r.filename = node.Name
		r.stmts(ctx, node.Stmts)
	case *syntax.Stmt:
		r.stmt(ctx, node)
	case syntax.Command:
		r.cmd(ctx, node)
	default:
		return fmt.Errorf("node can only be File, Stmt, or Command: %T", node)
	}
	// Return the first of: a fatal error, a non-fatal handler error, or the exit code.
	if err := r.exit.err; err != nil {
		return err
	}
	if code := r.exit.code; code != 0 {
		return ExitStatus(code)
	}
	return nil
}

// Close releases resources held by the Runner, such as os.Root file descriptors
// opened by AllowedPaths. It is safe to call Close multiple times.
func (r *Runner) Close() error {
	return r.sandbox.Close()
}

// AllowedPaths restricts file and directory access to the specified directories.
// Paths must be absolute directories that exist. When set, only files within
// these directories can be opened, read, or executed.
//
// When not set (default), all file access is blocked.
// An empty slice also blocks all file access.
func AllowedPaths(paths []string) RunnerOption {
	return func(r *Runner) error {
		sb, err := allowedpaths.New(paths)
		if err != nil {
			return err
		}
		r.sandbox = sb
		return nil
	}
}

// AllowedCommands restricts command execution to the specified command names.
// Names must use the "rshell:" namespace prefix (e.g. "rshell:cat",
// "rshell:find"). Names without a colon separator or with an unknown namespace
// are rejected. The bare command name (after the prefix) is stored internally
// and matched exactly against the command name (args[0]) at execution time.
//
// Only commands whose name appears in the list may be executed; all others are
// rejected with "<cmd>: command not allowed".
//
// After prefix stripping, path-containing names (e.g. "rshell:/bin/bash")
// will not match bare command names and vice versa. Empty strings and empty
// command names are rejected.
//
// When not set (default), no commands are allowed unless [AllowAllCommands] is
// used.
func AllowedCommands(names []string) RunnerOption {
	return func(r *Runner) error {
		m := make(map[string]bool, len(names))
		for _, n := range names {
			if n == "" {
				return fmt.Errorf("AllowedCommands: empty command name")
			}
			idx := strings.Index(n, ":")
			if idx < 0 {
				return fmt.Errorf("AllowedCommands: %q missing namespace prefix (expected \"rshell:<command>\")", n)
			}
			ns := n[:idx]
			cmd := n[idx+1:]
			if strings.Index(cmd, ":") >= 0 {
				return fmt.Errorf("AllowedCommands: %q contains multiple colons; expected format \"rshell:<command>\"", n)
			}
			if ns != "rshell" {
				return fmt.Errorf("AllowedCommands: %q has unknown namespace %q (only \"rshell\" is supported)", n, ns)
			}
			if cmd == "" {
				return fmt.Errorf("AllowedCommands: %q has empty command name", n)
			}
			m[cmd] = true
		}
		r.allowedCommands = m
		return nil
	}
}

// AllowAllCommands permits execution of any command (builtin or external),
// bypassing the [AllowedCommands] restriction. This is intended for testing
// convenience.
func AllowAllCommands() RunnerOption {
	return func(r *Runner) error {
		r.allowAllCommands = true
		return nil
	}
}

// ProcPath sets the path to the proc filesystem used by the ps builtin.
// When not set (default), ps uses "/proc".
func ProcPath(path string) RunnerOption {
	return func(r *Runner) error {
		r.procPath = path
		return nil
	}
}

// subshell creates a child Runner that inherits the parent's state.
// If background is false, the child shares the parent's environment overlay
// without copying, which is more efficient but must not be used concurrently.
func (r *Runner) subshell(background bool) *Runner {
	if !r.didReset {
		r.Reset()
	}
	r2 := &Runner{
		runnerConfig: r.runnerConfig,
		runnerState: runnerState{
			Dir:       r.Dir,
			Params:    r.Params,
			stdin:     r.stdin,
			stdout:    r.stdout,
			stderr:    r.stderr,
			filename:  r.filename,
			exit:      r.exit,
			lastExit:  r.lastExit,
			startTime: r.startTime,
		},
	}
	r2.writeEnv = newOverlayEnviron(r.writeEnv, background)
	r2.fillExpandConfig(r.ectx)
	r2.didReset = true
	return r2
}
