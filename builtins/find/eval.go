// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package find

import (
	"context"
	iofs "io/fs"
	"math"
	"path"
	"time"

	"github.com/DataDog/rshell/builtins"
)

// evalResult captures the outcome of evaluating an expression on a file.
type evalResult struct {
	matched bool
	prune   bool // skip descending into this directory
}

// evalContext holds state needed during expression evaluation.
type evalContext struct {
	callCtx      *builtins.CallContext
	ctx          context.Context
	now          time.Time
	relPath      string               // path relative to starting point
	info         iofs.FileInfo        // file info (lstat or stat depending on -L)
	depth        int                  // current depth
	printPath    string               // path to print (includes starting point prefix)
	newerCache   map[string]time.Time // cached -newer reference file modtimes
	newerErrors  map[string]bool      // tracks which -newer reference files failed to stat
	followLinks  bool                 // true when -L is active
	failed       bool                 // set by predicates that encounter errors
	execCommand  func(ctx context.Context, args []string) (uint8, error)
	batchExec    *batchCollector    // shared batch collector for -exec {} +
	batchExecDir *batchDirCollector // shared batch collector for -execdir {} +
}

// maxBatchArgs is the maximum number of accumulated paths before a
// batch -exec + or -execdir + invocation is flushed.
const maxBatchArgs = 4096

// batchCollector accumulates paths for -exec {} + mode.
type batchCollector struct {
	template []string // command template (with {} placeholder)
	paths    []string // accumulated file paths
	failed   bool     // true if any invocation returned non-zero
}

// add appends a path and flushes if the batch limit is reached.
func (bc *batchCollector) add(ctx context.Context, execCommand func(context.Context, []string) (uint8, error), filePath string) {
	bc.paths = append(bc.paths, filePath)
	if len(bc.paths) >= maxBatchArgs {
		bc.flush(ctx, execCommand)
	}
}

// flush executes the accumulated batch.
func (bc *batchCollector) flush(ctx context.Context, execCommand func(context.Context, []string) (uint8, error)) {
	if len(bc.paths) == 0 {
		return
	}
	args := buildBatchArgs(bc.template, bc.paths)
	code, _ := execCommand(ctx, args)
	if code != 0 {
		bc.failed = true
	}
	bc.paths = bc.paths[:0]
}

// batchDirCollector accumulates paths grouped by parent directory for -execdir {} +.
type batchDirCollector struct {
	template []string            // command template (with {} placeholder)
	byDir    map[string][]string // parent dir -> list of basenames
	dirOrder []string            // insertion order for deterministic flushing
	total    int                 // total accumulated paths across all dirs
	failed   bool
}

// add appends a path grouped by its parent directory.
func (bdc *batchDirCollector) add(ctx context.Context, execCommand func(context.Context, []string) (uint8, error), filePath string) {
	dir := path.Dir(filePath)
	base := "./" + path.Base(filePath)
	if bdc.byDir == nil {
		bdc.byDir = make(map[string][]string)
	}
	if _, ok := bdc.byDir[dir]; !ok {
		bdc.dirOrder = append(bdc.dirOrder, dir)
	}
	bdc.byDir[dir] = append(bdc.byDir[dir], base)
	bdc.total++
	if bdc.total >= maxBatchArgs {
		bdc.flush(ctx, execCommand)
	}
}

// flush executes accumulated batches, one per directory.
func (bdc *batchDirCollector) flush(ctx context.Context, execCommand func(context.Context, []string) (uint8, error)) {
	for _, dir := range bdc.dirOrder {
		if ctx.Err() != nil {
			bdc.failed = true
			break
		}
		bases := bdc.byDir[dir]
		if len(bases) == 0 {
			continue
		}
		_ = dir // -execdir conceptually runs from this dir, but our builtins don't support cwd changes
		args := buildBatchArgs(bdc.template, bases)
		code, _ := execCommand(ctx, args)
		if code != 0 {
			bdc.failed = true
		}
	}
	bdc.byDir = nil
	bdc.dirOrder = nil
	bdc.total = 0
}

// buildBatchArgs constructs the command arguments for batch mode.
// It replaces the {} placeholder in the template with the accumulated paths.
func buildBatchArgs(template, paths []string) []string {
	args := make([]string, 0, len(template)+len(paths))
	for _, t := range template {
		if t == "{}" {
			args = append(args, paths...)
		} else {
			args = append(args, t)
		}
	}
	return args
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

	case exprExec:
		return evalExec(ec, e.execArgs, ec.printPath)

	case exprExecDir:
		return evalExecDir(ec, e.execArgs, ec.printPath)

	case exprExecPlus:
		if ec.batchExec != nil {
			ec.batchExec.add(ec.ctx, ec.execCommand, ec.printPath)
		}
		return evalResult{matched: true}

	case exprExecDirPlus:
		if ec.batchExecDir != nil {
			ec.batchExecDir.add(ec.ctx, ec.execCommand, ec.printPath)
		}
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

// substituteExecArgs replaces standalone {} in the template with the given path.
func substituteExecArgs(template []string, replacement string) []string {
	args := make([]string, len(template))
	for i, a := range template {
		if a == "{}" {
			args[i] = replacement
		} else {
			args[i] = a
		}
	}
	return args
}

// evalExec runs the command template with {} replaced by the file path.
// Returns matched=true if the command exits with code 0.
func evalExec(ec *evalContext, cmdTemplate []string, filePath string) evalResult {
	if ec.execCommand == nil {
		ec.callCtx.Errf("find: -exec: command execution not available\n")
		ec.failed = true
		return evalResult{}
	}
	args := substituteExecArgs(cmdTemplate, filePath)
	code, err := ec.execCommand(ec.ctx, args)
	if err != nil {
		ec.callCtx.Errf("find: -exec: %s\n", err)
		ec.failed = true
		return evalResult{}
	}
	return evalResult{matched: code == 0}
}

// evalExecDir runs the command template with {} replaced by ./basename.
// -execdir conceptually runs from the file's parent directory.
func evalExecDir(ec *evalContext, cmdTemplate []string, filePath string) evalResult {
	if ec.execCommand == nil {
		ec.callCtx.Errf("find: -execdir: command execution not available\n")
		ec.failed = true
		return evalResult{}
	}
	base := "./" + path.Base(filePath)
	args := substituteExecArgs(cmdTemplate, base)
	code, err := ec.execCommand(ec.ctx, args)
	if err != nil {
		ec.callCtx.Errf("find: -execdir: %s\n", err)
		ec.failed = true
		return evalResult{}
	}
	return evalResult{matched: code == 0}
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
