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
	"io"
	iofs "io/fs"
	"strings"
	"time"

	"github.com/DataDog/rshell/interp/builtins"
)

// maxTraversalDepth limits directory recursion depth to prevent resource
// exhaustion. This is an intentional safety divergence from GNU find (which
// has no depth limit): the shell is designed for AI agent use where safety
// is the primary goal. When the user provides -maxdepth exceeding this
// limit, a warning is emitted and the value is clamped. Without -maxdepth,
// this cap applies silently as a defense-in-depth measure.
const maxTraversalDepth = 256

// Cmd is the find builtin command descriptor.
var Cmd = builtins.Command{Name: "find", MakeFlags: builtins.NoFlags(run)}

func run(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	// Parse global options (-L) and separate paths from expression.
	followLinks := false
	i := 0

	// Parse leading global options.
optLoop:
	for i < len(args) {
		switch args[i] {
		case "-L":
			followLinks = true
			i++
		case "-P":
			// -P overrides any earlier -L (last option wins).
			followLinks = false
			i++
		case "-H":
			callCtx.Errf("find: -H is not supported\n")
			return builtins.Result{Code: 1}
		case "--":
			i++ // consume --; stop option parsing
			break optLoop
		default:
			break optLoop
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
		if ref == "" {
			callCtx.Errf("find: '': No such file or directory\n")
			eagerNewerErrors[ref] = true
			failed = true
			continue
		}
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
				failed = true
				break
			}
			// Reject empty path operands — GNU find treats "" as a
			// non-existent path but continues walking remaining paths.
			if startPath == "" {
				callCtx.Errf("find: '': No such file or directory\n")
				failed = true
				continue
			}
			if walkPath(ctx, callCtx, startPath, walkOptions{
				expression:       expression,
				implicitPrint:    implicitPrint,
				followLinks:      followLinks,
				maxDepth:         maxDepth,
				minDepth:         minDepth,
				eagerNewerErrors: eagerNewerErrors,
			}) {
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
	if arg == "!" || arg == "(" {
		return true
	}
	return strings.HasPrefix(arg, "-") && len(arg) > 1
}

// walkOptions holds configuration for a single walkPath invocation.
type walkOptions struct {
	expression       *expr
	implicitPrint    bool
	followLinks      bool
	maxDepth         int
	minDepth         int
	eagerNewerErrors map[string]bool
}

// walkPath walks the directory tree rooted at startPath, evaluating the
// expression for each entry. Returns true if any error occurred.
func walkPath(
	ctx context.Context,
	callCtx *builtins.CallContext,
	startPath string,
	opts walkOptions,
) bool {
	now := callCtx.Now()
	failed := false
	newerCache := map[string]time.Time{}
	newerErrors := map[string]bool{}
	for k, v := range opts.eagerNewerErrors {
		newerErrors[k] = v
	}

	// Stat the starting path.
	var startInfo iofs.FileInfo
	var err error
	if opts.followLinks {
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

	// dirIterator streams directory entries one at a time via ReadDir(1),
	// keeping memory usage proportional to tree depth, not directory width.
	type dirIterator struct {
		dir           iofs.ReadDirFile
		parentPath    string
		depth         int
		ancestorIDs   map[builtins.FileID]string
		ancestorPaths map[string]bool
		done          bool
	}

	// processEntry evaluates the expression for a single file entry.
	// Returns (prune, isLoop).
	processEntry := func(path string, info iofs.FileInfo, depth int, ancestorIDs map[builtins.FileID]string, ancestorPaths map[string]bool) (bool, bool, map[builtins.FileID]string, map[string]bool) {
		// With -L, detect symlink loops BEFORE evaluating predicates.
		var childAncestorIDs map[builtins.FileID]string
		var childAncestorPaths map[string]bool
		if info.IsDir() && opts.followLinks {
			idOK := false
			if callCtx.FileIdentity != nil {
				if id, ok := callCtx.FileIdentity(path, info); ok {
					idOK = true
					if firstPath, seen := ancestorIDs[id]; seen {
						callCtx.Errf("find: File system loop detected; '%s' is part of the same file system loop as '%s'.\n",
							path, firstPath)
						failed = true
						return false, true, nil, nil
					}
					childAncestorIDs = make(map[builtins.FileID]string, len(ancestorIDs)+1)
					for k, v := range ancestorIDs {
						childAncestorIDs[k] = v
					}
					childAncestorIDs[id] = path
				}
			}
			if !idOK {
				if ancestorPaths[path] {
					callCtx.Errf("find: File system loop detected; '%s' has already been visited.\n", path)
					failed = true
					return false, true, nil, nil
				}
				childAncestorPaths = make(map[string]bool, len(ancestorPaths)+1)
				for k := range ancestorPaths {
					childAncestorPaths[k] = true
				}
				childAncestorPaths[path] = true
			}
		}

		ec := &evalContext{
			callCtx:     callCtx,
			ctx:         ctx,
			now:         now,
			relPath:     path,
			info:        info,
			depth:       depth,
			printPath:   path,
			newerCache:  newerCache,
			newerErrors: newerErrors,
			followLinks: opts.followLinks,
		}

		prune := false
		if depth >= opts.minDepth {
			result := evaluate(ec, opts.expression)
			prune = result.prune
			if len(newerErrors) > 0 || ec.failed {
				failed = true
			}
			if result.matched && opts.implicitPrint {
				callCtx.Outf("%s\n", path)
			}
		}

		return prune, false, childAncestorIDs, childAncestorPaths
	}

	// Process the starting path.
	prune, isLoop, childAncIDs, childAncPaths := processEntry(startPath, startInfo, 0, nil, nil)

	// Set up the iterator stack. Each open directory keeps a file handle
	// that reads one entry at a time, so memory is O(depth) not O(width).
	var iterStack []*dirIterator

	if !isLoop && !prune && startInfo.IsDir() && 0 < opts.maxDepth {
		dir, openErr := callCtx.OpenDir(ctx, startPath)
		if openErr != nil {
			callCtx.Errf("find: '%s': %s\n", startPath, callCtx.PortableErr(openErr))
			return true
		}
		iterStack = append(iterStack, &dirIterator{
			dir:           dir,
			parentPath:    startPath,
			depth:         1,
			ancestorIDs:   childAncIDs,
			ancestorPaths: childAncPaths,
		})
	}

	for len(iterStack) > 0 {
		if ctx.Err() != nil {
			failed = true
			break
		}

		top := iterStack[len(iterStack)-1]
		if top.done {
			top.dir.Close()
			iterStack = iterStack[:len(iterStack)-1]
			continue
		}

		// Read one entry at a time from the directory.
		dirEntries, readErr := top.dir.ReadDir(1)
		if readErr != nil {
			if readErr != io.EOF {
				callCtx.Errf("find: '%s': %s\n", top.parentPath, callCtx.PortableErr(readErr))
				failed = true
			}
			top.done = true
			continue
		}
		if len(dirEntries) == 0 {
			top.done = true
			continue
		}

		child := dirEntries[0]
		childPath := joinPath(top.parentPath, child.Name())

		var childInfo iofs.FileInfo
		if opts.followLinks {
			childInfo, err = callCtx.StatFile(ctx, childPath)
			if err != nil {
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

		prune, isLoop, cAncIDs, cAncPaths := processEntry(childPath, childInfo, top.depth, top.ancestorIDs, top.ancestorPaths)
		if isLoop {
			continue
		}

		// Descend into child directories unless pruned or beyond maxdepth.
		if childInfo.IsDir() && !prune && top.depth < opts.maxDepth {
			dir, openErr := callCtx.OpenDir(ctx, childPath)
			if openErr != nil {
				callCtx.Errf("find: '%s': %s\n", childPath, callCtx.PortableErr(openErr))
				failed = true
				continue
			}
			iterStack = append(iterStack, &dirIterator{
				dir:           dir,
				parentPath:    childPath,
				depth:         top.depth + 1,
				ancestorIDs:   cAncIDs,
				ancestorPaths: cAncPaths,
			})
		}
	}

	// Close any remaining open directory handles (e.g. on context cancellation).
	for _, it := range iterStack {
		it.dir.Close()
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
