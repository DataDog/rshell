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
	"errors"
	iofs "io/fs"
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
		} else if args[i] == "-P" {
			// -P overrides any earlier -L (last option wins).
			followLinks = false
			i++
		} else if args[i] == "-H" {
			callCtx.Errf("find: -H is not supported\n")
			return builtins.Result{Code: 1}
		} else if args[i] == "--" {
			i++ // consume --; stop option parsing
			break
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

	// Parse expression (includes -maxdepth/-mindepth as parser-recognized
	// options). The recursive-descent parser naturally handles token ownership,
	// so depth options can appear in any position without stealing arguments
	// from other predicates.
	exprArgs := args[i:]
	pr, err := parseExpression(exprArgs)
	if err != nil {
		callCtx.Errf("%s\n", err.Error())
		return builtins.Result{Code: 1}
	}
	expression := pr.expr

	maxDepth := pr.maxDepth
	if maxDepth < 0 {
		maxDepth = maxTraversalDepth
	}
	if maxDepth > maxTraversalDepth {
		callCtx.Errf("find: warning: -maxdepth %d exceeds safety limit %d; clamped to %d\n", maxDepth, maxTraversalDepth, maxTraversalDepth)
		maxDepth = maxTraversalDepth
	}
	minDepth := pr.minDepth
	if minDepth < 0 {
		minDepth = 0
	}

	// If no explicit action, add implicit -print.
	implicitPrint := expression == nil || !hasAction(expression)

	// Eagerly validate -newer reference paths before walking.
	// GNU find always reports missing reference files even if short-circuiting
	// or -mindepth prevents the predicate from being evaluated.
	failed := false
	eagerNewerErrors := map[string]bool{}
	seen := map[string]bool{}
	for _, ref := range collectNewerRefs(expression) {
		if seen[ref] {
			continue
		}
		seen[ref] = true
		statRef := callCtx.LstatFile
		if followLinks {
			statRef = callCtx.StatFile
		}
		if _, err := statRef(ctx, ref); err != nil {
			callCtx.Errf("find: '%s': %s\n", ref, callCtx.PortableErr(err))
			eagerNewerErrors[ref] = true
			failed = true
		}
	}

	// GNU find treats a missing -newer reference as a fatal argument error
	// and produces no result set, so skip the walk entirely.
	if !failed {
		for _, startPath := range paths {
			if ctx.Err() != nil {
				break
			}
			if walkPath(ctx, callCtx, startPath, expression, implicitPrint, followLinks, maxDepth, minDepth, eagerNewerErrors) {
				failed = true
			}
		}
	}

	if failed {
		return builtins.Result{Code: 1}
	}
	return builtins.Result{}
}

// isExpressionStart returns true if the argument starts a find expression.
// GNU find treats any dash-prefixed token with length > 1 as an expression
// token (not a path), so `-1` is an unknown predicate, not a path argument.
func isExpressionStart(arg string) bool {
	if arg == "!" || arg == "(" || arg == ")" {
		return true
	}
	return strings.HasPrefix(arg, "-") && len(arg) > 1
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
	eagerNewerErrors map[string]bool,
) bool {
	now := callCtx.Now()
	failed := false
	newerCache := map[string]time.Time{}
	newerErrors := map[string]bool{}
	for k, v := range eagerNewerErrors {
		newerErrors[k] = v
	}

	// Stat the starting path.
	var startInfo iofs.FileInfo
	var err error
	if followLinks {
		startInfo, err = callCtx.StatFile(ctx, startPath)
		if err != nil && errors.Is(err, iofs.ErrNotExist) {
			// Dangling symlink root: fall back to lstat like child entries.
			startInfo, err = callCtx.LstatFile(ctx, startPath)
		}
	} else {
		startInfo, err = callCtx.LstatFile(ctx, startPath)
	}
	if err != nil {
		callCtx.Errf("find: '%s': %s\n", startPath, callCtx.PortableErr(err))
		return true
	}

	// Cycle detection for -L mode: track ancestor directory identities
	// (dev+inode on Unix, volume serial+file index on Windows) along the
	// path from root to the current node. This correctly allows multiple
	// symlinks to the same target (no ancestor cycle) while detecting
	// actual loops. File identity is attempted per-entry; if it fails for
	// a specific directory, we fall back to path-based ancestor tracking
	// for that subtree. The maxTraversalDepth=256 cap remains as an
	// ultimate safety bound.

	// Use an explicit stack for traversal to avoid Go recursion depth issues.
	type stackEntry struct {
		path          string
		info          iofs.FileInfo
		depth         int
		ancestorIDs   map[builtins.FileID]string // ancestor dir identities (root→parent)
		ancestorPaths map[string]bool             // fallback: ancestor dir paths
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

		// With -L, detect symlink loops BEFORE evaluating predicates.
		// GNU find does not print or evaluate a directory that forms a loop;
		// it only reports the error and skips the entry entirely.
		var childAncestorIDs map[builtins.FileID]string
		var childAncestorPaths map[string]bool
		isLoop := false
		if entry.info.IsDir() && followLinks {
			idOK := false
			if callCtx.FileIdentity != nil {
				if id, ok := callCtx.FileIdentity(entry.path, entry.info); ok {
					idOK = true
					if firstPath, seen := entry.ancestorIDs[id]; seen {
						callCtx.Errf("find: File system loop detected; '%s' is part of the same file system loop as '%s'.\n",
							entry.path, firstPath)
						failed = true
						isLoop = true
					} else {
						// Build ancestor set for children: parent's ancestors + this dir.
						childAncestorIDs = make(map[builtins.FileID]string, len(entry.ancestorIDs)+1)
						for k, v := range entry.ancestorIDs {
							childAncestorIDs[k] = v
						}
						childAncestorIDs[id] = entry.path
					}
				}
			}
			if !idOK && !isLoop {
				// Fall back to path-based tracking. Lexical paths cannot
				// detect symlink cycles perfectly, but maxTraversalDepth=256
				// provides the ultimate safety bound.
				if entry.ancestorPaths[entry.path] {
					callCtx.Errf("find: File system loop detected; '%s' has already been visited.\n", entry.path)
					failed = true
					isLoop = true
				} else {
					childAncestorPaths = make(map[string]bool, len(entry.ancestorPaths)+1)
					for k := range entry.ancestorPaths {
						childAncestorPaths[k] = true
					}
					childAncestorPaths[entry.path] = true
				}
			}
		}
		if isLoop {
			continue
		}

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
			followLinks: followLinks,
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
						// Only fall back to lstat for broken symlinks (target missing).
						// Permission denied, sandbox blocked, etc. should be reported as-is.
						if errors.Is(err, iofs.ErrNotExist) {
							childInfo, err = callCtx.LstatFile(ctx, childPath)
						}
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
					path:          childPath,
					info:          childInfo,
					depth:         entry.depth + 1,
					ancestorIDs:   childAncestorIDs,
					ancestorPaths: childAncestorPaths,
				})
			}
		}
	}

	return failed
}

// collectNewerRefs walks the expression tree and returns all -newer reference paths.
func collectNewerRefs(e *expr) []string {
	if e == nil {
		return nil
	}
	if e.kind == exprNewer {
		return []string{e.strVal}
	}
	var refs []string
	refs = append(refs, collectNewerRefs(e.left)...)
	refs = append(refs, collectNewerRefs(e.right)...)
	refs = append(refs, collectNewerRefs(e.operand)...)
	return refs
}

// joinPath joins a directory and a name with a forward slash.
// The shell normalises all paths to forward slashes on all platforms,
// so hardcoding '/' is correct even on Windows.
func joinPath(dir, name string) string {
	if len(dir) == 0 {
		return name
	}
	if dir[len(dir)-1] == '/' {
		return dir + name
	}
	return dir + "/" + name
}
