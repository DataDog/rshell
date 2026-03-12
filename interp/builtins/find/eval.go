// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package find

import (
	"context"
	iofs "io/fs"
	"math"
	"time"

	"github.com/DataDog/rshell/interp/builtins"
)

// evalResult captures the outcome of evaluating an expression on a file.
type evalResult struct {
	matched bool
	prune   bool // skip descending into this directory
}

// evalContext holds state needed during expression evaluation.
type evalContext struct {
	callCtx     *builtins.CallContext
	ctx         context.Context
	now         time.Time
	relPath     string               // path relative to starting point
	info        iofs.FileInfo        // file info (lstat or stat depending on -L)
	depth       int                  // current depth
	printPath   string               // path to print (includes starting point prefix)
	newerCache  map[string]time.Time // cached -newer reference file modtimes
	newerErrors map[string]bool      // tracks which -newer reference files failed to stat
	followLinks bool                 // true when -L is active
	failed      bool                 // set by predicates that encounter errors
}

// evaluate evaluates an expression tree against a file. If e is nil, returns
// matched=true (match everything).
func evaluate(ec *evalContext, e *expr) evalResult {
	if e == nil {
		return evalResult{matched: true}
	}
	switch e.kind {
	case exprAnd:
		left := evaluate(ec, e.left)
		if !left.matched {
			return evalResult{prune: left.prune}
		}
		right := evaluate(ec, e.right)
		return evalResult{matched: right.matched, prune: left.prune || right.prune}

	case exprOr:
		left := evaluate(ec, e.left)
		if left.matched {
			return evalResult{matched: true, prune: left.prune}
		}
		right := evaluate(ec, e.right)
		return evalResult{matched: right.matched, prune: left.prune || right.prune}

	case exprNot:
		r := evaluate(ec, e.operand)
		return evalResult{matched: !r.matched, prune: r.prune}

	case exprName:
		name := baseName(ec.relPath)
		return evalResult{matched: matchGlob(e.strVal, name)}

	case exprIName:
		name := baseName(ec.relPath)
		return evalResult{matched: matchGlobFold(e.strVal, name)}

	case exprPath:
		return evalResult{matched: matchPathGlob(e.strVal, ec.printPath)}

	case exprIPath:
		return evalResult{matched: matchPathGlobFold(e.strVal, ec.printPath)}

	case exprType:
		return evalResult{matched: matchType(ec.info, e.strVal)}

	case exprSize:
		return evalResult{matched: compareSize(ec.info.Size(), e.sizeVal)}

	case exprEmpty:
		return evalResult{matched: evalEmpty(ec)}

	case exprNewer:
		return evalResult{matched: evalNewer(ec, e.strVal)}

	case exprMtime:
		return evalResult{matched: evalMtime(ec, e.numVal, e.numCmp)}

	case exprMmin:
		return evalResult{matched: evalMmin(ec, e.numVal, e.numCmp)}

	case exprPrint:
		ec.callCtx.Outf("%s\n", ec.printPath)
		return evalResult{matched: true}

	case exprPrint0:
		ec.callCtx.Outf("%s\x00", ec.printPath)
		return evalResult{matched: true}

	case exprPrune:
		return evalResult{matched: true, prune: true}

	case exprTrue:
		return evalResult{matched: true}

	case exprFalse:
		return evalResult{matched: false}

	default:
		return evalResult{matched: false}
	}
}

// evalEmpty returns true if the file is an empty regular file or empty directory.
// If ReadDir fails on a directory, the error is reported to stderr and
// ec.failed is set so that find exits non-zero, matching GNU find behaviour.
func evalEmpty(ec *evalContext) bool {
	if ec.info.IsDir() {
		entries, err := ec.callCtx.ReadDir(ec.ctx, ec.printPath)
		if err != nil {
			ec.callCtx.Errf("find: '%s': %s\n", ec.printPath, ec.callCtx.PortableErr(err))
			ec.failed = true
			return false
		}
		return len(entries) == 0
	}
	if ec.info.Mode().IsRegular() {
		return ec.info.Size() == 0
	}
	return false
}

// evalNewer returns true if the file is newer than the reference file.
// The reference file's modtime is resolved once and cached in newerCache
// to avoid redundant stat calls for every entry in the tree. Errors are
// tracked in newerErrors (shared across all entries) so a failed stat
// consistently returns false for all subsequent entries rather than
// matching against a zero-time sentinel.
func evalNewer(ec *evalContext, refPath string) bool {
	// Check if this reference path previously failed to stat.
	if ec.newerErrors[refPath] {
		return false
	}
	refTime, ok := ec.newerCache[refPath]
	if !ok {
		statRef := ec.callCtx.LstatFile
		if ec.followLinks {
			statRef = ec.callCtx.StatFile
		}
		refInfo, err := statRef(ec.ctx, refPath)
		if err != nil {
			ec.callCtx.Errf("find: '%s': %s\n", refPath, ec.callCtx.PortableErr(err))
			ec.newerErrors[refPath] = true
			return false
		}
		refTime = refInfo.ModTime()
		ec.newerCache[refPath] = refTime
	}
	return ec.info.ModTime().After(refTime)
}

// evalMtime checks modification time in days.
// -mtime n: file was last modified n*24 hours ago.
func evalMtime(ec *evalContext, n int64, cmp int) bool {
	modTime := ec.info.ModTime()
	diff := ec.now.Sub(modTime)
	days := int64(math.Floor(diff.Hours() / 24))
	return compareNumeric(days, n, cmp)
}

// evalMmin checks modification time in minutes.
// GNU find uses different comparison strategies:
//   - Exact (N): ceiling-bucketed comparison — a 5s-old file is in bucket 1.
//   - +N: raw second comparison — delta_seconds > N*60.
//   - -N: raw second comparison — delta_seconds < N*60.
//
// This matches GNU findutils behavior where +N/-N compare against raw
// seconds while exact N uses a window check.
func evalMmin(ec *evalContext, n int64, cmp int) bool {
	modTime := ec.info.ModTime()
	diff := ec.now.Sub(modTime)
	switch cmp {
	case 1: // +N: strictly older than N minutes
		return diff.Seconds() > float64(n)*60.0
	case -1: // -N: strictly newer than N minutes
		return diff.Seconds() < float64(n)*60.0
	default: // N: ceiling-bucketed exact match
		mins := int64(math.Ceil(diff.Minutes()))
		return mins == n
	}
}
