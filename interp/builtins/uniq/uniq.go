// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package uniq implements the uniq builtin command.
//
// uniq — report or omit repeated lines
//
// Usage: uniq [OPTION]... [INPUT]
//
// Filter adjacent matching lines from INPUT (or standard input),
// writing to standard output.
//
// Accepted flags:
//
//	-c, --count
//	    Prefix lines by the number of occurrences.
//
//	-d, --repeated
//	    Only print duplicate lines, one for each group.
//
//	-D, --all-repeated[=METHOD]
//	    Print all duplicate lines. METHOD is one of: none (default),
//	    prepend, separate. Delimit groups with empty lines per METHOD.
//
//	-f N, --skip-fields=N
//	    Avoid comparing the first N fields. A field is a run of blanks
//	    (space or tab) followed by non-blank characters.
//
//	-i, --ignore-case
//	    Ignore differences in case when comparing lines.
//
//	-s N, --skip-chars=N
//	    Avoid comparing the first N characters.
//
//	-u, --unique
//	    Only print unique lines (lines that are not repeated).
//
//	-w N, --check-chars=N
//	    Compare no more than N characters in lines.
//
//	-z, --zero-terminated
//	    Line delimiter is NUL (\0), not newline.
//
//	--group[=METHOD]
//	    Show all items, separating groups with an empty line. METHOD
//	    is one of: separate (default), prepend, append, both.
//	    Cannot be used with -c, -d, -D, or -u.
//
//	-h, --help
//	    Print this usage message to stdout and exit 0.
//
// The OUTPUT positional argument accepted by GNU uniq is rejected because
// this shell does not permit filesystem writes.
//
// Exit codes:
//
//	0  Success.
//	1  At least one error occurred (missing file, invalid argument, etc.).
//
// Memory safety:
//
//	Processing is streaming: only the current and previous lines are kept
//	in memory. A per-line cap of MaxLineBytes (1 MiB) prevents unbounded
//	allocation on very long lines. All loops check ctx.Err() at each
//	iteration to honour the shell's execution timeout.
package uniq

import (
	"bufio"
	"context"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/pflag"

	"github.com/DataDog/rshell/interp/builtins"
)

func init() {
	builtins.Register("uniq", run)
}

// MaxLineBytes is the per-line buffer cap for the line scanner.
const MaxLineBytes = 1 << 20 // 1 MiB

// MaxFieldOrChar is the maximum accepted value for -f, -s, -w flags.
const MaxFieldOrChar = 1<<31 - 1

// countFieldWidth is the width of the right-justified count field
// used by the -c flag, matching GNU uniq's format.
const countFieldWidth = 7

// countFieldPad is a string of spaces used for right-justifying the count.
const countFieldPad = "       " // must be countFieldWidth spaces

// maxGroupLines caps the number of lines buffered per group in
// --all-repeated mode to prevent unbounded memory growth.
const maxGroupLines = 100000

type groupMethod int

const (
	groupNone groupMethod = iota
	groupSeparate
	groupPrepend
	groupAppend
	groupBoth
)

type allRepeatedMethod int

const (
	allRepeatedNone allRepeatedMethod = iota
	allRepeatedPrepend
	allRepeatedSeparate
)

func run(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	fs := pflag.NewFlagSet("uniq", pflag.ContinueOnError)
	fs.SetOutput(io.Discard)

	help := fs.BoolP("help", "h", false, "print usage and exit")
	count := fs.BoolP("count", "c", false, "prefix lines by the number of occurrences")
	repeated := fs.BoolP("repeated", "d", false, "only print duplicate lines")
	unique := fs.BoolP("unique", "u", false, "only print unique lines")
	ignoreCase := fs.BoolP("ignore-case", "i", false, "ignore case when comparing")
	zeroTerm := fs.BoolP("zero-terminated", "z", false, "line delimiter is NUL, not newline")

	skipFields := fs.IntP("skip-fields", "f", 0, "avoid comparing the first N fields")
	skipChars := fs.IntP("skip-chars", "s", 0, "avoid comparing the first N characters")
	checkChars := fs.IntP("check-chars", "w", 0, "compare no more than N characters")

	allRepeatedStr := fs.StringP("all-repeated", "D", "", "print all duplicate lines; optionally delimited by METHOD")
	fs.Lookup("all-repeated").NoOptDefVal = "none"

	groupStr := fs.String("group", "", "show all items, separated by METHOD")
	fs.Lookup("group").NoOptDefVal = "separate"

	if err := fs.Parse(args); err != nil {
		callCtx.Errf("uniq: %v\n", err)
		return builtins.Result{Code: 1}
	}

	if *help {
		callCtx.Out("Usage: uniq [OPTION]... [INPUT]\n")
		callCtx.Out("Filter adjacent matching lines from INPUT (or stdin), writing to stdout.\n\n")
		fs.SetOutput(callCtx.Stdout)
		fs.PrintDefaults()
		return builtins.Result{}
	}

	positional := fs.Args()
	if len(positional) > 1 {
		callCtx.Errf("uniq: extra operand %q\n", positional[1])
		return builtins.Result{Code: 1}
	}

	if *skipFields < 0 || *skipFields > MaxFieldOrChar {
		callCtx.Errf("uniq: invalid number of fields to skip: %q\n", strconv.Itoa(*skipFields))
		return builtins.Result{Code: 1}
	}
	if *skipChars < 0 || *skipChars > MaxFieldOrChar {
		callCtx.Errf("uniq: invalid number of bytes to skip: %q\n", strconv.Itoa(*skipChars))
		return builtins.Result{Code: 1}
	}
	if *checkChars < 0 || *checkChars > MaxFieldOrChar {
		callCtx.Errf("uniq: invalid number of bytes to compare: %q\n", strconv.Itoa(*checkChars))
		return builtins.Result{Code: 1}
	}

	useCheckChars := fs.Changed("check-chars")

	var arMethod allRepeatedMethod
	useAllRepeated := fs.Changed("all-repeated")
	if useAllRepeated {
		switch {
		case hasPrefix("none", *allRepeatedStr):
			arMethod = allRepeatedNone
		case hasPrefix("prepend", *allRepeatedStr):
			arMethod = allRepeatedPrepend
		case hasPrefix("separate", *allRepeatedStr):
			arMethod = allRepeatedSeparate
		default:
			callCtx.Errf("uniq: invalid argument %q for '--all-repeated'\n", *allRepeatedStr)
			return builtins.Result{Code: 1}
		}
	}

	var grpMethod groupMethod
	useGroup := fs.Changed("group")
	if useGroup {
		switch {
		case hasPrefix("separate", *groupStr):
			grpMethod = groupSeparate
		case hasPrefix("prepend", *groupStr):
			grpMethod = groupPrepend
		case hasPrefix("append", *groupStr):
			grpMethod = groupAppend
		case hasPrefix("both", *groupStr):
			grpMethod = groupBoth
		default:
			callCtx.Errf("uniq: invalid argument %q for '--group'\n", *groupStr)
			return builtins.Result{Code: 1}
		}
	}

	if useGroup && (*count || *repeated || useAllRepeated || *unique) {
		callCtx.Errf("uniq: --group is mutually exclusive with -c/-d/-D/-u\n")
		return builtins.Result{Code: 1}
	}

	if useAllRepeated && *count {
		callCtx.Errf("uniq: printing all duplicated lines and repeat counts is meaningless\n")
		return builtins.Result{Code: 1}
	}

	file := "-"
	if len(positional) == 1 {
		file = positional[0]
	}

	delim := byte('\n')
	if *zeroTerm {
		delim = 0
	}

	cfg := &config{
		count:         *count,
		repeated:      *repeated,
		unique:        *unique,
		ignoreCase:    *ignoreCase,
		skipFields:    *skipFields,
		skipChars:     *skipChars,
		checkChars:    *checkChars,
		useCheckChars: useCheckChars,
		allRepeated:   useAllRepeated,
		arMethod:      arMethod,
		grpMethod:     grpMethod,
		delim:         delim,
	}

	if err := process(ctx, callCtx, file, cfg); err != nil {
		name := file
		if file == "-" {
			name = "standard input"
		}
		callCtx.Errf("uniq: %s: %s\n", name, callCtx.PortableErr(err))
		return builtins.Result{Code: 1}
	}
	return builtins.Result{}
}

type config struct {
	count         bool
	repeated      bool
	unique        bool
	ignoreCase    bool
	skipFields    int
	skipChars     int
	checkChars    int
	useCheckChars bool
	allRepeated   bool
	arMethod      allRepeatedMethod
	grpMethod     groupMethod
	delim         byte
}

func process(ctx context.Context, callCtx *builtins.CallContext, file string, cfg *config) error {
	var rc io.ReadCloser
	if file == "-" {
		if callCtx.Stdin == nil {
			return nil
		}
		rc = io.NopCloser(callCtx.Stdin)
	} else {
		f, err := callCtx.OpenFile(ctx, file, os.O_RDONLY, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		rc = f
	}

	sc := bufio.NewScanner(rc)
	buf := make([]byte, 4096)
	sc.Buffer(buf, MaxLineBytes)
	sc.Split(makeSplitFunc(cfg.delim))

	w := callCtx.Stdout
	delimStr := string(cfg.delim)

	if cfg.grpMethod != groupNone {
		return processGroup(ctx, w, sc, cfg, delimStr)
	}
	if cfg.allRepeated {
		return processAllRepeated(ctx, w, sc, cfg, delimStr)
	}
	return processDefault(ctx, w, sc, cfg, delimStr)
}

func processDefault(ctx context.Context, w io.Writer, sc *bufio.Scanner, cfg *config, delimStr string) error {
	var prev string
	var prevCount int
	first := true

	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Text()
		if first {
			prev = line
			prevCount = 1
			first = false
			continue
		}
		if linesEqual(prev, line, cfg) {
			prevCount++
			continue
		}
		if err := emitLine(w, prev, prevCount, cfg, delimStr); err != nil {
			return err
		}
		prev = line
		prevCount = 1
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if !first {
		return emitLine(w, prev, prevCount, cfg, delimStr)
	}
	return nil
}

func emitLine(w io.Writer, line string, n int, cfg *config, delimStr string) error {
	if cfg.repeated && n < 2 {
		return nil
	}
	if cfg.unique && n >= 2 {
		return nil
	}
	if cfg.count {
		s := strconv.Itoa(n)
		pad := max(0, countFieldWidth-len(s))
		_, err := io.WriteString(w, countFieldPad[:pad]+s+" "+line+delimStr)
		return err
	}
	_, err := io.WriteString(w, line+delimStr)
	return err
}

func processAllRepeated(ctx context.Context, w io.Writer, sc *bufio.Scanner, cfg *config, delimStr string) error {
	var group []string
	var prev string
	first := true
	firstGroup := true

	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Text()
		if first {
			prev = line
			group = append(group, line)
			first = false
			continue
		}
		if linesEqual(prev, line, cfg) {
			if len(group) < maxGroupLines {
				group = append(group, line)
			}
			continue
		}
		if err := emitAllRepeatedGroup(w, group, cfg, delimStr, &firstGroup); err != nil {
			return err
		}
		group = group[:0]
		group = append(group, line)
		prev = line
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if !first {
		return emitAllRepeatedGroup(w, group, cfg, delimStr, &firstGroup)
	}
	return nil
}

func emitAllRepeatedGroup(w io.Writer, group []string, cfg *config, delimStr string, firstGroup *bool) error {
	if len(group) < 2 {
		return nil
	}
	switch cfg.arMethod {
	case allRepeatedPrepend:
		if _, err := io.WriteString(w, delimStr); err != nil {
			return err
		}
	case allRepeatedSeparate:
		if !*firstGroup {
			if _, err := io.WriteString(w, delimStr); err != nil {
				return err
			}
		}
	}
	*firstGroup = false
	for _, line := range group {
		if _, err := io.WriteString(w, line+delimStr); err != nil {
			return err
		}
	}
	return nil
}

func processGroup(ctx context.Context, w io.Writer, sc *bufio.Scanner, cfg *config, delimStr string) error {
	var prev string
	first := true
	firstGroup := true

	for sc.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line := sc.Text()
		if first {
			prev = line
			if err := emitGroupStart(w, cfg, delimStr, &firstGroup); err != nil {
				return err
			}
			if _, err := io.WriteString(w, line+delimStr); err != nil {
				return err
			}
			first = false
			continue
		}
		if linesEqual(prev, line, cfg) {
			if _, err := io.WriteString(w, line+delimStr); err != nil {
				return err
			}
			continue
		}
		if cfg.grpMethod == groupAppend {
			if _, err := io.WriteString(w, delimStr); err != nil {
				return err
			}
		}
		if err := emitGroupStart(w, cfg, delimStr, &firstGroup); err != nil {
			return err
		}
		if _, err := io.WriteString(w, line+delimStr); err != nil {
			return err
		}
		prev = line
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if !first && (cfg.grpMethod == groupAppend || cfg.grpMethod == groupBoth) {
		if _, err := io.WriteString(w, delimStr); err != nil {
			return err
		}
	}
	return nil
}

func emitGroupStart(w io.Writer, cfg *config, delimStr string, firstGroup *bool) error {
	if cfg.grpMethod == groupPrepend || cfg.grpMethod == groupBoth {
		if _, err := io.WriteString(w, delimStr); err != nil {
			return err
		}
	} else if cfg.grpMethod == groupSeparate && !*firstGroup {
		if _, err := io.WriteString(w, delimStr); err != nil {
			return err
		}
	}
	*firstGroup = false
	return nil
}

func linesEqual(a, b string, cfg *config) bool {
	a = extractCompareKey(a, cfg)
	b = extractCompareKey(b, cfg)
	if cfg.ignoreCase {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func extractCompareKey(line string, cfg *config) string {
	s := line
	if cfg.skipFields > 0 {
		s = skipFieldsN(s, cfg.skipFields)
	}
	if cfg.skipChars > 0 {
		if cfg.skipChars >= len(s) {
			s = ""
		} else {
			s = s[cfg.skipChars:]
		}
	}
	if cfg.useCheckChars && cfg.checkChars < len(s) {
		s = s[:cfg.checkChars]
	}
	return s
}

func skipFieldsN(s string, n int) string {
	i := 0
	for field := 0; field < n && i < len(s); field++ {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		for i < len(s) && s[i] != ' ' && s[i] != '\t' {
			i++
		}
	}
	return s[i:]
}

func hasPrefix(full, abbrev string) bool {
	return len(abbrev) > 0 && len(abbrev) <= len(full) && full[:len(abbrev)] == abbrev
}

func makeSplitFunc(delim byte) bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		for i, b := range data {
			if b == delim {
				return i + 1, data[:i], nil
			}
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	}
}
