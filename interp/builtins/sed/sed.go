// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package sed implements the sed builtin command.
//
// sed — stream editor for filtering and transforming text
//
// Usage: sed [OPTION]... [script] [FILE]...
//
//	sed [OPTION]... -e script [-e script]... [FILE]...
//
// sed reads input files (or standard input if no files are given, or when
// FILE is -), applies editing commands from the script, and writes the
// result to standard output.
//
// Accepted flags:
//
//	-n, --quiet, --silent
//	    Suppress automatic printing of pattern space. Only lines
//	    explicitly printed via the p command are output.
//
//	-e script, --expression=script
//	    Add the script commands to the set of commands to execute.
//	    Multiple -e options are allowed; they are concatenated in order.
//
//	-E, --regexp-extended
//	    Use extended regular expressions (ERE) rather than basic (BRE).
//
//	-r
//	    GNU alias for -E (extended regular expressions).
//
//	-h, --help
//	    Print this usage message to stdout and exit 0.
//
// Supported sed commands:
//
//	s/regex/replacement/[flags]   Substitute matches of regex with replacement.
//	                              Flags: g (global), p (print), i/I (case-insensitive),
//	                              N (replace Nth match).
//	p                             Print the current pattern space.
//	d                             Delete pattern space, start next cycle.
//	q [code]                      Quit with optional exit code (prints pattern space first).
//	Q [code]                      Quit with optional exit code (does not print).
//	y/src/dst/                    Transliterate characters from src to dst.
//	a\text  /  a text             Append text after the current line.
//	i\text  /  i text             Insert text before the current line.
//	c\text  /  c text             Replace line(s) with text.
//	=                             Print the current line number.
//	l                             Print pattern space unambiguously.
//	n                             Read next input line into pattern space.
//	N                             Append next input line to pattern space.
//	h                             Copy pattern space to hold space.
//	H                             Append pattern space to hold space.
//	g                             Copy hold space to pattern space.
//	G                             Append hold space to pattern space.
//	x                             Exchange pattern and hold spaces.
//	b [label]                     Branch to label (or end of script).
//	: label                       Define a label for branching.
//	t [label]                     Branch to label if s/// made a substitution.
//	T [label]                     Branch to label if s/// did NOT make a substitution.
//	{...}                         Group commands.
//	!command                      Negate the address (apply to non-matching lines).
//
// Addressing:
//
//	N           Line number (1-based).
//	$           Last line.
//	/regex/     Lines matching regex.
//	addr1,addr2 Range of lines.
//	first~step  Every step-th line starting from first (GNU extension).
//
// Rejected commands (blocked for safety):
//
//	e           Execute pattern space as shell command (blocked: command execution).
//	w file      Write pattern space to file (blocked: file write).
//	W file      Write first line to file (blocked: file write).
//	r file      Read file contents (blocked: unsandboxed file read).
//	R file      Read one line from file (blocked: unsandboxed file read).
//
// Rejected flags:
//
//	-i, --in-place    Edit files in place (blocked: file write).
//	-f, --file        Read script from file (not implemented).
//	-s, --separate    Treat files as separate streams (not implemented).
//	-z, --null-data   NUL-separated input (not implemented).
//
// Exit codes:
//
//	0  Success (or custom code via q/Q command).
//	1  Invalid script syntax, missing file, or other error.
//
// Memory safety:
//
//	Input is processed line-by-line via a buffered scanner with a per-line
//	cap of 1 MiB (MaxLineBytes). Pattern space and hold space are each
//	bounded to MaxSpaceBytes (1 MiB). Branch loops are capped at
//	MaxBranchIterations (10 000) per input line to prevent infinite loops.
//	Non-regular-file inputs are subject to a MaxTotalReadBytes (256 MiB)
//	limit to guard against infinite sources.
//
// Regex safety:
//
//	All regular expressions use Go's regexp package, which implements RE2
//	(guaranteed linear-time matching, no backtracking). This prevents ReDoS
//	attacks. BRE patterns are converted to ERE syntax before compilation.
package sed

import (
	"context"
	"errors"
	"strings"

	"github.com/DataDog/rshell/interp/builtins"
)

// Cmd is the sed builtin command descriptor.
var Cmd = builtins.Command{Name: "sed", MakeFlags: registerFlags}

// MaxLineBytes is the per-line buffer cap for the line scanner.
const MaxLineBytes = 1 << 20 // 1 MiB

// MaxSpaceBytes is the maximum size for pattern space and hold space.
const MaxSpaceBytes = 1 << 20 // 1 MiB

// MaxBranchIterations is the maximum number of branch iterations per
// input line to prevent infinite loops.
const MaxBranchIterations = 10_000

// MaxTotalReadBytes is the maximum total bytes consumed from a single
// non-regular-file input source.
const MaxTotalReadBytes = 256 << 20 // 256 MiB

// MaxAppendQueueBytes is the maximum total bytes that can be accumulated
// in the append queue within a single cycle.
const MaxAppendQueueBytes = 1 << 20 // 1 MiB

// expressionSlice collects multiple -e values.
type expressionSlice []string

func (e *expressionSlice) String() string { return strings.Join(*e, "\n") }
func (e *expressionSlice) Set(val string) error {
	*e = append(*e, val)
	return nil
}
func (e *expressionSlice) Type() string { return "string" }

// registerFlags sets up sed flags and returns the handler.
func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := fs.BoolP("help", "h", false, "print usage and exit")
	quiet := fs.BoolP("quiet", "n", false, "suppress automatic printing of pattern space")
	fs.Lookup("quiet").NoOptDefVal = "true"
	// --silent is an alias for --quiet.
	silent := fs.Bool("silent", false, "alias for --quiet")
	fs.Lookup("silent").NoOptDefVal = "true"

	var expressions expressionSlice
	fs.VarP(&expressions, "expression", "e", "add script commands")

	extendedE := fs.BoolP("regexp-extended", "E", false, "use extended regular expressions")
	extendedR := fs.BoolP("regexp-extended-r", "r", false, "use extended regular expressions (GNU alias for -E)")
	fs.Lookup("regexp-extended-r").Hidden = true

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: sed [OPTION]... [script] [FILE]...\n")
			callCtx.Out("Stream editor for filtering and transforming text.\n")
			callCtx.Out("With no FILE, or when FILE is -, read standard input.\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}

		suppressPrint := *quiet || *silent
		useERE := *extendedE || *extendedR

		// Determine script and files.
		var scriptParts []string
		var files []string

		if len(expressions) > 0 {
			scriptParts = []string(expressions)
			files = args
		} else if len(args) > 0 {
			scriptParts = []string{args[0]}
			files = args[1:]
		} else {
			callCtx.Errf("sed: no script command has been specified\n")
			return builtins.Result{Code: 1}
		}

		// Parse the sed script.
		prog, err := parseScript(strings.Join(scriptParts, "\n"), useERE)
		if err != nil {
			callCtx.Errf("sed: %s\n", err)
			return builtins.Result{Code: 1}
		}

		if len(files) == 0 {
			files = []string{"-"}
		}

		// Create the execution engine.
		eng := &engine{
			callCtx:       callCtx,
			prog:          prog,
			suppressPrint: suppressPrint,
		}

		var failed bool
		for i, file := range files {
			if ctx.Err() != nil {
				break
			}
			isLastFile := i == len(files)-1
			if err := eng.processFile(ctx, callCtx, file, isLastFile); err != nil {
				var qe *quitError
				if errors.As(err, &qe) {
					// q command: print pattern space if requested, then exit.
					return builtins.Result{Code: qe.code}
				}
				name := file
				if file == "-" {
					name = "standard input"
				}
				callCtx.Errf("sed: %s: %s\n", name, callCtx.PortableErr(err))
				failed = true
			}
		}

		if failed {
			return builtins.Result{Code: 1}
		}
		return builtins.Result{}
	}
}
