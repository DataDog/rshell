// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package find implements the find builtin command.
//
// find — search for files in a directory hierarchy
//
// Usage: find [path...] [expression]
//
// Recursively descend directory trees rooted at each given path, evaluating
// the given expression for each file found. With no path, search the current
// directory. With no expression, -print is implied.
//
// Accepted primaries (tests):
//
//	-name pattern
//	    True if the filename (basename) matches the shell glob pattern.
//
//	-iname pattern
//	    Like -name but case-insensitive.
//
//	-path pattern / -wholename pattern
//	    True if the full path matches the shell glob pattern.
//
//	-ipath pattern / -iwholename pattern
//	    Like -path but case-insensitive.
//
//	-type c
//	    True if the file is of type c: f (regular file), d (directory),
//	    l (symbolic link), p (named pipe), s (socket), b (block device),
//	    c (character device).
//
//	-empty
//	    True if the file is empty (zero-length regular file or directory
//	    with no entries).
//
//	-size n[cwbkMG]
//	    True if the file uses n units of space. Suffixes: c (bytes),
//	    w (2-byte words), b (512-byte blocks, default), k (KiB), M (MiB),
//	    G (GiB). Prefix + for greater than, - for less than.
//
//	-newer file
//	    True if the file was modified more recently than file.
//
//	-mtime n
//	    True if the file was modified n*24 hours ago. +n means more than,
//	    -n means less than, n means exactly.
//
//	-perm mode / -perm -mode / -perm /mode
//	    True if file permission bits match. Plain mode is exact match,
//	    -mode means all bits set, /mode means any bit set.
//
//	-links n
//	    True if the file has n hard links. +n for more, -n for fewer.
//
//	-true
//	    Always true.
//
//	-false
//	    Always false.
//
// Depth control:
//
//	-maxdepth n
//	    Descend at most n directory levels.
//
//	-mindepth n
//	    Do not apply tests or actions at levels less than n.
//
// Actions:
//
//	-print
//	    Print the full pathname followed by a newline. This is the default
//	    action if no action is specified.
//
//	-print0
//	    Print the full pathname followed by a null character.
//
//	-prune
//	    If the file is a directory, do not descend into it. Always true.
//
//	-quit
//	    Exit immediately with status 0.
//
// Global options:
//
//	-depth
//	    Process directory contents before the directory itself.
//
//	-h, --help
//	    Print usage and exit.
//
// Operators:
//
//	( expr )    Grouping.
//	! expr      Logical NOT (also -not).
//	expr -a expr   Logical AND (also -and; implicit between primaries).
//	expr -o expr   Logical OR (also -or).
//
// Rejected actions (unsafe):
//
//	-exec, -execdir, -ok, -okdir  — execute external commands
//	-delete                       — delete files
//	-fprintf, -fprint, -fprint0, -fls — write to files
//
// Exit codes:
//
//	0  All paths processed successfully.
//	1  At least one error occurred (missing path, permission denied, etc.).
//
// Memory safety:
//
//	The walk is streaming — each entry is evaluated and printed immediately
//	without accumulating results. Recursion depth is bounded to MaxDepth
//	(200 levels). Total entries visited are bounded to MaxEntries (1M).
//	All loops check ctx.Err() at each iteration to honour the shell's
//	execution timeout and to support graceful cancellation.
package find

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/DataDog/rshell/interp/builtins"
)

// Cmd is the find builtin command descriptor.
var Cmd = builtins.Command{Name: "find", Run: run}

// MaxDepth is the hard limit on directory recursion depth.
const MaxDepth = 200

// MaxEntries is the hard limit on total entries visited.
const MaxEntries = 1_000_000

const secondsPerDay = 86400

// maxGlobRecursion limits pathGlobMatch recursion to prevent DoS from
// pathological patterns like *a*a*a*a*b.
const maxGlobRecursion = 1000

// unsafeActions are find actions that violate the safety rules (execute, delete, write).
var unsafeActions = map[string]bool{
	"-exec":    true,
	"-execdir": true,
	"-ok":      true,
	"-okdir":   true,
	"-delete":  true,
	"-fprintf": true,
	"-fprint":  true,
	"-fprint0": true,
	"-fls":     true,
	"-printf":  true,
}

type exprKind int

const (
	kindTest exprKind = iota
	kindAction
	kindAnd
	kindOr
	kindNot
	kindPrune
)

type expr struct {
	kind  exprKind
	test  func(path string, info fs.FileInfo, depth int) bool
	left  *expr
	right *expr
	// For actions
	action func(path string) (quit bool)
}

type walkState struct {
	callCtx  *builtins.CallContext
	ctx      context.Context
	failed   bool
	quit     bool
	prune    bool
	visited  int
	depth    bool // -depth option
	maxdepth int
	mindepth int
	root     *expr
}

func run(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	if callCtx.ReadDir == nil || callCtx.Stat == nil {
		callCtx.Errf("find: filesystem access not available\n")
		return builtins.Result{Code: 1}
	}

	if len(args) > 0 && (args[0] == "--help" || args[0] == "-h") {
		printHelp(callCtx)
		return builtins.Result{}
	}

	for _, arg := range args {
		if unsafeActions[arg] {
			callCtx.Errf("find: %s: action not permitted (unsafe)\n", arg)
			return builtins.Result{Code: 1}
		}
	}

	paths, exprArgs := splitPathsAndExpr(args)
	if len(paths) == 0 {
		paths = []string{"."}
	}

	state := &walkState{
		callCtx:  callCtx,
		ctx:      ctx,
		maxdepth: MaxDepth,
		mindepth: 0,
	}

	remaining, err := parseGlobalOptions(state, exprArgs)
	if err != nil {
		callCtx.Errf("find: %s\n", err)
		return builtins.Result{Code: 1}
	}

	root, parseErr := parseExpr(callCtx, remaining, state)
	if parseErr != "" {
		callCtx.Errf("find: %s\n", parseErr)
		return builtins.Result{Code: 1}
	}
	state.root = root

	for _, p := range paths {
		if ctx.Err() != nil || state.quit {
			break
		}
		info, statErr := callCtx.Stat(ctx, p)
		if statErr != nil {
			callCtx.Errf("find: %s: %s\n", p, callCtx.PortableErr(statErr))
			state.failed = true
			continue
		}
		state.walk(p, info, 0)
	}

	if state.failed {
		return builtins.Result{Code: 1}
	}
	return builtins.Result{}
}

func (s *walkState) walk(path string, info fs.FileInfo, depth int) {
	if s.ctx.Err() != nil || s.quit {
		return
	}
	s.visited++
	if s.visited > MaxEntries {
		s.callCtx.Errf("find: entry limit exceeded (%d)\n", MaxEntries)
		s.failed = true
		s.quit = true
		return
	}
	if depth > s.maxdepth {
		return
	}

	isDir := info.IsDir()

	if !s.depth {
		s.prune = false
		if depth >= s.mindepth {
			s.evalExpr(s.root, path, info, depth)
		}
		if s.quit {
			return
		}
		if isDir && !s.prune && depth < s.maxdepth {
			s.walkDir(path, depth)
		}
	} else {
		if isDir && depth < s.maxdepth {
			s.walkDir(path, depth)
		}
		if s.quit {
			return
		}
		if depth >= s.mindepth {
			s.evalExpr(s.root, path, info, depth)
		}
	}
}

func (s *walkState) walkDir(path string, depth int) {
	entries, err := s.callCtx.ReadDir(s.ctx, path)
	if err != nil {
		s.callCtx.Errf("find: %s: %s\n", path, s.callCtx.PortableErr(err))
		s.failed = true
		return
	}
	for _, e := range entries {
		if s.ctx.Err() != nil || s.quit {
			return
		}
		childPath := path + string(filepath.Separator) + e.Name()
		childInfo, err := e.Info()
		if err != nil {
			s.callCtx.Errf("find: %s: %s\n", childPath, s.callCtx.PortableErr(err))
			s.failed = true
			continue
		}
		s.walk(childPath, childInfo, depth+1)
	}
}

// evalExpr evaluates the expression tree for a file.
func (s *walkState) evalExpr(e *expr, path string, info fs.FileInfo, depth int) bool {
	if e == nil {
		return true
	}
	switch e.kind {
	case kindTest:
		return e.test(path, info, depth)
	case kindAction:
		quit := e.action(path)
		if quit {
			s.quit = true
		}
		return true
	case kindPrune:
		s.prune = true
		return true
	case kindAnd:
		if !s.evalExpr(e.left, path, info, depth) {
			return false
		}
		if s.quit {
			return true
		}
		return s.evalExpr(e.right, path, info, depth)
	case kindOr:
		if s.evalExpr(e.left, path, info, depth) {
			return true
		}
		if s.quit {
			return true
		}
		return s.evalExpr(e.right, path, info, depth)
	case kindNot:
		return !s.evalExpr(e.left, path, info, depth)
	}
	return true
}

func splitPathsAndExpr(args []string) (paths []string, exprArgs []string) {
	exprStarts := map[string]bool{
		"-name": true, "-iname": true,
		"-path": true, "-ipath": true,
		"-wholename": true, "-iwholename": true,
		"-type": true, "-empty": true,
		"-size": true, "-newer": true,
		"-mtime": true, "-perm": true,
		"-links": true, "-true": true, "-false": true,
		"-maxdepth": true, "-mindepth": true,
		"-depth": true,
		"-print": true, "-print0": true,
		"-prune": true, "-quit": true,
		"-and": true, "-a": true,
		"-or": true, "-o": true,
		"-not": true, "!": true,
		"(": true, ")": true,
	}
	for i, arg := range args {
		if exprStarts[arg] || unsafeActions[arg] {
			return args[:i], args[i:]
		}
	}
	return args, nil
}

func parseGlobalOptions(state *walkState, args []string) (remaining []string, err error) {
	remaining = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-depth":
			state.depth = true
		case "-maxdepth":
			if i+1 >= len(args) {
				return nil, errMissingArg("-maxdepth")
			}
			i++
			n, e := strconv.Atoi(args[i])
			if e != nil || n < 0 {
				return nil, errInvalidArg{"-maxdepth", args[i]}
			}
			if n > MaxDepth {
				n = MaxDepth
			}
			state.maxdepth = n
		case "-mindepth":
			if i+1 >= len(args) {
				return nil, errMissingArg("-mindepth")
			}
			i++
			n, e := strconv.Atoi(args[i])
			if e != nil || n < 0 {
				return nil, errInvalidArg{"-mindepth", args[i]}
			}
			state.mindepth = n
		default:
			remaining = append(remaining, args[i])
		}
	}
	return remaining, nil
}

func parseExpr(callCtx *builtins.CallContext, args []string, state *walkState) (*expr, string) {
	if len(args) == 0 {
		return defaultPrintExpr(callCtx), ""
	}

	hasAction := false
	for _, a := range args {
		if a == "-print" || a == "-print0" || a == "-prune" || a == "-quit" {
			hasAction = true
			break
		}
	}

	tokens := &tokenStream{args: args}
	e, errMsg := parseOr(callCtx, tokens, state)
	if errMsg != "" {
		return nil, errMsg
	}
	if tokens.pos < len(tokens.args) {
		return nil, "unexpected argument: " + tokens.args[tokens.pos]
	}

	if !hasAction {
		printExpr := defaultPrintExpr(callCtx)
		e = &expr{kind: kindAnd, left: e, right: printExpr}
	}

	return e, ""
}

type tokenStream struct {
	args []string
	pos  int
}

func (t *tokenStream) peek() string {
	if t.pos >= len(t.args) {
		return ""
	}
	return t.args[t.pos]
}

func (t *tokenStream) next() string {
	if t.pos >= len(t.args) {
		return ""
	}
	s := t.args[t.pos]
	t.pos++
	return s
}

func parseOr(callCtx *builtins.CallContext, tokens *tokenStream, state *walkState) (*expr, string) {
	left, errMsg := parseAnd(callCtx, tokens, state)
	if errMsg != "" {
		return nil, errMsg
	}
	for tokens.peek() == "-o" || tokens.peek() == "-or" {
		tokens.next()
		right, errMsg := parseAnd(callCtx, tokens, state)
		if errMsg != "" {
			return nil, errMsg
		}
		left = &expr{kind: kindOr, left: left, right: right}
	}
	return left, ""
}

func parseAnd(callCtx *builtins.CallContext, tokens *tokenStream, state *walkState) (*expr, string) {
	left, errMsg := parseUnary(callCtx, tokens, state)
	if errMsg != "" {
		return nil, errMsg
	}
	for {
		pk := tokens.peek()
		if pk == "-a" || pk == "-and" {
			tokens.next()
		} else if pk == "" || pk == "-o" || pk == "-or" || pk == ")" {
			break
		} else {
			// implicit AND
		}
		right, errMsg := parseUnary(callCtx, tokens, state)
		if errMsg != "" {
			return nil, errMsg
		}
		left = &expr{kind: kindAnd, left: left, right: right}
	}
	return left, ""
}

func parseUnary(callCtx *builtins.CallContext, tokens *tokenStream, state *walkState) (*expr, string) {
	pk := tokens.peek()
	if pk == "!" || pk == "-not" {
		tokens.next()
		operand, errMsg := parseUnary(callCtx, tokens, state)
		if errMsg != "" {
			return nil, errMsg
		}
		return &expr{kind: kindNot, left: operand}, ""
	}
	return parsePrimary(callCtx, tokens, state)
}

func parsePrimary(callCtx *builtins.CallContext, tokens *tokenStream, state *walkState) (*expr, string) {
	tok := tokens.peek()
	if tok == "" {
		return nil, "expected expression"
	}

	if tok == "(" {
		tokens.next()
		e, errMsg := parseOr(callCtx, tokens, state)
		if errMsg != "" {
			return nil, errMsg
		}
		if tokens.next() != ")" {
			return nil, "missing closing ')'"
		}
		return e, ""
	}

	tokens.next()
	switch tok {
	case "-name":
		return parseName(tokens, false)
	case "-iname":
		return parseName(tokens, true)
	case "-path", "-wholename":
		return parsePath(tokens, false)
	case "-ipath", "-iwholename":
		return parsePath(tokens, true)
	case "-type":
		return parseType(tokens)
	case "-empty":
		return &expr{kind: kindTest, test: func(path string, info fs.FileInfo, depth int) bool {
			if info.Mode().IsRegular() {
				return info.Size() == 0
			}
			if info.IsDir() {
				entries, err := callCtx.ReadDir(state.ctx, path)
				return err == nil && len(entries) == 0
			}
			return false
		}}, ""
	case "-size":
		return parseSize(tokens)
	case "-newer":
		return parseNewer(callCtx, tokens, state)
	case "-mtime":
		return parseMtime(tokens)
	case "-perm":
		return parsePerm(tokens)
	case "-links":
		return parseLinks(tokens)
	case "-true":
		return &expr{kind: kindTest, test: func(string, fs.FileInfo, int) bool { return true }}, ""
	case "-false":
		return &expr{kind: kindTest, test: func(string, fs.FileInfo, int) bool { return false }}, ""
	case "-print":
		return defaultPrintExpr(callCtx), ""
	case "-print0":
		return &expr{kind: kindAction, action: func(path string) bool {
			callCtx.Outf("%s\x00", path)
			return false
		}}, ""
	case "-prune":
		return &expr{kind: kindPrune}, ""
	case "-quit":
		return &expr{kind: kindAction, action: func(string) bool { return true }}, ""
	default:
		if unsafeActions[tok] {
			return nil, tok + ": action not permitted (unsafe)"
		}
		return nil, "unknown primary: " + tok
	}
}

func parseName(tokens *tokenStream, caseInsensitive bool) (*expr, string) {
	pattern := tokens.next()
	if pattern == "" {
		return nil, "missing argument to -name"
	}
	matchPattern := pattern
	if caseInsensitive {
		matchPattern = strings.ToLower(pattern)
	}
	if _, err := filepath.Match(matchPattern, ""); err != nil {
		return nil, "invalid pattern: " + pattern
	}
	return &expr{kind: kindTest, test: func(path string, info fs.FileInfo, depth int) bool {
		name := info.Name()
		if caseInsensitive {
			name = strings.ToLower(name)
		}
		matched, _ := filepath.Match(matchPattern, name)
		return matched
	}}, ""
}

func parsePath(tokens *tokenStream, caseInsensitive bool) (*expr, string) {
	pattern := tokens.next()
	if pattern == "" {
		return nil, "missing argument to -path"
	}
	matchPattern := pattern
	if caseInsensitive {
		matchPattern = strings.ToLower(pattern)
	}
	return &expr{kind: kindTest, test: func(path string, info fs.FileInfo, depth int) bool {
		p := filepath.ToSlash(path)
		if caseInsensitive {
			p = strings.ToLower(p)
		}
		return pathGlobMatch(matchPattern, p)
	}}, ""
}

func parseType(tokens *tokenStream) (*expr, string) {
	typeArg := tokens.next()
	if typeArg == "" {
		return nil, "missing argument to -type"
	}
	types := make(map[byte]bool)
	for _, part := range strings.Split(typeArg, ",") {
		if len(part) != 1 {
			return nil, "invalid type: " + typeArg
		}
		c := part[0]
		switch c {
		case 'f', 'd', 'l', 'p', 's', 'b', 'c':
			types[c] = true
		default:
			return nil, "invalid type: " + typeArg
		}
	}
	return &expr{kind: kindTest, test: func(path string, info fs.FileInfo, depth int) bool {
		mode := info.Mode()
		switch {
		case mode.IsRegular():
			return types['f']
		case mode.IsDir():
			return types['d']
		case mode&os.ModeSymlink != 0:
			return types['l']
		case mode&os.ModeNamedPipe != 0:
			return types['p']
		case mode&os.ModeSocket != 0:
			return types['s']
		case mode&os.ModeDevice != 0 && mode&os.ModeCharDevice == 0:
			return types['b']
		case mode&os.ModeCharDevice != 0:
			return types['c']
		}
		return false
	}}, ""
}

func parseSize(tokens *tokenStream) (*expr, string) {
	sizeArg := tokens.next()
	if sizeArg == "" {
		return nil, "missing argument to -size"
	}
	cmp, rest := parseNumericPrefix(sizeArg)

	var unitBytes int64 = 512 // default: 512-byte blocks
	if len(rest) > 0 {
		lastChar := rest[len(rest)-1]
		switch lastChar {
		case 'c':
			unitBytes = 1
			rest = rest[:len(rest)-1]
		case 'w':
			unitBytes = 2
			rest = rest[:len(rest)-1]
		case 'b':
			unitBytes = 512
			rest = rest[:len(rest)-1]
		case 'k':
			unitBytes = 1024
			rest = rest[:len(rest)-1]
		case 'M':
			unitBytes = 1024 * 1024
			rest = rest[:len(rest)-1]
		case 'G':
			unitBytes = 1024 * 1024 * 1024
			rest = rest[:len(rest)-1]
		default:
			if lastChar < '0' || lastChar > '9' {
				return nil, "invalid size: " + sizeArg
			}
		}
	}

	n, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || n < 0 {
		return nil, "invalid size: " + sizeArg
	}

	return &expr{kind: kindTest, test: func(path string, info fs.FileInfo, depth int) bool {
		size := info.Size()
		var fileUnits int64
		if unitBytes == 1 {
			fileUnits = size
		} else {
			fileUnits = size/unitBytes + boolToInt64(size%unitBytes != 0)
		}
		return compareNumeric(cmp, fileUnits, n)
	}}, ""
}

func parseNewer(callCtx *builtins.CallContext, tokens *tokenStream, state *walkState) (*expr, string) {
	refFile := tokens.next()
	if refFile == "" {
		return nil, "missing argument to -newer"
	}
	refInfo, err := callCtx.Stat(state.ctx, refFile)
	if err != nil {
		return nil, "cannot stat " + refFile + ": " + callCtx.PortableErr(err)
	}
	refTime := refInfo.ModTime()
	return &expr{kind: kindTest, test: func(path string, info fs.FileInfo, depth int) bool {
		return info.ModTime().After(refTime)
	}}, ""
}

func parseMtime(tokens *tokenStream) (*expr, string) {
	mtimeArg := tokens.next()
	if mtimeArg == "" {
		return nil, "missing argument to -mtime"
	}
	cmp, rest := parseNumericPrefix(mtimeArg)
	n, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || n < 0 {
		return nil, "invalid argument to -mtime: " + mtimeArg
	}
	return &expr{kind: kindTest, test: func(path string, info fs.FileInfo, depth int) bool {
		// Age in days (fractional days rounded down, GNU find style)
		ageSec := currentTime().Sub(info.ModTime()).Seconds()
		ageDays := int64(ageSec / secondsPerDay)
		return compareNumeric(cmp, ageDays, n)
	}}, ""
}

func parsePerm(tokens *tokenStream) (*expr, string) {
	permArg := tokens.next()
	if permArg == "" {
		return nil, "missing argument to -perm"
	}

	mode := permArg
	matchAny := false
	matchAll := false
	if strings.HasPrefix(mode, "/") {
		matchAny = true
		mode = mode[1:]
	} else if strings.HasPrefix(mode, "-") {
		matchAll = true
		mode = mode[1:]
	}

	perm, err := strconv.ParseUint(mode, 8, 32)
	if err != nil {
		return nil, "invalid mode: " + permArg
	}
	permBits := os.FileMode(perm)

	return &expr{kind: kindTest, test: func(path string, info fs.FileInfo, depth int) bool {
		filePerm := info.Mode().Perm()
		if matchAll {
			return filePerm&permBits == permBits
		}
		if matchAny {
			if permBits == 0 {
				return true
			}
			return filePerm&permBits != 0
		}
		return filePerm == permBits
	}}, ""
}

func parseLinks(tokens *tokenStream) (*expr, string) {
	linksArg := tokens.next()
	if linksArg == "" {
		return nil, "missing argument to -links"
	}
	cmp, rest := parseNumericPrefix(linksArg)
	n, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || n < 0 {
		return nil, "invalid argument to -links: " + linksArg
	}
	return &expr{kind: kindTest, test: func(path string, info fs.FileInfo, depth int) bool {
		return compareNumeric(cmp, nlinks(info), n)
	}}, ""
}

type numericComparison int

const (
	cmpExact numericComparison = iota
	cmpGreater
	cmpLess
)

func parseNumericPrefix(s string) (numericComparison, string) {
	if len(s) > 0 {
		switch s[0] {
		case '+':
			return cmpGreater, s[1:]
		case '-':
			return cmpLess, s[1:]
		}
	}
	return cmpExact, s
}

func compareNumeric(cmp numericComparison, actual, expected int64) bool {
	switch cmp {
	case cmpGreater:
		return actual > expected
	case cmpLess:
		return actual < expected
	default:
		return actual == expected
	}
}

func boolToInt64(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func defaultPrintExpr(callCtx *builtins.CallContext) *expr {
	return &expr{kind: kindAction, action: func(path string) bool {
		callCtx.Outf("%s\n", path)
		return false
	}}
}

func printHelp(callCtx *builtins.CallContext) {
	callCtx.Out("Usage: find [path...] [expression]\n")
	callCtx.Out("Search for files in a directory hierarchy.\n\n")
	callCtx.Out("Tests: -name, -iname, -path, -ipath, -wholename, -iwholename,\n")
	callCtx.Out("       -type, -empty, -size, -newer, -mtime, -perm, -links,\n")
	callCtx.Out("       -true, -false\n")
	callCtx.Out("Actions: -print, -print0, -prune, -quit\n")
	callCtx.Out("Options: -maxdepth N, -mindepth N, -depth\n")
	callCtx.Out("Operators: ( ), !, -not, -a, -and, -o, -or\n\n")
	callCtx.Out("  -h, --help   print usage and exit\n")
}

// pathGlobMatch matches a path against a glob pattern where '*' matches any
// character including path separators (unlike filepath.Match).
func pathGlobMatch(pattern, name string) bool {
	return pathGlobMatchDepth(pattern, name, 0)
}

func pathGlobMatchDepth(pattern, name string, depth int) bool {
	if depth > maxGlobRecursion {
		return false
	}
	for len(pattern) > 0 {
		switch pattern[0] {
		case '*':
			pattern = pattern[1:]
			if len(pattern) == 0 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if pathGlobMatchDepth(pattern, name[i:], depth+1) {
					return true
				}
			}
			return false
		case '?':
			if len(name) == 0 {
				return false
			}
			pattern = pattern[1:]
			name = name[1:]
		case '\\':
			pattern = pattern[1:]
			if len(pattern) == 0 {
				return false
			}
			if len(name) == 0 || pattern[0] != name[0] {
				return false
			}
			pattern = pattern[1:]
			name = name[1:]
		case '[':
			if len(name) == 0 {
				return false
			}
			end := strings.IndexByte(pattern, ']')
			if end < 0 {
				return false
			}
			matched, _ := filepath.Match(pattern[:end+1], string(name[0]))
			if !matched {
				return false
			}
			pattern = pattern[end+1:]
			name = name[1:]
		default:
			if len(name) == 0 || pattern[0] != name[0] {
				return false
			}
			pattern = pattern[1:]
			name = name[1:]
		}
	}
	return len(name) == 0
}

type errMissingArg string

func (e errMissingArg) Error() string {
	return "missing argument to " + string(e)
}

type errInvalidArg struct {
	flag string
	val  string
}

func (e errInvalidArg) Error() string {
	return "invalid argument " + e.val + " to " + e.flag
}
