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

// checkTracker is a pflag.Value that tracks all --check/-c modes set
// during argument parsing so conflicting modes (diagnose vs silent) can
// be detected. GNU sort rejects mixed modes with "options '-cC' are
// incompatible".
type checkTracker struct {
	last       string // last mode set
	sawDiag    bool   // saw diagnose/diagnose-first
	sawSilent  bool   // saw silent/quiet
	hasInvalid bool   // true if an unrecognized value was seen
	invalid    string // first unrecognized value
}

func (ct *checkTracker) String() string { return ct.last }
func (ct *checkTracker) Type() string   { return "string" }

func (ct *checkTracker) Set(s string) error {
	ct.last = s
	switch s {
	case "silent", "quiet":
		ct.sawSilent = true
	case "diagnose", "diagnose-first":
		ct.sawDiag = true
	default:
		if !ct.hasInvalid {
			ct.invalid = s
		}
		ct.hasInvalid = true
	}
	return nil
}

func (ct *checkTracker) conflict() bool { return ct.sawDiag && ct.sawSilent }

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
	var checkFlag checkTracker
	fs.VarP(&checkFlag, "check", "c", "check for sorted input; optionally =silent or =quiet")
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
			if checkFlag.hasInvalid {
				callCtx.Errf("sort: invalid argument %q for '--check'\n", checkFlag.invalid)
				return builtins.Result{Code: 2}
			}
			// Reject mixed diagnose/silent modes across repeated --check flags.
			if checkFlag.conflict() {
				callCtx.Errf("sort: options '-cC' are incompatible\n")
				return builtins.Result{Code: 2}
			}
			checkEnabled = true
			checkSilent = checkFlag.sawSilent
		}
		if *checkSilentShort {
			// -C is equivalent to --check=silent. Reject only when
			// --check was set to a diagnose mode (GNU compat).
			if fs.Changed("check") && checkFlag.sawDiag {
				callCtx.Errf("sort: options '-cC' are incompatible\n")
				return builtins.Result{Code: 2}
			}
			checkEnabled = true
			checkSilent = true
		}

		// Validate -t flag: must be a single byte.
		sep := byte(0)
		hasSep := false
		if fs.Changed("field-separator") {
			if len(*fieldSep) == 0 {
				callCtx.Errf("sort: empty tab\n")
				return builtins.Result{Code: 2}
			}
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

		// Validate incompatible global flags: -d and -n cannot coexist
		// unless every key has per-key opts that override the globals.
		if globalOpts.dictOrder && globalOpts.numeric {
			globalsUsed := len(keys) == 0
			for _, k := range keys {
				if !k.hasOpts {
					globalsUsed = true
					break
				}
			}
			if globalsUsed {
				callCtx.Errf("sort: options '-dn' are incompatible\n")
				return builtins.Result{Code: 2}
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

		// Shared byte counter across all files for cumulative memory tracking.
		var totalBytes int64

		// Check mode: verify the file is sorted (matches GNU).
		// GNU sort -c rejects multiple file operands.
		if checkEnabled {
			if len(files) > 1 {
				callCtx.Errf("sort: extra operand %q not allowed with -c\n", files[1])
				return builtins.Result{Code: 2}
			}
			file := files[0]
			lines, err := readFile(ctx, callCtx, file, &totalBytes)
			if err != nil {
				name := file
				if file == "-" {
					name = "standard input"
				}
				callCtx.Errf("sort: %s: %s\n", name, callCtx.PortableErr(err))
				return builtins.Result{Code: 1}
			}
			return checkSorted(ctx, callCtx, lines, cmpFn, checkSilent, *unique, file)
		}

		// Read all lines from all files.
		var allLines []string
		for _, file := range files {
			if ctx.Err() != nil {
				return builtins.Result{Code: 1}
			}
			lines, err := readFile(ctx, callCtx, file, &totalBytes)
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

		// Sort the lines. Check ctx.Err() periodically during sorting
		// so that context cancellation can interrupt long sort operations.
		var sortCmps int
		sortCmp := func(a, b string) int {
			sortCmps++
			if sortCmps&1023 == 0 && ctx.Err() != nil {
				return 0
			}
			return cmpFn(a, b)
		}
		if *stable || *unique {
			slices.SortStableFunc(allLines, sortCmp)
		} else {
			slices.SortFunc(allLines, sortCmp)
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
// totalBytes is a shared counter across all files for cumulative byte tracking.
func readFile(ctx context.Context, callCtx *builtins.CallContext, file string, totalBytes *int64) ([]string, error) {
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
	sc.Split(scanLinesPreserveCR)

	var lines []string
	for sc.Scan() {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		line := sc.Text()
		*totalBytes += int64(len(line))
		if *totalBytes > MaxTotalBytes {
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
	startField     int // 1-based
	startChar      int // 1-based, 0 means whole field
	endField       int // 1-based, 0 means end of line
	endChar        int // 1-based, 0 means end of field
	opts           keyOpts
	hasOpts        bool // true if modifiers were specified on this key
	startIgnBlanks bool // -b on start position (skip leading blanks for start offset)
	endIgnBlanks   bool // -b on end position (skip leading blanks for end offset)
}

// parseKeyDef parses a KEYDEF string like "2,2" or "1.2n,1.3" or "2nr".
func parseKeyDef(s string) (keySpec, error) {
	var k keySpec
	startPart := s
	endPart := ""
	if ci := strings.IndexByte(s, ','); ci >= 0 {
		startPart = s[:ci]
		endPart = s[ci+1:]
		if endPart == "" {
			return k, errors.New("invalid number after ','")
		}
	}

	start, opts, err := parseFieldSpec(startPart)
	if err != nil {
		return k, err
	}
	if start.hasDot && start.char == 0 {
		return k, errors.New("character offset is zero")
	}
	k.startField = start.field
	k.startChar = start.char
	if opts.hasAny {
		k.opts = opts.ko
		k.hasOpts = true
		k.startIgnBlanks = opts.ko.ignBlanks
	}

	if endPart != "" {
		end, endOpts, err := parseFieldSpec(endPart)
		if err != nil {
			return k, err
		}
		k.endField = end.field
		k.endChar = end.char
		if endOpts.hasAny {
			k.endIgnBlanks = endOpts.ko.ignBlanks
			k.opts = mergeOpts(k.opts, endOpts.ko)
			k.hasOpts = true
		}
	}

	if k.startField < 1 {
		return k, errors.New("invalid key: field number must be positive")
	}
	if endPart != "" && k.endField < 1 {
		return k, errors.New("invalid key: field number is zero")
	}
	// Validate incompatible per-key options: -d and -n cannot coexist.
	if k.hasOpts && k.opts.dictOrder && k.opts.numeric {
		return k, errors.New("options '-dn' are incompatible")
	}
	return k, nil
}

type fieldPos struct {
	field  int
	char   int
	hasDot bool
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
		fp.hasDot = true
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
// It works with byte positions in the original line to avoid reconstruction
// artifacts (e.g. synthetic joiners doubling blanks in blank-separated mode).
// ignBlanksStart/ignBlanksEnd control whether leading blanks are skipped
// when computing the start/end byte positions respectively (GNU sort -b).
func extractKey(line string, k keySpec, sep byte, hasSep bool, ignBlanksStart, ignBlanksEnd bool) string {
	// Compute field start/end byte positions in the original line.
	type fieldBound struct{ start, end int }
	var bounds []fieldBound

	if hasSep {
		pos := 0
		for {
			idx := strings.IndexByte(line[pos:], sep)
			if idx < 0 {
				bounds = append(bounds, fieldBound{pos, len(line)})
				break
			}
			bounds = append(bounds, fieldBound{pos, pos + idx})
			pos = pos + idx + 1
		}
	} else {
		// Blank-separated: fields are contiguous substrings of line.
		pos := 0
		for _, f := range splitBlankFields(line) {
			bounds = append(bounds, fieldBound{pos, pos + len(f)})
			pos += len(f)
		}
	}

	sf := k.startField - 1
	if sf >= len(bounds) {
		return ""
	}

	// Compute start byte position.
	keyStart := bounds[sf].start
	if ignBlanksStart {
		// GNU sort skips blanks past the field boundary (e.g. past
		// an empty field into separator characters) when -b is set.
		for keyStart < len(line) && (line[keyStart] == ' ' || line[keyStart] == '\t') {
			keyStart++
		}
	}
	if k.startChar > 0 {
		keyStart += k.startChar - 1
	}
	if keyStart >= len(line) {
		return ""
	}

	// Compute end byte position.
	if k.endField == 0 {
		return line[keyStart:]
	}

	ef := k.endField - 1
	if ef >= len(bounds) {
		// End field beyond available fields — treat as end-of-line.
		return line[keyStart:]
	}
	endFieldStart := bounds[ef].start
	if ignBlanksEnd {
		for endFieldStart < bounds[ef].end && (line[endFieldStart] == ' ' || line[endFieldStart] == '\t') {
			endFieldStart++
		}
	}

	keyEnd := bounds[ef].end
	if k.endChar > 0 {
		keyEnd = endFieldStart + k.endChar
	}
	if keyEnd > len(line) {
		keyEnd = len(line)
	}
	if keyStart >= keyEnd {
		return ""
	}
	return line[keyStart:keyEnd]
}

// splitBlankFields splits a line into fields using blank-to-non-blank
// transitions. Each field includes any preceding blanks (matching POSIX/GNU
// sort behavior where leading blanks are significant unless -b is set).
// For example, "  b  c" splits into ["  b", "  c"].
func splitBlankFields(line string) []string {
	var fields []string
	i := 0
	n := len(line)
	for i < n {
		start := i
		// Include leading blanks as part of this field.
		for i < n && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i >= n {
			// Only blanks remain — preserve as a field so trailing
			// blank fields are kept (matching GNU sort behavior).
			fields = append(fields, line[start:])
			break
		}
		// Non-blank content of the field.
		for i < n && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		fields = append(fields, line[start:i])
	}
	return fields
}

// extractKeyFromFields extracts a key substring from pre-split fields.
// joiner is the string used to rejoin multiple fields (the actual separator
// when -t is used, or " " for blank-separated fields).
func extractKeyFromFields(fields []string, k keySpec, joiner string) string {
	sf := k.startField - 1
	if sf >= len(fields) {
		return ""
	}

	// Simple case: no end field specified, no char positions.
	if k.endField == 0 && k.startChar == 0 {
		return strings.Join(fields[sf:], joiner)
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
			return startStr + joiner + strings.Join(fields[sf+1:], joiner)
		}
		return startStr
	}

	ef := k.endField - 1
	if ef >= len(fields) {
		// End field is beyond available fields — treat as end-of-line.
		if sf+1 < len(fields) {
			return startStr + joiner + strings.Join(fields[sf+1:], joiner)
		}
		return startStr
	}

	// GNU sort treats end-before-start (e.g. -k 2,1) as a zero-width key,
	// which falls back to whole-line comparison during tie-breaking.
	if ef < sf {
		return ""
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
		b.WriteString(joiner)
		b.WriteString(fields[i])
	}
	b.WriteString(joiner)
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

// compareNumeric compares two strings as numbers using string-based
// comparison to avoid float64 precision loss for large integers.
// Handles optional leading whitespace, sign, and decimal point.
// Non-numeric strings compare as 0. Matches GNU sort -n behavior.
func compareNumeric(a, b string) int {
	aNeg, aInt, aFrac := parseNumParts(a)
	bNeg, bInt, bFrac := parseNumParts(b)

	// Different signs: negative < positive.
	if aNeg != bNeg {
		if aNeg {
			return -1
		}
		return 1
	}

	// Same sign — compare magnitudes, flip for negative.
	c := compareMagnitude(aInt, aFrac, bInt, bFrac)
	if aNeg {
		c = -c
	}
	return c
}

// compareMagnitude compares two non-negative numbers represented as
// integer and fractional digit strings.
func compareMagnitude(aInt, aFrac, bInt, bFrac string) int {
	// Compare integer parts: longer digit string is larger.
	if len(aInt) != len(bInt) {
		if len(aInt) < len(bInt) {
			return -1
		}
		return 1
	}
	// Same length: compare digit-by-digit.
	if aInt < bInt {
		return -1
	}
	if aInt > bInt {
		return 1
	}
	// Integer parts equal: compare fractional parts.
	// Pad shorter fraction with trailing zeros conceptually.
	la, lb := len(aFrac), len(bFrac)
	minLen := la
	if lb < minLen {
		minLen = lb
	}
	for i := 0; i < minLen; i++ {
		if aFrac[i] < bFrac[i] {
			return -1
		}
		if aFrac[i] > bFrac[i] {
			return 1
		}
	}
	// Check remaining digits (longer fraction with non-zero trailing digits is larger).
	if la > lb {
		for i := lb; i < la; i++ {
			if aFrac[i] > '0' {
				return 1
			}
		}
	} else if lb > la {
		for i := la; i < lb; i++ {
			if bFrac[i] > '0' {
				return -1
			}
		}
	}
	return 0
}

// parseNumParts extracts the sign, integer digit string, and fractional
// digit string from a numeric prefix. Returns (negative, intDigits, fracDigits).
// Non-numeric input returns (false, "0", ""), which compares as zero.
func parseNumParts(s string) (bool, string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return false, "0", ""
	}
	neg := false
	i := 0
	if s[i] == '-' {
		neg = true
		i++
	}
	// GNU sort -n does NOT accept '+' as a sign prefix — treat +N as non-numeric.
	if i >= len(s) || (s[i] < '0' || s[i] > '9') && s[i] != '.' {
		return false, "0", ""
	}
	// Parse integer digits.
	intStart := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	intPart := s[intStart:i]
	// Strip leading zeros from integer part.
	j := 0
	for j < len(intPart)-1 && intPart[j] == '0' {
		j++
	}
	intPart = intPart[j:]
	// Canonicalize empty integer part (e.g. ".5") to "0".
	if intPart == "" {
		intPart = "0"
	}

	// Parse fractional digits.
	fracPart := ""
	if i < len(s) && s[i] == '.' {
		i++
		fracStart := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		fracPart = s[fracStart:i]
	}

	// Check if the value is actually zero (e.g. "-0", "-0.0").
	if isZeroNum(intPart, fracPart) {
		neg = false
	}
	return neg, intPart, fracPart
}

// isZeroNum returns true if the integer and fractional parts represent zero.
func isZeroNum(intPart, fracPart string) bool {
	for _, c := range intPart {
		if c != '0' {
			return false
		}
	}
	for _, c := range fracPart {
		if c != '0' {
			return false
		}
	}
	return true
}

// scanLinesPreserveCR is a bufio.SplitFunc that splits on \n but preserves
// \r in the token (unlike bufio.ScanLines which strips \r from \r\n).
// This ensures CRLF data is round-tripped faithfully through sort.
func scanLinesPreserveCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// buildCompare constructs the comparison function for sorting.
func buildCompare(keys []keySpec, globalOpts keyOpts, sep byte, hasSep bool, stableSort bool) func(a, b string) int {
	return func(a, b string) int {
		if len(keys) > 0 {
			for _, k := range keys {
				opts := globalOpts
				if k.hasOpts {
					opts = k.opts
				}
				// Determine start/end blank-skipping independently.
				// GNU sort applies -b per-position: start-b and end-b
				// are tracked separately on the key spec.
				startB := opts.ignBlanks
				endB := opts.ignBlanks
				if k.hasOpts {
					startB = k.startIgnBlanks
					endB = k.endIgnBlanks
				}
				ka := extractKey(a, k, sep, hasSep, startB, endB)
				kb := extractKey(b, k, sep, hasSep, startB, endB)
				// Don't apply -b again in compareStrings — extractKey
				// already handled blank-skipping during position computation.
				compOpts := opts
				compOpts.ignBlanks = false
				c := compareStrings(ka, kb, compOpts)
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
		// Last-resort: raw byte comparison (unless stable/unique).
		// GNU sort reverses the last-resort when -r is the global option.
		if stableSort {
			return 0
		}
		c := 0
		if a < b {
			c = -1
		} else if a > b {
			c = 1
		}
		if globalOpts.reverse {
			c = -c
		}
		return c
	}
}

// checkSorted verifies that lines are already sorted according to cmpFn.
// When unique is true, equal adjacent lines are also treated as a disorder
// (matching GNU sort -c -u which checks for strict ordering).
// file is the filename used in the diagnostic message (or "-" for stdin).
func checkSorted(ctx context.Context, callCtx *builtins.CallContext, lines []string, cmpFn func(a, b string) int, silent bool, unique bool, file string) builtins.Result {
	for i := 1; i < len(lines); i++ {
		if i&1023 == 0 && ctx.Err() != nil {
			return builtins.Result{Code: 1}
		}
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
