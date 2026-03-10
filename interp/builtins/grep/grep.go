// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package grep implements the grep builtin command.
//
// grep — print lines that match patterns
//
// Usage: grep [OPTION]... PATTERN [FILE]...
//
//	grep [OPTION]... -e PATTERN [-e PATTERN]... [FILE]...
//
// Search for PATTERN in each FILE. When FILE is -, read standard input.
// With no FILE, read standard input.
//
// Accepted flags:
//
//	-E, --extended-regexp
//	    Interpret PATTERN as an extended regular expression (ERE).
//
//	-F, --fixed-strings
//	    Interpret PATTERN as a list of fixed strings (not regexps),
//	    separated by newlines, any of which is to be matched.
//
//	-G, --basic-regexp
//	    Interpret PATTERN as a basic regular expression (BRE). This is
//	    the default.
//
//	-i, --ignore-case
//	    Ignore case distinctions in patterns and input data.
//
//	-v, --invert-match
//	    Invert the sense of matching, to select non-matching lines.
//
//	-c, --count
//	    Suppress normal output; instead print a count of matching lines
//	    for each input file.
//
//	-l, --files-with-matches
//	    Suppress normal output; instead print the name of each input
//	    file from which output would normally have been printed.
//
//	-L, --files-without-match
//	    Suppress normal output; instead print the name of each input
//	    file from which no output would normally have been printed.
//
//	-n, --line-number
//	    Prefix each line of output with the 1-based line number within
//	    its input file.
//
//	-H, --with-filename
//	    Print the file name for each match. This is the default when
//	    there is more than one file to search.
//
//	-h, --no-filename
//	    Suppress the prefixing of file names on output.
//
//	-o, --only-matching
//	    Print only the matched (non-empty) parts of a matching line,
//	    with each such part on a separate output line.
//
//	-q, --quiet, --silent
//	    Quiet. Do not write anything to standard output. Exit with zero
//	    status if any match is found, even if an error was detected.
//
//	-s, --no-messages
//	    Suppress error messages about nonexistent or unreadable files.
//
//	-x, --line-regexp
//	    Select only those matches that exactly match the whole line.
//
//	-w, --word-regexp
//	    Select only those lines containing matches that form whole
//	    words.
//
//	-e PATTERN, --regexp=PATTERN
//	    Use PATTERN as the pattern. If this option is used multiple
//	    times, search for all patterns given.
//
//	-m NUM, --max-count=NUM
//	    Stop reading a file after NUM matching lines.
//
//	-A NUM, --after-context=NUM
//	    Print NUM lines of trailing context after matching lines.
//
//	-B NUM, --before-context=NUM
//	    Print NUM lines of leading context before matching lines.
//
//	-C NUM, --context=NUM
//	    Print NUM lines of output context. Equivalent to -A NUM -B NUM.
//
// Exit codes:
//
//	0  At least one match was found.
//	1  No matches were found.
//	2  An error occurred.
//
// Memory safety:
//
//	All processing is streaming: input is read line-by-line with a per-line
//	cap of MaxLineBytes (1 MiB). Lines exceeding this cap cause an error
//	rather than an unbounded allocation. All read loops check ctx.Err() at
//	each iteration to honour the shell's execution timeout and support
//	graceful cancellation. Go's regexp package uses the RE2 engine which
//	guarantees linear-time matching, preventing ReDoS attacks.
package grep

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/DataDog/rshell/interp/builtins"
)

// Cmd is the grep builtin command descriptor.
var Cmd = builtins.Command{Name: "grep", MakeFlags: registerFlags}

// MaxLineBytes is the per-line buffer cap for the line scanner. Lines
// longer than this are reported as an error instead of being buffered.
const MaxLineBytes = 1 << 20 // 1 MiB

// MaxContextLines caps -A/-B/-C to prevent excessive memory use.
const MaxContextLines = 10_000 // 10k lines

const (
	scanBufInit = 4096 // initial scanner buffer
)

// Exit code constants matching POSIX grep convention.
const (
	exitMatch   = 0
	exitNoMatch = 1
	exitError   = 2
)

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	// Pattern mode flags.
	extendedRegexp := fs.BoolP("extended-regexp", "E", false, "use extended regular expressions")
	fixedStrings := fs.BoolP("fixed-strings", "F", false, "interpret pattern as fixed strings")
	basicRegexp := fs.BoolP("basic-regexp", "G", false, "use basic regular expressions (default)")

	// Matching flags.
	ignoreCase := fs.BoolP("ignore-case", "i", false, "ignore case distinctions")
	invertMatch := fs.BoolP("invert-match", "v", false, "select non-matching lines")
	wordRegexp := fs.BoolP("word-regexp", "w", false, "match only whole words")
	lineRegexp := fs.BoolP("line-regexp", "x", false, "match only whole lines")

	// Output flags.
	count := fs.BoolP("count", "c", false, "print only a count of matching lines per file")
	filesWithMatches := fs.BoolP("files-with-matches", "l", false, "print only names of files with matches")
	filesWithoutMatch := fs.BoolP("files-without-match", "L", false, "print only names of files without matches")
	lineNumber := fs.BoolP("line-number", "n", false, "prefix output with line numbers")
	withFilename := fs.BoolP("with-filename", "H", false, "always print filename prefix")
	noFilename := fs.BoolP("no-filename", "h", false, "suppress filename prefix")
	onlyMatching := fs.BoolP("only-matching", "o", false, "print only the matched parts")
	quiet := fs.BoolP("quiet", "q", false, "suppress all output")
	_ = fs.Bool("silent", false, "alias for --quiet")
	noMessages := fs.BoolP("no-messages", "s", false, "suppress error messages")
	maxCount := fs.IntP("max-count", "m", -1, "stop after NUM matches per file")

	// Context flags.
	afterContext := fs.IntP("after-context", "A", 0, "print NUM lines after each match")
	beforeContext := fs.IntP("before-context", "B", 0, "print NUM lines before each match")
	contextLines := fs.IntP("context", "C", -1, "print NUM lines of context around each match")

	// Pattern flags (multiple -e allowed).
	var patterns patternSlice
	fs.VarP(&patterns, "regexp", "e", "use PATTERN as the pattern")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		// --silent is an alias for --quiet.
		if fs.Changed("silent") {
			*quiet = true
		}

		// Determine context sizes: -C sets both if -A/-B not explicitly set.
		after := *afterContext
		before := *beforeContext
		if *contextLines >= 0 {
			if !fs.Changed("after-context") {
				after = *contextLines
			}
			if !fs.Changed("before-context") {
				before = *contextLines
			}
		}
		// Clamp context values.
		if after < 0 {
			after = 0
		}
		if before < 0 {
			before = 0
		}
		if after > MaxContextLines {
			after = MaxContextLines
		}
		if before > MaxContextLines {
			before = MaxContextLines
		}

		// Collect patterns: from -e flags and/or first positional argument.
		allPatterns := []string(patterns)
		if len(allPatterns) == 0 {
			if len(args) == 0 {
				callCtx.Errf("grep: no pattern specified\n")
				return builtins.Result{Code: exitError}
			}
			allPatterns = append(allPatterns, args[0])
			args = args[1:]
		}

		// Determine regex mode.
		mode := modeBRE
		if *extendedRegexp {
			mode = modeERE
		}
		if *fixedStrings {
			mode = modeFixed
		}
		// -G is the default, but if specified explicitly after -E/-F it resets.
		if *basicRegexp && !*extendedRegexp && !*fixedStrings {
			mode = modeBRE
		}

		// Compile pattern(s).
		re, err := compilePatterns(allPatterns, mode, *ignoreCase, *wordRegexp, *lineRegexp)
		if err != nil {
			callCtx.Errf("grep: %s\n", err.Error())
			return builtins.Result{Code: exitError}
		}

		files := args
		if len(files) == 0 {
			files = []string{"-"}
		}

		// Determine filename printing behavior.
		showFilename := len(files) > 1 || *withFilename
		if *noFilename {
			showFilename = false
		}

		contextFlagUsed := fs.Changed("after-context") || fs.Changed("before-context") || fs.Changed("context")

		opts := &grepOpts{
			re:                re,
			invertMatch:       *invertMatch,
			count:             *count,
			filesWithMatches:  *filesWithMatches,
			filesWithoutMatch: *filesWithoutMatch,
			lineNumber:        *lineNumber,
			showFilename:      showFilename,
			onlyMatching:      *onlyMatching,
			quiet:             *quiet,
			noMessages:        *noMessages,
			maxCount:          *maxCount,
			afterContext:      after,
			beforeContext:     before,
			contextRequested:  contextFlagUsed,
		}

		anyMatch := false
		anyError := false

		for _, file := range files {
			if ctx.Err() != nil {
				break
			}
			matched, err := grepFile(ctx, callCtx, file, opts)
			if err != nil {
				if !opts.noMessages {
					name := file
					if file == "-" {
						name = "(standard input)"
					}
					callCtx.Errf("grep: %s: %s\n", name, callCtx.PortableErr(err))
				}
				anyError = true
				continue
			}
			if matched {
				anyMatch = true
				if opts.quiet {
					return builtins.Result{Code: exitMatch}
				}
			}
		}

		if anyError {
			return builtins.Result{Code: exitError}
		}
		if anyMatch {
			return builtins.Result{Code: exitMatch}
		}
		return builtins.Result{Code: exitNoMatch}
	}
}

type regexMode int

const (
	modeBRE regexMode = iota
	modeERE
	modeFixed
)

type grepOpts struct {
	re                *regexp.Regexp
	invertMatch       bool
	count             bool
	filesWithMatches  bool
	filesWithoutMatch bool
	lineNumber        bool
	showFilename      bool
	onlyMatching      bool
	quiet             bool
	noMessages        bool
	maxCount          int
	afterContext      int
	beforeContext     int
	contextRequested  bool // true when any -A/-B/-C flag was used (even with 0)
}

// patternSlice collects multiple -e PATTERN values.
type patternSlice []string

func (p *patternSlice) String() string { return strings.Join(*p, "\n") }
func (p *patternSlice) Set(val string) error {
	*p = append(*p, val)
	return nil
}
func (p *patternSlice) Type() string { return "string" }

// compilePatterns builds a single regexp from one or more patterns.
func compilePatterns(patterns []string, mode regexMode, ignoreCase, wordRegexp, lineRegexp bool) (*regexp.Regexp, error) {
	var parts []string
	for _, p := range patterns {
		converted, err := convertPattern(p, mode)
		if err != nil {
			return nil, err
		}
		parts = append(parts, converted)
	}

	combined := strings.Join(parts, "|")

	if wordRegexp && !lineRegexp {
		combined = `(?:\b)(?:` + combined + `)(?:\b)`
	}
	if lineRegexp {
		combined = `^(?:` + combined + `)$`
	}

	if ignoreCase {
		combined = "(?i)" + combined
	}

	re, err := regexp.Compile(combined)
	if err != nil {
		return nil, errors.New("invalid regular expression: " + err.Error())
	}
	return re, nil
}

// convertPattern translates a pattern to Go RE2 syntax based on the mode.
func convertPattern(pattern string, mode regexMode) (string, error) {
	switch mode {
	case modeFixed:
		return regexp.QuoteMeta(pattern), nil
	case modeERE:
		// Go's regexp is already ERE-compatible. Just validate.
		if _, err := regexp.Compile(pattern); err != nil {
			return "", errors.New("invalid regular expression: " + err.Error())
		}
		return pattern, nil
	case modeBRE:
		return breToERE(pattern), nil
	default:
		return pattern, nil
	}
}

// breToERE converts a POSIX Basic Regular Expression to an Extended Regular
// Expression compatible with Go's RE2 engine.
//
// In BRE:
//   - (, ), {, }, +, ? are literal unless backslash-escaped
//   - \(, \), \{, \}, \+, \? are metacharacters
//
// In ERE (and Go regex):
//   - (, ), {, }, +, ? are metacharacters
//   - \(, \), \{, \}, \+, \? are literal
//
// So the conversion swaps the escaping for these characters.
func breToERE(bre string) string {
	var out strings.Builder
	out.Grow(len(bre))
	i := 0
	for i < len(bre) {
		if bre[i] == '\\' && i+1 < len(bre) {
			next := bre[i+1]
			switch next {
			case '(', ')', '{', '}', '+', '?', '|':
				// BRE \X → ERE X (metacharacter)
				out.WriteByte(next)
				i += 2
			default:
				// Pass through other escapes
				out.WriteByte('\\')
				out.WriteByte(next)
				i += 2
			}
		} else {
			ch := bre[i]
			switch ch {
			case '(', ')', '{', '}', '+', '?', '|':
				// BRE literal X → ERE \X (escaped literal)
				out.WriteByte('\\')
				out.WriteByte(ch)
			default:
				out.WriteByte(ch)
			}
			i++
		}
	}
	return out.String()
}

func openReader(ctx context.Context, callCtx *builtins.CallContext, file string) (io.ReadCloser, error) {
	if file == "-" {
		if callCtx.Stdin == nil {
			return nil, nil
		}
		return io.NopCloser(callCtx.Stdin), nil
	}
	return callCtx.OpenFile(ctx, file, os.O_RDONLY, 0)
}

// grepFile searches a single file. Returns (matched, error).
func grepFile(ctx context.Context, callCtx *builtins.CallContext, file string, opts *grepOpts) (bool, error) {
	rc, err := openReader(ctx, callCtx, file)
	if err != nil {
		return false, err
	}
	if rc == nil {
		return false, nil
	}
	defer rc.Close()

	displayName := file
	if file == "-" {
		displayName = "(standard input)"
	}

	sc := bufio.NewScanner(rc)
	buf := make([]byte, scanBufInit)
	sc.Buffer(buf, MaxLineBytes)

	var matchCount int
	lineNum := 0

	// Context tracking. contextRequested is true when any -A/-B/-C flag was
	// used, even with value 0. This controls the "--" group separator.
	contextRequested := opts.afterContext > 0 || opts.beforeContext > 0 || opts.contextRequested
	var beforeBuf []contextLine // ring buffer for before-context
	afterRemaining := 0         // lines of after-context still to print
	lastPrintedLine := 0        // last line number we printed (for separator)
	printedSeparator := false   // have we ever printed a match group?

	for sc.Scan() {
		if ctx.Err() != nil {
			return matchCount > 0, ctx.Err()
		}
		lineNum++
		line := sc.Text()

		matched := opts.re.MatchString(line)
		if opts.invertMatch {
			matched = !matched
		}

		if matched {
			// Check max-count limit before incrementing/printing.
			if opts.maxCount >= 0 && matchCount >= opts.maxCount {
				break
			}

			matchCount++

			if opts.quiet {
				return true, nil
			}

			if opts.count || opts.filesWithMatches || opts.filesWithoutMatch {
				continue
			}

			// Print group separator if needed.
			if contextRequested && printedSeparator && lastPrintedLine > 0 && lineNum > lastPrintedLine+1 {
				callCtx.Out("--\n")
			}

			// Print before-context lines.
			if opts.beforeContext > 0 {
				for _, cl := range beforeBuf {
					if cl.num <= lastPrintedLine {
						continue
					}
					printContextLine(callCtx, displayName, cl.num, cl.text, opts, '-')
					lastPrintedLine = cl.num
				}
			}

			// Print the match.
			if opts.onlyMatching && !opts.invertMatch {
				matches := opts.re.FindAllString(line, -1)
				for _, m := range matches {
					printMatchLine(callCtx, displayName, lineNum, m, opts)
				}
			} else {
				printMatchLine(callCtx, displayName, lineNum, line, opts)
			}
			lastPrintedLine = lineNum
			printedSeparator = true
			afterRemaining = opts.afterContext

			// Clear before buffer since we've consumed it.
			beforeBuf = beforeBuf[:0]
		} else {
			// Non-matching line: might be after-context or before-context.
			if afterRemaining > 0 && !opts.quiet && !opts.count && !opts.filesWithMatches && !opts.filesWithoutMatch {
				printContextLine(callCtx, displayName, lineNum, line, opts, '-')
				lastPrintedLine = lineNum
				afterRemaining--
			}

			// Add to before-context ring buffer.
			if opts.beforeContext > 0 {
				if len(beforeBuf) >= opts.beforeContext {
					beforeBuf = beforeBuf[1:]
				}
				beforeBuf = append(beforeBuf, contextLine{num: lineNum, text: line})
			}
		}
	}

	if err := sc.Err(); err != nil {
		return matchCount > 0, err
	}

	// Handle -c, -l, -L output.
	if opts.count {
		if opts.showFilename {
			callCtx.Outf("%s:%s\n", displayName, strconv.Itoa(matchCount))
		} else {
			callCtx.Outf("%s\n", strconv.Itoa(matchCount))
		}
	}
	if opts.filesWithMatches && matchCount > 0 {
		callCtx.Outf("%s\n", displayName)
	}
	if opts.filesWithoutMatch && matchCount == 0 {
		callCtx.Outf("%s\n", displayName)
	}

	return matchCount > 0, nil
}

type contextLine struct {
	num  int
	text string
}

func printMatchLine(callCtx *builtins.CallContext, filename string, lineNum int, line string, opts *grepOpts) {
	var prefix strings.Builder
	if opts.showFilename {
		prefix.WriteString(filename)
		prefix.WriteByte(':')
	}
	if opts.lineNumber {
		prefix.WriteString(strconv.Itoa(lineNum))
		prefix.WriteByte(':')
	}
	callCtx.Outf("%s%s\n", prefix.String(), line)
}

func printContextLine(callCtx *builtins.CallContext, filename string, lineNum int, line string, opts *grepOpts, sep byte) {
	var prefix strings.Builder
	if opts.showFilename {
		prefix.WriteString(filename)
		prefix.WriteByte(sep)
	}
	if opts.lineNumber {
		prefix.WriteString(strconv.Itoa(lineNum))
		prefix.WriteByte(sep)
	}
	callCtx.Outf("%s%s\n", prefix.String(), line)
}
