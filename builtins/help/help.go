// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package help implements the help builtin command.
//
// help — display help for commands
//
// Usage: help [command]
//
// With no arguments, list all available builtin commands with a brief
// description. When a command name is given, display detailed help for
// that command.
//
// Exit codes:
//
//	0  Success.
//	1  Unknown command or --help was requested.
package help

import (
	"context"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the help builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "help",
	Description: "display help for commands",
	Help: `Usage: help [command]
Display help for builtin commands.

With no arguments, list all available commands with a brief description.
When COMMAND is given, display detailed help for that command.`,
	MakeFlags: registerFlags,
}

func printUsage(callCtx *builtins.CallContext) {
	callCtx.Out("Usage: help [command]\n")
	callCtx.Out("Display help for builtin commands.\n")
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	helpFlag := fs.Bool("help", false, "print usage and exit")

	return func(_ context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *helpFlag {
			printUsage(callCtx)
			return builtins.Result{Code: 1}
		}

		// help <command> — show detailed help for a specific command.
		if len(args) > 0 {
			name := args[0]
			if callCtx.CommandAllowed != nil && !callCtx.CommandAllowed(name) {
				callCtx.Errf("help: no help topics match '%s'\n", name)
				return builtins.Result{Code: 1}
			}
			meta, ok := builtins.Meta(name)
			if !ok {
				callCtx.Errf("help: no help topics match '%s'\n", name)
				return builtins.Result{Code: 1}
			}
			if meta.Help != "" {
				callCtx.Outf("%s\n", meta.Help)
			} else {
				callCtx.Outf("%s - %s\n", meta.Name, meta.Description)
			}
			return builtins.Result{}
		}

		// No arguments — list all allowed commands.
		allNames := builtins.Names()
		var names []string
		for _, name := range allNames {
			if callCtx.CommandAllowed != nil && !callCtx.CommandAllowed(name) {
				continue
			}
			names = append(names, name)
		}

		// Find the longest command name for alignment.
		maxLen := 0
		for _, name := range names {
			if len(name) > maxLen {
				maxLen = len(name)
			}
		}

		for _, name := range names {
			meta, _ := builtins.Meta(name)
			callCtx.Outf("%-*s  %s\n", maxLen, name, meta.Description)
		}

		callCtx.Out("\nRun 'help <command>' for more information on a specific command.\n")
		return builtins.Result{}
	}
}
