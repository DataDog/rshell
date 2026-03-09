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
		script       string
		allowedPaths string
	)

	cmd := &cobra.Command{
		Use:           "rshell [file ...]",
		Short:         "A restricted shell interpreter for AI agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if script != "" && len(args) > 0 {
				return fmt.Errorf("cannot use --script with file arguments")
			}
			if script == "" && len(args) == 0 {
				return fmt.Errorf("requires either --script or file arguments (use \"-\" for stdin)")
			}

			var paths []string
			if allowedPaths != "" {
				paths = strings.Split(allowedPaths, ",")
			}

			if script != "" {
				return execute(cmd.Context(), script, "", paths, stdin, stdout, stderr)
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
				if err := execute(cmd.Context(), src, name, paths, bytes.NewReader(stdinData), stdout, stderr); err != nil {
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

func execute(ctx context.Context, script, name string, allowedPaths []string, stdin io.Reader, stdout, stderr io.Writer) error {
	// Parse.
	prog, err := syntax.NewParser().Parse(strings.NewReader(script), name)
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}

	// Build runner options.
	opts := []interp.RunnerOption{
		interp.StdIO(stdin, stdout, stderr),
	}
	if len(allowedPaths) > 0 {
		opts = append(opts, interp.AllowedPaths(allowedPaths))
	}

	runner, err := interp.New(opts...)
	if err != nil {
		return err
	}
	defer runner.Close()

	return runner.Run(ctx, prog)
}
