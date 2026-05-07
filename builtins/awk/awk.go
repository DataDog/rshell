// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package awk implements the awk builtin command.
//
// awk - pattern scanning and text processing
//
// Usage: awk [OPTION]... 'program' [FILE]...
//
//	awk [OPTION]... -f program-file [FILE]...
//
// Phase 1 implements a practical, intentionally restricted awk profile:
// program loading from an inline argument or -f files, -F one-character field
// separators, -v scalar variables, BEGIN/main/END rules, print, scalar
// assignment, arithmetic/comparison/boolean expressions, regex patterns and
// match operators, string concatenation, and read-only fields/built-in
// variables such as $0, $1, NF, NR, FNR, FILENAME, FS, OFS, and ORS.
//
// Blocked or deferred features include system(), command pipes, output
// redirection, getline, arrays, control flow statements, printf, user-defined
// functions, regex FS, and field mutation/$0 rebuilding.
package awk

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the awk builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "awk",
	Description: "pattern scanning and text processing",
	MakeFlags:   registerFlags,
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	*s = append(*s, v)
	return nil
}
func (s *stringList) Type() string { return "string" }

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	fs.SetInterspersed(false)
	help := fs.BoolP("help", "h", false, "print usage and exit")
	fieldSep := fs.StringP("field-separator", "F", "", "use a single-character input field separator")
	var programFiles stringList
	fs.VarP(&programFiles, "file", "f", "read awk program from file")
	var assignments stringList
	fs.VarP(&assignments, "assign", "v", "assign awk variable before execution")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			printHelp(callCtx, fs)
			return builtins.Result{}
		}
		programText, files, err := loadProgram(ctx, callCtx, args, programFiles)
		if err != nil {
			callCtx.Errf("awk: %v\n", err)
			return builtins.Result{Code: 1}
		}
		prog, err := parseProgram(programText)
		if err != nil {
			callCtx.Errf("awk: %v\n", err)
			return builtins.Result{Code: 1}
		}
		rt := newRuntime(callCtx, prog)
		if fs.Changed("field-separator") {
			if err := rt.setVar("FS", inputStringValue(DecodeAwkEscapes(*fieldSep))); err != nil {
				callCtx.Errf("awk: %v\n", err)
				return builtins.Result{Code: 1}
			}
		}
		for _, assignment := range assignments {
			name, value, ok := strings.Cut(assignment, "=")
			if !ok || !validVarName(name) {
				callCtx.Errf("awk: invalid -v assignment %q\n", assignment)
				return builtins.Result{Code: 1}
			}
			if err := rt.setVar(name, inputStringValue(DecodeAwkEscapes(value))); err != nil {
				callCtx.Errf("awk: %v\n", err)
				return builtins.Result{Code: 1}
			}
		}
		return rt.run(ctx, files)
	}
}

func printHelp(callCtx *builtins.CallContext, fs *builtins.FlagSet) {
	callCtx.Out("Usage: awk [OPTION]... 'program' [FILE]...\n")
	callCtx.Out("Pattern scanning and text processing.\n")
	callCtx.Out("With no FILE, or when FILE is -, read standard input.\n\n")
	fs.SetOutput(callCtx.Stdout)
	fs.PrintDefaults()
}

func loadProgram(ctx context.Context, callCtx *builtins.CallContext, args []string, programFiles []string) (string, []string, error) {
	var parts []string
	var files []string
	total := 0
	if len(programFiles) > 0 {
		for _, path := range programFiles {
			text, err := readProgramFile(ctx, callCtx, path, &total)
			if err != nil {
				return "", nil, err
			}
			parts = append(parts, text)
		}
		files = args
	} else {
		if len(args) == 0 {
			return "", nil, fmt.Errorf("missing program")
		}
		total += len(args[0])
		if total > MaxProgramBytes {
			return "", nil, fmt.Errorf("program exceeds %d bytes", MaxProgramBytes)
		}
		parts = append(parts, args[0])
		files = args[1:]
	}
	if len(parts) == 0 {
		return "", nil, fmt.Errorf("missing program")
	}
	return strings.Join(parts, "\n"), files, nil
}

func readProgramFile(ctx context.Context, callCtx *builtins.CallContext, path string, total *int) (string, error) {
	if path == "-" {
		if callCtx.Stdin == nil {
			return "", nil
		}
		return readProgram(ctx, callCtx.Stdin, total)
	}
	rc, err := callCtx.OpenFile(ctx, path, os.O_RDONLY, 0)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	return readProgram(ctx, rc, total)
}

type byteReader interface {
	Read([]byte) (int, error)
}

func readProgram(ctx context.Context, r byteReader, total *int) (string, error) {
	var b strings.Builder
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := r.Read(buf)
		if n > 0 {
			*total += n
			if *total > MaxProgramBytes {
				return "", fmt.Errorf("program exceeds %d bytes", MaxProgramBytes)
			}
			b.WriteString(string(buf[:n]))
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

func validVarName(name string) bool {
	if name == "" || !isIdentStart(rune(name[0])) {
		return false
	}
	for _, ch := range name[1:] {
		if !isIdentPart(ch) {
			return false
		}
	}
	return true
}
