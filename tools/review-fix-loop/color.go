// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import "os"

// useColor is true when stdout is a real terminal and NO_COLOR env var is unset.
var useColor = isatty() && os.Getenv("NO_COLOR") == ""

func isatty() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// paint wraps s with an ANSI SGR code and a reset, or returns s unchanged when
// color is disabled.
func paint(s, code string) string {
	if !useColor {
		return s
	}
	return code + s + "\033[0m"
}

// Convenience wrappers used throughout the tool.
func bold(s string) string       { return paint(s, "\033[1m") }
func dim(s string) string        { return paint(s, "\033[2m") }
func boldRed(s string) string    { return paint(s, "\033[1;31m") }
func boldGreen(s string) string  { return paint(s, "\033[1;32m") }
func boldBlue(s string) string   { return paint(s, "\033[1;34m") }
func boldYellow(s string) string { return paint(s, "\033[1;33m") }

// agentColor returns the ANSI SGR start code for the given agent name so each
// agent has a distinct color in the prefix.
func agentColor(name string) string {
	switch name {
	case "code-review":
		return "\033[1;36m" // bold cyan
	case "address-pr-comments":
		return "\033[1;33m" // bold yellow
	case "fix-ci-tests":
		return "\033[1;35m" // bold magenta
	default:
		return "\033[1m" // bold white
	}
}
