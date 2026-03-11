// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package sort implements the sort builtin command.
//
// sort — sort lines of text files
//
// Usage: sort [OPTION]... [FILE]...
//
// Write sorted concatenation of all FILE(s) to standard output.
// With no FILE, or when FILE is -, read standard input.
//
// Accepted flags:
//
//	-r, --reverse
//	    Reverse the result of comparisons (sort descending).
//
//	-n, --numeric-sort
//	    Compare according to string numerical value.
//
//	-u, --unique
//	    Output only the first of an equal run.
//
//	-k, --key=KEYDEF
//	    Sort via a key definition; KEYDEF is F[.C][OPTS][,F[.C][OPTS]].
//
//	-t, --field-separator=SEP
//	    Use SEP as the field separator.
//
//	-b, --ignore-leading-blanks
//	    Ignore leading blanks when finding sort keys.
//
//	-f, --ignore-case
//	    Fold lowercase to uppercase for comparisons.
//
//	-d, --dictionary-order
//	    Consider only blanks and alphanumeric characters.
//
//	-c
//	    Check whether input is sorted; exit 1 if not.
//
//	-C, --check=silent
//	    Like -c but do not print the diagnostic line.
//
//	-s, --stable
//	    Stabilize sort by disabling last-resort comparison.
//
//	-h, --help
//	    Print usage to stdout and exit 0.
//
// Rejected flags (unsafe):
//
//	-o FILE (writes to filesystem)
//	-T DIR  (writes temp files)
//	--compress-program (executes a binary)
//
// Exit codes:
//
//	0  Success (or input is sorted when using -c/-C).
//	1  Error, or input is NOT sorted when using -c/-C.
//
// Memory safety:
//
//	sort must buffer all input before producing output. A maximum of
//	MaxLines (1,000,000) lines is enforced to prevent OOM. Per-line cap
//	of MaxLineBytes (1 MiB) is enforced via the scanner. All loops check
//	ctx.Err() at each iteration to honour the shell's execution timeout.
package sort

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/DataDog/rshell/interp/builtins"
)

// Cmd is the sort builtin command descriptor.
var Cmd = builtins.Command{Name: "sort", MakeFlags: registerFlags}

// MaxLines is the maximum number of lines sort will buffer. Beyond this
// the command errors out to prevent unbounded memory growth.
const MaxLines = 1_000_000

// MaxLineBytes is the per-line buffer cap for the line scanner.
const MaxLineBytes = 1 << 20 // 1 MiB

// MaxTotalBytes is the cumulative byte cap across all input lines. This
// prevents OOM when many lines are each below MaxLineBytes but collectively
// consume excessive memory. 256 MiB is generous for agent workloads.
const MaxTotalBytes = 256 * 1024 * 1024 // 256 MiB

// registerFlags registers all sort flags and returns the bound handler.
func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := fs.BoolP("help", "h", false, "print usage and exit")
	reverse := fs.BoolP("reverse", "r", false, "reverse the result of comparisons")
	numeric := fs.BoolP("numeric-sort", "n", false, "compare according to string numerical value")
	unique := fs.BoolP("unique", "u", false, "output only the first of an equal run")
	keyDefs := fs.StringArrayP("key", "k", nil, "sort via a key; KEYDEF is F[.C][OPTS][,F[.C][OPTS]]")
	fieldSep := fs.StringP("field-separator", "t", "", "use SEP as the field separator")
	ignBlanks := fs.BoolP("ignore-leading-blanks", "b", false, "ignore leading blanks")
	ignCase := fs.BoolP("ignore-case", "f", false, "fold lower case to upper case characters")
	dictOrder := fs.BoolP("dictionary-order", "d", false, "consider only blanks and alphanumeric characters")
	// --check accepts optional values: "diagnose" (default), "silent", "quiet".
	// -c is shorthand for --check (diagnose mode).
	// -C is shorthand for silent check mode.
	checkFlag := fs.StringP("check", "c", "", "check for sorted input; optionally =silent or =quiet")
	checkSilentShort := fs.BoolP("check-silent-short", "C", false, "like -c, but do not report first bad line")
	stable := fs.BoolP("stable", "s", false, "stabilize sort by disabling last-resort comparison")

	// --check with no value means diagnose mode.
	fs.Lookup("check").NoOptDefVal = "diagnose"
	// Hide internal flag.
	fs.MarkHidden("check-silent-short")

	// Rejected flags — declare them so pflag parses them, then reject in handler.
	fs.StringP("output", "o", "", "")
	fs.String("temporary-directory", "", "")
	fs.String("compress-program", "", "")

	// Hide rejected flags from help.
	fs.MarkHidden("output")
	fs.MarkHidden("temporary-directory")
	fs.MarkHidden("compress-program")

	return func(ctx context.Context, callCtx *builtins.CallContext, files []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: sort [OPTION]... [FILE]...\n")
			callCtx.Out("Write sorted concatenation of all FILE(s) to standard output.\n")
			callCtx.Out("With no FILE, or when FILE is -, read standard input.\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}

		// Reject dangerous flags.
		if fs.Changed("output") {
			callCtx.Errf("sort: --output/-o is not supported (writes to filesystem)\n")
			return builtins.Result{Code: 1}
		}
		if fs.Changed("temporary-directory") {
			callCtx.Errf("sort: --temporary-directory is not supported (writes temp files)\n")
			return builtins.Result{Code: 1}
		}
		if fs.Changed("compress-program") {
			callCtx.Errf("sort: --compress-program is not supported (executes a binary)\n")
			return builtins.Result{Code: 1}
		}

		// Resolve check mode from --check[=VALUE] and -C flags.
		checkEnabled := false
		checkSilent := false
		if fs.Changed("check") {
			checkEnabled = true
			switch *checkFlag {
			case "silent", "quiet":
				checkSilent = true
			case "diagnose", "":
				// default: print diagnostic
			}
		}
		if *checkSilentShort {
			checkEnabled = true
			checkSilent = true
		}

		// Validate -t flag: must be a single byte.
		sep := byte(0)
		hasSep := false
		if *fieldSep != "" {
			if len(*fieldSep) != 1 {
				callCtx.Errf("sort: multi-character tab %q\n", *fieldSep)
				return builtins.Result{Code: 2}
			}
			sep = (*fieldSep)[0]
			hasSep = true
		}

		// Parse key definitions.
		globalOpts := keyOpts{
			numeric:    *numeric,
			reverse:    *reverse,
			ignBlanks:  *ignBlanks,
			ignCase:    *ignCase,
			dictOrder:  *dictOrder,
		}

		var keys []keySpec
		if keyDefs != nil {
			for _, kd := range *keyDefs {
				k, err := parseKeyDef(kd)
				if err != nil {
					callCtx.Errf("sort: %s\n", err.Error())
					return builtins.Result{Code: 2}
				}
				keys = append(keys, k)
			}
		}

		// Default to stdin when no files given.
		if len(files) == 0 {
			files = []string{"-"}
		}

		// Build comparison function. Disable last-resort byte comparison
		// when -s (stable) or -u (unique) is set — both require that
		// key-equal lines compare as equal.
		disableLastResort := *stable || *unique
		cmpFn := buildCompare(keys, globalOpts, sep, hasSep, disableLastResort)

		// Check mode: verify the file is sorted (matches GNU).
		// GNU sort -c rejects multiple file operands.
		if checkEnabled {
			if len(files) > 1 {
				callCtx.Errf("sort: extra operand %q not allowed with -c\n", files[1])
				return builtins.Result{Code: 2}
			}
			file := files[0]
			lines, err := readFile(ctx, callCtx, file)
			if err != nil {
				name := file
				if file == "-" {
					name = "standard input"
				}
				callCtx.Errf("sort: %s: %s\n", name, callCtx.PortableErr(err))
				return builtins.Result{Code: 1}
			}
			return checkSorted(callCtx, lines, cmpFn, checkSilent, *unique, file)
		}

		// Read all lines from all files.
		var allLines []string
		for _, file := range files {
			if ctx.Err() != nil {
				return builtins.Result{Code: 1}
			}
			lines, err := readFile(ctx, callCtx, file)
			if err != nil {
				name := file
				if file == "-" {
					name = "standard input"
				}
				callCtx.Errf("sort: %s: %s\n", name, callCtx.PortableErr(err))
				return builtins.Result{Code: 1}
			}
			allLines = append(allLines, lines...)
			if len(allLines) > MaxLines {
				callCtx.Errf("sort: input exceeds maximum of %d lines\n", MaxLines)
				return builtins.Result{Code: 1}
			}
		}

		// Sort the lines.
		if *stable {
			slices.SortStableFunc(allLines, func(a, b string) int {
				return cmpFn(a, b)
			})
		} else {
			slices.SortFunc(allLines, func(a, b string) int {
				return cmpFn(a, b)
			})
		}

		// Unique: suppress consecutive equal lines.
		if *unique {
			allLines = dedup(allLines, cmpFn)
		}

		// Output.
		for _, line := range allLines {
			if ctx.Err() != nil {
				return builtins.Result{Code: 1}
			}
			callCtx.Outf("%s\n", line)
		}

		return builtins.Result{}
	}
}

// readFile reads all lines from a file (or stdin for "-"), stripping trailing newlines.
func readFile(ctx context.Context, callCtx *builtins.CallContext, file string) ([]string, error) {
	var rc io.ReadCloser
	if file == "-" {
		if callCtx.Stdin == nil {
			return nil, nil
		}
		rc = io.NopCloser(callCtx.Stdin)
	} else {
		f, err := callCtx.OpenFile(ctx, file, os.O_RDONLY, 0)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		rc = f
	}

	sc := bufio.NewScanner(rc)
	buf := make([]byte, 4096)
	sc.Buffer(buf, MaxLineBytes)

	var lines []string
	var totalBytes int64
	for sc.Scan() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		line := sc.Text()
		totalBytes += int64(len(line))
		if totalBytes > MaxTotalBytes {
			return nil, errors.New("input exceeds maximum total size")
		}
		lines = append(lines, line)
		if len(lines) > MaxLines {
			return nil, errors.New("too many input lines")
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}

// keyOpts holds the modifier flags for a sort key or the global default.
type keyOpts struct {
	numeric   bool
	reverse   bool
	ignBlanks bool
	ignCase   bool
	dictOrder bool
}

// keySpec represents a parsed -k KEYDEF.
type keySpec struct {
	startField int // 1-based
	startChar  int // 1-based, 0 means whole field
	endField   int // 1-based, 0 means end of line
	endChar    int // 1-based, 0 means end of field
	opts       keyOpts
	hasOpts    bool // true if modifiers were specified on this key
}

// parseKeyDef parses a KEYDEF string like "2,2" or "1.2n,1.3" or "2nr".
func parseKeyDef(s string) (keySpec, error) {
	var k keySpec
	startPart := s
	endPart := ""
	if ci := strings.IndexByte(s, ','); ci >= 0 {
		startPart = s[:ci]
		endPart = s[ci+1:]
	}

	start, opts, err := parseFieldSpec(startPart)
	if err != nil {
		return k, err
	}
	k.startField = start.field
	k.startChar = start.char
	if opts.hasAny {
		k.opts = opts.ko
		k.hasOpts = true
	}

	if endPart != "" {
		end, endOpts, err := parseFieldSpec(endPart)
		if err != nil {
			return k, err
		}
		k.endField = end.field
		k.endChar = end.char
		if endOpts.hasAny {
			k.opts = mergeOpts(k.opts, endOpts.ko)
			k.hasOpts = true
		}
	}

	if k.startField < 1 {
		return k, errors.New("invalid key: field number must be positive")
	}
	return k, nil
}

type fieldPos struct {
	field int
	char  int
}

type parsedOpts struct {
	ko     keyOpts
	hasAny bool
}

// parseFieldSpec parses "F[.C][OPTS]" returning field/char positions and options.
func parseFieldSpec(s string) (fieldPos, parsedOpts, error) {
	var fp fieldPos
	var po parsedOpts

	// Extract trailing option letters.
	i := 0
	for i < len(s) && (s[i] >= '0' && s[i] <= '9' || s[i] == '.') {
		i++
	}
	numPart := s[:i]
	optPart := s[i:]

	// Parse options.
	for _, c := range optPart {
		po.hasAny = true
		switch c {
		case 'n':
			po.ko.numeric = true
		case 'r':
			po.ko.reverse = true
		case 'b':
			po.ko.ignBlanks = true
		case 'f':
			po.ko.ignCase = true
		case 'd':
			po.ko.dictOrder = true
		default:
			return fp, po, errors.New(fmt.Sprintf("invalid key option: %c", c))
		}
	}

	// Parse F[.C].
	dotIdx := strings.IndexByte(numPart, '.')
	if dotIdx >= 0 {
		f, err := strconv.Atoi(numPart[:dotIdx])
		if err != nil {
			return fp, po, errors.New("invalid field number in key")
		}
		fp.field = f
		c, err := strconv.Atoi(numPart[dotIdx+1:])
		if err != nil {
			return fp, po, errors.New("invalid character position in key")
		}
		fp.char = c
	} else {
		if numPart == "" {
			return fp, po, errors.New("empty field specification")
		}
		f, err := strconv.Atoi(numPart)
		if err != nil {
			return fp, po, errors.New("invalid field number in key")
		}
		fp.field = f
	}

	return fp, po, nil
}

func mergeOpts(a, b keyOpts) keyOpts {
	if b.numeric {
		a.numeric = true
	}
	if b.reverse {
		a.reverse = true
	}
	if b.ignBlanks {
		a.ignBlanks = true
	}
	if b.ignCase {
		a.ignCase = true
	}
	if b.dictOrder {
		a.dictOrder = true
	}
	return a
}

// extractKey extracts the sort key substring from a line based on a keySpec.
func extractKey(line string, k keySpec, sep byte, hasSep bool) string {
	var fields []string
	if hasSep {
		fields = strings.Split(line, string(sep))
	} else {
		fields = splitBlankFields(line)
	}
	return extractKeyFromFields(fields, k)
}

// splitBlankFields splits a line into fields using blank-to-non-blank transitions.
func splitBlankFields(line string) []string {
	var fields []string
	i := 0
	n := len(line)
	for i < n {
		// Skip leading blanks.
		for i < n && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= n {
			break
		}
		start := i
		for i < n && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		fields = append(fields, line[start:i])
	}
	return fields
}

// extractKeyFromFields extracts a key substring from pre-split fields.
func extractKeyFromFields(fields []string, k keySpec) string {
	sf := k.startField - 1
	if sf >= len(fields) {
		return ""
	}

	// Simple case: no end field specified, no char positions.
	if k.endField == 0 && k.startChar == 0 {
		return strings.Join(fields[sf:], " ")
	}

	startStr := fields[sf]
	sc := k.startChar
	if sc > 0 {
		sc-- // convert to 0-based
		if sc >= len(startStr) {
			startStr = ""
		} else {
			startStr = startStr[sc:]
		}
	}

	if k.endField == 0 {
		// From startChar to end of line.
		if sf+1 < len(fields) {
			return startStr + " " + strings.Join(fields[sf+1:], " ")
		}
		return startStr
	}

	ef := k.endField - 1
	if ef >= len(fields) {
		ef = len(fields) - 1
	}

	if sf == ef {
		// Same field.
		s := fields[sf]
		start := 0
		if k.startChar > 0 {
			start = k.startChar - 1
		}
		end := len(s)
		if k.endChar > 0 && k.endChar <= len(s) {
			end = k.endChar
		}
		if start >= len(s) {
			return ""
		}
		if end > len(s) {
			end = len(s)
		}
		if start > end {
			return ""
		}
		return s[start:end]
	}

	// Multiple fields.
	var b strings.Builder
	b.WriteString(startStr)
	for i := sf + 1; i < ef; i++ {
		b.WriteString(" ")
		b.WriteString(fields[i])
	}
	b.WriteString(" ")
	endStr := fields[ef]
	if k.endChar > 0 && k.endChar <= len(endStr) {
		endStr = endStr[:k.endChar]
	}
	b.WriteString(endStr)
	return b.String()
}

// compareStrings compares two strings applying the given key options.
func compareStrings(a, b string, opts keyOpts) int {
	if opts.ignBlanks {
		a = trimLeadingBlanks(a)
		b = trimLeadingBlanks(b)
	}
	if opts.dictOrder {
		a = dictFilter(a)
		b = dictFilter(b)
	}
	if opts.numeric {
		return compareNumeric(a, b)
	}
	if opts.ignCase {
		au := foldCase(a)
		bu := foldCase(b)
		if au < bu {
			return -1
		}
		if au > bu {
			return 1
		}
		return 0
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// trimLeadingBlanks strips leading spaces and tabs from s. Unlike
// strings.TrimSpace, it does NOT strip trailing whitespace — matching
// GNU sort -b behavior.
func trimLeadingBlanks(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[i:]
}

// foldCase converts a string to uppercase for case-insensitive comparison.
func foldCase(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		b.WriteByte(c)
	}
	return b.String()
}

// dictFilter removes non-blank, non-alphanumeric characters.
func dictFilter(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			b.WriteByte(c)
		}
	}
	return b.String()
}

// compareNumeric compares two strings as numbers. Leading whitespace and
// optional sign are handled. Non-numeric strings compare as 0.
// Supports decimal numbers (e.g. "1.5", "-3.14") matching GNU sort -n.
func compareNumeric(a, b string) int {
	na := parseNum(a)
	nb := parseNum(b)
	if na < nb {
		return -1
	}
	if na > nb {
		return 1
	}
	return 0
}

// parseNum extracts a leading numeric value from s (with optional leading
// whitespace, sign, and decimal point), returning 0 if s is not numeric.
// Matches GNU sort -n behavior which uses strtod-like parsing.
func parseNum(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	// Find the end of the numeric prefix: optional sign, digits, optional
	// decimal point and more digits.
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	if i >= len(s) || (s[i] < '0' || s[i] > '9') && s[i] != '.' {
		return 0
	}
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	n, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0
	}
	return n
}

// buildCompare constructs the comparison function for sorting.
func buildCompare(keys []keySpec, globalOpts keyOpts, sep byte, hasSep bool, stableSort bool) func(a, b string) int {
	return func(a, b string) int {
		if len(keys) > 0 {
			for _, k := range keys {
				ka := extractKey(a, k, sep, hasSep)
				kb := extractKey(b, k, sep, hasSep)
				opts := globalOpts
				if k.hasOpts {
					opts = k.opts
				}
				c := compareStrings(ka, kb, opts)
				if opts.reverse {
					c = -c
				}
				if c != 0 {
					return c
				}
			}
		} else {
			c := compareStrings(a, b, globalOpts)
			if globalOpts.reverse {
				c = -c
			}
			if c != 0 {
				return c
			}
		}
		// Last-resort: raw byte comparison (unless stable).
		if stableSort {
			return 0
		}
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	}
}

// checkSorted verifies that lines are already sorted according to cmpFn.
// When unique is true, equal adjacent lines are also treated as a disorder
// (matching GNU sort -c -u which checks for strict ordering).
// file is the filename used in the diagnostic message (or "-" for stdin).
func checkSorted(callCtx *builtins.CallContext, lines []string, cmpFn func(a, b string) int, silent bool, unique bool, file string) builtins.Result {
	for i := 1; i < len(lines); i++ {
		c := cmpFn(lines[i-1], lines[i])
		if c > 0 || (unique && c == 0) {
			if !silent {
				callCtx.Errf("sort: %s:%d: disorder: %s\n", file, i+1, lines[i])
			}
			return builtins.Result{Code: 1}
		}
	}
	return builtins.Result{}
}

// dedup removes consecutive equal lines (per cmpFn).
func dedup(lines []string, cmpFn func(a, b string) int) []string {
	if len(lines) == 0 {
		return lines
	}
	result := []string{lines[0]}
	for i := 1; i < len(lines); i++ {
		if cmpFn(lines[i-1], lines[i]) != 0 {
			result = append(result, lines[i])
		}
	}
	return result
}
