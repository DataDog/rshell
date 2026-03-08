// Package main provides the rshell CLI — a restricted shell interpreter.
package main

import (
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
		Use:           "rshell",
		Short:         "A restricted shell interpreter for AI agents",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var paths []string
			if allowedPaths != "" {
				paths = strings.Split(allowedPaths, ",")
			}
			return execute(cmd.Context(), script, paths, stdin, stdout, stderr)
		},
	}

	cmd.SetArgs(args)
	cmd.SetIn(stdin)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)

	cmd.Flags().StringVarP(&script, "script", "s", "", "path to the shell script to execute (use - for stdin)")
	cmd.Flags().StringVarP(&allowedPaths, "allowed-path", "a", "", "comma-separated list of directories the shell is allowed to access")
	_ = cmd.MarkFlagRequired("script")

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

func execute(ctx context.Context, script string, allowedPaths []string, stdin io.Reader, stdout, stderr io.Writer) error {
	// Read script source.
	var src io.Reader
	if script == "-" {
		src = stdin
	} else {
		f, err := os.Open(script)
		if err != nil {
			return err
		}
		defer f.Close()
		src = f
	}

	// Parse.
	prog, err := syntax.NewParser().Parse(src, script)
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
