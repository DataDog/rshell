// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package awk implements the awk builtin command.
//
// awk — pattern-directed scanning and processing language
//
// Usage: awk [OPTION]... 'program' [FILE]...
//
// Scan each input FILE (or standard input) for lines matching patterns
// in the AWK program and execute the associated actions.
//
// Accepted flags:
//
//	-F fs, --field-separator=fs
//	    Set the input field separator FS to fs. Default is whitespace
//	    (runs of spaces and tabs, with leading/trailing stripped).
//
//	-v var=val, --assign=var=val
//	    Assign variable var the value val before execution begins.
//	    May be specified multiple times.
//
//	-f progfile, --file=progfile
//	    Read the AWK program from progfile instead of the first
//	    command-line argument. May be specified multiple times;
//	    the programs are concatenated.
//
//	-h, --help
//	    Print this usage message to stdout and exit 0.
//
// Blocked features (safety):
//
//	system()              — blocked: would execute shell commands
//	print > file          — blocked: would write to filesystem
//	print >> file         — blocked: would write to filesystem
//	print | cmd           — blocked: would execute commands
//	getline < file        — blocked: would read files outside sandbox
//	cmd | getline         — blocked: would execute commands
//	close()               — blocked: no I/O redirection support
//
// Exit codes:
//
//	0  All input processed successfully.
//	1  Error occurred (bad flags, parse error, runtime error, missing file).
//
// Memory safety:
//
//	Input is processed line-by-line with a per-line cap of MaxLineBytes
//	(1 MiB). Arrays are capped at MaxArraySize entries. String values
//	are capped at MaxStringLen bytes. Awk-level loops are capped at
//	MaxLoopIterations to prevent infinite loops. All loops check
//	ctx.Err() to honour the shell's execution timeout.
package awk

import (
	"bufio"
	"context"
	"io"
	"os"
	"strings"

	"github.com/spf13/pflag"

	"github.com/DataDog/rshell/interp/builtins"
	awkcore "github.com/DataDog/rshell/interp/builtins/internal/awkcore"
)

// Cmd is the awk builtin command descriptor.
var Cmd = builtins.Command{Name: "awk", Run: run}

// MaxLineBytes is the per-line buffer cap for the input scanner.
const MaxLineBytes = 1 << 20 // 1 MiB

func run(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	fs := pflag.NewFlagSet("awk", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)

	help := fs.BoolP("help", "h", false, "print usage and exit")
	fieldSep := fs.StringP("field-separator", "F", "", "input field separator")
	progFiles := fs.StringArrayP("file", "f", nil, "read program from file")
	assigns := fs.StringArrayP("assign", "v", nil, "assign variable (var=val)")

	if err := fs.Parse(args); err != nil {
		callCtx.Errf("awk: %v\n", err)
		return builtins.Result{Code: 1}
	}

	if *help {
		callCtx.Out("Usage: awk [OPTION]... 'program' [FILE]...\n")
		callCtx.Out("Pattern-directed scanning and processing language.\n\n")
		fs.SetOutput(callCtx.Stdout)
		fs.PrintDefaults()
		return builtins.Result{}
	}

	remaining := fs.Args()

	var programText string
	if len(*progFiles) > 0 {
		var sb strings.Builder
		for _, pf := range *progFiles {
			src, err := readProgFile(ctx, callCtx, pf)
			if err != nil {
				callCtx.Errf("awk: %s: %s\n", pf, callCtx.PortableErr(err))
				return builtins.Result{Code: 1}
			}
			if sb.Len() > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(src)
		}
		programText = sb.String()
	} else {
		if len(remaining) == 0 {
			callCtx.Errf("awk: no program text\n")
			return builtins.Result{Code: 1}
		}
		programText = remaining[0]
		remaining = remaining[1:]
	}

	prog, err := awkcore.Parse(programText)
	if err != nil {
		callCtx.Errf("awk: %s\n", err)
		return builtins.Result{Code: 1}
	}

	interp := awkcore.NewInterpreter(prog, callCtx.Stdout, callCtx.Stderr)

	if fs.Changed("field-separator") {
		interp.SetFS(*fieldSep)
	}

	for _, a := range *assigns {
		idx := strings.IndexByte(a, '=')
		if idx < 1 {
			callCtx.Errf("awk: invalid -v assignment: %s\n", a)
			return builtins.Result{Code: 1}
		}
		name := a[:idx]
		val := a[idx+1:]
		if !isValidVarName(name) {
			callCtx.Errf("awk: invalid variable name: %s\n", name)
			return builtins.Result{Code: 1}
		}
		interp.SetVar(name, val)
	}

	files := remaining
	if len(files) == 0 {
		files = []string{"-"}
	}

	if err := interp.ExecBegin(ctx); err != nil {
		if re, ok := err.(*awkcore.RuntimeError); ok {
			callCtx.Errf("awk: %s\n", re)
			return builtins.Result{Code: re.ExitCode()}
		}
		callCtx.Errf("awk: %s\n", err)
		return builtins.Result{Code: 1}
	}

	var failed bool
	for _, file := range files {
		if ctx.Err() != nil {
			break
		}
		if err := processFile(ctx, callCtx, interp, file); err != nil {
			if re, ok := err.(*awkcore.RuntimeError); ok {
				if re.IsExit() {
					if err2 := interp.ExecEnd(ctx); err2 != nil {
						if re2, ok2 := err2.(*awkcore.RuntimeError); ok2 {
							return builtins.Result{Code: re2.ExitCode()}
						}
					}
					return builtins.Result{Code: re.ExitCode()}
				}
				callCtx.Errf("awk: %s\n", re)
				return builtins.Result{Code: re.ExitCode()}
			}
			if file == "-" {
				callCtx.Errf("awk: standard input: %s\n", callCtx.PortableErr(err))
			} else {
				callCtx.Errf("awk: %s: %s\n", file, callCtx.PortableErr(err))
			}
			failed = true
		}
	}

	if err := interp.ExecEnd(ctx); err != nil {
		if re, ok := err.(*awkcore.RuntimeError); ok {
			callCtx.Errf("awk: %s\n", re)
			return builtins.Result{Code: re.ExitCode()}
		}
		callCtx.Errf("awk: %s\n", err)
		return builtins.Result{Code: 1}
	}

	if failed {
		return builtins.Result{Code: 1}
	}
	code := interp.ExitCode()
	return builtins.Result{Code: code}
}

func processFile(ctx context.Context, callCtx *builtins.CallContext, interp *awkcore.Interpreter, file string) error {
	var rc io.ReadCloser
	if file == "-" {
		if callCtx.Stdin == nil {
			return nil
		}
		rc = io.NopCloser(callCtx.Stdin)
		interp.SetFilename("") // POSIX: FILENAME is unset for stdin
	} else {
		f, err := callCtx.OpenFile(ctx, file, os.O_RDONLY, 0)
		if err != nil {
			return err
		}
		rc = f
		interp.SetFilename(file)
	}
	defer rc.Close()

	interp.ResetFNR()

	sc := bufio.NewScanner(rc)
	buf := make([]byte, 4096)
	sc.Buffer(buf, MaxLineBytes)

	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Text()
		if err := interp.ExecLine(ctx, line); err != nil {
			return err
		}
	}
	return sc.Err()
}

func readProgFile(ctx context.Context, callCtx *builtins.CallContext, path string) (string, error) {
	f, err := callCtx.OpenFile(ctx, path, os.O_RDONLY, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var sb strings.Builder
	sc := bufio.NewScanner(f)
	buf := make([]byte, 4096)
	sc.Buffer(buf, MaxLineBytes)
	first := true
	for sc.Scan() {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if !first {
			sb.WriteByte('\n')
		}
		sb.WriteString(sc.Text())
		first = false
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return sb.String(), nil
}

func isValidVarName(name string) bool {
	if len(name) == 0 {
		return false
	}
	for i, c := range name {
		if i == 0 {
			if !isLetter(c) {
				return false
			}
		} else {
			if !isLetter(c) && !isDigit(c) {
				return false
			}
		}
	}
	reserved := map[string]bool{
		"BEGIN": true, "END": true, "if": true, "else": true,
		"while": true, "for": true, "do": true, "break": true,
		"continue": true, "next": true, "exit": true, "delete": true,
		"in": true, "getline": true, "print": true, "printf": true,
		"function": true, "return": true,
	}
	return !reserved[name]
}

func isLetter(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isDigit(c rune) bool {
	return c >= '0' && c <= '9'
}
