// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package find implements the find builtin command.
//
// find — search for files in a directory hierarchy
//
// Usage: find [-H] [-L] [-P] [PATH...] [EXPRESSION]
//
// Search the directory tree rooted at each PATH, evaluating the given
// EXPRESSION for each file found. If no PATH is given, the current
// directory (.) is used. If no EXPRESSION is given, -print is implied.
//
// Global options:
//
//	--help  Print usage information and exit.
//	-L      Follow symbolic links.
//
// Supported predicates:
//
//	-name PATTERN    — basename matches shell glob PATTERN
//	-iname PATTERN   — like -name but case-insensitive
//	-path PATTERN    — full path matches shell glob PATTERN
//	-ipath PATTERN   — like -path but case-insensitive
//	-type TYPE       — file type: b,c,d,f,l,p,s. Comma-separated for OR.
//	-size N[cwbkMG]  — file size. +N = greater, -N = less, N = exact.
//	-empty           — empty regular file or directory
//	-newer FILE      — modified more recently than FILE
//	-mtime N         — modified N days ago (+N = more, -N = less)
//	-mmin N          — modified N minutes ago (+N = more, -N = less)
//	-atime N         — accessed N days ago (+N = more, -N = less)
//	-amin N          — accessed N minutes ago (+N = more, -N = less)
//	-ctime N         — status changed N days ago (+N = more, -N = less)
//	-cmin N          — status changed N minutes ago (+N = more, -N = less)
//	-readable        — file is readable by the current user
//	-writable        — file is writable by the current user
//	-executable      — file is executable by the current user
//	-perm MODE       — permission bits match MODE (octal or symbolic)
//	-user NAME       — file owned by user NAME (name or numeric UID)
//	-group NAME      — file belongs to group NAME (name or numeric GID)
//	-uid N           — file's numeric user ID matches N
//	-gid N           — file's numeric group ID matches N
//	-nouser          — no user corresponds to file's numeric user ID
//	-nogroup         — no group corresponds to file's numeric group ID
//	-links N         — file has N hard links
//	-inum N          — file has inode number N
//	-samefile FILE   — file refers to the same inode as FILE
//	-maxdepth N      — descend at most N levels
//	-mindepth N      — apply tests only at depth >= N
//	-daystart        — measure times from start of today
//	-depth           — process directory contents before directory itself
//	-mount / -xdev   — don't descend into other filesystems
//	-print           — print path followed by newline
//	-print0          — print path followed by NUL
//	-ls              — print file details in ls -dils format
//	-printf FORMAT   — print formatted output
//	-prune           — skip directory subtree
//	-quit            — exit immediately
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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/rshell/builtins"
)

// isNotExist checks whether an error represents a "not found" condition.
// The sandbox's PortablePathError wraps errors with errors.New(), stripping
// the fs.ErrNotExist sentinel, so we check both errors.Is and the string.
func isNotExist(err error) bool {
	if os.IsNotExist(err) {
		return true
	}
	// PortablePathError rewrites the inner error as a plain string;
	// check for the canonical portable message.
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error() == "no such file or directory"
	}
	return false
}

// maxTraversalDepth limits directory recursion depth to prevent resource
// exhaustion. This is an intentional safety divergence from GNU find (which
// has no depth limit): the shell is designed for AI agent use where safety
// is the primary goal. When the user provides -maxdepth exceeding this
// limit, a warning is emitted and the value is clamped. Without -maxdepth,
// this cap applies silently as a defense-in-depth measure.
const maxTraversalDepth = 256

// Cmd is the find builtin command descriptor.
var Cmd = builtins.Command{Name: "find", Description: "search for files in a directory hierarchy", MakeFlags: builtins.NoFlags(run)}

func run(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	// Parse global options (-L) and separate paths from expression.
	followLinks := false
	i := 0

	// Parse leading global options.
optLoop:
	for i < len(args) {
		switch args[i] {
		case "--help":
			callCtx.Out("Usage: find [-H] [-L] [-P] [path...] [expression]\n\n")
			callCtx.Out("Search directory trees, evaluating an expression for each file found.\n")
			callCtx.Out("Default path is the current directory; default expression is -print.\n\n")
			callCtx.Out("Options:\n")
			callCtx.Out("  -L                         Follow symbolic links.\n")
			callCtx.Out("  -P                         Never follow symbolic links (default).\n\n")
			callCtx.Out("Tests:\n")
			callCtx.Out("  -name PATTERN              Base name matches shell glob PATTERN.\n")
			callCtx.Out("  -iname PATTERN             Like -name but case-insensitive.\n")
			callCtx.Out("  -path PATTERN              Full path matches shell glob PATTERN.\n")
			callCtx.Out("  -ipath PATTERN             Like -path but case-insensitive.\n")
			callCtx.Out("  -type TYPE                 File type: b,c,d,f,l,p,s. Comma-separated for OR.\n")
			callCtx.Out("  -size N[cwbkMG]            File size (+N=greater, -N=less, N=exact).\n")
			callCtx.Out("  -empty                     Empty regular file or directory.\n")
			callCtx.Out("  -newer FILE                Modified more recently than FILE.\n")
			callCtx.Out("  -mtime N                   Modified N days ago (+N=more, -N=less).\n")
			callCtx.Out("  -mmin N                    Modified N minutes ago (+N=more, -N=less).\n")
			callCtx.Out("  -atime N                   Accessed N days ago (+N=more, -N=less).\n")
			callCtx.Out("  -amin N                    Accessed N minutes ago (+N=more, -N=less).\n")
			callCtx.Out("  -ctime N                   Status changed N days ago (+N=more, -N=less).\n")
			callCtx.Out("  -cmin N                    Status changed N minutes ago (+N=more, -N=less).\n")
			callCtx.Out("  -readable                  File is readable by the current user.\n")
			callCtx.Out("  -writable                  File is writable by the current user.\n")
			callCtx.Out("  -executable                File is executable by the current user.\n")
			callCtx.Out("  -perm MODE                 Permission bits match MODE (octal or symbolic).\n")
			callCtx.Out("  -user NAME                 File is owned by user NAME (name or numeric UID).\n")
			callCtx.Out("  -group NAME                File belongs to group NAME (name or numeric GID).\n")
			callCtx.Out("  -uid N                     File's numeric user ID is N.\n")
			callCtx.Out("  -gid N                     File's numeric group ID is N.\n")
			callCtx.Out("  -nouser                    No user corresponds to file's numeric user ID.\n")
			callCtx.Out("  -nogroup                   No group corresponds to file's numeric group ID.\n")
			callCtx.Out("  -links N                   File has N hard links.\n")
			callCtx.Out("  -inum N                    File has inode number N.\n")
			callCtx.Out("  -samefile FILE             File refers to the same inode as FILE.\n")
			callCtx.Out("  -maxdepth N                Descend at most N levels.\n")
			callCtx.Out("  -mindepth N                Apply tests only at depth >= N.\n")
			callCtx.Out("  -daystart                  Measure times from start of today.\n")
			callCtx.Out("  -depth                     Process directory contents before directory itself.\n")
			callCtx.Out("  -mount / -xdev             Don't descend into other filesystems.\n")
			callCtx.Out("  -true                      Always true.\n")
			callCtx.Out("  -false                     Always false.\n\n")
			callCtx.Out("Actions:\n")
			callCtx.Out("  -print                     Print path followed by newline.\n")
			callCtx.Out("  -print0                    Print path followed by NUL.\n")
			callCtx.Out("  -ls                        Print file details in ls -dils format.\n")
			callCtx.Out("  -printf FORMAT             Print formatted output.\n")
			callCtx.Out("  -prune                     Skip directory subtree.\n")
			callCtx.Out("  -quit                      Exit immediately.\n\n")
			callCtx.Out("Operators:\n")
			callCtx.Out("  ( EXPR )                   Grouping.\n")
			callCtx.Out("  ! EXPR / -not EXPR         Negation.\n")
			callCtx.Out("  EXPR -a EXPR / EXPR -and EXPR  Conjunction (implicit).\n")
			callCtx.Out("  EXPR -o EXPR / EXPR -or EXPR   Disjunction.\n\n")
			callCtx.Out("Blocked predicates [sandbox]:\n")
			callCtx.Out("  -exec, -execdir, -delete, -ok, -okdir          Execution/deletion.\n")
			callCtx.Out("  -fls, -fprint, -fprint0, -fprintf              File writes.\n")
			callCtx.Out("  -regex, -iregex                                ReDoS risk.\n")
			return builtins.Result{}
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
		paths = append(paths, filepath.ToSlash(arg))
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

	// -depth + -prune: warn like GNU find.
	if pr.depthFirst && expression != nil && hasPrune(expression) {
		callCtx.Errf("find: warning: -prune is ignored when -depth is given\n")
	}

	// If no explicit action, add implicit -print.
	implicitPrint := expression == nil || !hasAction(expression)

	// Eagerly validate -newer reference paths before walking.
	// GNU find reports missing reference files even if short-circuiting
	// or -mindepth prevents the predicate from being evaluated.
	// With -L, stat the reference (following symlinks) to get the target
	// mtime; fall back to lstat for dangling symlinks.
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
			// With -L, stat fails on dangling symlinks — fall back to
			// lstat so the symlink's own mtime can be used. Only fall
			// back for "not found" errors; permission/sandbox errors
			// must be reported.
			if followLinks && isNotExist(err) {
				if _, lerr := callCtx.LstatFile(ctx, ref); lerr == nil {
					continue
				}
			}
			callCtx.Errf("find: '%s': %s\n", ref, callCtx.PortableErr(err))
			eagerNewerErrors[ref] = true
			failed = true
		}
	}

	// Eagerly validate -user/-group names before walking.
	// GNU find treats unknown user/group names as fatal argument errors.
	for _, name := range collectUserRefs(expression) {
		if _, err := strconv.ParseUint(name, 10, 32); err == nil {
			continue // numeric UID, always valid
		}
		if _, ok := lookupUidByName(name); !ok {
			callCtx.Errf("find: '%s' is not the name of a known user\n", name)
			failed = true
		}
	}
	for _, name := range collectGroupRefs(expression) {
		if _, err := strconv.ParseUint(name, 10, 32); err == nil {
			continue // numeric GID, always valid
		}
		if _, ok := lookupGidByName(name); !ok {
			callCtx.Errf("find: '%s' is not the name of a known group\n", name)
			failed = true
		}
	}

	// Capture invocation time once so -mtime/-mmin predicates use a
	// consistent reference across all root paths (matches GNU find).
	now := callCtx.Now()

	// -daystart: shift time reference to start of today (midnight local).
	if pr.daystart {
		y, m, d := now.Date()
		now = time.Date(y, m, d, 0, 0, 0, 0, now.Location())
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
			wr := walkPath(ctx, callCtx, startPath, walkOptions{
				expression:       expression,
				implicitPrint:    implicitPrint,
				followLinks:      followLinks,
				maxDepth:         maxDepth,
				minDepth:         minDepth,
				now:              now,
				eagerNewerErrors: eagerNewerErrors,
				depthFirst:       pr.depthFirst,
				mount:            pr.mount,
			})
			if wr.failed {
				failed = true
			}
			if wr.quit {
				break
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
	now              time.Time
	eagerNewerErrors map[string]bool
	depthFirst       bool // -depth: post-order traversal
	mount            bool // -mount/-xdev: don't cross filesystems
}

// walkResult holds the outcome of a walk operation.
type walkResult struct {
	failed bool // at least one error occurred
	quit   bool // -quit was triggered
}

// walkPath walks the directory tree rooted at startPath, evaluating the
// expression for each entry.
func walkPath(
	ctx context.Context,
	callCtx *builtins.CallContext,
	startPath string,
	opts walkOptions,
) walkResult {
	now := opts.now
	failed := false
	quit := false
	newerCache := map[string]time.Time{}
	newerErrors := map[string]bool{}
	for k, v := range opts.eagerNewerErrors {
		newerErrors[k] = v
	}
	samefileCache := map[string]builtins.FileID{}
	samefileErrs := map[string]bool{}
	userCache := map[string]uint32{}
	userErrors := map[string]bool{}
	groupCache := map[string]uint32{}
	groupErrors := map[string]bool{}

	// Stat the starting path.
	var startInfo iofs.FileInfo
	var err error
	if opts.followLinks {
		startInfo, err = callCtx.StatFile(ctx, startPath)
		if err != nil && isNotExist(err) {
			startInfo, err = callCtx.LstatFile(ctx, startPath)
		}
	} else {
		startInfo, err = callCtx.LstatFile(ctx, startPath)
	}
	if err != nil {
		callCtx.Errf("find: '%s': %s\n", startPath, callCtx.PortableErr(err))
		return walkResult{failed: true}
	}

	// -mount/-xdev: record device ID of starting path.
	var startDevID uint64
	if opts.mount && callCtx.FileIdentity != nil {
		if id, ok := callCtx.FileIdentity(startPath, startInfo); ok {
			startDevID = id.Dev
		}
	}

	// Cycle detection for -L mode: track ancestor directory identities.

	// dirIterator streams directory entries one at a time via ReadDir(1).
	type dirIterator struct {
		dir           iofs.ReadDirFile
		parentPath    string
		parentInfo    iofs.FileInfo // for -depth deferred eval
		depth         int
		ancestorIDs   map[builtins.FileID]string
		ancestorPaths map[string]bool
		done          bool
		isStartDir    bool // true for the starting path's iterator (handled separately in -depth)
	}

	// checkLoop detects symlink loops for -L mode.
	checkLoop := func(path string, info iofs.FileInfo, ancestorIDs map[builtins.FileID]string, ancestorPaths map[string]bool) (bool, map[builtins.FileID]string, map[string]bool) {
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
						return true, nil, nil
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
					return true, nil, nil
				}
				childAncestorPaths = make(map[string]bool, len(ancestorPaths)+1)
				for k := range ancestorPaths {
					childAncestorPaths[k] = true
				}
				childAncestorPaths[path] = true
			}
		}
		return false, childAncestorIDs, childAncestorPaths
	}

	// processEntry evaluates the expression for a single file entry.
	// Returns (prune, quit).
	processEntry := func(path string, info iofs.FileInfo, depth int) (bool, bool) {
		ec := &evalContext{
			callCtx:       callCtx,
			ctx:           ctx,
			now:           now,
			relPath:       path,
			info:          info,
			depth:         depth,
			printPath:     path,
			startPath:     startPath,
			newerCache:    newerCache,
			newerErrors:   newerErrors,
			samefileCache: samefileCache,
			samefileErrs:  samefileErrs,
			userCache:     userCache,
			userErrors:    userErrors,
			groupCache:    groupCache,
			groupErrors:   groupErrors,
			followLinks:   opts.followLinks,
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
			if result.quit {
				return prune, true
			}
		}

		return prune, false
	}

	// isCrossDevice checks if a file is on a different filesystem than the start.
	isCrossDevice := func(path string, info iofs.FileInfo) bool {
		if !opts.mount || callCtx.FileIdentity == nil {
			return false
		}
		if id, ok := callCtx.FileIdentity(path, info); ok {
			return id.Dev != startDevID
		}
		return false
	}

	// Process the starting path (pre-order unless -depth).
	isLoop, childAncIDs, childAncPaths := checkLoop(startPath, startInfo, nil, nil)

	startPrune := false
	if !opts.depthFirst && !isLoop {
		var q bool
		startPrune, q = processEntry(startPath, startInfo, 0)
		if q {
			return walkResult{failed: failed, quit: true}
		}
	}

	// Set up the iterator stack.
	var iterStack []*dirIterator

	if !isLoop && !startPrune && startInfo.IsDir() && 0 < opts.maxDepth {
		dir, openErr := callCtx.OpenDir(ctx, startPath)
		if openErr != nil {
			callCtx.Errf("find: '%s': %s\n", startPath, callCtx.PortableErr(openErr))
			return walkResult{failed: true}
		}
		iterStack = append(iterStack, &dirIterator{
			dir:           dir,
			parentPath:    startPath,
			parentInfo:    startInfo,
			depth:         1,
			ancestorIDs:   childAncIDs,
			ancestorPaths: childAncPaths,
			isStartDir:    true,
		})
	}

	for len(iterStack) > 0 {
		if ctx.Err() != nil || quit {
			failed = failed || ctx.Err() != nil
			break
		}

		top := iterStack[len(iterStack)-1]
		if top.done {
			top.dir.Close()
			// -depth: evaluate the parent directory AFTER its children.
			// Skip the starting dir here; it's handled at the end.
			if opts.depthFirst && !quit && !top.isStartDir {
				parentDepth := top.depth - 1
				_, q := processEntry(top.parentPath, top.parentInfo, parentDepth)
				if q {
					quit = true
				}
			}
			iterStack = iterStack[:len(iterStack)-1]
			continue
		}

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
			if err != nil && isNotExist(err) {
				childInfo, err = callCtx.LstatFile(ctx, childPath)
			}
			if err != nil {
				callCtx.Errf("find: '%s': %s\n", childPath, callCtx.PortableErr(err))
				failed = true
				continue
			}
		} else {
			childInfo, err = callCtx.LstatFile(ctx, childPath)
			if err != nil {
				callCtx.Errf("find: '%s': %s\n", childPath, callCtx.PortableErr(err))
				failed = true
				continue
			}
		}

		// -mount/-xdev: skip entries on different filesystems.
		if isCrossDevice(childPath, childInfo) {
			continue
		}

		isLoop, cAncIDs, cAncPaths := checkLoop(childPath, childInfo, top.ancestorIDs, top.ancestorPaths)
		if isLoop {
			continue
		}

		// Evaluate the entry (pre-order, unless -depth defers directories).
		prune := false
		if !opts.depthFirst || !childInfo.IsDir() {
			var q bool
			prune, q = processEntry(childPath, childInfo, top.depth)
			if q {
				quit = true
				break
			}
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
				parentInfo:    childInfo,
				depth:         top.depth + 1,
				ancestorIDs:   cAncIDs,
				ancestorPaths: cAncPaths,
			})
		} else if opts.depthFirst && childInfo.IsDir() {
			// -depth + pruned or maxdepth: still need to evaluate the dir itself.
			_, q := processEntry(childPath, childInfo, top.depth)
			if q {
				quit = true
				break
			}
		}
	}

	// Close any remaining open directory handles.
	for _, it := range iterStack {
		it.dir.Close()
	}

	// -depth: evaluate the starting path last.
	if opts.depthFirst && !quit && !isLoop {
		_, q := processEntry(startPath, startInfo, 0)
		if q {
			quit = true
		}
	}

	return walkResult{failed: failed, quit: quit}
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

// collectUserRefs walks the expression tree and returns all -user name arguments.
func collectUserRefs(e *expr) []string {
	if e == nil {
		return nil
	}
	if e.kind == exprUser {
		return []string{e.strVal}
	}
	var refs []string
	refs = append(refs, collectUserRefs(e.left)...)
	refs = append(refs, collectUserRefs(e.right)...)
	refs = append(refs, collectUserRefs(e.operand)...)
	return refs
}

// collectGroupRefs walks the expression tree and returns all -group name arguments.
func collectGroupRefs(e *expr) []string {
	if e == nil {
		return nil
	}
	if e.kind == exprGroup {
		return []string{e.strVal}
	}
	var refs []string
	refs = append(refs, collectGroupRefs(e.left)...)
	refs = append(refs, collectGroupRefs(e.right)...)
	refs = append(refs, collectGroupRefs(e.operand)...)
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
