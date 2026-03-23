// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package main provides the rshell CLI — a restricted shell interpreter.
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
	"github.com/spf13/cobra"
	"mvdan.cc/sh/v3/syntax"
)

const exitCodeTimeout = 124

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var (
		command         string
		allowedPaths    string
		allowedCommands string
		allowAllCmds    bool
		timeout         time.Duration
		procPath        string
	)

	cmd := &cobra.Command{
		Use:           "rshell [file ...]",
		Short:         "A restricted shell interpreter for AI agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		// Reject the hidden --command long form: -c is short-only (bash convention).
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			return rejectLongCommand(args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			commandSet := cmd.Flags().Changed("command")
			if commandSet && len(args) > 0 {
				return fmt.Errorf("cannot use -c with file arguments")
			}

			if timeout < 0 {
				return fmt.Errorf("--timeout must be >= 0")
			}

			runCtx := cmd.Context()
			if timeout > 0 {
				var cancel context.CancelFunc
				runCtx, cancel = context.WithTimeout(runCtx, timeout)
				defer cancel()
			}

			var paths []string
			if allowedPaths != "" {
				paths = strings.Split(allowedPaths, ",")
			}

			var cmds []string
			if allowedCommands != "" {
				cmds = strings.Split(allowedCommands, ",")
			}

			execOpts := executeOpts{
				allowedPaths:     paths,
				allowedCommands:  cmds,
				allowAllCommands: allowAllCmds,
				procPath:         procPath,
			}

			if commandSet {
				return execute(runCtx, command, "", execOpts, stdin, stdout, stderr)
			}

			if len(args) > 0 {
				// Read stdin once so each execute() call gets its own
				// reader, avoiding a data race on the shared io.Reader.
				stdinData, err := readAllContext(runCtx, stdin)
				if err != nil {
					return fmt.Errorf("reading stdin: %w", err)
				}

				for _, file := range args {
					f, err := os.Open(file)
					if err != nil {
						return fmt.Errorf("reading %s: %w", file, err)
					}
					data, err := readAllContext(runCtx, f)
					f.Close()
					if err != nil {
						return fmt.Errorf("reading %s: %w", file, err)
					}
					if err := execute(runCtx, string(data), file, execOpts, bytes.NewReader(stdinData), stdout, stderr); err != nil {
						return err
					}
				}
				return nil
			}

			// No -c and no file args: read from stdin.
			stdinData, err := readAllContext(runCtx, stdin)
			if err != nil {
				return fmt.Errorf("reading stdin: %w", err)
			}
			return execute(runCtx, string(stdinData), "", execOpts, strings.NewReader(""), stdout, stderr)
		},
	}

	cmd.SetArgs(args)
	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	cmd.Flags().StringVarP(&command, "command", "c", "", "shell command string to execute")
	cmd.Flags().MarkHidden("command") //nolint:errcheck // flag is guaranteed to exist
	cmd.Flags().StringVarP(&allowedPaths, "allowed-paths", "p", "", "comma-separated list of paths (files or directories) the shell is allowed to access")
	cmd.Flags().StringVar(&allowedCommands, "allowed-commands", "", "comma-separated list of namespaced commands (e.g. rshell:cat,rshell:find)")
	cmd.Flags().BoolVar(&allowAllCmds, "allow-all-commands", false, "allow execution of all commands (builtins and external)")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "maximum execution time for the entire shell run (e.g. 100ms, 5s, 1m)")
	cmd.Flags().StringVar(&procPath, "proc-path", "", "path to the proc filesystem used by ps (default \"/proc\")")

	if err := cmd.ExecuteContext(ctx); err != nil {
		var status interp.ExitStatus
		if errors.As(err, &status) {
			return int(status)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			if timeout > 0 {
				fmt.Fprintf(stderr, "error: execution timed out after %s\n", timeout)
			} else {
				fmt.Fprintln(stderr, "error: execution timed out")
			}
			return exitCodeTimeout
		}
		if errors.Is(err, context.Canceled) {
			fmt.Fprintln(stderr, "error: execution canceled")
			return exitCodeTimeout
		}
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// readAllContext reads all bytes from r, but returns ctx.Err() immediately if
// the context is cancelled or its deadline expires before the read completes.
// It spawns a goroutine to perform the read; the goroutine may outlive this
// call if the underlying reader blocks (e.g. stdin from a pipe), but it will
// be reclaimed when the process exits.
func readAllContext(ctx context.Context, r io.Reader) ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(r)
		ch <- result{data, err}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		return res.data, res.err
	}
}

// rejectLongCommand scans raw CLI args for "--command" or "--command=..." and
// returns an error if found. The flag is registered with a long name so that
// cobra/pflag help formatting works correctly, but only the -c shorthand is
// intended to be user-facing.
func rejectLongCommand(rawArgs []string) error {
	for _, a := range rawArgs {
		if a == "--" {
			break // everything after "--" is a positional arg
		}
		if a == "--command" || strings.HasPrefix(a, "--command=") {
			return fmt.Errorf("unknown flag: --command")
		}
	}
	return nil
}

// executeOpts holds options for the execute function.
type executeOpts struct {
	allowedPaths     []string
	allowedCommands  []string
	allowAllCommands bool
	procPath         string
}

func execute(ctx context.Context, script, name string, opts executeOpts, stdin io.Reader, stdout, stderr io.Writer) error {
	// Parse.
	prog, err := syntax.NewParser().Parse(strings.NewReader(script), name)
	if err != nil {
		// Bash returns exit code 2 for syntax/parse errors.
		fmt.Fprintf(stderr, "%v\n", err)
		return interp.ExitStatus(2)
	}

	// Build runner options.
	runOpts := []interp.RunnerOption{
		interp.StdIO(stdin, stdout, stderr),
	}
	if len(opts.allowedPaths) > 0 {
		runOpts = append(runOpts, interp.AllowedPaths(opts.allowedPaths))
	}
	if opts.allowAllCommands {
		runOpts = append(runOpts, interpoption.AllowAllCommands().(interp.RunnerOption))
	} else if len(opts.allowedCommands) > 0 {
		runOpts = append(runOpts, interp.AllowedCommands(opts.allowedCommands))
	}
	if opts.procPath != "" {
		runOpts = append(runOpts, interp.ProcPath(opts.procPath))
	}

	runner, err := interp.New(runOpts...)
	if err != nil {
		return err
	}
	defer runner.Close()

	return runner.Run(ctx, prog)
}
