// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package awk implements a restricted awk interpreter. File reads and command
// pipes use rshell's sandbox capabilities.
package awk

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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
	callCtx.Out("This is a practical rshell awk profile, not a full GNU awk clone.\n")
	callCtx.Out("With no FILE, or when FILE is -, read standard input.\n\n")

	callCtx.Out("Supported profile:\n")
	callCtx.Out("  - Inline programs, -f program files, -F separators, -v assignments, FILE args, and - for stdin.\n")
	callCtx.Out("  - BEGIN/main/END rules; regex, comparison, boolean, and range patterns.\n")
	callCtx.Out("  - Fields and records: $0, $1..$NF, NF, NR, FNR, FILENAME, FS, RS, OFS, ORS, SUBSEP, RSTART, RLENGTH.\n")
	callCtx.Out("  - Scalars, associative arrays, composite keys, ENVIRON, IGNORECASE, arithmetic, comparisons, regex match, ternary, and string concatenation.\n")
	callCtx.Out("  - if/else, for, for-in, while, break, continue, next, exit, and user-defined functions with return.\n")
	callCtx.Out("  - Evaluated expression nodes have a 4,194,304-operation limit per awk run. Main-input records, per-record rule evaluations, executed statements, explicit loop iterations, and user-function calls each have a 1,048,576-operation limit; function depth is capped at 256.\n")
	callCtx.Out("  - Evaluated strings, byte-weighted array sorting, and regex cache misses share a 67,108,864-unit aggregate work limit per awk run.\n")
	callCtx.Out("  - Stdout, including output command-pipe stdout, is capped at 10,485,760 bytes per awk run.\n")
	callCtx.Out("  - Substitution calls retain at most 32,768 aggregate match indices.\n")
	callCtx.Out("  - print, printf, sprintf, length, substr, index, tolower, toupper, int, split, sub, gsub, gensub, match, strtonum, asorti, delete, and close.\n")
	callCtx.Out("  - Output command pipes such as print x | \"sort\" and rshell command strings such as print x | \"cat | sort\".\n")
	callCtx.Out("  - Command-pipe buffers and lookahead metadata are bounded across all active pipes.\n")
	callCtx.Out("  - getline, getline var, getline var < file, and \"cmd\" | getline var; file reads use rshell path policy and command strings run through rshell.\n\n")

	callCtx.Out("Not supported:\n")
	callCtx.Out("  - system(). Use supported awk command pipes/getline pipes instead; command strings run through rshell and its active sandbox.\n")
	callCtx.Out("  - print/printf file output redirection to file targets, such as print x > \"file\" or printf ... >> \"file\". Output command pipes remain supported and their command strings follow normal rshell policy.\n")
	callCtx.Out("  - ARGV/ARGC mutation, BEGINFILE/ENDFILE, nextfile, do/while, switch, include/load, namespaces, and indirect function calls.\n")
	callCtx.Out("  - GNU awk CSV mode, FIELDWIDTHS, FPAT, PROCINFO, SYMTAB, FUNCTAB, typed regexps, and extension loading.\n")
	callCtx.Out("  - Many GNU/POSIX utility builtins are intentionally absent, including asort, patsplit, math/time/random helpers, bitwise, typeof, and i18n functions.\n\n")

	fs.SetOutput(callCtx.Stdout)
	fs.PrintDefaults()
}

func loadProgram(ctx context.Context, callCtx *builtins.CallContext, args []string, programFiles []string) (string, []string, error) {
	var parts []string
	var files []string
	total := 0
	if len(programFiles) > 0 {
		if len(programFiles) > MaxProgramFiles {
			return "", nil, fmt.Errorf("too many program files (maximum %d)", MaxProgramFiles)
		}
		total = len(programFiles) - 1 // strings.Join inserts one newline between each file.
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
		return readProgramStdin(ctx, callCtx.Stdin, total)
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

type programReadResult struct {
	text  string
	total int
	err   error
}

func readProgramStdin(ctx context.Context, r byteReader, total *int) (string, error) {
	if ctx.Done() == nil {
		return readProgram(ctx, r, total)
	}
	result := make(chan programReadResult, 1)
	initialTotal := *total
	go func() {
		readTotal := initialTotal
		text, err := readProgram(ctx, r, &readTotal)
		result <- programReadResult{text: text, total: readTotal, err: err}
	}()
	select {
	case read := <-result:
		if err := ctx.Err(); err != nil {
			return "", err
		}
		*total = read.total
		return read.text, read.err
	case <-ctx.Done():
		if setter, ok := r.(interface {
			SetReadDeadline(time.Time) error
		}); ok && setter.SetReadDeadline(time.Unix(1, 0)) == nil {
			go func() {
				<-result
				_ = setter.SetReadDeadline(time.Time{})
			}()
		}
		return "", ctx.Err()
	}
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
