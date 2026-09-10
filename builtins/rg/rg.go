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
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

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
	wordRegexp := fs.BoolP("word-regexp", "w", false, "match only whole words")
	lineRegexp := fs.BoolP("line-regexp", "x", false, "match only whole lines")

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

		resolvedFilesWithMatches := filesWithMatches.pos > 0 &&
			filesWithMatches.pos > filesWithoutMatch.pos && filesWithMatches.pos > count.pos
		resolvedFilesWithoutMatch := filesWithoutMatch.pos > 0 &&
			filesWithoutMatch.pos > filesWithMatches.pos && filesWithoutMatch.pos > count.pos
		resolvedCount := count.pos > 0 &&
			count.pos > filesWithMatches.pos && count.pos > filesWithoutMatch.pos

		if *listFiles {
			return runListFiles(ctx, callCtx, args, globs, *hidden)
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

		re, err := compilePatterns(rawPatterns, *fixedStrings, caseMode, *wordRegexp, *lineRegexp)
		if err != nil {
			callCtx.Errf("rg: %s\n", err.Error())
			return builtins.Result{Code: exitError}
		}

		contextFlagUsed := fs.Changed("after-context") || fs.Changed("before-context") || fs.Changed("context")
		if *onlyMatching {
			after = 0
			before = 0
			contextFlagUsed = false
		}

		opts := &rgOpts{
			re:                re,
			invertMatch:       *invertMatch,
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
	return callCtx.OpenFile(ctx, file, os.O_RDONLY, 0)
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
	if len(paths) == 0 {
		if stdinHasData(callCtx) {
			paths = []string{"-"}
		} else {
			paths = []string{"."}
			recursive = true
		}
	}

	files, sawDir, walkErr := expandOperands(ctx, callCtx, paths, globs, hidden)

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

	for _, file := range files {
		if ctx.Err() != nil {
			anyError = true
			break
		}
		matched, err := searchFile(ctx, callCtx, file, opts)
		if err != nil {
			name := file
			if file == "-" {
				name = "(standard input)"
			}
			callCtx.Errf("rg: %s: %s\n", name, callCtx.PortableErr(err))
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
func runListFiles(ctx context.Context, callCtx *builtins.CallContext, paths []string, globs globSlice, hidden bool) builtins.Result {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	files, _, walkErr := expandOperands(ctx, callCtx, paths, globs, hidden)
	for _, f := range files {
		callCtx.Outf("%s\n", f)
	}
	if walkErr {
		return builtins.Result{Code: exitError}
	}
	if len(files) > 0 {
		return builtins.Result{Code: exitMatch}
	}
	return builtins.Result{Code: exitNoMatch}
}

// expandOperands resolves a list of file/directory operands to a sorted,
// deduplicated list of regular files to search, recursively expanding
// directories (excluding hidden entries and glob-excluded paths, subject to
// the given options). "-" (stdin) is passed through unchanged. Returns the
// file list, whether any operand was a directory (used to decide whether to
// show filenames, matching ripgrep's behavior of always labeling directory
// search results), and whether any traversal error occurred (already
// reported to stderr).
func expandOperands(ctx context.Context, callCtx *builtins.CallContext, paths []string, globs globSlice, hidden bool) ([]string, bool, bool) {
	var files []string
	failed := false
	sawDir := false
	seen := make(map[string]bool)

	for _, p := range paths {
		if ctx.Err() != nil {
			return files, sawDir, true
		}
		if p == "-" {
			files = append(files, p)
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
			found, walkFailed := walkDir(ctx, callCtx, clean, globs, hidden)
			if walkFailed {
				failed = true
			}
			for _, f := range found {
				if !seen[f] {
					seen[f] = true
					files = append(files, f)
				}
			}
			continue
		}
		if !info.Mode().IsRegular() {
			callCtx.Errf("rg: '%s': not a regular file\n", builtins.SafeOperand(p))
			failed = true
			continue
		}
		if !seen[clean] {
			seen[clean] = true
			files = append(files, clean)
		}
	}
	return files, sawDir, failed
}

// walkDir recursively lists regular files under root, in sorted order,
// honoring the hidden and glob filters. Symbolic links are never followed.
func walkDir(ctx context.Context, callCtx *builtins.CallContext, root string, globs globSlice, hidden bool) ([]string, bool) {
	var out []string
	failed := false

	type frame struct {
		path  string
		depth int
	}
	stack := []frame{{path: root, depth: 0}}

	for len(stack) > 0 {
		if ctx.Err() != nil {
			return out, true
		}
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		entries, err := callCtx.ReadDir(ctx, top.path)
		if err != nil {
			callCtx.Errf("rg: '%s': %s\n", builtins.SafeOperand(top.path), callCtx.PortableErr(err))
			failed = true
			continue
		}

		// Collect and sort children so results are deterministic; ReadDir
		// entries are already sorted by name per CallContext's contract,
		// but re-sort defensively for directories vs files ordering below.
		var children []iofs.DirEntry
		children = append(children, entries...)
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })

		for _, entry := range children {
			if ctx.Err() != nil {
				return out, true
			}
			name := entry.Name()
			childPath := joinRel(top.path, name)

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
				stack = append(stack, frame{path: childPath, depth: top.depth + 1})
				continue
			}

			if info.Mode().IsRegular() {
				out = append(out, childPath)
			}
		}
	}

	sort.Strings(out)
	return out, failed
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
		allowed := true
		for _, g := range globs {
			neg := strings.HasPrefix(g, "!")
			if !neg {
				continue
			}
			pat := g[1:]
			if globMatch(pat, path) || globMatch(pat, path+"/") {
				allowed = false
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
	if strings.Contains(pat, "/") {
		if ok, _ := filepath.Match(pat, path); ok {
			return true
		}
		// Support patterns starting with "**/" to mean "at any depth".
		if strings.HasPrefix(pat, "**/") {
			suffix := pat[3:]
			for {
				if ok, _ := filepath.Match(suffix, path); ok {
					return true
				}
				idx := strings.Index(path, "/")
				if idx < 0 {
					return false
				}
				path = path[idx+1:]
			}
		}
		return false
	}
	base := path
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		base = path[idx+1:]
	}
	ok, _ := filepath.Match(pat, base)
	return ok
}

// searchFile searches a single file. Returns (matched, error).
func searchFile(ctx context.Context, callCtx *builtins.CallContext, file string, opts *rgOpts) (bool, error) {
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

	// Binary detection: probe the first binaryProbeSize bytes before
	// scanning, matching grep's approach.
	const binaryProbeSize = 32 * 1024
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

	sc := bufio.NewScanner(reader)
	buf := make([]byte, scanBufInit)
	sc.Buffer(buf, MaxLineBytes)

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

	for sc.Scan() {
		if ctx.Err() != nil {
			return matchCount > 0, ctx.Err()
		}
		lineNum++
		lineBytes := sc.Bytes()

		if !opts.textMode && !isBinary && containsNUL(lineBytes) {
			isBinary = true
		}

		matched := opts.re.Match(lineBytes)
		if opts.invertMatch {
			matched = !matched
		}

		if matched {
			if opts.maxCount >= 0 && matchCount >= opts.maxCount {
				break
			}
			matchCount++

			if opts.quiet {
				return true, nil
			}
			if isBinary || suppressLines {
				continue
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

			switch {
			case opts.onlyMatching && opts.invertMatch:
				// Inverted -o selects lines with no matching parts.
			case opts.onlyMatching:
				// Unlike GNU grep, ripgrep prints every non-overlapping match,
				// including empty ones (e.g. a pattern like "x*" against a line
				// with no "x" still emits one empty line per position); do not
				// filter out zero-width matches here.
				for _, idx := range opts.re.FindAllIndex(lineBytes, -1) {
					printMatchLine(callCtx, displayName, lineNum, lineBytes[idx[0]:idx[1]], opts)
				}
			default:
				printMatchLine(callCtx, displayName, lineNum, lineBytes, opts)
			}
			lastPrintedLine = lineNum
			printedSeparator = true
			afterRemaining = opts.afterContext

			beforeBuf = beforeBuf[:0]
			beforeBufBytes = 0
		} else {
			if !isBinary && afterRemaining > 0 && !opts.quiet && !suppressLines {
				if afterGroupBytes+len(lineBytes) <= MaxContextBytes {
					printContextLine(callCtx, displayName, lineNum, lineBytes, opts, '-')
					lastPrintedLine = lineNum
					afterGroupBytes += len(lineBytes)
				}
				afterRemaining--
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
		return matchCount > 0, err
	}

	if isBinary {
		if matchCount > 0 && !opts.quiet && !suppressLines {
			callCtx.Errf("rg: %s: binary file matches\n", displayName)
		}
		if !suppressLines {
			return matchCount > 0, nil
		}
	}

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
	text []byte
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
func compilePatterns(patterns []string, fixedStrings bool, caseMode caseHandling, wordRegexp, lineRegexp bool) (*regexp.Regexp, error) {
	var parts []string
	anyUpper := false
	for _, p := range patterns {
		if fixedStrings {
			parts = append(parts, regexp.QuoteMeta(p))
		} else {
			if _, err := regexp.Compile(p); err != nil {
				return nil, errors.New("invalid regular expression: " + err.Error())
			}
			parts = append(parts, p)
		}
		if hasUpper(p) {
			anyUpper = true
		}
	}

	combined := strings.Join(parts, "|")

	if wordRegexp && !lineRegexp {
		combined = `\b(?:` + combined + `)\b`
	}
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

func hasUpper(s string) bool {
	for _, r := range s {
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	return false
}
