// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package rg implements the rg (ripgrep-compatible) builtin command.
//
// rg — recursively search directories for a regex pattern
//
// Usage: rg [OPTION]... PATTERN [PATH]...
//
//	rg [OPTION]... -e PATTERN [-e PATTERN]... [PATH]...
//
// Search for PATTERN in each PATH. A PATH that is a directory is searched
// recursively. With no PATH, rg searches the current directory, unless
// standard input is piped or redirected, in which case standard input is
// searched instead. Use "-" to search standard input explicitly.
//
// This is a native re-implementation, not a wrapper around the ripgrep
// binary: rg never executes external processes. It supports the common
// subset of ripgrep's CLI surface implementable with Go's linear-time RE2
// regexp engine and the sandboxed filesystem capabilities on CallContext.
//
// Accepted flags:
//
//	-e PATTERN, --regexp=PATTERN
//	    A pattern to search for. May be given multiple times; when
//	    combined with a positional pattern all supplied patterns are
//	    searched (matching any is a match).
//
//	-F, --fixed-strings
//	    Treat all patterns as literal strings, not regular expressions.
//
//	-i, --ignore-case
//	    Case insensitive search.
//
//	-s, --case-sensitive
//	    Search case sensitively (default). Overrides -i/-S when given
//	    later on the command line.
//
//	-S, --smart-case
//	    Search case-insensitively unless the pattern contains an
//	    uppercase character, in which case the search is case-sensitive.
//
//	-v, --invert-match
//	    Invert matching: select non-matching lines.
//
//	-w, --word-regexp
//	    Only show matches surrounded by word boundaries.
//
//	-x, --line-regexp
//	    Only show matches that span the whole line.
//
//	-n, --line-number
//	    Show line numbers (1-based). This is rg's default when
//	    connected to a terminal; because rshell scripts never run
//	    attached to a terminal, line numbers are off unless requested.
//
//	-N, --no-line-number
//	    Suppress line numbers (this is already the default; provided
//	    for compatibility with scripts that pass it explicitly).
//
//	-H, --with-filename
//	    Always print the file path with each matching line.
//
//	-I, --no-filename
//	    Never print the file path with each matching line.
//
//	-o, --only-matching
//	    Print only the matched parts of a matching line, each on its
//	    own output line. A pattern that can match the empty string
//	    prints one empty output line per zero-width match position.
//
//	-c, --count
//	    Show a count of matching lines for each searched file, instead
//	    of the matching lines themselves.
//
//	-l, --files-with-matches
//	    Print only the paths of files containing at least one match.
//
//	--files-without-match
//	    Print only the paths of files containing no matches.
//
//	-q, --quiet
//	    Do not print anything to stdout. Exits 0 as soon as a match is
//	    found.
//
//	-m NUM, --max-count=NUM
//	    Stop searching a file after NUM matching lines.
//
//	-A NUM, --after-context=NUM
//	    Show NUM lines after each match.
//
//	-B NUM, --before-context=NUM
//	    Show NUM lines before each match.
//
//	-C NUM, --context=NUM
//	    Show NUM lines before and after each match.
//
//	-a, --text
//	    Search binary files as if they were text.
//
//	-g GLOB, --glob=GLOB
//	    Include or exclude files/directories matching GLOB. May be
//	    given multiple times; a glob prefixed with '!' excludes. Later
//	    globs take precedence over earlier ones for the same path.
//
//	--hidden
//	    Search hidden files and directories (dotfiles). Off by default.
//
//	--files
//	    Print each file that would be searched, without searching it.
//
//	--no-config
//	    No-op. rg never reads a configuration file, so this flag exists
//	    only for command-line compatibility with scripts that pass it.
//
//	-h, --help
//	    Print usage and exit.
//
// Exit codes:
//
//	0  At least one match was found (or --files listed at least one path).
//	1  No matches were found.
//	2  A usage or search error occurred (e.g. invalid pattern, file open
//	   error not silenced, invalid flag value).
//
// Deliberately unsupported (rejected by pflag as unknown flags):
//
//	Ignore-file processing (--no-ignore, -u/--unrestricted, .gitignore/
//	.ignore/.rgignore support): deferred; this version does not consult
//	any ignore files. Only hidden-file filtering and -g globs are applied.
//	--pre / --pre-glob: would execute an external preprocessing command.
//	--hostname-bin: would execute an external command to read a hostname.
//	-z/--search-zip, --json, -t/-T/--type*, -f/--file, -U/--multiline,
//	-P/--pcre2, --engine, -E/--encoding, --replace, --sort*, -L/--follow,
//	--one-file-system, --mmap, --threads, color/heading/hyperlink output
//	controls: out of scope for the initial implementation.
//
// Memory safety:
//
//	All file processing is streaming: input is read line-by-line with a
//	per-line cap of MaxLineBytes (1 MiB); lines exceeding this cap cause
//	an error rather than an unbounded allocation. Directory traversal is
//	iterative (not recursive in the Go call-stack sense) and bounded by
//	MaxTraversalDepth. All read/walk loops check ctx.Err() to honor the
//	shell's execution timeout. Go's regexp package uses the RE2 engine,
//	which guarantees linear-time matching and prevents ReDoS attacks.
//	Symbolic links are never followed during traversal (matching find's
//	default), which also prevents symlink-loop traversal.
package rg

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"regexp"
	"regexp/syntax"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the rg builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "rg",
	Description: "recursively search directories for a regex pattern",
	MakeFlags:   registerFlags,
}

// MaxLineBytes is the per-line buffer cap for the line scanner. Lines
// longer than this are reported as an error instead of being buffered.
const MaxLineBytes = 1 << 20 // 1 MiB

// MaxContextLines caps -A/-B/-C to prevent excessive memory use.
const MaxContextLines = 1_000 // 1k lines

// MaxContextBytes is the aggregate byte cap applied per match group to both
// the before-context sliding window and the after-context output stream.
const MaxContextBytes = 512 * 1024 // 512 KiB

// MaxTraversalDepth limits directory recursion depth to prevent resource
// exhaustion, matching the find and ls builtins.
const MaxTraversalDepth = 256

const scanBufInit = 4096 // initial scanner buffer

// Exit code constants matching ripgrep's convention.
const (
	exitMatch   = 0
	exitNoMatch = 1
	exitError   = 2
)

// containsNUL reports whether p contains a NUL byte, the heuristic used to
// detect binary files.
func containsNUL(p []byte) bool {
	return bytes.IndexByte(p, 0) >= 0
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	// Pattern flags.
	var patterns patternSlice
	fs.VarP(&patterns, "regexp", "e", "use PATTERN as the pattern")
	fixedStrings := fs.BoolP("fixed-strings", "F", false, "treat patterns as literal strings")

	// Case handling: last of -i/-s/-S wins.
	var caseSeq int
	ignoreCase := newOrderedBoolFlag(&caseSeq)
	caseSensitive := newOrderedBoolFlag(&caseSeq)
	smartCase := newOrderedBoolFlag(&caseSeq)
	fs.VarP(ignoreCase, "ignore-case", "i", "case insensitive search")
	fs.VarP(caseSensitive, "case-sensitive", "s", "search case sensitively (default)")
	fs.VarP(smartCase, "smart-case", "S", "case insensitive unless pattern has uppercase")
	fs.Lookup("ignore-case").NoOptDefVal = "true"
	fs.Lookup("case-sensitive").NoOptDefVal = "true"
	fs.Lookup("smart-case").NoOptDefVal = "true"

	// Matching flags.
	invertMatch := fs.BoolP("invert-match", "v", false, "select non-matching lines")

	// -w/-x resolve by command-line order (last one wins), matching
	// ripgrep: "rg -x -w PATTERN" performs word matching, while
	// "rg -w -x PATTERN" performs whole-line matching (verified directly).
	var wordLineSeq int
	wordRegexpFlag := newOrderedBoolFlag(&wordLineSeq)
	lineRegexpFlag := newOrderedBoolFlag(&wordLineSeq)
	fs.VarP(wordRegexpFlag, "word-regexp", "w", "match only whole words")
	fs.VarP(lineRegexpFlag, "line-regexp", "x", "match only whole lines")
	fs.Lookup("word-regexp").NoOptDefVal = "true"
	fs.Lookup("line-regexp").NoOptDefVal = "true"

	// Line-number flags: last of -n/-N wins.
	var lineNumSeq int
	lineNumberOn := newOrderedBoolFlag(&lineNumSeq)
	lineNumberOff := newOrderedBoolFlag(&lineNumSeq)
	fs.VarP(lineNumberOn, "line-number", "n", "show line numbers")
	fs.VarP(lineNumberOff, "no-line-number", "N", "suppress line numbers")
	fs.Lookup("line-number").NoOptDefVal = "true"
	fs.Lookup("no-line-number").NoOptDefVal = "true"

	// Filename flags: last of -H/-I wins.
	var filenameSeq int
	withFilename := newOrderedBoolFlag(&filenameSeq)
	noFilename := newOrderedBoolFlag(&filenameSeq)
	fs.VarP(withFilename, "with-filename", "H", "always print filename prefix")
	fs.VarP(noFilename, "no-filename", "I", "never print filename prefix")
	fs.Lookup("with-filename").NoOptDefVal = "true"
	fs.Lookup("no-filename").NoOptDefVal = "true"

	onlyMatching := fs.BoolP("only-matching", "o", false, "print only the matched parts")

	// Output-mode flags: last of -c/-l/--files-without-match wins.
	var outputSeq int
	count := newOrderedBoolFlag(&outputSeq)
	filesWithMatches := newOrderedBoolFlag(&outputSeq)
	filesWithoutMatch := newOrderedBoolFlag(&outputSeq)
	fs.VarP(count, "count", "c", "print only a count of matching lines per file")
	fs.VarP(filesWithMatches, "files-with-matches", "l", "print only names of files with matches")
	fs.Var(filesWithoutMatch, "files-without-match", "print only names of files without matches")
	fs.Lookup("count").NoOptDefVal = "true"
	fs.Lookup("files-with-matches").NoOptDefVal = "true"
	fs.Lookup("files-without-match").NoOptDefVal = "true"

	quiet := fs.BoolP("quiet", "q", false, "suppress all output")
	maxCount := fs.IntP("max-count", "m", -1, "stop after NUM matches per file")

	// Context flags.
	afterContext := fs.IntP("after-context", "A", 0, "print NUM lines after each match")
	beforeContext := fs.IntP("before-context", "B", 0, "print NUM lines before each match")
	contextLines := fs.IntP("context", "C", -1, "print NUM lines of context around each match")

	textMode := fs.BoolP("text", "a", false, "search binary files as if they were text")

	var globs globSlice
	fs.VarP(&globs, "glob", "g", "include or exclude files/dirs matching GLOB")

	hidden := fs.Bool("hidden", false, "search hidden files and directories")
	listFiles := fs.Bool("files", false, "print files that would be searched, without searching")
	_ = fs.Bool("no-config", false, "no-op; rg never reads a configuration file")

	help := fs.BoolP("help", "h", false, "print usage and exit")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		// Validate all explicitly set numeric flags BEFORE the --help
		// short-circuit below, matching the house convention (see head's
		// registerFlags) and verified directly against real ripgrep:
		// "rg --max-count=-1 --help" and "rg -A -1 --help" both exit 2 with
		// the negative-value error, never reaching help output. -m is a
		// semantically non-negative count too (the internal -1 sentinel
		// means "unset"/"unlimited", not "negative"); reject an explicit
		// negative value rather than treating it as unlimited.
		if fs.Changed("max-count") && *maxCount < 0 {
			callCtx.Errf("rg: invalid value for --max-count: number must be non-negative\n")
			return builtins.Result{Code: exitError}
		}

		// -A/-B/-C are semantically non-negative counts; ripgrep rejects an
		// explicit negative value rather than treating it as "no context",
		// so validate before applying the -C-sets-both-sides default.
		if fs.Changed("after-context") && *afterContext < 0 {
			callCtx.Errf("rg: invalid value for --after-context: number must be non-negative\n")
			return builtins.Result{Code: exitError}
		}
		if fs.Changed("before-context") && *beforeContext < 0 {
			callCtx.Errf("rg: invalid value for --before-context: number must be non-negative\n")
			return builtins.Result{Code: exitError}
		}
		if fs.Changed("context") && *contextLines < 0 {
			callCtx.Errf("rg: invalid value for --context: number must be non-negative\n")
			return builtins.Result{Code: exitError}
		}

		if *help {
			printHelp(callCtx, fs)
			return builtins.Result{}
		}

		// Resolve case-handling mode: last of -i/-s/-S wins; default is
		// case-sensitive.
		caseMode := caseSensitiveMode
		switch {
		case smartCase.pos > 0 && smartCase.pos > ignoreCase.pos && smartCase.pos > caseSensitive.pos:
			caseMode = smartCaseMode
		case ignoreCase.pos > 0 && ignoreCase.pos > caseSensitive.pos && ignoreCase.pos > smartCase.pos:
			caseMode = ignoreCaseMode
		}

		lineNumber := lineNumberOn.pos > lineNumberOff.pos

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
		if after > MaxContextLines {
			after = MaxContextLines
		}
		if before > MaxContextLines {
			before = MaxContextLines
		}

		resolvedFilesWithMatches := filesWithMatches.pos > 0 &&
			filesWithMatches.pos > filesWithoutMatch.pos && filesWithMatches.pos > count.pos
		resolvedFilesWithoutMatch := filesWithoutMatch.pos > 0 &&
			filesWithoutMatch.pos > filesWithMatches.pos && filesWithoutMatch.pos > count.pos
		resolvedCount := count.pos > 0 &&
			count.pos > filesWithMatches.pos && count.pos > filesWithoutMatch.pos

		// Validate every -g/--glob pattern up front. filepath.Match's error
		// is otherwise silently discarded by globMatch, which would leave a
		// malformed glob (e.g. an unclosed "[" character class) silently
		// matching nothing rather than reported as invalid input — and,
		// worse, an explicit file operand would still be searched with the
		// bad glob quietly ignored, since globs only gate directory
		// traversal.
		if err := validateGlobs(globs); err != nil {
			callCtx.Errf("rg: %s\n", err.Error())
			return builtins.Result{Code: exitError}
		}

		if *listFiles {
			return runListFiles(ctx, callCtx, args, globs, *hidden, *quiet)
		}

		// Collect patterns: -e flags plus an optional leading positional
		// pattern.
		var rawPatterns []string
		rawPatterns = append(rawPatterns, []string(patterns)...)
		remaining := args
		if len(rawPatterns) == 0 {
			if len(remaining) == 0 {
				callCtx.Errf("rg: no pattern given\n")
				return builtins.Result{Code: exitError}
			}
			rawPatterns = append(rawPatterns, remaining[0])
			remaining = remaining[1:]
		}

		// Resolve -w/-x conflict: last given wins.
		wordRegexp := wordRegexpFlag.pos > 0 && wordRegexpFlag.pos > lineRegexpFlag.pos
		lineRegexp := lineRegexpFlag.pos > 0 && lineRegexpFlag.pos > wordRegexpFlag.pos

		re, err := compilePatterns(rawPatterns, *fixedStrings, caseMode, wordRegexp, lineRegexp)
		if err != nil {
			callCtx.Errf("rg: %s\n", err.Error())
			return builtins.Result{Code: exitError}
		}

		// Unlike GNU grep, ripgrep does not suppress -A/-B/-C context when
		// -o is also given (verified directly): -o only changes what is
		// printed for the matching line itself, not whether context lines
		// are printed around it.
		contextFlagUsed := fs.Changed("after-context") || fs.Changed("before-context") || fs.Changed("context")

		opts := &rgOpts{
			re:                re,
			invertMatch:       *invertMatch,
			wordRegexp:        wordRegexp && !lineRegexp,
			count:             resolvedCount,
			filesWithMatches:  resolvedFilesWithMatches,
			filesWithoutMatch: resolvedFilesWithoutMatch,
			lineNumber:        lineNumber,
			onlyMatching:      *onlyMatching,
			quiet:             *quiet,
			maxCount:          *maxCount,
			afterContext:      after,
			beforeContext:     before,
			contextRequested:  contextFlagUsed,
			textMode:          *textMode,
		}

		return runSearch(ctx, callCtx, remaining, globs, *hidden, withFilename.pos, noFilename.pos, opts)
	}
}

func printHelp(callCtx *builtins.CallContext, fs *builtins.FlagSet) {
	callCtx.Out("Usage: rg [OPTION]... PATTERN [PATH]...\n")
	callCtx.Out("Recursively search PATH (default: current directory) for lines matching PATTERN.\n")
	callCtx.Out("With no PATH and stdin piped or redirected, search standard input instead.\n\n")
	fs.SetOutput(callCtx.Stdout)
	fs.PrintDefaults()
}

// caseHandling selects how pattern case is interpreted.
type caseHandling int

const (
	caseSensitiveMode caseHandling = iota
	ignoreCaseMode
	smartCaseMode
)

type rgOpts struct {
	re                *regexp.Regexp
	invertMatch       bool
	wordRegexp        bool
	count             bool
	filesWithMatches  bool
	filesWithoutMatch bool
	lineNumber        bool
	showFilename      bool
	onlyMatching      bool
	quiet             bool
	maxCount          int
	afterContext      int
	beforeContext     int
	contextRequested  bool
	textMode          bool
}

// orderedBoolFlag records the relative order in which competing boolean
// flags were set, so "last one wins" semantics can be resolved after
// parsing completes.
type orderedBoolFlag struct {
	seq *int
	pos int
}

func newOrderedBoolFlag(seq *int) *orderedBoolFlag {
	return &orderedBoolFlag{seq: seq}
}

func (f *orderedBoolFlag) String() string {
	if f.pos > 0 {
		return "true"
	}
	return "false"
}

func (f *orderedBoolFlag) Set(s string) error {
	b, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	if !b {
		f.pos = 0
		return nil
	}
	*f.seq = *f.seq + 1
	f.pos = *f.seq
	return nil
}

func (f *orderedBoolFlag) Type() string { return "bool" }

func (f *orderedBoolFlag) IsBoolFlag() bool { return true }

// patternSlice collects multiple -e PATTERN values.
type patternSlice []string

func (p *patternSlice) String() string { return strings.Join(*p, "\n") }
func (p *patternSlice) Set(val string) error {
	*p = append(*p, val)
	return nil
}
func (p *patternSlice) Type() string { return "string" }

// globSlice collects multiple -g GLOB values, in order given.
type globSlice []string

func (g *globSlice) String() string { return strings.Join(*g, ",") }
func (g *globSlice) Set(val string) error {
	*g = append(*g, val)
	return nil
}
func (g *globSlice) Type() string { return "string" }

// openReader opens file for reading, or returns the shell's stdin when
// file is "-".
func openReader(ctx context.Context, callCtx *builtins.CallContext, file string) (io.ReadCloser, error) {
	if file == "-" {
		if callCtx.Stdin == nil {
			return nil, nil
		}
		return io.NopCloser(callCtx.Stdin), nil
	}
	// Every non-"-" operand reaching here has already been resolved to a
	// regular file by expandOperands/walkDir (via StatFile/DirEntry.Info).
	// Use OpenRegularFile rather than OpenFile: it performs a nonblocking,
	// identity-verified open (os.SameFile against the earlier stat),
	// closing the small window between that check and this open, and
	// rejects descriptor portals such as /dev/fd/N or /proc/self/fd/N
	// even if one were substituted for the checked path in that window.
	if callCtx.OpenRegularFile == nil {
		return nil, errors.New("regular-file capability not available")
	}
	return callCtx.OpenRegularFile(ctx, file)
}

// stdinHasData reports whether the shell's stdin appears to be something
// other than an interactive terminal (a pipe, redirect, or similar). This
// controls the no-PATH default: rg searches "." when stdin looks like a
// terminal (or is nil), and searches stdin when it looks like a pipe/file.
func stdinHasData(callCtx *builtins.CallContext) bool {
	if callCtx.Stdin == nil {
		return false
	}
	if f, ok := callCtx.Stdin.(*os.File); ok {
		info, err := f.Stat()
		if err != nil {
			return false
		}
		return info.Mode()&os.ModeCharDevice == 0
	}
	// A non-*os.File stdin (e.g. a pipe created by the interpreter for
	// redirects/heredocs/command substitution) is always non-interactive.
	return true
}

// runSearch resolves the operands to search (falling back to stdin or the
// current directory when none are given), applies traversal, and searches
// each resulting file.
func runSearch(
	ctx context.Context,
	callCtx *builtins.CallContext,
	paths []string,
	globs globSlice,
	hidden bool,
	withFilenamePos, noFilenamePos int,
	opts *rgOpts,
) builtins.Result {
	recursive := false
	implicitDot := false
	if len(paths) == 0 {
		if stdinHasData(callCtx) {
			paths = []string{"-"}
		} else {
			paths = []string{"."}
			recursive = true
			implicitDot = true
		}
	}

	files, sawDir, walkErr := expandOperands(ctx, callCtx, paths, globs, hidden, implicitDot)

	// sawDir (not "len(files) discovered by traversal > 0") is the correct
	// signal: a directory operand that yields no searchable files (an
	// empty directory, or one whose entire contents are filtered out by
	// -g) still means every result gets a filename prefix, matching
	// ripgrep's guarantee that a directory operand always enables path
	// prefixes regardless of how many files it happens to contribute
	// (verified directly: "rg x empty-dir file" still prints "file:x",
	// not bare "x").
	if len(files) > 1 || sawDir {
		recursive = true
	}

	showFilename := recursive
	if withFilenamePos > 0 || noFilenamePos > 0 {
		showFilename = withFilenamePos > noFilenamePos
	}
	opts.showFilename = showFilename

	anyMatch := false
	anyError := walkErr

	for _, fe := range files {
		if ctx.Err() != nil {
			anyError = true
			break
		}
		matched, err := searchFile(ctx, callCtx, fe.access, fe.display, opts, fe.discoveredByTraversal)
		if err != nil {
			callCtx.Errf("rg: %s: %s\n", fe.display, callCtx.PortableErr(err))
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

// runListFiles implements --files: print the files that would be searched
// without searching their contents.
func runListFiles(ctx context.Context, callCtx *builtins.CallContext, paths []string, globs globSlice, hidden bool, quiet bool) builtins.Result {
	implicitDot := false
	if len(paths) == 0 {
		paths = []string{"."}
		implicitDot = true
	}
	// --files never searches content, so the discovered-via-traversal set
	// expandOperands returns is irrelevant here and discarded.
	files, _, walkErr := expandOperands(ctx, callCtx, paths, globs, hidden, implicitDot)
	// -q suppresses all stdout, including --files' listing (verified
	// directly): only the exit status reports whether anything was found.
	if !quiet {
		for _, fe := range files {
			callCtx.Outf("%s\n", fe.display)
		}
	}
	if walkErr {
		return builtins.Result{Code: exitError}
	}
	if len(files) > 0 {
		return builtins.Result{Code: exitMatch}
	}
	return builtins.Result{Code: exitNoMatch}
}

// fileEntry pairs the cleaned path used for every sandboxed filesystem
// access (access) with the filename-bearing label reported in output
// (display). ripgrep's own filename-bearing output preserves the operand's
// original spelling verbatim rather than any cleaned or joined path
// (verified directly: "rg -H x ./f" prints "./f:x", and "rg -H x a/../f"
// prints "a/../f:x"); the two paths only ever differ for this reason —
// display is never used for filesystem access, and access is never shown
// to the user.
type fileEntry struct {
	access  string
	display string
	// discoveredByTraversal marks this OCCURRENCE as having been found by
	// recursively walking a directory operand, as opposed to being named
	// directly (an explicit file operand, or stdin). ripgrep applies
	// different binary-file semantics to the two cases (verified
	// directly): a discovered-by-traversal binary file is silently
	// skipped, while an explicitly named file or stdin operand still
	// reports its binary match. This is a per-OCCURRENCE property, not a
	// per-PATH one: since round 8 removed operand deduplication, the same
	// path can appear multiple times with different provenance in the
	// same command (verified directly: "rg needle dir/f dir" reports
	// dir/f's binary match exactly once — the explicit occurrence reports
	// it, the directory-discovered occurrence of the SAME path is still
	// silently skipped — regardless of which operand comes first).
	discoveredByTraversal bool
}

// expandOperands resolves a list of file/directory operands to a list of
// regular files to search (in operand order; not deduplicated — see the
// per-operand comments below), recursively expanding directories
// (excluding hidden entries and glob-excluded paths, subject to the given
// options). "-" (stdin) is passed through unchanged. Returns the file
// list, whether any operand was a directory (used to decide whether to
// show filenames, matching ripgrep's behavior of always labeling directory
// search results), and whether any traversal error occurred (already
// reported to stderr).
// implicitDot, when true, indicates paths was defaulted to ["."] because
// the caller supplied no path operand at all (as opposed to the user
// explicitly writing "." on the command line). ripgrep's own display
// output distinguishes the two (verified directly): with no operand,
// "rg needle" prints bare "top.txt:needle" (no "./" prefix, at any
// depth), while an explicit "rg needle ." prints "./top.txt:needle" —
// same search, different display root.
func expandOperands(ctx context.Context, callCtx *builtins.CallContext, paths []string, globs globSlice, hidden bool, implicitDot bool) ([]fileEntry, bool, bool) {
	var files []fileEntry
	failed := false
	// sawDir tracks whether any operand was a directory, independent of
	// whether that directory actually yielded any files (an empty
	// directory, or one whose entire contents are filtered out by -g,
	// still counts): ripgrep always shows the file path prefix once any
	// operand is a directory (verified directly: "rg x empty-dir file"
	// still prints "file:x", not bare "x"), so this must not be inferred
	// from whether any file was actually discovered, which would be false
	// in exactly this case.
	sawDir := false
	// fileBudget/pathByteBudget bound, respectively, the cumulative number
	// of files and the cumulative path-byte length collected across every
	// directory operand in this invocation (not just per directory, which
	// MaxDirEntriesPerLevel already bounds independently, and not just by
	// count, since a tree of paths near the platform path-length limit
	// could otherwise retain many times the memory a shorter-path tree of
	// the same file count would): every discovered path is retained in
	// `files`/`discovered` before any search starts. Passed to
	// walkDir as pointers so multiple directory operands in the same
	// command share one running budget rather than each getting a fresh
	// MaxTotalDiscoveredFiles/MaxTotalDiscoveredPathBytes allowance.
	fileBudget := MaxTotalDiscoveredFiles
	pathByteBudget := MaxTotalDiscoveredPathBytes

	for _, p := range paths {
		if ctx.Err() != nil {
			return files, sawDir, true
		}
		if p == "-" {
			// "<stdin>" matches ripgrep's own filename-bearing output for
			// stdin exactly (verified directly, including in the binary-file
			// notice), not the POSIX-style "(standard input)" label grep
			// uses.
			files = append(files, fileEntry{access: p, display: "<stdin>"})
			continue
		}
		if p == "" {
			// An empty operand is never a valid path (filepath.Clean("")
			// would otherwise normalize it to ".", silently searching the
			// current directory instead of reporting the bad argument).
			callCtx.Errf("rg: '': %s\n", callCtx.PortableErr(os.ErrNotExist))
			failed = true
			continue
		}
		clean := filepath.ToSlash(filepath.Clean(p))
		// Explicit operands follow symlinks (read operations follow symlinks
		// by design, per RULES.md); only directory traversal in walkDir
		// skips symlinks, to avoid following into unbounded or unintended
		// targets while listing a tree.
		info, err := callCtx.StatFile(ctx, clean)
		if err != nil {
			callCtx.Errf("rg: '%s': %s\n", builtins.SafeOperand(p), callCtx.PortableErr(err))
			failed = true
			continue
		}
		if info.IsDir() {
			sawDir = true
			if fileBudget <= 0 || pathByteBudget <= 0 {
				callCtx.Errf("rg: '%s': too many files discovered (exceeded traversal limits), directory not searched\n", builtins.SafeOperand(p))
				failed = true
				continue
			}
			displayRoot := p
			if implicitDot {
				// See implicitDot's doc comment: no "./" prefix at all when the
				// path was defaulted rather than typed by the user.
				displayRoot = ""
			}
			found, truncated, walkFailed := walkDir(ctx, callCtx, clean, displayRoot, globs, hidden, &fileBudget, &pathByteBudget)
			if walkFailed {
				failed = true
			}
			if truncated {
				callCtx.Errf("rg: warning: '%s': too many files discovered (exceeded traversal limits), some files were not searched\n", builtins.SafeOperand(p))
				failed = true
			}
			// Like explicit operands, a file reached by more than one
			// directory operand (e.g. two overlapping directory arguments)
			// is not deduplicated: ripgrep re-searches and re-reports it
			// once per directory operand that reaches it (verified
			// directly: "rg z a a/shared" prints the shared file's match
			// twice), mirroring "rg x f f" for explicit files. Each entry
			// found is already tagged discoveredByTraversal=true by walkDir.
			files = append(files, found...)
			continue
		}
		if !info.Mode().IsRegular() {
			callCtx.Errf("rg: '%s': not a regular file\n", builtins.SafeOperand(p))
			failed = true
			continue
		}
		// Every explicit file operand is appended, even if the same path was
		// already named (or already discovered via a directory operand):
		// ripgrep searches and reports each explicit operand's own
		// occurrence (verified directly: "rg x f f" prints two "f:x" lines,
		// and "rg needle dir/f dir"/"rg needle dir dir/f" both report
		// dir/f's binary match exactly once — from THIS explicit occurrence,
		// regardless of where this operand falls relative to the directory
		// operand that also reaches the same path). display is the operand
		// exactly as given (p), never the cleaned path, matching ripgrep's
		// own output for an explicit operand. discoveredByTraversal is
		// false (the zero value): this occurrence is explicit, regardless
		// of whether the same path is ALSO reached by a directory operand
		// elsewhere in the same command (that would be a separate fileEntry
		// with its own, independently-tagged, provenance).
		files = append(files, fileEntry{access: clean, display: p})
	}
	return files, sawDir, failed
}

// MaxDirEntriesPerLevel caps the number of entries walkDir will process
// from any single directory. CallContext.ReadDir/ReadDirLimited return
// every entry in one directory as an in-memory slice, so without a cap an
// adversarial or merely huge directory (millions of entries) could exhaust
// memory before any file is searched. This mirrors the cap the ls builtin
// applies via MaxDirEntries, sized larger here since rg's job is to search
// (not print) every entry, so real-world large directories (e.g. build
// output, node_modules) should not be truncated in the common case.
const MaxDirEntriesPerLevel = 1_000_000

// MaxTotalDiscoveredFiles bounds the cumulative number of files a single
// directory operand's traversal (and each subsequent directory operand's
// remaining share of the budget) may add to the file list before any
// search starts. MaxDirEntriesPerLevel bounds each individual directory
// independently, but a tree containing many directories that each stay
// under that per-directory cap can still contain an unbounded total file
// count, and every discovered path is retained in memory (in walkDir's
// own output slice, and again in expandOperands' files/discovered)
// before any file is opened. This bound is the same order of magnitude as
// MaxDirEntriesPerLevel and the codebase's other large aggregate caps
// (e.g. du's maxDedupEntries), chosen so ordinary large real-world trees
// (e.g. a big monorepo) are not truncated, while a pathological tree with
// an effectively unbounded total file count cannot exhaust memory.
const MaxTotalDiscoveredFiles = 1_000_000

// MaxTotalDiscoveredPathBytes bounds the cumulative byte length of every
// discovered path across a directory operand's traversal, independent of
// MaxTotalDiscoveredFiles. An entry-count cap alone assumes an average
// path length; a tree of paths each near the platform path-length limit
// (e.g. Linux's 4096-byte PATH_MAX) could otherwise retain several GiB of
// path bytes (across walkDir's own output and expandOperands'
// files/discovered) while staying under the file-count cap. 128 MiB
// comfortably covers real-world large trees at ordinary path lengths
// (1,000,000 files at ~128 bytes/path average) while bounding the
// adversarial long-path case tightly, matching the cumulative-byte-budget
// pattern the sort builtin already uses for its own MaxTotalBytes.
const MaxTotalDiscoveredPathBytes = 128 * 1024 * 1024

// walkDir recursively lists regular files under root, in sorted order,
// honoring the hidden and glob filters. Symbolic links are never followed.
// Each directory level is capped at MaxDirEntriesPerLevel entries, and the
// cumulative traversal is capped by the shared fileBudget/byteBudget pair
// (see expandOperands).
// walkDir traverses root (the cleaned path used for every actual
// filesystem access) and returns each discovered regular file's DISPLAY
// path, built from displayRoot (the operand exactly as the caller spelled
// it, unmodified) instead of root. ripgrep's own filename-bearing output
// preserves the operand's original spelling verbatim — verified directly:
// "rg -H x ./f" prints "./f:x" and "rg -H x a/../f" prints "a/../f:x",
// neither cleaned; a directory operand behaves the same way for every
// path discovered beneath it ("rg -H x ." on a file at "sub/f" prints
// "./sub/f:x", and "rg -H x sub/." prints "sub/./f:x") — the traversal
// itself must still use the cleaned path for sandboxed I/O, but the
// display path is rawDisplayJoin(displayRoot, ...)+relative-path, with NO
// further cleaning applied at any level.
func walkDir(ctx context.Context, callCtx *builtins.CallContext, root, displayRoot string, globs globSlice, hidden bool, fileBudget, byteBudget *int) ([]fileEntry, bool, bool) {
	var out []fileEntry
	failed := false
	truncated := false

	type frame struct {
		path        string
		displayPath string
		depth       int
	}
	stack := []frame{{path: root, displayPath: displayRoot, depth: 0}}

	budgetExhausted := func() bool {
		return *fileBudget <= 0 || *byteBudget <= 0
	}

	for len(stack) > 0 {
		if ctx.Err() != nil {
			return out, truncated, true
		}
		if budgetExhausted() {
			// Cumulative-across-this-invocation budget exhausted (by entry
			// count or by path bytes retained): stop discovering further
			// files rather than continuing to grow out/the caller's
			// files/discovered maps without bound. Each individual
			// directory is already independently bounded by
			// MaxDirEntriesPerLevel via readDirBounded; this additionally
			// bounds the total across every directory in the tree, and
			// across every directory operand in the same command (the
			// budgets are shared pointers from expandOperands).
			truncated = true
			break
		}
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		entries, dirTruncated, err := readDirBounded(ctx, callCtx, top.path)
		if err != nil {
			callCtx.Errf("rg: '%s': %s\n", builtins.SafeOperand(top.path), callCtx.PortableErr(err))
			failed = true
			continue
		}
		if dirTruncated {
			callCtx.Errf("rg: warning: directory '%s': too many entries (exceeded %d limit), some files were not searched\n", builtins.SafeOperand(top.path), MaxDirEntriesPerLevel)
			failed = true
		}

		// Sort children so results are deterministic; ReadDir/ReadDirLimited
		// entries are already sorted by name per CallContext's contract, but
		// re-sort defensively since callers must not depend on that here.
		var children []iofs.DirEntry
		children = append(children, entries...)
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })

		for _, entry := range children {
			if ctx.Err() != nil {
				return out, truncated, true
			}
			if budgetExhausted() {
				truncated = true
				break
			}
			name := entry.Name()
			childPath := joinRel(top.path, name)
			childDisplayPath := rawDisplayJoin(top.displayPath, name)

			if !hidden && isHiddenName(name) && !globIncludesHidden(globs, childPath) {
				continue
			}
			if !pathAllowed(globs, childPath, entry.IsDir()) {
				continue
			}

			info, err := entry.Info()
			if err != nil {
				callCtx.Errf("rg: '%s': %s\n", builtins.SafeOperand(childPath), callCtx.PortableErr(err))
				failed = true
				continue
			}

			if info.Mode()&os.ModeSymlink != 0 {
				// Never follow symlinks during traversal (default rg/find
				// behavior); skip both symlinked files and directories.
				continue
			}

			if info.IsDir() {
				if top.depth+1 > MaxTraversalDepth {
					callCtx.Errf("rg: '%s': max traversal depth exceeded\n", builtins.SafeOperand(childPath))
					failed = true
					continue
				}
				stack = append(stack, frame{path: childPath, displayPath: childDisplayPath, depth: top.depth + 1})
				continue
			}

			if info.Mode().IsRegular() {
				out = append(out, fileEntry{access: childPath, display: childDisplayPath, discoveredByTraversal: true})
				*fileBudget--
				*byteBudget -= len(childPath)
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].access < out[j].access })
	return out, truncated, failed
}

// readDirBounded dispatches to ReadDirLimited (capped at MaxDirEntriesPerLevel)
// when available, falling back to unbounded ReadDir otherwise. Matches the
// dispatch pattern used by the ls builtin's readDir helper.
func readDirBounded(ctx context.Context, callCtx *builtins.CallContext, dir string) (entries []iofs.DirEntry, truncated bool, err error) {
	if callCtx.ReadDirLimited != nil {
		return callCtx.ReadDirLimited(ctx, dir, 0, MaxDirEntriesPerLevel)
	}
	entries, err = callCtx.ReadDir(ctx, dir)
	return entries, false, err
}

// joinRel joins a directory path and a child name using '/' regardless of
// platform, preserving a leading "./" root exactly as callers passed it.
func joinRel(dir, name string) string {
	if dir == "." {
		return name
	}
	if strings.HasSuffix(dir, "/") {
		return dir + name
	}
	return dir + "/" + name
}

// rawDisplayJoin builds a filename-bearing DISPLAY path by concatenating
// dir (the caller's original directory-operand spelling, or an
// already-built display path one level up) with name, doing NO other
// normalization — unlike joinRel, this never special-cases dir == ".":
// ripgrep's own filename-bearing output preserves the operand's original
// spelling verbatim at every level (verified directly): "rg -H x ." on a
// file at "sub/f" prints "./sub/f:x" (the leading "./" is kept, unlike
// joinRel's clean "sub/f"), "rg -H x ./sub" also prints "./sub/f:x", and
// "rg -H x sub/." prints "sub/./f:x" (the redundant "/." is kept too). A
// dir already ending in '/' is not given a second one (verified directly:
// "rg -H x sub/" on the same file prints "sub/f:x", and "rg -H x sub//"
// prints "sub//f:x" — an existing trailing slash, however many, is never
// added to or removed).
func rawDisplayJoin(dir, name string) string {
	if dir == "" {
		// The implicitDot sentinel (see expandOperands): no path operand at
		// all was given, so there is no prefix to join onto, at any depth.
		return name
	}
	if strings.HasSuffix(dir, "/") {
		return dir + name
	}
	return dir + "/" + name
}

// isHiddenName reports whether a file/directory base name is a dotfile
// (starts with '.'), excluding "." and "..".
func isHiddenName(name string) bool {
	return strings.HasPrefix(name, ".") && name != "." && name != ".."
}

// globIncludesHidden reports whether any positive (non-negated) glob would
// match this path, in which case an explicit -g include overrides the
// default hidden-file skip — matching the observed rg behavior where a -g
// pattern matching a dotfile causes it to be searched even without
// --hidden.
func globIncludesHidden(globs globSlice, path string) bool {
	matchedPositive := false
	for _, g := range globs {
		neg := strings.HasPrefix(g, "!")
		pat := g
		if neg {
			pat = g[1:]
		}
		// A glob overrides the default hidden-entry skip exactly when it
		// matches this hidden entry's OWN path — a '/'-containing glob is
		// not disqualified merely for containing a '/'; it can reveal a
		// hidden entry that sits below a visible ancestor directory, as
		// long as the glob's match actually reaches this exact path.
		// Verified directly against real ripgrep across many shapes: with
		// a visible "sub/" containing a hidden "sub/.h" or a hidden
		// "sub/.hiddendir/", "-g 'sub/*'" reveals the hidden FILE
		// "sub/.h" (glob matches its exact path), and "-g 'sub/**'"
		// reveals the hidden DIRECTORY "sub/.hiddendir" itself ("**"
		// matches zero-or-more trailing components, so it matches
		// "sub/.hiddendir" exactly) — but "-g 'sub/.hiddendir/*'" and
		// "-g 'sub/.hiddendir/**'" do NOT reveal "sub/.hiddendir" itself
		// (both require at least the hidden directory to already be
		// visible before matching something under it), and a
		// hidden-at-the-top entry like ".cache" is never revealed by
		// ".cache/**" or ".cache/*" (same reason: those need ".cache"
		// itself to already be visible). Since hidden-directory traversal
		// is refused directory-by-directory as the walk descends, this
		// exact-path check at each level reproduces that: a glob can only
		// reveal a hidden entry it matches directly, never one nested
		// beneath another still-hidden ancestor.
		if globMatch(pat, path) {
			matchedPositive = !neg
		}
	}
	return matchedPositive
}

// pathAllowed applies -g/--glob include/exclude rules to path, matching
// ripgrep's observed semantics: a directory is always traversed unless a
// negated glob explicitly excludes it (so include globs targeting file
// extensions never prevent descending into a directory that might contain
// matching files); a file defaults to allowed when every configured glob is
// a negation (exclude-only mode), but defaults to denied as soon as at
// least one non-negated (include) glob is configured (allowlist mode) —
// only files matching an include glob are searched. Within either mode,
// globs are applied in order and the last matching glob for a given path
// wins, so a later include glob can re-admit a file excluded by an earlier
// one, and vice versa.
func pathAllowed(globs globSlice, path string, isDir bool) bool {
	if len(globs) == 0 {
		return true
	}
	if isDir {
		// A directory is allowed by default (traversal must never be
		// pruned just because an include glob targets file extensions),
		// but the last glob that matches it — include or exclude — wins,
		// matching ripgrep's documented "glob given later takes
		// precedence" rule: an earlier "!foo/**" exclusion can be
		// re-admitted by a later "foo/**" include (verified directly:
		// "rg -g '!foo/**' -g 'foo/**' x ." still searches foo/**).
		allowed := true
		for _, g := range globs {
			neg := strings.HasPrefix(g, "!")
			pat := g
			if neg {
				pat = g[1:]
			}
			if globMatch(pat, path) || globMatch(pat, path+"/") {
				allowed = !neg
			}
		}
		return allowed
	}

	hasInclude := false
	for _, g := range globs {
		if !strings.HasPrefix(g, "!") {
			hasInclude = true
			break
		}
	}
	allowed := !hasInclude
	for _, g := range globs {
		neg := strings.HasPrefix(g, "!")
		pat := g
		if neg {
			pat = g[1:]
		}
		if globMatch(pat, path) {
			allowed = !neg
		}
	}
	return allowed
}

// globMatch matches path against a gitignore-style glob pattern. '*'
// matches any run of characters except '/'; a pattern containing a literal
// '/' is matched against the full relative path, otherwise it is matched
// against the base name only, mirroring ripgrep's glob semantics for
// simple patterns (full gitignore semantics such as directory-only
// trailing slashes and anchored leading slashes are not implemented).
func globMatch(pat, path string) bool {
	if !strings.Contains(pat, "/") {
		base := path
		if idx := strings.LastIndex(path, "/"); idx >= 0 {
			base = path[idx+1:]
		}
		ok, _ := filepath.Match(pat, base)
		return ok
	}
	return globMatchSegments(strings.Split(pat, "/"), strings.Split(path, "/"))
}

// globMatchSegments matches a '/'-delimited glob against a '/'-delimited
// path, path-component by path-component, giving "**" its gitignore
// meaning: match zero or more whole path components (so "a/**/f.txt"
// matches both "a/f.txt" and "a/b/c/f.txt", and "a/**" matches every path
// under "a"). filepath.Match alone cannot express this, since its '*'
// never crosses a '/'; each non-"**" component is still matched with
// filepath.Match, so '*'/'?'/'[...]' keep their normal single-component
// semantics within a component.
//
// Uses dynamic programming, not naive recursive backtracking: a pattern
// with many "**" segments matched against a long, non-matching path can
// otherwise blow up combinatorially (each "**" branches into every
// possible number of consumed path components, and those branches
// multiply across segments), taking catastrophically long — the glob
// equivalent of a ReDoS attack via a crafted -g pattern and directory
// tree. Runs in O(len(patSegs)*len(pathSegs)) time, but — unlike a full
// two-dimensional table — only O(len(pathSegs)) space: cur[j]/next[j]
// record whether patSegs[i:] matches pathSegs[j:] for the pattern
// position currently being processed, and only the immediately-previous
// row (next, i.e. patSegs[i+1:]) is ever needed to compute the current
// one, so the two rows are swapped and reused rather than keeping every
// row alive. This matters because len(patSegs) is attacker-controlled (a
// shell script can supply a glob up to the script size limit, i.e.
// millions of '/'-separated segments) while len(pathSegs) is bounded by
// MaxTraversalDepth; a full O(np*na) table would let one long -g argument
// allocate hundreds of MiB to a few GiB before any match is attempted,
// whereas this is bounded by MaxTraversalDepth regardless of pattern
// length.
func globMatchSegments(patSegs, pathSegs []string) bool {
	np, na := len(patSegs), len(pathSegs)
	// A pattern ending in one or more "**" segments (e.g. "a/**" or
	// "a/**/**") only matches paths strictly INSIDE the directory named
	// by the preceding fixed segments — it never matches that prefix
	// path exactly by having every trailing "**" consume zero components
	// (verified directly against real ripgrep: "-g 'a/**'" does not match
	// a literal path "a", nor does "-g '.cache/**'" match ".cache"
	// itself — only strictly-nested paths like "a/x" or ".cache/a" match).
	// This is a property of the trailing run specifically: a "**" earlier
	// in the pattern, still followed by fixed segments (e.g. the middle
	// "**" in "a/**/b"), CAN consume zero components (verified directly:
	// "-g 'a/**/b'" matches "a/b"), and a leading "**" can too ("-g
	// '**/foo'" matches a top-level "foo"). So only the base case at the
	// very end of the DP needs adjusting: requiring pathSegs to have
	// strictly more components than the pattern's non-trailing-"**"
	// prefix, rather than allowing an exact-length match, exactly when
	// the pattern's suffix is a run of one or more "**" segments.
	fixedPrefixLen := np
	for fixedPrefixLen > 0 && patSegs[fixedPrefixLen-1] == "**" {
		fixedPrefixLen--
	}
	requireExtra := fixedPrefixLen < np // pattern ends in >=1 "**" segments
	// next[j] = true iff patSegs[i+1:] matches pathSegs[j:], for the i
	// currently being filled in (starts as the i==np base row).
	next := make([]bool, na+1)
	if requireExtra {
		// The trailing "**" run may only match starting at some j strictly
		// less than na (i.e. it must consume at least one component), so
		// pathSegs[na:] (the empty suffix) is not itself a valid match for
		// patSegs[fixedPrefixLen:] — next[na] stays false here. Every j<na
		// is still a valid start for the trailing "**" to match
		// pathSegs[j:na] (one or more remaining components).
		for j := 0; j < na; j++ {
			next[j] = true
		}
	} else {
		next[na] = true
	}
	cur := make([]bool, na+1)
	// The trailing run of "**" segments (patSegs[fixedPrefixLen:]) is
	// already fully accounted for in the initial next[] above; the loop
	// only needs to process the fixed prefix in front of it.
	for i := fixedPrefixLen - 1; i >= 0; i-- {
		if patSegs[i] == "**" {
			// "**" matches zero or more path components: cur[j] is true
			// iff the rest of the pattern matches starting from any
			// j' >= j, which is exactly "next[j] is true, OR (j<na and
			// cur[j+1] is true)" — i.e. "** matches zero more here" or
			// "** consumes one more component and we're still deciding".
			// This recurrence avoids re-scanning all of pathSegs[j:] for
			// every position.
			cur[na] = next[na]
			for j := na - 1; j >= 0; j-- {
				cur[j] = next[j] || cur[j+1]
			}
		} else {
			cur[na] = false
			for j := na - 1; j >= 0; j-- {
				ok, _ := filepath.Match(patSegs[i], pathSegs[j])
				cur[j] = ok && next[j+1]
			}
		}
		cur, next = next, cur
	}
	return next[0]
}

// searchFile searches a single file, opened via accessPath (the cleaned
// path used for every sandboxed filesystem call), but reporting results
// under displayName (the caller-supplied filename-bearing label, which
// preserves the original operand spelling verbatim rather than any
// cleaned/joined path — see walkDir and expandOperands). Returns (matched,
// error).
func searchFile(ctx context.Context, callCtx *builtins.CallContext, accessPath, displayName string, opts *rgOpts, discoveredByTraversal bool) (bool, error) {
	rc, err := openReader(ctx, callCtx, accessPath)
	if err != nil {
		return false, err
	}
	if rc == nil {
		return false, nil
	}
	defer rc.Close()

	// Binary detection: probe the first binaryProbeSize bytes before
	// scanning. ripgrep's own binary-detection buffer is exactly 64 KiB
	// (verified directly: a NUL at byte offset 65535 is caught — the
	// whole file is treated as binary with no output — while a NUL at
	// offset 65536 is not, and ripgrep instead prints whatever matched
	// before it plus a notice); match that size exactly so this
	// implementation's probe-vs-late-detection boundary lines up with
	// real ripgrep's, rather than using grep's smaller 32 KiB probe.
	const binaryProbeSize = 64 * 1024
	isBinary := false
	var reader io.Reader = rc
	if !opts.textMode {
		probeBuf := make([]byte, binaryProbeSize)
		n, _ := rc.Read(probeBuf) //nolint:errcheck — EOF is fine; err handled by scanner
		probeBuf = probeBuf[:n]
		if containsNUL(probeBuf) {
			isBinary = true
		}
		if n > 0 {
			reader = io.MultiReader(bytes.NewReader(probeBuf), rc)
		}
	}

	// ripgrep applies different binary-file semantics depending on how the
	// file was named (verified directly): a file discovered by recursively
	// walking a directory operand is silently skipped the moment it looks
	// binary — no match, no "binary file matches" notice, no count —
	// while an explicitly named file or stdin operand still searches it
	// (reporting a match via the "binary file matches" notice) exactly as
	// before. -a/--text disables binary detection entirely, so this only
	// applies when isBinary is actually true.
	if isBinary && discoveredByTraversal {
		return false, nil
	}

	// -m 0 means "don't search anything" (ripgrep's documented behavior):
	// short-circuit before entering the scan loop, since otherwise an
	// input that never matches (e.g. an infinite non-matching stream)
	// would be read to EOF for a result that is already fully determined.
	if opts.maxCount == 0 {
		// -m 0 means the file is intentionally never searched at all, not
		// "confirmed to have zero matches": ripgrep reports no output and
		// exit 1 uniformly across every mode, including
		// --files-without-match (verified directly) — an unsearched file
		// must not be reported as a positive --files-without-match result.
		return false, nil
	}

	sc := bufio.NewScanner(reader)
	buf := make([]byte, scanBufInit)
	// bufio.Scanner's ScanLines needs room in its internal buffer for the
	// line's content PLUS its trailing delimiter byte before it can
	// recognize and strip the delimiter and return the token; without the
	// +1, a line whose content is exactly MaxLineBytes long spuriously
	// fails with "token too long" even though it does not exceed the
	// documented cap (verified directly: an exact-1-MiB matching line
	// must succeed, per this package's own doc comment "lines exceeding
	// this cap cause an error" — exactly at the cap must not exceed it).
	sc.Buffer(buf, MaxLineBytes+1)

	var matchCount int
	lineNum := 0

	contextRequested := opts.afterContext > 0 || opts.beforeContext > 0 || opts.contextRequested
	var beforeBuf []contextLine
	beforeBufBytes := 0
	afterRemaining := 0
	afterGroupBytes := 0
	lastPrintedLine := 0
	printedSeparator := false

	suppressLines := opts.count || opts.filesWithMatches || opts.filesWithoutMatch

	// reportedCount is what -c actually prints. For every combination
	// except non-inverted -c -o, it is the same as matchCount (one per
	// selected line). But ripgrep's -c counts individual matched
	// substrings when combined with plain -o (verified directly: a line
	// "xx" counts as 2 for "rg -c -o x", not 1), since -o's whole purpose
	// is to enumerate each match on the line; -o -v has no matched
	// substring to enumerate (the line was selected because the pattern
	// did NOT match it), so it stays line-counted like every other mode.
	reportedCount := 0

	// reportable is what searchFile returns to the caller to decide overall
	// exit status (0 if any file is reportable, 1 otherwise). For every
	// mode except --files-without-match, "reportable" means "had a match".
	// --files-without-match inverts this: a file is reportable exactly when
	// it has *no* match (that's the file that gets printed), so a file
	// containing only matches must report false, not matchCount>0. This
	// also governs -q's exit status when combined with
	// --files-without-match, matching ripgrep's observed behavior.
	reportable := func() bool {
		if opts.filesWithoutMatch {
			return matchCount == 0
		}
		return matchCount > 0
	}

	for sc.Scan() {
		if ctx.Err() != nil {
			return reportable(), ctx.Err()
		}
		lineNum++
		lineBytes := sc.Bytes()

		if !opts.textMode && !isBinary && containsNUL(lineBytes) {
			isBinary = true
		}

		matched := matchAny(opts.re, lineBytes, opts.wordRegexp)
		if opts.invertMatch {
			matched = !matched
		}

		// limitReached reports whether -m's cap has already been satisfied
		// by a prior counted match.
		limitReached := opts.maxCount >= 0 && matchCount >= opts.maxCount

		// A further match line arriving once the limit is reached does not
		// count toward matchCount and does not open (or extend) a trailing-
		// context window of its own. But if it happens to fall inside a
		// still-open window from an earlier match (afterRemaining > 0), it
		// is nevertheless printed — with match formatting, since it is a
		// real match — as ripgrep documents ("more contextual lines might
		// be printed than the given limit"). It is otherwise treated
		// exactly like a context line: it consumes one unit of the
		// remaining window and does not reset that window's size.
		if matched && limitReached && !opts.count && !isBinary && !suppressLines && !opts.quiet {
			if afterRemaining == 0 {
				break
			}
			if afterGroupBytes+len(lineBytes) <= MaxContextBytes {
				// Apply the same -o/-v formatting rules as an ordinary
				// matching line (e.g. -o must still isolate each matched
				// substring here, not print the whole line).
				printMatchOutput(callCtx, displayName, lineNum, lineBytes, opts)
				lastPrintedLine = lineNum
				afterGroupBytes += len(lineBytes)
			}
			afterRemaining--
			if afterRemaining == 0 {
				break
			}
			continue
		}
		if matched && limitReached {
			// count/binary/quiet/suppressed modes never print context, so
			// there is no open-window exception to consider for them: once
			// the limit is reached there is nothing further to compute.
			break
		}

		if matched {
			matchCount++

			if opts.quiet {
				return reportable(), nil
			}
			// -l/--files-without-match: the boolean result these modes report
			// is already fully determined by the presence of one match, so
			// (like ripgrep) stop reading the rest of the file immediately
			// rather than scanning to EOF for no further benefit.
			if opts.filesWithMatches || opts.filesWithoutMatch {
				break
			}
			if opts.count {
				// -c counts individual matched substrings when combined with
				// plain -o (not -o -v, which has no substring to isolate);
				// otherwise it counts selected lines, same as matchCount.
				if opts.onlyMatching && !opts.invertMatch {
					reportedCount += len(matchIndices(opts.re, lineBytes, opts.wordRegexp))
				} else {
					reportedCount++
				}
				// -m caps the number of matching LINES (matchCount), not the
				// number of individual matches reportedCount may enumerate
				// per line (verified directly: "-m2" still lets a -c -o count
				// include every match within each of the first 2 matching
				// lines, even if that is more than 2). Stop once matchCount
				// reaches the cap instead of scanning the remainder of the
				// file for a count that will be discarded anyway. -c never
				// prints context, so there is no open-window exception to
				// consider here.
				if opts.maxCount >= 0 && matchCount >= opts.maxCount {
					break
				}
				continue
			}
			if isBinary {
				// In normal line-output mode, ripgrep stops scanning entirely
				// after the first binary match (its help documents this) —
				// content is never printed for a binary match, so there is
				// nothing further to compute once one is found. Without this,
				// a binary match with no -m limit on an infinite stream (e.g.
				// piped stdin) would read the rest of the stream for no
				// observable benefit. -c is the one exception: it needs an
				// exact count, so it keeps scanning up to -m's cap exactly
				// like the non-binary count path above (verified directly:
				// "rg -c" on a binary file with 5 matching lines reports 5,
				// not 1).
				break
			}

			if contextRequested && printedSeparator && lastPrintedLine > 0 && lineNum > lastPrintedLine+1 {
				callCtx.Out("--\n")
			}

			if opts.beforeContext > 0 {
				for _, cl := range beforeBuf {
					if cl.num <= lastPrintedLine {
						continue
					}
					printContextLine(callCtx, displayName, cl.num, cl.text, opts, '-')
					lastPrintedLine = cl.num
				}
			}
			afterGroupBytes = 0

			printMatchOutput(callCtx, displayName, lineNum, lineBytes, opts)
			lastPrintedLine = lineNum
			printedSeparator = true
			afterRemaining = opts.afterContext

			beforeBuf = beforeBuf[:0]
			beforeBufBytes = 0

			// -m reached and no trailing context remains to be printed for
			// this match: nothing more in the file can affect the output.
			if opts.maxCount >= 0 && matchCount >= opts.maxCount && afterRemaining == 0 {
				break
			}
		} else {
			if !isBinary && afterRemaining > 0 && !opts.quiet && !suppressLines {
				if afterGroupBytes+len(lineBytes) <= MaxContextBytes {
					printContextLine(callCtx, displayName, lineNum, lineBytes, opts, '-')
					lastPrintedLine = lineNum
					afterGroupBytes += len(lineBytes)
				}
				afterRemaining--
				// The last requested trailing-context line for a limiting
				// match has now been emitted; nothing further to scan for.
				if afterRemaining == 0 && opts.maxCount >= 0 && matchCount >= opts.maxCount {
					break
				}
			}

			if !isBinary && opts.beforeContext > 0 {
				for len(beforeBuf) > 0 && (len(beforeBuf) >= opts.beforeContext || beforeBufBytes+len(lineBytes) > MaxContextBytes) {
					beforeBufBytes -= len(beforeBuf[0].text)
					beforeBuf = beforeBuf[1:]
				}
				cp := make([]byte, len(lineBytes))
				copy(cp, lineBytes)
				beforeBuf = append(beforeBuf, contextLine{num: lineNum, text: cp})
				beforeBufBytes += len(lineBytes)
			}
		}
	}

	if err := sc.Err(); err != nil {
		return reportable(), err
	}

	if isBinary {
		if matchCount > 0 && !opts.quiet && !suppressLines {
			callCtx.Errf("rg: %s: binary file matches\n", displayName)
		}
		if !suppressLines {
			return reportable(), nil
		}
	}

	// ripgrep suppresses -c output for a file with zero matches unless its
	// separate --include-zero flag is given (not implemented here); print
	// a count line only when the file actually matched. Report
	// reportedCount (individual matched substrings under plain -o, lines
	// otherwise), not matchCount, which only tracks matching lines for
	// -m's limiting purposes.
	if opts.count && matchCount > 0 {
		if opts.showFilename {
			callCtx.Outf("%s:%s\n", displayName, strconv.Itoa(reportedCount))
		} else {
			callCtx.Outf("%s\n", strconv.Itoa(reportedCount))
		}
	}
	if opts.filesWithMatches && matchCount > 0 {
		callCtx.Outf("%s\n", displayName)
	}
	// -q suppresses all stdout, including --files-without-match's filename
	// line at EOF (verified directly): the exit status alone reports the
	// result. filesWithMatches/count above never reach this problem since
	// -q already returns early the moment a match is found (matchCount>0
	// is exactly the condition under which those two print).
	if opts.filesWithoutMatch && matchCount == 0 && !opts.quiet {
		callCtx.Outf("%s\n", displayName)
	}

	return reportable(), nil
}

type contextLine struct {
	num  int
	text []byte
}

// printMatchOutput prints a matching line according to -o/-v formatting
// rules: the whole line normally (or under -o -v, since there is no
// matched substring to isolate), or each matched substring on its own line
// under plain -o. Shared between an ordinary matching line and a match
// that falls inside an already-open -m trailing-context window.
func printMatchOutput(callCtx *builtins.CallContext, filename string, lineNum int, line []byte, opts *rgOpts) {
	switch {
	case opts.onlyMatching && opts.invertMatch:
		// -v selects a line because the pattern does NOT match it, so
		// there is no matched substring to isolate; ripgrep prints the
		// whole line in this combination (verified directly), the same
		// as it would without -o.
		printMatchLine(callCtx, filename, lineNum, line, opts)
	case opts.onlyMatching:
		// Unlike GNU grep, ripgrep prints every non-overlapping match,
		// including empty ones (e.g. a pattern like "x*" against a line
		// with no "x" still emits one empty line per position); do not
		// filter out zero-width matches here.
		for _, idx := range matchIndices(opts.re, line, opts.wordRegexp) {
			printMatchLine(callCtx, filename, lineNum, line[idx[0]:idx[1]], opts)
		}
	default:
		printMatchLine(callCtx, filename, lineNum, line, opts)
	}
}

func printMatchLine(callCtx *builtins.CallContext, filename string, lineNum int, line []byte, opts *rgOpts) {
	if opts.showFilename {
		callCtx.Stdout.Write([]byte(filename)) //nolint:errcheck
		callCtx.Stdout.Write([]byte{':'})      //nolint:errcheck
	}
	if opts.lineNumber {
		callCtx.Stdout.Write([]byte(strconv.Itoa(lineNum))) //nolint:errcheck
		callCtx.Stdout.Write([]byte{':'})                   //nolint:errcheck
	}
	callCtx.Stdout.Write(line)         //nolint:errcheck
	callCtx.Stdout.Write([]byte{'\n'}) //nolint:errcheck
}

func printContextLine(callCtx *builtins.CallContext, filename string, lineNum int, line []byte, opts *rgOpts, sep byte) {
	if opts.showFilename {
		callCtx.Stdout.Write([]byte(filename)) //nolint:errcheck
		callCtx.Stdout.Write([]byte{sep})      //nolint:errcheck
	}
	if opts.lineNumber {
		callCtx.Stdout.Write([]byte(strconv.Itoa(lineNum))) //nolint:errcheck
		callCtx.Stdout.Write([]byte{sep})                   //nolint:errcheck
	}
	callCtx.Stdout.Write(line)         //nolint:errcheck
	callCtx.Stdout.Write([]byte{'\n'}) //nolint:errcheck
}

// compilePatterns builds a single regexp from one or more patterns, applying
// the fixed-strings, case-handling, word-regexp, and line-regexp options.
// errNewlineNotAllowed matches ripgrep's own error text (verified
// directly) for a pattern whose only possible match requires a literal
// newline character.
var errNewlineNotAllowed = errors.New("the literal \"\\n\" is not allowed in a regex\n\n" +
	"Consider enabling multiline mode with the --multiline flag (or -U for short).\n" +
	"When multiline mode is enabled, new line characters can be matched.")

// requiresNewlineMatch reports whether pattern, compiled as a regular
// expression, can only succeed by matching a literal newline character
// somewhere in the match — i.e. there is no way to satisfy the pattern
// without consuming a '\n'. Malformed patterns are reported as not
// requiring a newline; compilePatterns' own regexp.Compile call reports
// the syntax error separately.
func requiresNewlineMatch(pattern string) bool {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return false
	}
	return mustMatchNewline(re.Simplify())
}

// mustMatchNewline recursively determines whether every successful match
// of re is required to consume a literal '\n': a literal or character
// class matches unconditionally if it denotes exactly (or, for a
// multi-rune literal, includes) the newline rune; a capture group defers
// to its single child; a concatenation requires a newline if ANY
// mandatory component does (every component of a concatenation must
// match for the whole to match); an alternation requires a newline only
// if EVERY branch does (any branch not requiring one lets the overall
// pattern avoid matching a newline by taking that branch); and a
// mandatory repetition (+, or {n,...} with n>=1) defers to its body.
// Matches ripgrep's own rejection rule exactly for every case verified
// directly: bare \n, [\n], concatenations like "a\n", groups, mandatory
// repetitions, and all-newline alternations are rejected; [^\n],
// [a\n] (a class containing '\n' among other runes), and any
// alternation with at least one non-newline-requiring branch are not.
func mustMatchNewline(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpLiteral:
		for _, r := range re.Rune {
			if r == '\n' {
				return true
			}
		}
		return false
	case syntax.OpCharClass:
		// re.Rune is a flattened [lo,hi] range-pair list; "exactly newline"
		// means the whole class denotes the single rune '\n' and nothing
		// else — a class that ALSO matches other runes (e.g. "[a\n]") does
		// not unconditionally require a newline, since other input can
		// still satisfy it.
		return len(re.Rune) == 2 && re.Rune[0] == '\n' && re.Rune[1] == '\n'
	case syntax.OpCapture:
		return mustMatchNewline(re.Sub[0])
	case syntax.OpConcat:
		for _, s := range re.Sub {
			if mustMatchNewline(s) {
				return true
			}
		}
		return false
	case syntax.OpAlternate:
		if len(re.Sub) == 0 {
			return false
		}
		for _, s := range re.Sub {
			if !mustMatchNewline(s) {
				return false
			}
		}
		return true
	case syntax.OpPlus:
		if len(re.Sub) > 0 {
			return mustMatchNewline(re.Sub[0])
		}
		return false
	case syntax.OpRepeat:
		if re.Min >= 1 && len(re.Sub) > 0 {
			return mustMatchNewline(re.Sub[0])
		}
		return false
	}
	return false
}

func compilePatterns(patterns []string, fixedStrings bool, caseMode caseHandling, wordRegexp, lineRegexp bool) (*regexp.Regexp, error) {
	var parts []string
	anyUpper := false
	for _, p := range patterns {
		// ripgrep rejects any pattern whose only possible match requires a
		// literal newline character, since this implementation (like
		// ripgrep without -U/--multiline, which is rejected as unknown)
		// scans one line at a time and can never satisfy such a pattern
		// (verified directly: exit 2 with "the literal \"\\n\" is not
		// allowed in a regex", for both a raw newline byte and the \n
		// escape sequence, and for -F fixed-string patterns too).
		if fixedStrings {
			if strings.Contains(p, "\n") {
				return nil, errNewlineNotAllowed
			}
		} else if requiresNewlineMatch(p) {
			return nil, errNewlineNotAllowed
		}
		if fixedStrings {
			parts = append(parts, regexp.QuoteMeta(p))
		} else {
			if _, err := regexp.Compile(p); err != nil {
				return nil, errors.New("invalid regular expression: " + err.Error())
			}
			// Wrap each pattern in its own noncapturing group before
			// joining with "|". Without this, an inline flag such as
			// "(?i)" in one -e pattern leaks into every alternative joined
			// after it in Go's combined regex (inline flags apply from
			// their position to the end of the enclosing group, and the
			// enclosing group here would otherwise be the whole "a|b|c"
			// expression) — e.g. "-e '(?i)a' -e b" must only case-fold
			// "a", not "b" too, matching ripgrep, where each -e pattern is
			// an independently compiled, independently scoped regex.
			parts = append(parts, "(?:"+p+")")
		}
		// -F makes every character in p a literal, so smart-case detection
		// must inspect the raw runes directly rather than applying regex
		// escape-sequence rules; otherwise a fixed-string pattern like
		// `\A` would be misread as the (regex-only) start-of-text anchor
		// and skipped, when it is actually two literal characters
		// (backslash, 'A') that should force case-sensitive matching
		// (verified directly against real ripgrep).
		if fixedStrings {
			if hasUpperLiteral(p) {
				anyUpper = true
			}
		} else if hasUpper(p) {
			anyUpper = true
		}
	}

	combined := strings.Join(parts, "|")

	// Word-boundary wrapping is deliberately NOT done here with Go's \b:
	// Go's regexp \b uses an ASCII-only definition of "word character",
	// but ripgrep enables Unicode mode by default, so e.g. "café" is a
	// single word to ripgrep (verified directly). Matches are instead
	// filtered for Unicode word boundaries after compilation, in
	// matchIndices/matchAny below; wordRegexp is threaded through rgOpts
	// for that purpose.
	if lineRegexp {
		combined = `^(?:` + combined + `)$`
	}

	ignoreCase := false
	switch caseMode {
	case ignoreCaseMode:
		ignoreCase = true
	case smartCaseMode:
		ignoreCase = !anyUpper
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

// hasUpper reports whether pattern contains an uppercase literal character,
// for -S/--smart-case's "case-insensitive unless the pattern has an
// uppercase character" rule. This mirrors ripgrep's own algorithm (used for
// its PCRE2 backend, and equivalent in effect to its default engine's
// AST-based literal analysis): scan runes left to right, treating a
// backslash-escaped Unicode property class (\pX, \p{Name}), a Perl
// character-class/anchor shorthand (\w, \W, \s, \S, \d, \D, \b, \B, \A, \z,
// \Z), or any other single escaped character as regex syntax rather than a
// literal to inspect — so "foo\pL" and "foo\w" are case-insensitive despite
// containing uppercase letters in their syntax, matching ripgrep exactly.
// An explicit character class range such as "[A-Z]" is still a literal
// uppercase range and makes the pattern case-sensitive, also matching
// ripgrep. Uses Unicode-aware uppercase detection (not ASCII-only), so a
// literal like "É" also triggers case-sensitive matching.
// isWordRune reports whether r is a Unicode "word" character for -w
// purposes: a letter, digit, or underscore. This matches ripgrep's
// definition (Unicode mode is on by default).
// isWordRune reports whether r is a Unicode "word" character for -w's
// half-boundary check, matching ripgrep's (Rust regex's) definition: a
// letter, decimal digit, any combining mark, or connector punctuation
// (which includes ASCII '_' but also other Unicode connectors). Combining
// marks matter for NFD-decomposed input: a base letter followed by a
// standalone combining accent (e.g. "e" + U+0301) forms one user-visible
// character, and ripgrep treats the mark as still part of the same word so
// that a search for the bare base letter does not spuriously satisfy a
// word boundary in the middle of the composed character (verified
// directly).
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.M, r) || unicode.Is(unicode.Pc, r)
}

// hasWordBoundaries reports whether [start:end) in line is flanked by
// Unicode word boundaries on both sides, per -w/--word-regexp. A word
// boundary exists at a position where a word rune is adjacent to a
// non-word rune (or the start/end of the line, treated as non-word).
// Matches Go's own \b semantics, but with a Unicode word-rune definition
// instead of an ASCII-only one.
func hasWordBoundaries(line []byte, start, end int) bool {
	// A zero-width match (start == end, from a pattern that can match the
	// empty string) is NOT unconditionally rejected: ripgrep's \b{-half}
	// assertions only inspect the context immediately outside the match,
	// which is well-defined even when the match itself is empty (both
	// "outside" checks simply look at the same single position from
	// either side). Verified directly: "rg -w -c -o ''" on "abc !\n"
	// reports 2 matches (between the space and '!', and at end-of-line),
	// not 0 — the same half-boundary checks below, unmodified, correctly
	// accept exactly those two positions and reject the other four
	// (start-of-line before 'a', and immediately after each of 'a','b','c',
	// all of which sit directly against a word character on the
	// non-word-required side).
	// ripgrep's -w does not wrap the pattern in ordinary \b assertions on
	// both sides (which would require the matched text itself to start
	// and end on a word character). It uses "half" boundary assertions
	// instead — \b{start-half} and \b{end-half} — which only inspect the
	// context OUTSIDE the match: the left side is satisfied by the start
	// of the line or a non-word character immediately before the match,
	// and the right side is satisfied by the end of the line or a
	// non-word character immediately after, regardless of whether the
	// match's own first/last rune is itself a word character. This is
	// why "rg -w -e '-2'" matches "-2" inside "(-2)" even though neither
	// '-' nor '2' at the boundary forms an ordinary word/non-word
	// transition with itself (verified directly against real ripgrep).
	leftOK := start == 0
	if !leftOK {
		r, _ := utf8.DecodeLastRune(line[:start])
		leftOK = !isWordRune(r)
	}
	rightOK := end == len(line)
	if !rightOK {
		r, _ := utf8.DecodeRune(line[end:])
		rightOK = !isWordRune(r)
	}
	return leftOK && rightOK
}

// matchIndices returns all non-overlapping match indices for re against
// line, applying a Unicode-aware word-boundary filter when wordRegexp is
// true (the pattern is compiled without Go's ASCII-only \b in that case;
// see compilePatterns).
func matchIndices(re *regexp.Regexp, line []byte, wordRegexp bool) [][]int {
	all := re.FindAllIndex(line, -1)
	if !wordRegexp {
		return all
	}
	var out [][]int
	for _, idx := range all {
		if hasWordBoundaries(line, idx[0], idx[1]) {
			out = append(out, idx)
		}
	}
	return out
}

// matchAny reports whether re matches anywhere in line, applying the same
// Unicode word-boundary filter as matchIndices when wordRegexp is true.
func matchAny(re *regexp.Regexp, line []byte, wordRegexp bool) bool {
	if !wordRegexp {
		return re.Match(line)
	}
	return len(matchIndices(re, line, wordRegexp)) > 0
}

// hasUpperLiteral reports whether s contains any Unicode uppercase rune,
// with no regex-escape interpretation. Used for -F/--fixed-strings smart-
// case detection, where every character (including a literal backslash) is
// already a literal, unlike hasUpper's regex-aware scan.
func hasUpperLiteral(s string) bool {
	for _, r := range s {
		if unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// validateGlobs reports an error if any glob in globs is not syntactically
// valid, mirroring ripgrep's own glob-parse-error behavior (exit 2) rather
// than the silent "never matches" that filepath.Match's discarded error
// would otherwise produce. Each glob is checked with filepath.Match itself
// (against an arbitrary probe string) so the accepted syntax matches
// exactly what globMatch will later evaluate.
// MaxGlobSegments bounds the number of '/'-delimited segments accepted in
// a single -g/--glob pattern. globMatchSegments' DP cost is
// O(len(patSegs) * len(pathSegs)); len(pathSegs) is already bounded by
// MaxTraversalDepth (256), but pattern segment count is otherwise
// attacker-controlled up to the shell script size limit (a glob argument
// could contain millions of '/' characters). Without this cap, one -g
// pattern matched against every candidate path during traversal could
// perform hundreds of millions of redundant comparisons and monopolize
// CPU well past the shell's own timeout even though at most one
// directory entry ever matches. 4096 segments is far beyond any
// legitimate glob (real-world globs are a handful of segments) while
// keeping the worst case (4096 * MaxTraversalDepth, roughly one million
// comparisons per candidate path) comfortably bounded.
const MaxGlobSegments = 4096

func validateGlobs(globs globSlice) error {
	for _, g := range globs {
		pat := g
		if strings.HasPrefix(pat, "!") {
			pat = pat[1:]
		}
		if _, err := filepath.Match(pat, "probe"); err != nil {
			return fmt.Errorf("error parsing glob '%s': %w", g, err)
		}
		if n := strings.Count(pat, "/") + 1; n > MaxGlobSegments {
			return fmt.Errorf("glob '%s' has too many path segments (%d, max %d)", g, n, MaxGlobSegments)
		}
	}
	return nil
}

func hasUpper(pattern string) bool {
	runes := []rune(pattern)
	i := 0
	for i < len(runes) {
		r := runes[i]
		if r == '\\' && i+1 < len(runes) {
			switch runes[i+1] {
			case 'p', 'P':
				// \pX or \p{Name}: skip the whole property-class token.
				i += 2
				if i < len(runes) && runes[i] == '{' {
					for i < len(runes) && runes[i] != '}' {
						i++
					}
					if i < len(runes) {
						i++ // consume closing '}'
					}
				} else if i < len(runes) {
					i++ // single-letter property name, e.g. \pL
				}
				continue
			case 'w', 'W', 's', 'S', 'd', 'D', 'b', 'B', 'A', 'z', 'Z':
				// Perl class/anchor shorthand: the letter is escape syntax,
				// not a literal character, regardless of its case.
				i += 2
				continue
			default:
				// Any other escaped character is a literal (e.g. \. \\ \( );
				// not itself checked for uppercase.
				i += 2
				continue
			}
		}
		// "(?" introduces group/flag syntax, not literal text: non-capturing
		// groups "(?:...)", named captures "(?P<name>...)", and inline flags
		// "(?i)", "(?U)", "(?im:...)" etc. can all contain uppercase letters
		// (e.g. the 'P' in \(?P<name>\) or 'U' in \(?U\)) that are regex
		// syntax, not literal characters to case-fold (verified directly:
		// ripgrep matches "FOO" against "(?P<x>foo)" under -S). Skip past
		// the flag/name-introducer letters up to (not including) '<' or the
		// closing punctuation, so any *literal* text inside the group
		// (after '<...>' for a named capture, or after ':' for a flag
		// group) is still inspected normally on the next iterations.
		if r == '(' && i+1 < len(runes) && runes[i+1] == '?' {
			i += 2
			for i < len(runes) && runes[i] != ':' && runes[i] != ')' && runes[i] != '<' {
				i++
			}
			if i < len(runes) && runes[i] == '<' {
				// Named capture "(?P<name>": the name itself is an
				// identifier, not pattern text to case-fold against input;
				// skip through the closing '>' too.
				for i < len(runes) && runes[i] != '>' {
					i++
				}
			}
			if i < len(runes) {
				i++ // consume ':', ')', or '>'
			}
			continue
		}
		if unicode.IsUpper(r) {
			return true
		}
		i++
	}
	return false
}
