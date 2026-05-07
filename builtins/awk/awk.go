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
// This implements a practical, intentionally restricted awk profile: program
// loading from an inline argument or -f files, -F field
// separators, -v scalar variables, BEGIN/main/END rules, print and printf,
// scalar and associative array assignment, if/else, for/while loops, next,
// arithmetic/comparison/boolean expressions, regex patterns and match
// operators, regex field separators, string concatenation, scalar built-in
// functions, split, delete, ENVIRON, and field/built-in variables such as $0,
// $1, NF, NR, FNR, FILENAME, FS, OFS, and ORS.
//
// Blocked or deferred features include system(), command pipes, output
// redirection, getline, user-defined functions, and many additional POSIX/GNU
// awk builtins.
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

type orderedOptionKind int

const (
	orderedOptionFieldSeparator orderedOptionKind = iota
	orderedOptionAssignment
)

type orderedOption struct {
	kind  orderedOptionKind
	value string
}

type fieldSeparatorOption struct {
	options *[]orderedOption
	value   string
}

func (f *fieldSeparatorOption) String() string { return f.value }
func (f *fieldSeparatorOption) Set(v string) error {
	f.value = v
	*f.options = append(*f.options, orderedOption{kind: orderedOptionFieldSeparator, value: v})
	return nil
}
func (f *fieldSeparatorOption) Type() string { return "string" }

type assignmentOption struct {
	options *[]orderedOption
	values  []string
}

func (a *assignmentOption) String() string { return strings.Join(a.values, ",") }
func (a *assignmentOption) Set(v string) error {
	a.values = append(a.values, v)
	*a.options = append(*a.options, orderedOption{kind: orderedOptionAssignment, value: v})
	return nil
}
func (a *assignmentOption) Type() string { return "string" }

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	fs.SetInterspersed(false)
	help := fs.BoolP("help", "h", false, "print usage and exit")
	var orderedOptions []orderedOption
	fieldSep := fieldSeparatorOption{options: &orderedOptions}
	fs.VarP(&fieldSep, "field-separator", "F", "use an input field separator regular expression")
	var programFiles stringList
	fs.VarP(&programFiles, "file", "f", "read awk program from file")
	assignments := assignmentOption{options: &orderedOptions}
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
		for _, opt := range orderedOptions {
			name := "FS"
			value := opt.value
			if opt.kind == orderedOptionAssignment {
				var ok bool
				name, value, ok = strings.Cut(opt.value, "=")
				if !ok || !validVarName(name) {
					callCtx.Errf("awk: invalid -v assignment %q\n", opt.value)
					return builtins.Result{Code: 1}
				}
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
	return validIdentifierName(name) && !isSpecialPatternName(name)
}

func validIdentifierName(name string) bool {
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

func isSpecialPatternName(name string) bool {
	return name == "BEGIN" || name == "END"
}
