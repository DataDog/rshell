// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package awk implements the awk builtin command.
//
// awk — pattern scanning and processing language
//
// Usage: awk [-F sepstring] [-v assignment]... 'program' [FILE]...
//
// awk reads each input file (or standard input when no file is given, or when
// FILE is -) one record at a time, splitting the record into fields, and
// applies the supplied program. The program is a sequence of pattern-action
// blocks: for each record, every pattern is tested, and matching actions are
// executed in order.
//
// Accepted flags:
//
//	-F sepstring
//	    Set the input field separator (FS). May be a single character, a
//	    multi-character string, or a regular expression (compiled with RE2).
//
//	-v var=value
//	    Pre-assign awk variable. Repeatable. Assignments take effect before
//	    BEGIN blocks execute.
//
//	-h, --help
//	    Print usage to stdout and exit 0.
//
// Supported language subset:
//
//	Program structure
//	  BEGIN { ... }         executed once before any input is read
//	  END   { ... }         executed once after all input has been read
//	  /regex/ { action }    action runs on records matching regex
//	  expr   { action }     action runs when expr is true
//	  /regex/               equivalent to /regex/ { print }
//	  { action }            runs for every record
//
//	Statements
//	  print [list]                  comma-separated, joined by OFS
//	  printf format, args...        formatted output
//	  next                          skip remaining patterns; read next record
//	  exit [n]                      run END (if not already in END), then exit
//	  if (c) s [else s]
//	  while (c) s
//	  for (init; cond; post) s
//	  for (var in array) s
//	  break, continue
//	  { stmt; stmt; ... }
//	  var = expr,  arr[i] = expr,  delete arr[i],  delete arr
//
//	Expressions
//	  Numeric literals (decimal/float), string literals
//	  Field references: $0, $1, ..., $NF, $(expr)
//	  Binary: + - * / % ^ ** == != < <= > >= ~ !~ && ||
//	  Unary:  - + !
//	  Pre/post increment/decrement: ++ --
//	  Compound assign: += -= *= /= %= ^=
//	  Ternary cond ? a : b
//	  String concatenation by juxtaposition: "x" $1
//	  Array subscript: a[i],  a[i,j]  (uses SUBSEP)
//	  Membership: (i in array)
//
//	Built-in functions
//	  Strings:  length([s]), substr(s,m[,n]), index(s,t), split(s,a[,sep]),
//	            sub(re,repl[,target]), gsub(re,repl[,target]),
//	            match(s,re), sprintf(fmt, ...), tolower(s), toupper(s)
//	  Numeric:  int(x), sqrt(x), exp(x), log(x), sin(x), cos(x),
//	            atan2(y,x), rand(), srand([seed])
//
//	Special variables
//	  NR, NF, FNR, FS, OFS, ORS, RS, FILENAME, SUBSEP, RSTART, RLENGTH
//	  CONVFMT, OFMT
//
// Blocked constructs (security):
//
//	system(cmd)               command execution — rejected at parse time.
//	print expr > "file"       file write — rejected.
//	print expr >> "file"      file write — rejected.
//	printf ... > "file"       file write — rejected.
//	print | "cmd"             pipe to command — rejected.
//	"cmd" | getline           pipe from command — rejected.
//	getline < "file"          unsandboxed file read — rejected.
//	close("file"|"cmd")       only valid for non-redirect uses; otherwise rejected.
//	fflush("file"|"cmd")      same as close.
//	ENVIRON[...]              environment exposure — rejected.
//
// Out of v1 scope (rejected at parse time):
//
//	function name(...) { ... }  user-defined functions.
//	getline                     in any form (even unredirected).
//	|& two-way pipe             always blocked.
//
// Exit codes:
//
//	0  Program completed successfully (or via "exit" with code 0).
//	1  Parse error, runtime error, missing file, or "exit n" with n != 0.
//
// Memory safety:
//
//   - Per-record buffer cap: 1 MiB (MaxRecordBytes). Records longer than
//     this abort with an error rather than allocating unbounded memory.
//   - String values are capped at 1 MiB (MaxStringBytes); operations that
//     would produce a longer string return an error.
//   - Arrays are capped at 1 000 000 entries (MaxArrayEntries).
//   - Per-loop-construct iteration cap at 1 000 000 (MaxLoopIterations)
//     to prevent runaway loops in user scripts. Each loop construct (while,
//     do-while, for) has its own independent counter; nested loops each get
//     their own cap.
//   - Non-regular-file inputs are subject to a 256 MiB total read cap.
//   - All read and statement loops check ctx.Err() each iteration to honour
//     the shell's 30-second execution timeout.
//
// Regex safety:
//
//	All regular expressions compile through Go's regexp package (RE2),
//	which is guaranteed linear-time with no backtracking. ReDoS attacks
//	are not possible.
package awk

import (
	"context"
	"strings"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the awk builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "awk",
	Description: "pattern scanning and processing language",
	MakeFlags:   registerFlags,
}

// MaxRecordBytes is the per-record buffer cap.
const MaxRecordBytes = 1 << 20 // 1 MiB

// MaxStringBytes is the maximum length of any string value.
const MaxStringBytes = 1 << 20 // 1 MiB

// MaxArrayEntries caps the number of keys per associative array.
const MaxArrayEntries = 1_000_000

// MaxLoopIterations caps iterations per loop construct (while, do-while, for)
// to prevent runaway loops in user scripts. Each loop construct has its own
// independent counter; the overall guard against unbounded execution is the
// shell's execution timeout.
const MaxLoopIterations = 1_000_000

// MaxTotalReadBytes caps total bytes read from a non-regular-file input
// (e.g. /dev/zero, FIFOs, pipes). This is intentionally higher than
// tail's 32 MiB cap: awk processes data record-by-record without buffering
// the entire input, so a larger cap is safe and prevents premature truncation
// on large legitimate inputs.
const MaxTotalReadBytes = 256 << 20 // 256 MiB

// MaxFields is the maximum number of fields a single record may produce.
const MaxFields = 1_000_000

// varAssignmentSlice collects multiple -v values.
type varAssignmentSlice []string

func (v *varAssignmentSlice) String() string { return strings.Join(*v, ",") }
func (v *varAssignmentSlice) Set(val string) error {
	*v = append(*v, val)
	return nil
}
func (v *varAssignmentSlice) Type() string { return "string" }

// registerFlags registers awk flags on the supplied flagset and returns the
// bound handler.
func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	// Disable interspersed flags: awk flags must appear before the program
	// text.  Options after the program text (e.g. -F, after "program") are
	// file arguments, not awk flags, matching GNU awk invocation order.
	fs.SetInterspersed(false)

	help := fs.BoolP("help", "h", false, "print usage and exit")

	fieldSep := fs.StringP("field-separator", "F", "", "input field separator (FS)")

	var assignments varAssignmentSlice
	fs.VarP(&assignments, "assign", "v", "pre-assign variable: var=value (repeatable)")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: awk [OPTION]... 'program' [FILE]...\n")
			callCtx.Out("Pattern scanning and processing language.\n")
			callCtx.Out("With no FILE, or when FILE is -, read standard input.\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}

		if len(args) == 0 {
			callCtx.Errf("awk: missing program text\n")
			return builtins.Result{Code: 1}
		}

		program := args[0]
		files := args[1:]
		// Strip a leading "--" from the file list — pflag passes it through when
		// interspersed parsing is disabled and the program text precedes it.
		if len(files) > 0 && files[0] == "--" {
			files = files[1:]
		}

		prog, err := parseProgram(program)
		if err != nil {
			callCtx.Errf("awk: %s\n", err)
			return builtins.Result{Code: 1}
		}

		runtime := newRuntime(callCtx)
		// Use Changed() rather than a non-empty check so that an explicit
		// -F '' (empty separator → character-by-character splitting) is honoured.
		if fs.Changed("field-separator") {
			if err := runtime.setFS(*fieldSep); err != nil {
				callCtx.Errf("awk: -F: %s\n", err)
				return builtins.Result{Code: 1}
			}
		}
		for _, a := range assignments {
			if err := runtime.applyVarAssignment(a); err != nil {
				callCtx.Errf("awk: -v: %s\n", err)
				return builtins.Result{Code: 1}
			}
		}

		exitCode, err := run(ctx, runtime, prog, files)
		if err != nil {
			if builtins.IsBrokenPipe(err) {
				return builtins.Result{Code: exitCode}
			}
			callCtx.Errf("awk: %s\n", err)
			return builtins.Result{Code: 1}
		}
		return builtins.Result{Code: exitCode}
	}
}
