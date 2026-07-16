// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

// Package ntfsdu implements the ntfs-du builtin command.
//
// ntfs-du — whole-disk NTFS disk-usage analysis (Windows only)
//
// Usage: ntfs-du [OPTION]... [DIRECTORY]
//
// ntfs-du reports disk usage across an NTFS volume by reading the raw Master
// File Table ($MFT) directly from the volume device (\\.\<drive>:). Unlike du,
// which walks a directory tree through the AllowedPaths sandbox, ntfs-du's
// runtime is a function of the MFT size (roughly the number of records on the
// volume), *independent of the starting directory*. Prefer du for a small,
// bounded subtree (e.g. a logs directory); reach for ntfs-du to locate the
// largest files, directories, and extensions across an entire disk.
//
// Windows only, by construction. ntfs-du reads the raw NTFS $MFT through the
// Windows volume API and has no cross-platform implementation
// — the entire package is behind //go:build windows and is registered only on
// Windows (see interp/register_builtins_windows.go). On other platforms it is
// not a recognized command.
//
// Other requirements and constraints:
//
//   - NTFS-formatted volumes only. Non-NTFS volumes (FAT, ReFS, network
//     shares) fail to open or parse and return an error.
//   - Administrator privileges. Opening the raw volume device requires
//     GENERIC_READ on \\.\<drive>:, which is only granted to elevated
//     processes. A non-elevated run fails with "access is denied".
//
// Security note: ntfs-du reads the raw volume device directly via the Windows
// API, deliberately bypassing the AllowedPaths sandbox — the same trade-off
// documented for ss, df, and ip route. Because it enumerates the entire MFT,
// it can surface file names and sizes across the whole volume regardless of the
// configured sandbox roots. See the Security Design Decisions section in
// AGENTS.md.
//
// Output: machine-readable only for now (a human-readable renderer is
// planned). --output json emits a single pretty-printed object.
// The object carries the scan target, deduplicated subtree total,
// per-immediate-child buckets, a depth-limited directory tree, the largest
// files, the largest extensions, and any --find results.
//
// Accepted flags:
//
//	--apparent-size        Report logical (apparent) sizes instead of on-disk
//	                       allocation. Default reports allocation, matching
//	                       Explorer's "size on disk".
//	--top-files N          Report the N largest files (default 10; 0 disables).
//	--top-ext N            Report the N largest extensions by aggregated size
//	                       (default 10; 0 disables).
//	-d, --max-depth N      Directory-tree depth from the target (default 1;
//	                       capped at 16). 0 emits only the immediate-child
//	                       bucket list.
//	--min SIZE             Hide entries smaller than SIZE (e.g. 100M, 1G) and
//	                       set the top-files floor. Suffixes K/M/G/T (base
//	                       1024) are accepted. Defaults to 100M, since the
//	                       command is aimed at large space consumers; pass
//	                       --min 0 to include everything.
//	--exclude PATH         Exclude an absolute path's subtree from all totals
//	                       (repeatable).
//	--find-ext CSV         Report files matching these comma-separated
//	                       extensions (e.g. .dmp,.etl). Repeatable.
//	--find-glob PATTERN    Report files whose basename matches this glob
//	                       (filepath.Match syntax). Repeatable.
//	--find-regex PATTERN   Report files whose basename matches this RE2 regex.
//	                       Repeatable.
//	--find-limit N         Cap each --find query's result block (default 100;
//	                       max 1000).
//	--output FORMAT        Output format; currently only "json" (default), a
//	                       single pretty-printed object.
//	-h, --help             Print usage to stdout and exit 0.
//
// With no DIRECTORY operand, ntfs-du scans the root of the drive containing
// the shell's current working directory (e.g. C:\).
//
// Exit codes:
//
//	0  Scan completed successfully.
//	1  An error occurred (not elevated, non-NTFS volume, invalid argument, etc.).
package ntfsdu

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the ntfs-du builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "ntfs-du",
	Description: "quickly find large directories, files, and file extensions on an NTFS volume (Windows only, requires Administrator)",
	MakeFlags:   registerFlags,
}

// maxTreeDepth caps the requested directory-tree depth.
const maxTreeDepth = 16

// maxFindLimit caps a single --find query's result block. Requests above this
// are rejected rather than silently truncated.
const maxFindLimit = 1000

// options holds the resolved flag values after pflag parsing. Fields are plain
// scalars/slices so this struct is platform-independent; the Windows-only run()
// translates it into ntfsmft.Options.
type options struct {
	apparent  bool
	topFiles  int
	topExt    int
	maxDepth  int
	minSize   int64
	exclude   []string
	findExt   []string
	findGlob  []string
	findRegex []string
	findLimit int
	target    string // positional operand, or "" for the default drive root
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	// Preserve registration order so PrintDefaults emits flags in a stable
	// shape rather than alphabetical.
	fs.SortFlags = false

	apparent := fs.Bool("apparent-size", false, "report apparent (logical content) size instead of the default on-disk allocation; on-disk is the clusters actually used (\"size on disk\": reflects cluster rounding, sparse files, and compression)")
	topFiles := fs.Int("top-files", 10, "report the N largest files (0 disables)")
	topExt := fs.Int("top-ext", 10, "report the N largest extensions by size (0 disables)")
	maxDepth := fs.IntP("max-depth", "d", 1, "directory-tree depth from the target (0 = buckets only, max 16)")
	minSize := fs.String("min", "100M", "hide entries smaller than SIZE (e.g. 100M, 1G; 0 shows all)")
	exclude := fs.StringArray("exclude", nil, "exclude an absolute path's subtree from totals (repeatable)")
	findExt := fs.StringArray("find-ext", nil, "find files with these comma-separated extensions (repeatable)")
	findGlob := fs.StringArray("find-glob", nil, "find files whose basename matches this glob (repeatable)")
	findRegex := fs.StringArray("find-regex", nil, "find files whose basename matches this RE2 regex (repeatable)")
	findLimit := fs.Int("find-limit", 100, "cap each --find query's results (max 1000)")
	output := fs.String("output", "json", "output format (currently only \"json\")")
	helpFlag := fs.BoolP("help", "h", false, "print usage and exit")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		// Validate explicitly-set flag values BEFORE the --help short-circuit,
		// matching head/tail (builtins/head/head.go) and the args-trim contract
		// in builtins/builtins.go: pflag accepts these values syntactically (a
		// bad --output, a negative --max-depth, etc. all parse), so an invalid
		// value followed by --help must report the value rather than silently
		// printing help.
		sz, err := parseSize(*minSize)
		if err != nil {
			callCtx.Errf("ntfs-du: invalid --min value: %s\n", err)
			return builtins.Result{Code: 1}
		}

		if *maxDepth < 0 {
			callCtx.Errf("ntfs-du: invalid --max-depth %d\n", *maxDepth)
			return builtins.Result{Code: 1}
		}
		depth := min(*maxDepth, maxTreeDepth)

		if *findLimit < 0 {
			callCtx.Errf("ntfs-du: invalid --find-limit %d\n", *findLimit)
			return builtins.Result{Code: 1}
		}
		if *findLimit > maxFindLimit {
			callCtx.Errf("ntfs-du: --find-limit %d exceeds maximum of %d\n", *findLimit, maxFindLimit)
			return builtins.Result{Code: 1}
		}

		if *topFiles < 0 || *topExt < 0 {
			callCtx.Errf("ntfs-du: --top-files and --top-ext must be non-negative\n")
			return builtins.Result{Code: 1}
		}

		if *output != "json" {
			callCtx.Errf("ntfs-du: invalid --output format %q (want \"json\")\n", *output)
			return builtins.Result{Code: 1}
		}

		if *helpFlag {
			fs.SetOutput(callCtx.Stdout)
			callCtx.Out("Usage: ntfs-du [OPTION]... [DIRECTORY]\n")
			callCtx.Out("Quickly find large directories, files, and file extensions on an NTFS\n")
			callCtx.Out("volume by reading the raw $MFT.\n")
			callCtx.Out("Windows only; requires Administrator. With no DIRECTORY, scans the\n")
			callCtx.Out("root of the drive containing the current working directory.\n\n")
			fs.PrintDefaults()
			return builtins.Result{}
		}

		// Operand count is checked after the --help short-circuit so that --help
		// wins over an operand-count error, matching GNU (`du a b --help` prints
		// help rather than erroring on the extra operand).
		if len(args) > 1 {
			callCtx.Errf("ntfs-du: at most one directory operand may be given\n")
			return builtins.Result{Code: 1}
		}

		opts := options{
			apparent:  *apparent,
			topFiles:  *topFiles,
			topExt:    *topExt,
			maxDepth:  depth,
			minSize:   sz,
			exclude:   *exclude,
			findExt:   *findExt,
			findGlob:  *findGlob,
			findRegex: *findRegex,
			findLimit: *findLimit,
		}
		if len(args) == 1 {
			opts.target = args[0]
		}

		return run(ctx, callCtx, opts)
	}
}

// parseSize accepts "" -> 0, or a number with an optional K/M/G/T suffix
// (binary multiples). An optional trailing "B", "b", "iB", or "ib" is allowed.
// Examples: "100", "1K", "100M", "5G", "2T". Negative values are rejected.
func parseSize(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "B")
	s = strings.TrimSuffix(s, "b")
	s = strings.TrimSuffix(s, "i")
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	mult := int64(1)
	switch s[len(s)-1] {
	case 'K', 'k':
		mult = 1024
		s = s[:len(s)-1]
	case 'M', 'm':
		mult = 1024 * 1024
		s = s[:len(s)-1]
	case 'G', 'g':
		mult = 1024 * 1024 * 1024
		s = s[:len(s)-1]
	case 'T', 't':
		mult = 1024 * 1024 * 1024 * 1024
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %q: %w", s, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("negative size")
	}
	if n > (1<<63-1)/mult {
		return 0, fmt.Errorf("size overflow")
	}
	return n * mult, nil
}
