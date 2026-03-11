// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package find implements the find builtin command.
//
// find — search for files in a directory hierarchy
//
// Usage: find [-L] [PATH...] [EXPRESSION]
//
// Search the directory tree rooted at each PATH, evaluating the given
// EXPRESSION for each file found. If no PATH is given, the current
// directory (.) is used. If no EXPRESSION is given, -print is implied.
//
// Global options:
//
//	-L  Follow symbolic links.
//
// Supported predicates:
//
//	-name PATTERN    — basename matches shell glob PATTERN
//	-iname PATTERN   — like -name but case-insensitive
//	-path PATTERN    — full path matches shell glob PATTERN
//	-ipath PATTERN   — like -path but case-insensitive
//	-type TYPE       — file type: f (regular), d (directory), l (symlink),
//	                   p (named pipe), s (socket). Comma-separated for OR.
//	-size N[cwbkMG]  — file size. +N = greater, -N = less, N = exact.
//	-empty           — empty regular file or directory
//	-newer FILE      — modified more recently than FILE
//	-mtime N         — modified N days ago (+N = more, -N = less)
//	-mmin N          — modified N minutes ago (+N = more, -N = less)
//	-maxdepth N      — descend at most N levels
//	-mindepth N      — apply tests only at depth >= N
//	-print           — print path followed by newline
//	-print0          — print path followed by NUL
//	-prune           — skip directory subtree
//	-true            — always true
//	-false           — always false
//
// Operators:
//
//	( EXPR )         — grouping
//	! EXPR, -not EXPR — negation
//	EXPR -a EXPR, EXPR -and EXPR, EXPR EXPR — conjunction (implicit)
//	EXPR -o EXPR, EXPR -or EXPR — disjunction
//
// Blocked predicates (sandbox safety):
//
//	-exec, -execdir, -delete, -ok, -okdir — execution/deletion
//	-fls, -fprint, -fprint0, -fprintf — file writes
//	-regex, -iregex — ReDoS risk
//
// Exit codes:
//
//	0  All paths searched successfully.
//	1  At least one error occurred.
package find

import (
	"context"
	iofs "io/fs"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/rshell/interp/builtins"
)

// maxTraversalDepth limits directory recursion depth to prevent exhaustion.
const maxTraversalDepth = 256

// Cmd is the find builtin command descriptor.
var Cmd = builtins.Command{Name: "find", MakeFlags: builtins.NoFlags(run)}

func run(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	// Parse global options (-L) and separate paths from expression.
	followLinks := false
	i := 0

	// Parse leading global options.
	for i < len(args) {
		if args[i] == "-L" {
			followLinks = true
			i++
		} else if args[i] == "-P" || args[i] == "-H" {
			// -P is default (no follow), -H follows only for command-line args.
			// We treat -H same as -P for simplicity.
			i++
		} else {
			break
		}
	}

	// Separate paths from expression. Paths are args before the first
	// expression token (anything starting with - or ! or ( or )).
	var paths []string
	for i < len(args) {
		arg := args[i]
		if isExpressionStart(arg) {
			break
		}
		paths = append(paths, arg)
		i++
	}

	if len(paths) == 0 {
		paths = []string{"."}
	}

	// Parse -maxdepth and -mindepth from leading expression args only.
	// GNU find treats these as "global options" that should appear before
	// test predicates (it warns: "you have used a non-option after a test").
	// Parsing them from arbitrary positions would corrupt predicate arguments
	// (e.g. find . -name -maxdepth would consume the -name argument).
	// Commands like "find . -name '*.go' -maxdepth 1" are intentionally
	// unsupported; use "find . -maxdepth 1 -name '*.go'" instead.
	exprArgs := args[i:]
	maxDepth := maxTraversalDepth
	minDepth := 0
	j := 0
	for j < len(exprArgs) {
		if exprArgs[j] == "-maxdepth" {
			j++
			if j >= len(exprArgs) {
				callCtx.Errf("find: missing argument to '-maxdepth'\n")
				return builtins.Result{Code: 1}
			}
			n, err := strconv.Atoi(exprArgs[j])
			if err != nil || n < 0 {
				callCtx.Errf("find: invalid argument '%s' to -maxdepth\n", exprArgs[j])
				return builtins.Result{Code: 1}
			}
			maxDepth = n
			if maxDepth > maxTraversalDepth {
				maxDepth = maxTraversalDepth
			}
			j++
			continue
		}
		if exprArgs[j] == "-mindepth" {
			j++
			if j >= len(exprArgs) {
				callCtx.Errf("find: missing argument to '-mindepth'\n")
				return builtins.Result{Code: 1}
			}
			n, err := strconv.Atoi(exprArgs[j])
			if err != nil || n < 0 {
				callCtx.Errf("find: invalid argument '%s' to -mindepth\n", exprArgs[j])
				return builtins.Result{Code: 1}
			}
			minDepth = n
			j++
			continue
		}
		break // stop at first non-depth-option
	}
	filteredArgs := exprArgs[j:]

	// Parse expression.
	expression, err := parseExpression(filteredArgs)
	if err != nil {
		callCtx.Errf("%s\n", err.Error())
		return builtins.Result{Code: 1}
	}

	// If no explicit action, add implicit -print.
	implicitPrint := expression == nil || !hasAction(expression)

	failed := false
	for _, startPath := range paths {
		if ctx.Err() != nil {
			break
		}
		if walkPath(ctx, callCtx, startPath, expression, implicitPrint, followLinks, maxDepth, minDepth) {
			failed = true
		}
	}

	if failed {
		return builtins.Result{Code: 1}
	}
	return builtins.Result{}
}

// isExpressionStart returns true if the argument starts a find expression.
func isExpressionStart(arg string) bool {
	if arg == "!" || arg == "(" || arg == ")" {
		return true
	}
	if strings.HasPrefix(arg, "-") && len(arg) > 1 {
		// Distinguish expression predicates from paths like "-" or paths
		// that happen to start with "-" (unlikely but possible).
		// All find predicates start with a letter after the dash.
		c := arg[1]
		return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	return false
}

// walkPath walks the directory tree rooted at startPath, evaluating the
// expression for each entry. Returns true if any error occurred.
func walkPath(
	ctx context.Context,
	callCtx *builtins.CallContext,
	startPath string,
	expression *expr,
	implicitPrint bool,
	followLinks bool,
	maxDepth int,
	minDepth int,
) bool {
	now := callCtx.Now()
	failed := false
	newerCache := map[string]time.Time{}
	newerErrors := map[string]bool{}

	// visited tracks directory paths already traversed when following
	// symlinks (-L) to detect and break symlink loops. Without this,
	// cyclic symlinks would expand until maxTraversalDepth, causing
	// excessive CPU/memory usage.
	//
	// Limitation: We use path strings because the syscall package
	// (needed for dev+inode tracking) is banned by the import allowlist.
	// Path-based detection can miss cycles that re-enter the same
	// directory under different textual paths (e.g. dir/link/link/...).
	// The maxTraversalDepth=256 cap provides the ultimate safety bound
	// for cases the visited-set misses, consistent with ls -R.
	var visited map[string]bool
	if followLinks {
		visited = map[string]bool{}
	}

	// Stat the starting path.
	var startInfo iofs.FileInfo
	var err error
	if followLinks {
		startInfo, err = callCtx.StatFile(ctx, startPath)
	} else {
		startInfo, err = callCtx.LstatFile(ctx, startPath)
	}
	if err != nil {
		callCtx.Errf("find: '%s': %s\n", startPath, callCtx.PortableErr(err))
		return true
	}

	// Use an explicit stack for traversal to avoid Go recursion depth issues.
	type stackEntry struct {
		path  string
		info  iofs.FileInfo
		depth int
	}

	stack := []stackEntry{{path: startPath, info: startInfo, depth: 0}}

	for len(stack) > 0 {
		if ctx.Err() != nil {
			break
		}

		// Pop from the end (DFS).
		entry := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		// Build the print path — this is what gets printed and matched.
		printPath := entry.path

		ec := &evalContext{
			callCtx:     callCtx,
			ctx:         ctx,
			now:         now,
			relPath:     entry.path,
			info:        entry.info,
			depth:       entry.depth,
			printPath:   printPath,
			newerCache:  newerCache,
			newerErrors: newerErrors,
		}

		// Evaluate expression at this depth.
		prune := false
		if entry.depth >= minDepth {
			result := evaluate(ec, expression)
			prune = result.prune
			if len(newerErrors) > 0 {
				failed = true
			}

			if result.matched && implicitPrint {
				callCtx.Outf("%s\n", printPath)
			}
		}

		// Descend into directories unless pruned or beyond maxdepth.
		if entry.info.IsDir() && !prune && entry.depth < maxDepth {
			// With -L, check for symlink loops by tracking visited directory paths.
			if visited != nil {
				if visited[entry.path] {
					continue // skip already-visited directory (symlink loop)
				}
				visited[entry.path] = true
			}

			entries, readErr := callCtx.ReadDir(ctx, entry.path)
			if readErr != nil {
				callCtx.Errf("find: '%s': %s\n", entry.path, callCtx.PortableErr(readErr))
				failed = true
				continue
			}

			// Add children in reverse order so they come off the stack in
			// alphabetical order (DFS with correct ordering).
			// NOTE: ReadDir returns entries sorted by name (see builtins.go),
			// so find output is always alphabetically ordered. This intentionally
			// diverges from GNU find, which uses filesystem-dependent readdir order.
			for j := len(entries) - 1; j >= 0; j-- {
				if ctx.Err() != nil {
					break
				}
				child := entries[j]
				childPath := joinPath(entry.path, child.Name())

				var childInfo iofs.FileInfo
				if followLinks {
					childInfo, err = callCtx.StatFile(ctx, childPath)
					if err != nil {
						// If stat fails on a symlink target, fall back to lstat.
						childInfo, err = callCtx.LstatFile(ctx, childPath)
						if err != nil {
							callCtx.Errf("find: '%s': %s\n", childPath, callCtx.PortableErr(err))
							failed = true
							continue
						}
					}
				} else {
					childInfo, err = callCtx.LstatFile(ctx, childPath)
					if err != nil {
						callCtx.Errf("find: '%s': %s\n", childPath, callCtx.PortableErr(err))
						failed = true
						continue
					}
				}

				stack = append(stack, stackEntry{
					path:  childPath,
					info:  childInfo,
					depth: entry.depth + 1,
				})
			}
		}
	}

	return failed
}

// joinPath joins a directory and a name with a forward slash.
func joinPath(dir, name string) string {
	if len(dir) == 0 {
		return name
	}
	if dir[len(dir)-1] == '/' {
		return dir + name
	}
	return dir + "/" + name
}
