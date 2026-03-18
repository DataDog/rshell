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

	"github.com/DataDog/rshell/builtins"
)

// evalResult captures the outcome of evaluating an expression on a file.
type evalResult struct {
	matched bool
	prune   bool // skip descending into this directory
	quit    bool // stop all iteration immediately (-quit)
}

// evalContext holds state needed during expression evaluation.
type evalContext struct {
	callCtx       *builtins.CallContext
	ctx           context.Context
	now           time.Time
	relPath       string               // path relative to starting point
	info          iofs.FileInfo        // file info (lstat or stat depending on -L)
	depth         int                  // current depth
	printPath     string               // path to print (includes starting point prefix)
	newerCache    map[string]time.Time // cached -newer reference file modtimes
	newerErrors   map[string]bool      // tracks which -newer reference files failed to stat
	followLinks   bool                 // true when -L is active
	failed        bool                 // set by predicates that encounter errors
	execDirParent string               // absolute path of the file's parent directory for -execdir
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
		if left.quit {
			return left
		}
		if !left.matched {
			return evalResult{prune: left.prune}
		}
		right := evaluate(ec, e.right)
		return evalResult{matched: right.matched, prune: left.prune || right.prune, quit: right.quit}

	case exprOr:
		left := evaluate(ec, e.left)
		if left.quit {
			return left
		}
		if left.matched {
			return evalResult{matched: true, prune: left.prune}
		}
		right := evaluate(ec, e.right)
		return evalResult{matched: right.matched, prune: left.prune || right.prune, quit: right.quit}

	case exprNot:
		r := evaluate(ec, e.operand)
		return evalResult{matched: !r.matched, prune: r.prune, quit: r.quit}

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

	case exprPerm:
		// Use full 12-bit mode (including setuid/setgid/sticky), not just Perm() which is only 9 bits.
		return evalResult{matched: matchPerm(ec.info.Mode(), e.permVal, e.permCmp)}

	case exprExecDir:
		return evalExecDir(ec, e)

	case exprQuit:
		return evalResult{matched: true, quit: true}

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
// For directories, uses IsDirEmpty which reads at most one entry rather than
// materializing the full listing. If the check fails, the error is reported
// to stderr and ec.failed is set so that find exits non-zero, matching GNU
// find behaviour.
func evalEmpty(ec *evalContext) bool {
	if ec.info.IsDir() {
		empty, err := ec.callCtx.IsDirEmpty(ec.ctx, ec.printPath)
		if err != nil {
			ec.callCtx.Errf("find: '%s': %s\n", ec.printPath, ec.callCtx.PortableErr(err))
			ec.failed = true
			return false
		}
		return empty
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
			// With -L, stat fails on dangling symlinks — fall back to
			// lstat so the symlink's own mtime can be used. Only fall
			// back for "not found" errors; permission/sandbox errors
			// must be reported.
			if ec.followLinks && isNotExist(err) {
				refInfo, err = ec.callCtx.LstatFile(ec.ctx, refPath)
			}
			if err != nil {
				ec.callCtx.Errf("find: '%s': %s\n", refPath, ec.callCtx.PortableErr(err))
				ec.newerErrors[refPath] = true
				return false
			}
		}
		refTime = refInfo.ModTime()
		ec.newerCache[refPath] = refTime
	}
	return ec.info.ModTime().After(refTime)
}

// evalMtime checks modification time in days.
// GNU find uses different comparison strategies for -mtime:
//   - Exact (N): day-bucketed comparison — N*86400 <= delta < (N+1)*86400.
//   - +N: raw second comparison — delta > (N+1)*86400.
//   - -N: raw second comparison — delta < N*86400.
//
// GNU find captures 'now' via time() (second precision) but gets file mtime
// from stat() (nanosecond precision). This means for very fresh files,
// delta can be slightly negative, causing -mtime -0 to match files created
// within the same second. We replicate this by truncating now to seconds
// for +N/-N comparisons.
//
// maxMtimeN is the largest N for which (N+1)*24*time.Hour does not overflow.
const maxMtimeN = int64(math.MaxInt64/(int64(24*time.Hour))) - 1

func evalMtime(ec *evalContext, n int64, cmp cmpOp) bool {
	modTime := ec.info.ModTime()
	switch cmp {
	case cmpMore: // +N: strictly older than (N+1) days
		if n > maxMtimeN {
			return false // threshold beyond representable duration
		}
		// Truncate now to second precision to match GNU find's time().
		diff := ec.now.Truncate(time.Second).Sub(modTime)
		return diff >= time.Duration(n+1)*24*time.Hour
	case cmpLess: // -N: strictly newer than N days
		if n > maxMtimeN {
			return true // threshold beyond representable duration
		}
		// Truncate now to second precision to match GNU find's time().
		diff := ec.now.Truncate(time.Second).Sub(modTime)
		return diff < time.Duration(n)*24*time.Hour
	default: // N: day-bucketed exact match
		// Do not clamp negative diff — future-dated files must produce
		// negative day buckets so they never match non-negative N,
		// matching GNU find behavior.
		diff := ec.now.Sub(modTime)
		days := int64(math.Floor(diff.Hours() / 24))
		return days == n
	}
}

// evalMmin checks modification time in minutes.
// GNU find uses different comparison strategies:
//   - Exact (N): ceiling-bucketed comparison — a 5s-old file is in bucket 1.
//   - +N: raw second comparison — delta_seconds > N*60.
//   - -N: raw second comparison — delta_seconds < N*60.
//
// This matches GNU findutils behavior where +N/-N compare against raw
// seconds while exact N uses a window check.
// maxMminN is the largest N for which time.Duration(N)*time.Minute
// does not overflow int64 nanoseconds.
const maxMminN = int64(math.MaxInt64 / int64(time.Minute))

func evalMmin(ec *evalContext, n int64, cmp cmpOp) bool {
	modTime := ec.info.ModTime()
	diff := ec.now.Sub(modTime)
	switch cmp {
	case cmpMore: // +N: strictly older than N minutes
		if n > maxMminN {
			return false // threshold is beyond representable duration; nothing qualifies
		}
		return diff > time.Duration(n)*time.Minute
	case cmpLess: // -N: strictly newer than N minutes
		if n > maxMminN {
			return true // threshold is beyond representable duration; everything qualifies
		}
		return diff < time.Duration(n)*time.Minute
	default: // N: ceiling-bucketed exact match
		mins := int64(math.Ceil(diff.Minutes()))
		return mins == n
	}
}

// evalExecDir executes a command in the directory of each matched file.
// The filename is passed as ./basename, preventing leading-dash interpretation.
func evalExecDir(ec *evalContext, e *expr) evalResult {
	if ec.callCtx.RunCommand == nil {
		ec.callCtx.Errf("find: -execdir: command execution not available\n")
		ec.failed = true
		return evalResult{}
	}
	if ec.callCtx.CommandAllowed != nil && !ec.callCtx.CommandAllowed(e.execCmd) {
		ec.callCtx.Errf("find: -execdir: '%s': command not allowed\n", e.execCmd)
		ec.failed = true
		return evalResult{}
	}
	base := baseName(ec.relPath)
	replacement := "./" + base
	if base == "/" {
		replacement = "/"
	}
	args := make([]string, len(e.execArgs))
	for i, a := range e.execArgs {
		if a == "{}" {
			args[i] = replacement
		} else {
			args[i] = a
		}
	}
	exitCode, err := ec.callCtx.RunCommand(ec.ctx, ec.execDirParent, e.execCmd, args)
	if err != nil {
		ec.callCtx.Errf("find: '%s': %s\n", e.execCmd, err)
		ec.failed = true
		return evalResult{}
	}
	return evalResult{matched: exitCode == 0}
}
