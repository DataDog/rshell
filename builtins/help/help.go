// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package help implements the help builtin command.
//
// help — display help for rshell features and commands
//
// Usage: help [--all] [feature|command]
//
// With no arguments, list rshell features with descriptions, a concise
// unsupported-feature summary, allowed commands, a compact list of
// not-allowed commands, and the configured AllowedPaths sandbox roots (or a
// notice when none are configured). When --all is given, disabled commands
// are shown as a full description table. When a feature or command name is
// given, display detailed help for that topic.
//
// Flags:
//
//	--all   show all commands (including not allowed) with descriptions
//
// Exit codes:
//
//	0  Success (including when --help was requested).
//	1  Unknown topic.
package help

import (
	"bytes"
	"context"
	"strings"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/internal/version"
)

// Cmd is the help builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "help",
	Description: "display help for features and commands",
	MakeFlags:   registerFlags,
}

func printUsage(callCtx *builtins.CallContext) {
	callCtx.Out("Usage: help [--all] [feature|command]\n")
	callCtx.Out("Display help for rshell features and commands.\n")
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	helpFlag := fs.Bool("help", false, "print usage and exit")
	allFlag := fs.Bool("all", false, "show all commands (including not allowed) with descriptions; ignored when a topic is given")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *helpFlag {
			printUsage(callCtx)
			return builtins.Result{}
		}

		// help <feature|command> — show detailed help for a specific topic.
		if len(args) > 1 {
			printUsage(callCtx)
			return builtins.Result{Code: 1}
		}
		if len(args) > 0 {
			name := args[0]
			if feature, ok := builtins.Feature(name); ok {
				printFeatureDetails(callCtx, feature)
				return builtins.Result{}
			}
			if callCtx.CommandAllowed != nil && !callCtx.CommandAllowed(name) {
				callCtx.Errf("help: no help topics match '%s'\n", name)
				return builtins.Result{Code: 1}
			}
			meta, ok := builtins.Meta(name)
			if !ok {
				callCtx.Errf("help: no help topics match '%s'\n", name)
				return builtins.Result{Code: 1}
			}

			// Use static Help text if available (for commands that don't
			// handle --help, like echo, true, false).
			if meta.Help != "" {
				callCtx.Outf("%s\n", meta.Help)
				return builtins.Result{}
			}

			// Otherwise, invoke the command with --help and capture the output.
			if handler, ok := builtins.Lookup(name); ok && handler != nil {
				var buf bytes.Buffer
				captureCtx := *callCtx
				captureCtx.Stdout = &buf
				captureCtx.Stderr = &buf
				handler(ctx, &captureCtx, []string{"--help"})
				if buf.Len() > 0 {
					callCtx.Outf("%s", buf.String())
					return builtins.Result{}
				}
			}

			callCtx.Outf("%s - %s\n", meta.Name, meta.Description)
			return builtins.Result{}
		}

		// No arguments — list features and commands.
		allNames := builtins.Names()
		var allowed, notAllowed []string
		for _, name := range allNames {
			if callCtx.CommandAllowed != nil && !callCtx.CommandAllowed(name) {
				notAllowed = append(notAllowed, name)
				continue
			}
			meta, _ := builtins.Meta(name)
			if meta.RemediationOnly && !callCtx.RemediationMode {
				notAllowed = append(notAllowed, name)
				continue
			}
			allowed = append(allowed, name)
		}

		// Header: version line only.
		printHeader(callCtx)

		callCtx.Out("Features:\n")
		printFeatureTable(callCtx, builtins.Features())
		printUnsupportedSummary(callCtx, builtins.UnsupportedSummary())

		// Commands section label, with count when not all commands are enabled.
		if len(notAllowed) > 0 {
			callCtx.Outf("\nCommands (%d of %d enabled):\n", len(allowed), len(allNames))
		} else {
			callCtx.Out("\nCommands:\n")
		}
		printCommandTable(callCtx, allowed)

		// Show disabled commands when restrictions are active.
		if len(notAllowed) > 0 {
			label := "Disabled commands"
			if len(notAllowed) == 1 {
				label = "Disabled command"
			}
			if *allFlag {
				// --all: full description table for disabled commands.
				callCtx.Outf("\n%s:\n", label)
				printCommandTable(callCtx, notAllowed)
			} else {
				// Default: compact comma-separated list.
				callCtx.Outf("\n%s: %s\n", label, wrapNames(notAllowed, 80))
			}
		} else if *allFlag {
			callCtx.Out("\nAll commands are allowed in this session.\n")
		}

		printAllowedPaths(callCtx)

		callCtx.Out("\nRun 'help <feature|command>' for more information on a specific topic.\n")
		return builtins.Result{}
	}
}

// printHeader writes the version line with the current shell mode.
func printHeader(callCtx *builtins.CallContext) {
	mode := "read-only mode"
	if callCtx.RemediationMode {
		mode = "remediation mode"
	}
	if version.Version != "" {
		callCtx.Outf("rshell (%s) - %s\n\n", version.Version, mode)
	} else {
		callCtx.Outf("rshell - %s\n\n", mode)
	}
}

// printFeatureTable prints an aligned feature name/description table.
func printFeatureTable(callCtx *builtins.CallContext, features []builtins.FeatureMeta) {
	maxLen := 0
	for _, feature := range features {
		if len(feature.Name) > maxLen {
			maxLen = len(feature.Name)
		}
	}
	for _, feature := range features {
		callCtx.Outf("%-*s  %s\n", maxLen, feature.Name, feature.Description)
	}
}

// printAllowedPaths writes the configured AllowedPaths sandbox roots, one per
// line. An empty list means no allowed paths are configured, which blocks all
// user-controllable filesystem access — surface that explicitly so operators
// can tell the difference from "no information available". A few builtins
// (ss, ip route, df, ps) intentionally read kernel-state paths outside the
// sandbox and are unaffected by this configuration.
func printAllowedPaths(callCtx *builtins.CallContext) {
	if callCtx.AllowedPathsList == nil {
		return
	}
	paths := callCtx.AllowedPathsList()
	callCtx.Out("\nAllowed paths:\n")
	if len(paths) == 0 {
		callCtx.Out("  (no allowed paths configured — no filesystem paths are reachable)\n")
		return
	}
	for _, p := range paths {
		callCtx.Outf("  %s\n", p)
	}
}

func printUnsupportedSummary(callCtx *builtins.CallContext, items []string) {
	if len(items) == 0 {
		return
	}
	callCtx.Out("\nNot supported:\n")
	for _, item := range items {
		callCtx.Outf("  - %s\n", item)
	}
}

func printFeatureDetails(callCtx *builtins.CallContext, feature builtins.FeatureMeta) {
	callCtx.Outf("%s - %s\n", feature.Name, feature.Description)
	printBulletSection(callCtx, "Supported", feature.Supported)
	printBulletSection(callCtx, "Not supported", feature.Unsupported)
	printBulletSection(callCtx, "Notes", feature.Notes)
}

func printBulletSection(callCtx *builtins.CallContext, title string, items []string) {
	if len(items) == 0 {
		return
	}
	callCtx.Outf("\n%s:\n", title)
	for _, item := range items {
		callCtx.Outf("  - %s\n", item)
	}
}

// printCommandTable prints an aligned name/description table.
func printCommandTable(callCtx *builtins.CallContext, names []string) {
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
}

// wrapNames formats a list of names into comma-separated lines that stay
// within the given line width. Continuation lines are indented by two spaces.
func wrapNames(names []string, lineWidth int) string {
	if len(names) == 0 {
		return ""
	}
	var b strings.Builder
	col := 0
	lineStart := true // true at the very start and after each newline indent
	for i, name := range names {
		token := name
		if i < len(names)-1 {
			token += ","
		}
		needed := len(token)
		if !lineStart {
			needed++ // space before token
		}
		if !lineStart && col+needed > lineWidth {
			b.WriteString("\n  ")
			col = 2
			lineStart = true
			needed = len(token)
		}
		if !lineStart {
			b.WriteByte(' ')
			col++
		}
		b.WriteString(token)
		col += len(token)
		lineStart = false
	}
	return b.String()
}
