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
//	-b, --ignore-leading-blanks
//	    Ignore leading blanks when finding sort keys.
//
//	-d, --dictionary-order
//	    Consider only blanks and alphanumeric characters in keys.
//
//	-f, --ignore-case
//	    Fold lowercase characters to uppercase for comparison.
//
//	-i, --ignore-nonprinting
//	    Consider only printable characters in keys.
//
//	-n, --numeric-sort
//	    Compare according to string numerical value.
//
//	-g, --general-numeric-sort
//	    Compare according to general numerical value, including
//	    scientific notation (e.g. 1e2).
//
//	-h, --human-numeric-sort
//	    Compare human-readable numbers (e.g. 2K, 1G).
//
//	-M, --month-sort
//	    Compare abbreviated month names (JAN < FEB < ... < DEC).
//
//	-V, --version-sort
//	    Natural sort of version numbers within text.
//
//	-R, --random-sort
//	    Shuffle lines by hashing keys with a random salt.
//
//	-r, --reverse
//	    Reverse the result of comparisons.
//
//	-u, --unique
//	    With -c, check strict ordering; without -c, output only the
//	    first of an equal run.
//
//	-s, --stable
//	    Stabilize sort by disabling last-resort comparison.
//
//	-c, --check
//	    Check for sorted input; print a diagnostic for the first
//	    out-of-order line found and exit 1.
//
//	-C, --check-quiet
//	    Like -c, but do not report the first out-of-order line.
//
//	-k, --key=KEYDEF
//	    Sort via a key; KEYDEF gives location and type.
//	    May be specified multiple times.
//
//	-t, --field-separator=SEP
//	    Use SEP instead of non-blank to blank transition as the
//	    field separator.
//
//	-z, --zero-terminated
//	    Line delimiter is NUL, not newline.
//
//	-m, --merge
//	    Merge already sorted files; do not sort.
//
//	--help
//	    Print this usage message to stdout and exit 0.
//
// Rejected flags (unsafe):
//
//	-o, --output            — writes to filesystem
//	--compress-program      — executes external binary
//	-T, --temporary-directory — creates temp files
//	--random-source         — reads from arbitrary files
//
// Exit codes:
//
//	0  All files processed successfully (or input is sorted in check mode).
//	1  Disorder detected in check mode.
//	2  Usage error or runtime error (invalid flag, missing file, etc.).
//
// Memory safety:
//
//	sort must buffer all input lines before sorting. To prevent memory
//	exhaustion, the implementation caps the total number of lines at
//	MaxLines (100 000) and per-line size at MaxLineBytes (1 MiB).
//	Check mode (-c/-C) streams line by line without buffering.
//	All loops check ctx.Err() to honour cancellation and timeouts.
package sort

import (
	"bufio"
	"context"
	"io"
	"os"
	stdsort "sort"
	"strconv"
	"time"

	"github.com/DataDog/rshell/interp/builtins"
)

// Cmd is the sort builtin command descriptor.
var Cmd = builtins.Command{Name: "sort", MakeFlags: registerFlags}

// MaxLines caps the total number of lines that sort will buffer.
const MaxLines = 100_000

// MaxLineBytes is the per-line buffer cap for the line scanner.
const MaxLineBytes = 1 << 20 // 1 MiB

type sortMode int

const (
	modeLexicographic  sortMode = iota
	modeNumeric                 // -n
	modeGeneralNumeric          // -g
	modeHumanNumeric            // -h
	modeMonth                   // -M
	modeVersion                 // -V
	modeRandom                  // -R
)

type keyOpts struct {
	mode           sortMode
	reverse        bool
	ignoreCase     bool
	dictOrder      bool
	ignoreNonPrint bool
	blanksStart    bool
	blanksEnd      bool
}

type keyDef struct {
	startField int
	startChar  int
	endField   int
	endChar    int
	opts       keyOpts
	hasOpts    bool
}

type sortConfig struct {
	keys       []keyDef
	globalOpts keyOpts
	sep        byte
	hasSep     bool
	zeroTerm   bool
	unique     bool
	stable     bool
	check      bool
	checkQuiet bool
	salt       uint64
}

const (
	siK = 1024.0
	siM = siK * 1024
	siG = siM * 1024
	siT = siG * 1024
	siP = siT * 1024
	siE = siP * 1024
	siZ = siE * 1024
	siY = siZ * 1024
)

var defaultNaN, _ = strconv.ParseFloat("NaN", 64)

// registerFlags registers all sort flags on the framework-provided FlagSet and
// returns a bound handler whose flag variables are captured by closure.
func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := fs.Bool("help", false, "print usage and exit")
	numericSort := fs.BoolP("numeric-sort", "n", false, "compare according to string numerical value")
	generalNumeric := fs.BoolP("general-numeric-sort", "g", false, "compare according to general numerical value")
	humanNumeric := fs.BoolP("human-numeric-sort", "h", false, "compare human-readable numbers (e.g. 2K, 1G)")
	monthSort := fs.BoolP("month-sort", "M", false, "compare abbreviated month names")
	versionSort := fs.BoolP("version-sort", "V", false, "natural sort of version numbers")
	randomSort := fs.BoolP("random-sort", "R", false, "shuffle by hashing keys")
	reverse := fs.BoolP("reverse", "r", false, "reverse the result of comparisons")
	unique := fs.BoolP("unique", "u", false, "output only unique lines")
	stable := fs.BoolP("stable", "s", false, "stabilize sort by disabling last-resort comparison")
	ignoreCase := fs.BoolP("ignore-case", "f", false, "fold lowercase to uppercase")
	dictOrder := fs.BoolP("dictionary-order", "d", false, "consider only blanks and alphanumerics")
	ignoreNonPrint := fs.BoolP("ignore-nonprinting", "i", false, "consider only printable characters")
	ignoreBlanks := fs.BoolP("ignore-leading-blanks", "b", false, "ignore leading blanks")
	keys := fs.StringArrayP("key", "k", nil, "sort via a key; KEYDEF gives location and type")
	separator := fs.StringP("field-separator", "t", "", "use SEP as field separator")
	zeroTerm := fs.BoolP("zero-terminated", "z", false, "line delimiter is NUL, not newline")
	merge := fs.BoolP("merge", "m", false, "merge already sorted files; do not sort")
	var checkFlag, checkQuietFlag bool
	fs.BoolVarP(&checkFlag, "check", "c", false, "check for sorted input")
	fs.BoolVarP(&checkQuietFlag, "check-quiet", "C", false, "like -c, but do not report first line")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	if *help {
		callCtx.Out("Usage: sort [OPTION]... [FILE]...\n")
		callCtx.Out("Write sorted concatenation of all FILE(s) to standard output.\n")
		callCtx.Out("With no FILE, or when FILE is -, read standard input.\n\n")
		fs.SetOutput(callCtx.Stdout)
		fs.PrintDefaults()
		return builtins.Result{}
	}

	var sep byte
	var hasSep bool
	if fs.Changed("field-separator") {
		if len(*separator) == 0 {
			callCtx.Errf("sort: empty field separator\n")
			return builtins.Result{Code: 2}
		}
		if len(*separator) != 1 {
			callCtx.Errf("sort: multi-character tab %q\n", *separator)
			return builtins.Result{Code: 2}
		}
		sep = (*separator)[0]
		hasSep = true
	}

	if checkFlag && checkQuietFlag {
		callCtx.Errf("sort: incompatible options: -c and -C\n")
		return builtins.Result{Code: 2}
	}

	var globalMode sortMode
	modeCount := 0
	if *numericSort {
		globalMode = modeNumeric
		modeCount++
	}
	if *generalNumeric {
		globalMode = modeGeneralNumeric
		modeCount++
	}
	if *humanNumeric {
		globalMode = modeHumanNumeric
		modeCount++
	}
	if *monthSort {
		globalMode = modeMonth
		modeCount++
	}
	if *versionSort {
		globalMode = modeVersion
		modeCount++
	}
	if *randomSort {
		globalMode = modeRandom
		modeCount++
	}
	if modeCount > 1 {
		callCtx.Errf("sort: incompatible sort mode options\n")
		return builtins.Result{Code: 2}
	}

	if (*dictOrder || *ignoreNonPrint) && modeCount == 1 && globalMode != modeVersion {
		callCtx.Errf("sort: options are incompatible\n")
		return builtins.Result{Code: 2}
	}

	globalOpts := keyOpts{
		mode:           globalMode,
		reverse:        *reverse,
		ignoreCase:     *ignoreCase,
		dictOrder:      *dictOrder,
		ignoreNonPrint: *ignoreNonPrint,
		blanksStart:    *ignoreBlanks,
		blanksEnd:      *ignoreBlanks,
	}

	var keyDefs []keyDef
	for _, ks := range *keys {
		kd, errMsg := parseKeyDef(ks)
		if errMsg != "" {
			callCtx.Errf("sort: %s\n", errMsg)
			return builtins.Result{Code: 2}
		}
		keyDefs = append(keyDefs, kd)
	}

	var salt uint64
	if globalMode == modeRandom || hasRandomKey(keyDefs) {
		salt = uint64(time.Now().UnixNano())
	}

	cfg := &sortConfig{
		keys:       keyDefs,
		globalOpts: globalOpts,
		sep:        sep,
		hasSep:     hasSep,
		zeroTerm:   *zeroTerm,
		unique:     *unique,
		stable:     *stable,
		check:      checkFlag,
		checkQuiet: checkQuietFlag,
		salt:       salt,
	}

	_ = *merge

	files := fs.Args()
	if len(files) == 0 {
		files = []string{"-"}
	}

	if cfg.check || cfg.checkQuiet {
		return runCheck(ctx, callCtx, cfg, files)
	}

	lines, failed := readAllLines(ctx, callCtx, cfg, files)
	if failed {
		return builtins.Result{Code: 2}
	}

	stdsort.SliceStable(lines, func(i, j int) bool {
		return compareLines(cfg, lines[i], lines[j]) < 0
	})

	delim := "\n"
	if cfg.zeroTerm {
		delim = "\x00"
	}
	var prev string
	for idx, line := range lines {
		if ctx.Err() != nil {
			break
		}
		if cfg.unique && idx > 0 && equalByKeys(cfg, prev, line) {
			continue
		}
		callCtx.Out(line)
		callCtx.Out(delim)
		prev = line
	}

	return builtins.Result{}
	}
}

// ---------------------------------------------------------------------------
// Line reading
// ---------------------------------------------------------------------------

type openResult struct {
	reader io.Reader
	name   string
	closer func()
}

func openInput(ctx context.Context, callCtx *builtins.CallContext, file string) (openResult, error) {
	if file == "-" {
		name := "standard input"
		if callCtx.Stdin == nil {
			return openResult{name: name, closer: func() {}}, nil
		}
		return openResult{reader: callCtx.Stdin, name: name, closer: func() {}}, nil
	}
	f, err := callCtx.OpenFile(ctx, file, os.O_RDONLY, 0)
	if err != nil {
		return openResult{}, err
	}
	return openResult{reader: f, name: file, closer: func() { f.Close() }}, nil
}

func readAllLines(ctx context.Context, callCtx *builtins.CallContext, cfg *sortConfig, files []string) ([]string, bool) {
	var lines []string
	for _, file := range files {
		if ctx.Err() != nil {
			return nil, true
		}
		o, err := openInput(ctx, callCtx, file)
		if err != nil {
			callCtx.Errf("sort: %s: %s\n", file, callCtx.PortableErr(err))
			return nil, true
		}
		if o.reader == nil {
			continue
		}
		sc := newLineScanner(o.reader, cfg.zeroTerm)
		for sc.Scan() {
			if ctx.Err() != nil {
				o.closer()
				return nil, true
			}
			if len(lines) >= MaxLines {
				callCtx.Errf("sort: input exceeds maximum of %d lines\n", MaxLines)
				o.closer()
				return nil, true
			}
			lines = append(lines, sc.Text())
		}
		if err := sc.Err(); err != nil {
			callCtx.Errf("sort: %s: %s\n", o.name, callCtx.PortableErr(err))
			o.closer()
			return nil, true
		}
		o.closer()
	}
	return lines, false
}

// ---------------------------------------------------------------------------
// Check mode
// ---------------------------------------------------------------------------

func runCheck(ctx context.Context, callCtx *builtins.CallContext, cfg *sortConfig, files []string) builtins.Result {
	var prev string
	hasPrev := false
	lineNum := 0

	for _, file := range files {
		if ctx.Err() != nil {
			return builtins.Result{Code: 2}
		}
		o, err := openInput(ctx, callCtx, file)
		if err != nil {
			callCtx.Errf("sort: %s: %s\n", file, callCtx.PortableErr(err))
			return builtins.Result{Code: 2}
		}
		if o.reader == nil {
			continue
		}
		sc := newLineScanner(o.reader, cfg.zeroTerm)
		for sc.Scan() {
			if ctx.Err() != nil {
				o.closer()
				return builtins.Result{Code: 2}
			}
			line := sc.Text()
			lineNum++
			if hasPrev {
				cmp := compareLines(cfg, prev, line)
				if cmp > 0 || (cfg.unique && equalByKeys(cfg, prev, line)) {
					if !cfg.checkQuiet {
						callCtx.Errf("sort: %s:%d: disorder: %s\n", o.name, lineNum, line)
					}
					o.closer()
					return builtins.Result{Code: 1}
				}
			}
			prev = line
			hasPrev = true
		}
		if err := sc.Err(); err != nil {
			callCtx.Errf("sort: %s: %s\n", o.name, callCtx.PortableErr(err))
			o.closer()
			return builtins.Result{Code: 2}
		}
		o.closer()
	}
	return builtins.Result{}
}

// ---------------------------------------------------------------------------
// Comparison
// ---------------------------------------------------------------------------

func compareLines(cfg *sortConfig, a, b string) int {
	if len(cfg.keys) > 0 {
		for i := range cfg.keys {
			key := &cfg.keys[i]
			opts := &key.opts
			if !key.hasOpts {
				opts = &cfg.globalOpts
			}
			ka := extractKey(a, key, cfg)
			kb := extractKey(b, key, cfg)
			cmp := compareWithOpts(opts, ka, kb, cfg.salt)
			if cmp != 0 {
				return cmp
			}
		}
	} else {
		cmp := compareWithOpts(&cfg.globalOpts, a, b, cfg.salt)
		if cmp != 0 {
			return cmp
		}
	}
	if !cfg.stable {
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
	}
	return 0
}

func equalByKeys(cfg *sortConfig, a, b string) bool {
	if len(cfg.keys) > 0 {
		for i := range cfg.keys {
			key := &cfg.keys[i]
			opts := &key.opts
			if !key.hasOpts {
				opts = &cfg.globalOpts
			}
			ka := extractKey(a, key, cfg)
			kb := extractKey(b, key, cfg)
			if compareWithOpts(opts, ka, kb, cfg.salt) != 0 {
				return false
			}
		}
		return true
	}
	return compareWithOpts(&cfg.globalOpts, a, b, cfg.salt) == 0
}

func compareWithOpts(opts *keyOpts, a, b string, salt uint64) int {
	var cmp int
	switch opts.mode {
	case modeNumeric:
		cmp = cmpFloat(parseNumericValue(a), parseNumericValue(b))
	case modeGeneralNumeric:
		cmp = cmpFloatGeneral(parseGeneralNumeric(a), parseGeneralNumeric(b))
	case modeHumanNumeric:
		cmp = cmpFloat(parseHumanValue(a), parseHumanValue(b))
	case modeMonth:
		cmp = cmpInt(monthIndex(a), monthIndex(b))
	case modeVersion:
		cmp = versionCompare(a, b)
	case modeRandom:
		cmp = cmpUint64(fnvHash(a, salt), fnvHash(b, salt))
	default:
		ta := transformKey(opts, a)
		tb := transformKey(opts, b)
		cmp = strCompare(ta, tb)
	}
	if opts.reverse {
		cmp = -cmp
	}
	return cmp
}

func transformKey(opts *keyOpts, s string) string {
	if opts.blanksStart {
		s = trimLeadingBlanks(s)
	}
	if !opts.ignoreCase && !opts.dictOrder && !opts.ignoreNonPrint {
		return s
	}
	buf := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if opts.ignoreNonPrint && !isPrint(c) {
			continue
		}
		if opts.dictOrder && !isAlnum(c) && !isBlank(c) {
			continue
		}
		if opts.ignoreCase && c >= 'a' && c <= 'z' {
			c -= 32
		}
		buf = append(buf, c)
	}
	return string(buf)
}

// ---------------------------------------------------------------------------
// Numeric parsers
// ---------------------------------------------------------------------------

func parseNumericValue(s string) float64 {
	s = trimLeadingBlanks(s)
	if len(s) == 0 {
		return 0
	}
	neg := false
	i := 0
	if s[i] == '-' {
		neg = true
		i++
	} else if s[i] == '+' {
		i++
	}
	if i >= len(s) || (!isDigit(s[i]) && s[i] != '.') {
		return 0
	}
	val := 0.0
	hasDigits := false
	for i < len(s) && isDigit(s[i]) {
		val = val*10 + float64(s[i]-'0')
		hasDigits = true
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		factor := 0.1
		for i < len(s) && isDigit(s[i]) {
			val += float64(s[i]-'0') * factor
			factor *= 0.1
			hasDigits = true
			i++
		}
	}
	if !hasDigits {
		return 0
	}
	if neg {
		return -val
	}
	return val
}

func parseGeneralNumeric(s string) float64 {
	s = trimLeadingBlanks(s)
	if len(s) == 0 {
		return defaultNaN
	}
	end := findFloatEnd(s)
	if end == 0 {
		return defaultNaN
	}
	f, err := strconv.ParseFloat(s[:end], 64)
	if err != nil {
		return defaultNaN
	}
	return f
}

func findFloatEnd(s string) int {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	if i < len(s) {
		rem := s[i:]
		if len(rem) >= 8 && eqFold8(rem[:8], "infinity") {
			return i + 8
		}
		if len(rem) >= 3 {
			up := [3]byte{toUpper(rem[0]), toUpper(rem[1]), toUpper(rem[2])}
			if up == [3]byte{'I', 'N', 'F'} {
				return i + 3
			}
			if up == [3]byte{'N', 'A', 'N'} {
				return i + 3
			}
		}
	}
	hasDigits := false
	for i < len(s) && isDigit(s[i]) {
		i++
		hasDigits = true
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && isDigit(s[i]) {
			i++
			hasDigits = true
		}
	}
	if !hasDigits {
		return 0
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && isDigit(s[j]) {
			j++
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			i = j
		}
	}
	return i
}

func parseHumanValue(s string) float64 {
	s = trimLeadingBlanks(s)
	if len(s) == 0 {
		return 0
	}
	neg := false
	i := 0
	if s[i] == '-' {
		neg = true
		i++
	} else if s[i] == '+' {
		i++
	}
	val := 0.0
	hasDigits := false
	for i < len(s) && isDigit(s[i]) {
		val = val*10 + float64(s[i]-'0')
		hasDigits = true
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		factor := 0.1
		for i < len(s) && isDigit(s[i]) {
			val += float64(s[i]-'0') * factor
			factor *= 0.1
			hasDigits = true
			i++
		}
	}
	if !hasDigits {
		return 0
	}
	if i < len(s) {
		switch s[i] {
		case 'K':
			val *= siK
		case 'M':
			val *= siM
		case 'G':
			val *= siG
		case 'T':
			val *= siT
		case 'P':
			val *= siP
		case 'E':
			val *= siE
		case 'Z':
			val *= siZ
		case 'Y':
			val *= siY
		}
	}
	if neg {
		return -val
	}
	return val
}

func monthIndex(s string) int {
	s = trimLeadingBlanks(s)
	if len(s) < 3 {
		return 0
	}
	a, b, c := toUpper(s[0]), toUpper(s[1]), toUpper(s[2])
	switch {
	case a == 'J' && b == 'A' && c == 'N':
		return 1
	case a == 'F' && b == 'E' && c == 'B':
		return 2
	case a == 'M' && b == 'A' && c == 'R':
		return 3
	case a == 'A' && b == 'P' && c == 'R':
		return 4
	case a == 'M' && b == 'A' && c == 'Y':
		return 5
	case a == 'J' && b == 'U' && c == 'N':
		return 6
	case a == 'J' && b == 'U' && c == 'L':
		return 7
	case a == 'A' && b == 'U' && c == 'G':
		return 8
	case a == 'S' && b == 'E' && c == 'P':
		return 9
	case a == 'O' && b == 'C' && c == 'T':
		return 10
	case a == 'N' && b == 'O' && c == 'V':
		return 11
	case a == 'D' && b == 'E' && c == 'C':
		return 12
	}
	return 0
}

func versionCompare(a, b string) int {
	ia, ib := 0, 0
	for ia < len(a) || ib < len(b) {
		if ia >= len(a) {
			return -1
		}
		if ib >= len(b) {
			return 1
		}
		ca, cb := a[ia], b[ib]
		if isDigit(ca) && isDigit(cb) {
			na, endA := parseVersionInt(a, ia)
			nb, endB := parseVersionInt(b, ib)
			if na != nb {
				if na < nb {
					return -1
				}
				return 1
			}
			ia = endA
			ib = endB
			continue
		}
		if ca != cb {
			if ca < cb {
				return -1
			}
			return 1
		}
		ia++
		ib++
	}
	return 0
}

func parseVersionInt(s string, start int) (int64, int) {
	val := int64(0)
	i := start
	for i < len(s) && isDigit(s[i]) {
		nv := val*10 + int64(s[i]-'0')
		if nv < val {
			val = 1<<63 - 1
			for i < len(s) && isDigit(s[i]) {
				i++
			}
			return val, i
		}
		val = nv
		i++
	}
	return val, i
}

// ---------------------------------------------------------------------------
// Key parsing
// ---------------------------------------------------------------------------

func parseKeyDef(s string) (keyDef, string) {
	commaIdx := indexByte(s, ',')
	startStr := s
	endStr := ""
	if commaIdx >= 0 {
		startStr = s[:commaIdx]
		endStr = s[commaIdx+1:]
	}

	sf, sc, sOpts, errMsg := parsePosOpts(startStr)
	if errMsg != "" {
		return keyDef{}, errMsg
	}
	if sf < 1 {
		return keyDef{}, "invalid number at field start: field number is zero"
	}

	var key keyDef
	key.startField = sf
	key.startChar = sc

	for i := 0; i < len(sOpts); i++ {
		c := sOpts[i]
		if c == 'b' {
			key.opts.blanksStart = true
			key.hasOpts = true
		} else {
			if e := applyKeyOpt(&key.opts, c, s); e != "" {
				return keyDef{}, e
			}
			key.hasOpts = true
		}
	}

	if endStr != "" {
		ef, ec, eOpts, errMsg := parsePosOpts(endStr)
		if errMsg != "" {
			return keyDef{}, errMsg
		}
		key.endField = ef
		key.endChar = ec

		for i := 0; i < len(eOpts); i++ {
			c := eOpts[i]
			if c == 'b' {
				key.opts.blanksEnd = true
				key.hasOpts = true
			} else {
				if e := applyKeyOpt(&key.opts, c, s); e != "" {
					return keyDef{}, e
				}
				key.hasOpts = true
			}
		}
	}

	return key, ""
}

func parsePosOpts(s string) (field, char int, opts string, errMsg string) {
	if len(s) == 0 {
		return 0, 0, "", "empty key position"
	}
	i := 0
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	if i == 0 {
		return 0, 0, "", "invalid key position: " + s
	}
	n, err := strconv.ParseInt(s[:i], 10, 64)
	if err != nil || n < 0 || n > 1<<31-1 {
		return 0, 0, "", "invalid field number: " + s[:i]
	}
	field = int(n)
	if i < len(s) && s[i] == '.' {
		i++
		j := i
		for j < len(s) && isDigit(s[j]) {
			j++
		}
		if j == i {
			return 0, 0, "", "invalid character offset in key: " + s
		}
		cn, err := strconv.ParseInt(s[i:j], 10, 64)
		if err != nil || cn < 0 || cn > 1<<31-1 {
			return 0, 0, "", "invalid character offset: " + s[i:j]
		}
		if cn == 0 {
			return 0, 0, "", "character offset is zero: " + s
		}
		char = int(cn)
		i = j
	}
	opts = s[i:]
	return field, char, opts, ""
}

func applyKeyOpt(opts *keyOpts, c byte, keyStr string) string {
	switch c {
	case 'n':
		opts.mode = modeNumeric
	case 'g':
		opts.mode = modeGeneralNumeric
	case 'h':
		opts.mode = modeHumanNumeric
	case 'M':
		opts.mode = modeMonth
	case 'V':
		opts.mode = modeVersion
	case 'R':
		opts.mode = modeRandom
	case 'r':
		opts.reverse = true
	case 'f':
		opts.ignoreCase = true
	case 'd':
		opts.dictOrder = true
	case 'i':
		opts.ignoreNonPrint = true
	default:
		return "invalid option '" + string(rune(c)) + "' in key: " + keyStr
	}
	return ""
}

// ---------------------------------------------------------------------------
// Field / key extraction
// ---------------------------------------------------------------------------

func extractKey(line string, key *keyDef, cfg *sortConfig) string {
	pos := computeFieldStarts(line, cfg.sep, cfg.hasSep)

	opts := &key.opts
	if !key.hasOpts {
		opts = &cfg.globalOpts
	}

	start := getFieldByteOffset(pos, key.startField, key.startChar, opts.blanksStart, line)

	var end int
	if key.endField == 0 {
		end = len(line)
	} else {
		end = getFieldEndByteOffset(pos, key.endField, key.endChar, opts.blanksEnd, line, cfg.hasSep)
	}

	if start >= len(line) {
		return ""
	}
	if end > len(line) {
		end = len(line)
	}
	if start >= end {
		return ""
	}
	return line[start:end]
}

func computeFieldStarts(line string, sep byte, hasSep bool) []int {
	var pos []int
	if hasSep {
		pos = append(pos, 0)
		for i := 0; i < len(line); i++ {
			if line[i] == sep {
				pos = append(pos, i+1)
			}
		}
	} else {
		i := 0
		for i <= len(line) {
			pos = append(pos, i)
			for i < len(line) && isBlank(line[i]) {
				i++
			}
			if i >= len(line) {
				break
			}
			for i < len(line) && !isBlank(line[i]) {
				i++
			}
		}
	}
	return pos
}

func getFieldByteOffset(pos []int, field, char int, skipBlanks bool, line string) int {
	if field < 1 || field > len(pos) {
		return len(line)
	}
	offset := pos[field-1]
	if skipBlanks {
		for offset < len(line) && isBlank(line[offset]) {
			offset++
		}
	}
	if char > 1 {
		offset += char - 1
	}
	return offset
}

func getFieldEndByteOffset(pos []int, field, char int, skipBlanks bool, line string, hasSep bool) int {
	if field < 1 || field > len(pos) {
		return len(line)
	}
	if char > 0 {
		offset := pos[field-1]
		if skipBlanks {
			for offset < len(line) && isBlank(line[offset]) {
				offset++
			}
		}
		offset += char
		if offset > len(line) {
			offset = len(line)
		}
		return offset
	}
	if field < len(pos) {
		end := pos[field]
		if hasSep && end > 0 {
			end--
		}
		return end
	}
	return len(line)
}

// ---------------------------------------------------------------------------
// Scanner
// ---------------------------------------------------------------------------

func newLineScanner(r io.Reader, zeroTerm bool) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	buf := make([]byte, 4096)
	sc.Buffer(buf, MaxLineBytes)
	delim := byte('\n')
	if zeroTerm {
		delim = 0
	}
	sc.Split(makeSplitFunc(delim))
	return sc
}

func makeSplitFunc(delim byte) bufio.SplitFunc {
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		for i := 0; i < len(data); i++ {
			if data[i] == delim {
				return i + 1, data[:i], nil
			}
		}
		if atEOF {
			return len(data), data, nil
		}
		return 0, nil, nil
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func isBlank(c byte) bool { return c == ' ' || c == '\t' }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isAlpha(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isAlnum(c byte) bool { return isAlpha(c) || isDigit(c) }
func isPrint(c byte) bool { return c >= 0x20 && c <= 0x7E }
func toUpper(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 32
	}
	return c
}

func trimLeadingBlanks(s string) string {
	i := 0
	for i < len(s) && isBlank(s[i]) {
		i++
	}
	return s[i:]
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func strCompare(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpFloat(a, b float64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpFloatGeneral(a, b float64) int {
	aNaN := a != a
	bNaN := b != b
	if aNaN && bNaN {
		return 0
	}
	if aNaN {
		return -1
	}
	if bNaN {
		return 1
	}
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpInt(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpUint64(a, b uint64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func fnvHash(s string, salt uint64) uint64 {
	h := salt ^ 14695981039346656037
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

func eqFold8(a, b string) bool {
	if len(a) != 8 || len(b) != 8 {
		return false
	}
	for i := 0; i < 8; i++ {
		if toUpper(a[i]) != toUpper(b[i]) {
			return false
		}
	}
	return true
}

func hasRandomKey(keys []keyDef) bool {
	for _, k := range keys {
		if k.opts.mode == modeRandom {
			return true
		}
	}
	return false
}
