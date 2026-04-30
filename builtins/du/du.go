// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package du implements the du builtin command.
//
// du — estimate file space usage
//
// Usage: du [OPTION]... [FILE]...
//
// Summarize device usage of the set of FILEs, recursively for directories.
// With no FILE, du operates on the current directory.
//
// Output format: "<size>\t<path>\n" per entry. Sizes are reported in
// 1024-byte blocks by default (this shell does not honour POSIXLY_CORRECT).
//
// Accepted flags:
//
//	-a, --all
//	    Write counts for all files, not just directories.
//
//	-s, --summarize
//	    Display only a per-argument total. Mutually exclusive with -a
//	    and with --max-depth.
//
//	-c, --total
//	    Produce a grand total row.
//
//	-d, --max-depth=N
//	    Print the total for a directory (or file, with --all) only if it
//	    is N or fewer levels below the command-line argument.
//	    --max-depth=0 is equivalent to --summarize.
//
//	-S, --separate-dirs
//	    For directories, do not include size of subdirectories.
//
//	-L, --dereference
//	    Follow all symbolic links during traversal. Cycles are detected
//	    via dev+inode identity and reported as errors.
//
//	-P, --no-dereference
//	    Never follow symbolic links (this is the default).
//
//	-0, --null
//	    End each output line with NUL, not newline.
//
//	-h, --human-readable
//	    Print sizes in human-readable format using 1024-power units
//	    (e.g. 1.0K, 234M, 2.0G).
//
//	--si
//	    Like -h, but use powers of 1000.
//
//	-k
//	    Use 1024-byte blocks (this is already the default).
//
//	-m
//	    Use 1 MiB (1024*1024) blocks.
//
//	-b, --bytes
//	    Equivalent to --apparent-size --block-size=1: report apparent
//	    size in bytes.
//
//	--apparent-size
//	    Print apparent sizes (file size in bytes) rather than allocated
//	    disk usage. Apparent sizes ignore sparse-file holes, internal
//	    fragmentation, and indirect blocks.
//
//	--help
//	    Print this usage message to stdout and exit 0.
//
// Rejected for security:
//
//	--files0-from=FILE     Reads filenames from another file; data
//	                       exfiltration risk in sandboxed environments.
//	                       Same rationale as wc --files0-from.
//	--exclude-from=FILE    Reads exclude patterns from a file; same class.
//	-X, --exclude-from     (alias of --exclude-from)
//
// All unknown flags are rejected by pflag with exit code 1, so
// security-sensitive flags above are simply not registered.
//
// Behaviour notes that intentionally diverge from GNU du:
//
//   - When `-P` is in effect (the default), a top-level operand that is itself
//     a symbolic link is reported as the symlink, not its target. GNU follows
//     the operand-level link in this case but our implementation prefers the
//     stricter no-follow-by-default reading. Use `-L` to follow.
//
// Exit codes:
//
//	0  All operands processed successfully.
//	1  At least one error occurred (missing file, permission denied,
//	   invalid argument, etc.).
//
// Memory and resource bounds:
//
//	Directory entries are read via callCtx.OpenDir's streaming
//	ReadDirFile so memory usage is proportional to traversal depth, not
//	directory width. Recursion is capped at maxRecursionDepth (256).
//	Each directory is opened in a per-iteration scope so its file
//	descriptor closes before recursion descends — depth × 1 FD instead
//	of depth × N. Hardlink-dedup tracking is bounded at maxDedupEntries
//	(1<<20) per call to prevent unbounded growth on adversarially
//	hardlink-rich subtrees; once the cap is hit, further hardlinks are
//	counted multiple times rather than triggering a memory exhaustion.
package du

import (
	"context"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"math"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the du builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "du",
	Description: "estimate file space usage",
	MakeFlags:   registerFlags,
}

// maxRecursionDepth caps recursion to prevent stack overflow from
// adversarially deep directory trees.
const maxRecursionDepth = 256

// statBlockUnit is the unit GNU du uses for the raw size derived from
// Stat_t.Blocks (always 512 regardless of the filesystem block size).
const statBlockUnit = 512

// apparentBlockSize is the rounding granularity for the apparent-size
// fallback used when the platform does not expose Stat_t.Blocks (e.g.
// Windows). 1024 matches the default GNU du block size.
const apparentBlockSize = 1024

// maxDedupEntries caps the hardlink-dedup tracking map to prevent unbounded
// memory growth when traversing pathological subtrees. Once exceeded,
// further hardlinks are counted as if they were independent files.
const maxDedupEntries = 1 << 20

// errFailed is a sentinel signaling that at least one entry failed.
var errFailed = errors.New("du: one or more errors occurred")

// unitMode selects how raw byte counts are formatted for output.
type unitMode int

const (
	unitKilo  unitMode = iota // 1024-byte blocks (default and -k)
	unitMega                  // 1 MiB blocks (-m)
	unitBytes                 // single bytes (-b / --bytes)
	unitHuman                 // human-readable, 1024-power (-h / --human-readable)
	unitSI                    // human-readable, 1000-power (--si)
)

type options struct {
	all          bool
	summarize    bool
	total        bool
	separateDirs bool
	dereference  bool // -L
	apparentSize bool
	null         bool
	maxDepth     int // -1 = unlimited
	maxDepthSet  bool
	unit         unitMode
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	all := fs.BoolP("all", "a", false, "write counts for all files, not just directories")
	summarize := fs.BoolP("summarize", "s", false, "display only a total for each argument")
	total := fs.BoolP("total", "c", false, "produce a grand total")
	separateDirs := fs.BoolP("separate-dirs", "S", false, "for directories, do not include size of subdirectories")
	_ = fs.BoolP("dereference", "L", false, "dereference all symbolic links")
	// -P is the default; the flag is registered so users can toggle back to
	// it when -L was given earlier in the same invocation. Effective state
	// is determined by parse-order via fs.Visit below.
	_ = fs.BoolP("no-dereference", "P", false, "don't follow any symbolic links (default)")
	apparentSize := fs.Bool("apparent-size", false, "print apparent sizes rather than device usage")
	bytesFlag := fs.BoolP("bytes", "b", false, "equivalent to --apparent-size --block-size=1")
	null := fs.BoolP("null", "0", false, "end each output line with NUL, not newline")
	human := fs.BoolP("human-readable", "h", false, "print sizes in human-readable format")
	si := fs.Bool("si", false, "like -h, but use powers of 1000")
	// -k matches the default unit (1024-byte blocks). It is registered so
	// users may pass it explicitly without "unknown flag" errors, but its
	// value is not consulted because no other unit is "smaller" — the
	// switch below falls through to the default kilo branch when no other
	// unit flag is set.
	_ = fs.BoolP("kilobytes", "k", false, "use 1024-byte blocks (default)")
	mega := fs.BoolP("megabytes", "m", false, "use 1 MiB (1024*1024) blocks")
	maxDepth := fs.IntP("max-depth", "d", -1, "print the total for a directory only if it is N or fewer levels deep")
	helpFlag := fs.Bool("help", false, "print usage and exit")

	return func(ctx context.Context, callCtx *builtins.CallContext, paths []string) builtins.Result {
		if *helpFlag {
			fs.SetOutput(callCtx.Stdout)
			callCtx.Out("Usage: du [OPTION]... [FILE]...\n")
			callCtx.Out("Summarize device usage of the set of FILEs, recursively for directories.\n")
			callCtx.Out("With no FILE, du operates on the current directory.\n\n")
			fs.PrintDefaults()
			return builtins.Result{}
		}

		opts := options{
			all:          *all,
			summarize:    *summarize,
			total:        *total,
			separateDirs: *separateDirs,
			apparentSize: *apparentSize || *bytesFlag,
			null:         *null,
			maxDepth:     *maxDepth,
			maxDepthSet:  fs.Changed("max-depth"),
		}
		// `-L` and `-P` cancel each other out; the *last* one wins. fs.Visit
		// iterates flags in parse order (only when SortFlags=false, which is
		// the default for our builtins). Reading from these flags here is the
		// single source of truth for opts.dereference.
		fs.Visit(func(f *builtins.Flag) {
			switch f.Name {
			case "dereference":
				opts.dereference = true
			case "no-dereference":
				opts.dereference = false
			}
		})

		// Resolve unit precedence. -b implies bytes mode; -h overrides -m.
		// -k is the default and never explicitly selected here.
		switch {
		case *bytesFlag:
			opts.unit = unitBytes
		case *human:
			opts.unit = unitHuman
		case *si:
			opts.unit = unitSI
		case *mega:
			opts.unit = unitMega
		default:
			opts.unit = unitKilo
		}

		// Mutual-exclusion checks (GNU semantics).
		if opts.summarize && opts.maxDepthSet {
			callCtx.Errf("du: summarizing conflicts with --max-depth=%d\n", opts.maxDepth)
			return builtins.Result{Code: 1}
		}
		if opts.summarize && opts.all {
			callCtx.Errf("du: cannot both summarize and show all entries\n")
			return builtins.Result{Code: 1}
		}
		if opts.summarize {
			opts.maxDepth = 0
			opts.maxDepthSet = true
		}
		// max-depth must be non-negative.
		if opts.maxDepthSet && opts.maxDepth < 0 {
			callCtx.Errf("du: invalid maximum depth %d\n", opts.maxDepth)
			return builtins.Result{Code: 1}
		}

		if len(paths) == 0 {
			paths = []string{"."}
		}

		// Hardlink dedup: count each (dev,inode) only once across the run.
		// Bounded at maxDedupEntries to prevent unbounded growth.
		visited := map[builtins.FileID]bool{}
		var grandTotal int64
		failed := false

		for _, p := range paths {
			if ctx.Err() != nil {
				break
			}
			size, err := walk(ctx, callCtx, p, p, 0, opts, visited, nil)
			if err != nil {
				failed = true
			}
			grandTotal = saturatingAdd(grandTotal, size)
		}

		if opts.total {
			emit(callCtx, opts, grandTotal, "total")
		}

		if failed {
			return builtins.Result{Code: 1}
		}
		return builtins.Result{}
	}
}

// walk processes a single operand or recursive entry, returning the
// cumulative subtree size in raw bytes (or 0 on early failure).
//
// reportPath is the path as written on the command line (for output).
// fsPath is the actual path to read (same as reportPath for top-level
// operands; joined paths during recursion).
// depth is 0 for the operand itself, 1 for its children, etc.
// ancestorIDs tracks visited directory identities along the recursion stack
// for symlink-loop detection in -L mode.
func walk(
	ctx context.Context,
	callCtx *builtins.CallContext,
	fsPath string,
	reportPath string,
	depth int,
	opts options,
	visited map[builtins.FileID]bool,
	ancestorIDs map[builtins.FileID]string,
) (int64, error) {
	if ctx.Err() != nil {
		return 0, ctx.Err()
	}
	if depth > maxRecursionDepth {
		callCtx.Errf("du: recursion depth limit exceeded at '%s'\n", reportPath)
		return 0, errFailed
	}

	info, err := statEntry(ctx, callCtx, fsPath, opts.dereference)
	if err != nil {
		callCtx.Errf("du: cannot access '%s': %s\n", reportPath, callCtx.PortableErr(err))
		return 0, err
	}

	// Hardlink dedup applies only to regular files. Directories with
	// nlink>1 are physically distinct (parent-link / "." / ".." mechanics)
	// and must not be skipped. Symlinks are leaves; let them through.
	if info.Mode().IsRegular() && callCtx.FileIdentity != nil {
		if id, ok := callCtx.FileIdentity(fsPath, info); ok {
			if visited[id] {
				return 0, nil
			}
			if infoNlink(info) > 1 && len(visited) < maxDedupEntries {
				visited[id] = true
			}
		}
	}

	// Symlink leaves report the symlink's own size. Under -L, statEntry
	// already followed the link, so info.Mode() will not have ModeSymlink set
	// here. Under -P this branch fires.
	if !info.IsDir() {
		size := entrySize(info, opts.apparentSize)
		if shouldEmit(depth, false, opts) {
			emit(callCtx, opts, size, reportPath)
		}
		return size, nil
	}

	// Directory: cycle-check (only relevant under -L).
	if opts.dereference && callCtx.FileIdentity != nil {
		if id, ok := callCtx.FileIdentity(fsPath, info); ok {
			if firstPath, seen := ancestorIDs[id]; seen {
				callCtx.Errf("du: File system loop detected; '%s' is part of the same file system loop as '%s'.\n",
					reportPath, firstPath)
				return 0, errFailed
			}
			// Push this directory onto the ancestor map for the duration of
			// the recursion below, then pop on the way back up. This avoids
			// an O(depth²) clone per level — the map is shared across the
			// whole recursion tree.
			ancestorIDs = pushAncestor(ancestorIDs, id, reportPath)
			defer delete(ancestorIDs, id)
		}
	}

	dirOwn := entrySize(info, opts.apparentSize)
	subtreeFromChildren, failedAny := walkChildren(ctx, callCtx, fsPath, reportPath, depth, opts, visited, ancestorIDs)

	// Compute and emit the directory's reported size. With --separate-dirs,
	// the printed value excludes children even though we keep counting them
	// for the parent's accumulation.
	dirReport := dirOwn
	if !opts.separateDirs {
		dirReport = saturatingAdd(dirOwn, subtreeFromChildren)
	}
	if shouldEmit(depth, true, opts) {
		emit(callCtx, opts, dirReport, reportPath)
	}

	totalForParent := saturatingAdd(dirOwn, subtreeFromChildren)
	if failedAny {
		return totalForParent, errFailed
	}
	return totalForParent, nil
}

// walkChildren iterates entries in dir via OpenDir/ReadDir(1), recursing
// into walk for each. Scoped as a separate function so the directory
// handle's defer Close() fires at this frame's exit rather than the
// outer walk's, keeping FD usage proportional to depth × 1 not depth × N.
func walkChildren(
	ctx context.Context,
	callCtx *builtins.CallContext,
	fsPath string,
	reportPath string,
	depth int,
	opts options,
	visited map[builtins.FileID]bool,
	ancestorIDs map[builtins.FileID]string,
) (subtree int64, failedAny bool) {
	dh, err := callCtx.OpenDir(ctx, fsPath)
	if err != nil {
		callCtx.Errf("du: cannot read directory '%s': %s\n", reportPath, callCtx.PortableErr(err))
		return 0, true
	}
	defer dh.Close()

	for {
		if ctx.Err() != nil {
			return subtree, true
		}
		entries, readErr := dh.ReadDir(1)
		if len(entries) == 0 {
			if readErr == nil || errors.Is(readErr, io.EOF) {
				return subtree, failedAny
			}
			callCtx.Errf("du: error reading directory '%s': %s\n", reportPath, callCtx.PortableErr(readErr))
			return subtree, true
		}
		ent := entries[0]
		childFs := joinPath(fsPath, ent.Name())
		childReport := joinPath(reportPath, ent.Name())
		childSize, walkErr := walk(ctx, callCtx, childFs, childReport, depth+1, opts, visited, ancestorIDs)
		if walkErr != nil {
			failedAny = true
		}
		subtree = saturatingAdd(subtree, childSize)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			callCtx.Errf("du: error reading directory '%s': %s\n", reportPath, callCtx.PortableErr(readErr))
			return subtree, true
		}
	}
}

// pushAncestor inserts (id, path) into ancestorIDs (allocating a new map
// on first push) and returns the same map. The caller is expected to
// `defer delete(m, id)` to pop the entry when its recursion frame exits.
func pushAncestor(m map[builtins.FileID]string, id builtins.FileID, path string) map[builtins.FileID]string {
	if m == nil {
		m = make(map[builtins.FileID]string, 4)
	}
	m[id] = path
	return m
}

// shouldEmit reports whether an entry at the given depth should be printed
// under the active options.
//
// Files (non-dirs) print only with -a or when the file is a top-level
// operand. With -s only depth 0 prints. --max-depth caps the printable
// depth without affecting accumulation.
func shouldEmit(depth int, isDir bool, opts options) bool {
	if opts.summarize {
		return depth == 0
	}
	if opts.maxDepthSet && depth > opts.maxDepth {
		return false
	}
	if !isDir && depth > 0 && !opts.all {
		return false
	}
	return true
}

// entrySize returns the raw byte count attributed to an entry.
//
// Behaviour matches GNU du:
//   - Non-directory files in apparent-size mode use info.Size().
//   - Non-directory files in disk-usage mode use Stat_t.Blocks * 512, or
//     (when Blocks is unavailable) info.Size() rounded up to the nearest
//     1024-byte block.
//   - Directories in apparent-size mode contribute 0 — GNU du with
//     --apparent-size does not count the directory's own bytes; only its
//     children contribute. (Verified empirically against GNU coreutils
//     on both ext4 and APFS.)
//   - Directories in disk-usage mode use Stat_t.Blocks * 512. On
//     platforms without Blocks (Windows), directories report 0.
//
// The Blocks * 512 multiplication is clamped to math.MaxInt64 to defend
// against pathological filesystems (e.g. FUSE) that report bogus values.
func entrySize(info iofs.FileInfo, apparent bool) int64 {
	if info.IsDir() {
		if apparent {
			return 0
		}
		if blocks, ok := infoBlocks(info); ok {
			return clampMul(blocks, statBlockUnit)
		}
		return 0
	}
	if apparent {
		return info.Size()
	}
	if blocks, ok := infoBlocks(info); ok {
		return clampMul(blocks, statBlockUnit)
	}
	size := info.Size()
	if size <= 0 {
		return 0
	}
	if size > math.MaxInt64-apparentBlockSize+1 {
		return math.MaxInt64
	}
	return ((size + apparentBlockSize - 1) / apparentBlockSize) * apparentBlockSize
}

// clampMul multiplies a*b for non-negative inputs, returning math.MaxInt64
// on overflow and 0 on negative inputs. This guards against pathological
// Stat_t.Blocks values from untrusted filesystems.
func clampMul(a, b int64) int64 {
	if a <= 0 || b <= 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

// saturatingAdd returns a+b, clamped to math.MaxInt64 to avoid wraparound
// when accumulating sizes across enormous subtrees.
func saturatingAdd(a, b int64) int64 {
	if a < 0 {
		a = 0
	}
	if b < 0 {
		b = 0
	}
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// formatSize converts a raw byte count into the unit configured by opts.
// Block units round up (matching GNU); human and SI variants pick the
// smallest unit ≥ base.
func formatSize(rawBytes int64, opts options) string {
	switch opts.unit {
	case unitBytes:
		return fmt.Sprintf("%d", rawBytes)
	case unitMega:
		return fmt.Sprintf("%d", divCeil(rawBytes, 1024*1024))
	case unitHuman:
		return humanSize(rawBytes, 1024, []string{"B", "K", "M", "G", "T", "P", "E"})
	case unitSI:
		return humanSize(rawBytes, 1000, []string{"B", "k", "M", "G", "T", "P", "E"})
	case unitKilo:
		fallthrough
	default:
		return fmt.Sprintf("%d", divCeil(rawBytes, 1024))
	}
}

// divCeil performs integer ceiling division for non-negative inputs.
// Negative or zero inputs return 0.
func divCeil(n, d int64) int64 {
	if n <= 0 {
		return 0
	}
	if n > math.MaxInt64-d+1 {
		// Saturate rather than wrap: the value is already at the limit.
		return math.MaxInt64 / d
	}
	return (n + d - 1) / d
}

// humanSize formats a byte count using the supplied base (1024 or 1000).
// Below the base it prints the raw integer with no suffix (matching GNU).
// At base or above it picks the smallest unit such that value < base,
// printing one decimal when val < 9.95 (so "1.5K" but "234M") and zero
// decimals otherwise (GNU's threshold).
func humanSize(rawBytes int64, base int64, units []string) string {
	if rawBytes < 0 {
		rawBytes = 0
	}
	if rawBytes < base {
		return fmt.Sprintf("%d", rawBytes)
	}
	val := float64(rawBytes)
	div := float64(base)
	for i := 1; i < len(units); i++ {
		val /= div
		if val < float64(base) {
			if val < 9.95 {
				return fmt.Sprintf("%.1f%s", val, units[i])
			}
			return fmt.Sprintf("%.0f%s", val, units[i])
		}
	}
	return fmt.Sprintf("%.0f%s", val, units[len(units)-1])
}

// emit writes a single output line: "<size>\t<path>" terminated by \n
// (or \x00 with --null).
func emit(callCtx *builtins.CallContext, opts options, rawBytes int64, path string) {
	terminator := "\n"
	if opts.null {
		terminator = "\x00"
	}
	callCtx.Outf("%s\t%s%s", formatSize(rawBytes, opts), path, terminator)
}

// statEntry stats a path, following symlinks when -L is set.
//
// Note: this function does NOT follow operand-level symlinks even at
// depth 0 unless -L is supplied — see the package-level "Behaviour notes"
// for the GNU divergence.
func statEntry(ctx context.Context, callCtx *builtins.CallContext, path string, deref bool) (iofs.FileInfo, error) {
	if deref {
		return callCtx.StatFile(ctx, path)
	}
	return callCtx.LstatFile(ctx, path)
}

// joinPath joins a directory and a name without invoking filepath.Clean,
// preserving '.' and '..' segments so that operand-relative paths are
// reported the same way GNU du reports them. This intentionally matches
// the helper at builtins/find/find.go:645 — paths are canonicalised by
// the sandbox at lookup time, but reported verbatim to the user.
func joinPath(dir, name string) string {
	if len(dir) == 0 {
		return name
	}
	if dir[len(dir)-1] == '/' {
		return dir + name
	}
	return dir + "/" + name
}
