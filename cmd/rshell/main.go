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
		script          string
		allowedPaths    string
		allowedCommands string
	)

	cmd := &cobra.Command{
		Use:           "rshell [file ...]",
		Short:         "A restricted shell interpreter for AI agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			scriptSet := cmd.Flags().Changed("script")
			if scriptSet && len(args) > 0 {
				return fmt.Errorf("cannot use --script with file arguments")
			}
			if !scriptSet && len(args) == 0 {
				return fmt.Errorf("requires either --script or file arguments (use \"-\" for stdin)")
			}

			var paths []string
			if allowedPaths != "" {
				paths = splitAndTrim(allowedPaths)
			}
			var cmds []string
			allowedCommandsSet := cmd.Flags().Changed("allowed-commands")
			if allowedCommands != "" {
				cmds = splitAndTrim(allowedCommands)
			} else if allowedCommandsSet {
				// Explicitly passing an empty --allowed-commands means deny-all.
				cmds = []string{}
			}

			if scriptSet {
				return execute(cmd.Context(), script, "", paths, cmds, stdin, stdout, stderr)
			}

			// Read stdin once so each execute() call gets its own
			// reader, avoiding a data race on the shared io.Reader.
			stdinData, err := io.ReadAll(stdin)
			if err != nil {
				return fmt.Errorf("reading stdin: %w", err)
			}

			for _, file := range args {
				var src string
				var name string
				if file == "-" {
					src = string(stdinData)
					name = ""
				} else {
					data, err := os.ReadFile(file)
					if err != nil {
						return fmt.Errorf("reading %s: %w", file, err)
					}
					src = string(data)
					name = file
				}
				if err := execute(cmd.Context(), src, name, paths, cmds, bytes.NewReader(stdinData), stdout, stderr); err != nil {
					return err
				}
			}
			return nil
		},
	}

	cmd.SetArgs(args)
	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	cmd.Flags().StringVarP(&script, "script", "s", "", "shell script to execute")
	cmd.Flags().StringVarP(&allowedPaths, "allowed-path", "a", "", "comma-separated list of directories the shell is allowed to access")
	cmd.Flags().StringVar(&allowedCommands, "allowed-commands", "", "comma-separated list of commands the shell is allowed to execute")

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

func execute(ctx context.Context, script, name string, allowedPaths, allowedCommands []string, stdin io.Reader, stdout, stderr io.Writer) error {
	// Parse.
	prog, err := syntax.NewParser().Parse(strings.NewReader(script), name)
	if err != nil {
		// Bash returns exit code 2 for syntax/parse errors.
		fmt.Fprintf(stderr, "%v\n", err)
		return interp.ExitStatus(2)
	}

	// Build runner options.
	opts := []interp.RunnerOption{
		interp.StdIO(stdin, stdout, stderr),
	}
	if len(allowedPaths) > 0 {
		opts = append(opts, interp.AllowedPaths(allowedPaths))
	}
	if len(allowedCommands) == 1 && strings.EqualFold(allowedCommands[0], "all") {
		opts = append(opts, interp.AllowAllCommands())
	} else if allowedCommands != nil {
		opts = append(opts, interp.AllowedCommands(allowedCommands))
	}

	runner, err := interp.New(opts...)
	if err != nil {
		return err
	}
	defer runner.Close()

	return runner.Run(ctx, prog)
}

// splitAndTrim splits s on commas and trims whitespace from each element.
// Returns nil for empty input.
func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}
