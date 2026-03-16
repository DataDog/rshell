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

	"github.com/DataDog/rshell/interp"
	"github.com/spf13/cobra"
	"mvdan.cc/sh/v3/syntax"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	var (
		command         string
		allowedPaths    string
		allowedCommands string
		allowAllCmds    bool
	)

	cmd := &cobra.Command{
		Use:           "rshell [file ...]",
		Short:         "A restricted shell interpreter for AI agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			commandSet := cmd.Flags().Changed("")
			if commandSet && len(args) > 0 {
				return fmt.Errorf("cannot use -c with file arguments")
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
			}

			if commandSet {
				return execute(cmd.Context(), command, "", execOpts, stdin, stdout, stderr)
			}

			if len(args) > 0 {
				// Read stdin once so each execute() call gets its own
				// reader, avoiding a data race on the shared io.Reader.
				stdinData, err := io.ReadAll(stdin)
				if err != nil {
					return fmt.Errorf("reading stdin: %w", err)
				}

				for _, file := range args {
					data, err := os.ReadFile(file)
					if err != nil {
						return fmt.Errorf("reading %s: %w", file, err)
					}
					if err := execute(cmd.Context(), string(data), file, execOpts, bytes.NewReader(stdinData), stdout, stderr); err != nil {
						return err
					}
				}
				return nil
			}

			// No -c and no file args: read from stdin.
			stdinData, err := io.ReadAll(stdin)
			if err != nil {
				return fmt.Errorf("reading stdin: %w", err)
			}
			return execute(cmd.Context(), string(stdinData), "", execOpts, strings.NewReader(""), stdout, stderr)
		},
	}

	cmd.SetArgs(args)
	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	cmd.Flags().StringVarP(&command, "", "c", "", "shell command string to execute")
	cmd.Flags().StringVarP(&allowedPaths, "allowed-path", "p", "", "comma-separated list of directories the shell is allowed to access")
	cmd.Flags().StringVar(&allowedCommands, "allowed-commands", "", "comma-separated list of commands the shell is allowed to execute")
	cmd.Flags().BoolVar(&allowAllCmds, "allow-all-commands", false, "allow execution of all commands (builtins and external)")

	if err := cmd.Execute(); err != nil {
		var status interp.ExitStatus
		if errors.As(err, &status) {
			return int(status)
		}
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// executeOpts holds options for the execute function.
type executeOpts struct {
	allowedPaths     []string
	allowedCommands  []string
	allowAllCommands bool
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
		runOpts = append(runOpts, interp.AllowAllCommands())
	} else if len(opts.allowedCommands) > 0 {
		runOpts = append(runOpts, interp.AllowedCommands(opts.allowedCommands))
	}

	runner, err := interp.New(runOpts...)
	if err != nil {
		return err
	}
	defer runner.Close()

	return runner.Run(ctx, prog)
}
