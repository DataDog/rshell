// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package df implements the df builtin command.
//
// df — report file system disk space usage
//
// Usage: df [OPTION]...
//
// Show information about the file system on which each FILE resides, or
// all file systems by default. This implementation does not accept FILE
// operands; pipe through grep to filter.
//
// Mount enumeration is delegated to the internal diskstats package, which
// reads /proc/self/mountinfo on Linux and calls getfsstat(2) on macOS.
// The /proc read is exempt from the AllowedPaths sandbox because the path
// is hardcoded and never derived from user input — the same documented
// exception the ss and ip route builtins use.
//
// Accepted flags:
//
//	-h, --human-readable
//	    Print sizes in powers of 1024 (e.g. 1023M, 1.5G).
//
//	-H, --si
//	    Print sizes in powers of 1000 (e.g. 1.1G).
//
//	-k
//	    Use 1024-byte blocks (POSIX default; the column header reads
//	    "1K-blocks").
//
//	-P, --portability
//	    Use the POSIX output format. Single space-separated header line:
//	    "Filesystem 1024-blocks Used Available Capacity Mounted on".
//
//	-T, --print-type
//	    Add a column showing the filesystem type.
//
//	-i, --inodes
//	    List inode usage instead of block usage.
//
//	-a, --all
//	    Include pseudo, duplicate, and inaccessible filesystems.
//
//	-t, --type=TYPE
//	    Limit the listing to filesystems of TYPE. May be repeated.
//
//	-x, --exclude-type=TYPE
//	    Exclude filesystems of TYPE. May be repeated. If TYPE is named
//	    by both -t and -x, exclusion wins.
//
//	-l, --local
//	    Limit the listing to local filesystems.
//
//	--total
//	    Append a grand-total row.
//
//	--no-sync
//	    Accepted as a no-op (this is the default behaviour).
//
//	--help
//	    Print usage to stdout and exit 0.
//
// Rejected flags (intentionally not registered, rejected as unknown by
// pflag with exit 1):
//
//	--sync   — invokes sync(2), modifying kernel buffer state. Violates
//	           the "no system state mutation" rule.
//	-B/--block-size, --output, [FILE]…   — deferred to a later version.
//	-v, --version                        — not meaningful in this shell.
//
// Exit codes:
//
//	0  Success — listing was written.
//	1  Error — unsupported platform, unknown flag, extra operand, or
//	   failure to enumerate the mount table.
package df

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/diskstats"
)

// Cmd is the df builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "df",
	Description: "report file system disk space usage",
	MakeFlags:   makeFlags,
}

// unitMode controls how byte counts are formatted in block columns.
type unitMode int

const (
	unitsK         unitMode = iota // 1024-byte blocks (POSIX default)
	unitsHuman1024                 // -h: powers of 1024
	unitsHuman1000                 // -H: powers of 1000
)

// flags carries the parsed flag state. It is constructed once per
// invocation by makeFlags and consumed by the bound handler.
type flags struct {
	help         *bool
	human        *bool
	si           *bool
	posix        *bool
	printType    *bool
	inodes       *bool
	all          *bool
	local        *bool
	total        *bool
	noSync       *bool
	includeTypes *[]string
	excludeTypes *[]string
}

func makeFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	f := &flags{
		help:         fs.Bool("help", false, "print usage and exit"),
		human:        fs.BoolP("human-readable", "h", false, "print sizes in powers of 1024 (e.g. 1023M)"),
		si:           fs.BoolP("si", "H", false, "print sizes in powers of 1000 (e.g. 1.1G)"),
		posix:        fs.BoolP("portability", "P", false, "use the POSIX output format"),
		printType:    fs.BoolP("print-type", "T", false, "print file system type"),
		inodes:       fs.BoolP("inodes", "i", false, "list inode information instead of block usage"),
		all:          fs.BoolP("all", "a", false, "include pseudo, duplicate, inaccessible file systems"),
		local:        fs.BoolP("local", "l", false, "limit listing to local file systems"),
		total:        fs.Bool("total", false, "append a grand total row"),
		noSync:       fs.Bool("no-sync", false, "do not invoke sync before getting usage info (default; accepted for compatibility)"),
		includeTypes: fs.StringArrayP("type", "t", nil, "limit listing to file systems of type TYPE"),
		excludeTypes: fs.StringArrayP("exclude-type", "x", nil, "limit listing to file systems not of type TYPE"),
	}
	// -k is registered separately because it has no long form. It is a
	// no-op in this v1 implementation — 1024-byte blocks are already
	// the default — but POSIX scripts pass it explicitly.
	fs.BoolP("kibibytes", "k", false, "use 1024-byte blocks (POSIX default)")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *f.help {
			printHelp(callCtx, fs)
			return builtins.Result{}
		}

		if len(args) > 0 {
			callCtx.Errf("df: extra operand '%s'\n", args[0])
			callCtx.Errf("Try 'df --help' for more information.\n")
			return builtins.Result{Code: 1}
		}

		mounts, err := diskstats.List(ctx)
		switch {
		case errors.Is(err, diskstats.ErrMaxMounts):
			// Non-fatal: print what we have, warn on stderr.
			callCtx.Errf("df: warning: mount table truncated at %d entries\n", diskstats.MaxMounts)
		case errors.Is(err, diskstats.ErrNotSupported):
			callCtx.Errf("df: not supported on this platform\n")
			return builtins.Result{Code: 1}
		case err != nil:
			callCtx.Errf("df: %v\n", err)
			return builtins.Result{Code: 1}
		}

		// Capture whether any explicit type filter was given so we can
		// distinguish "filters left no rows" (a usage error per GNU df)
		// from "no mounts at all" (still success).
		filterRequested := len(*f.includeTypes) > 0 || len(*f.excludeTypes) > 0

		mounts = filterMounts(mounts, f)
		sort.Slice(mounts, func(i, j int) bool {
			return mounts[i].MountPoint < mounts[j].MountPoint
		})

		if err := ctx.Err(); err != nil {
			return builtins.Result{Code: 1}
		}

		// GNU df: if -t/-x leaves no rows, exit 1 with a stderr
		// message. Scripts use this exit status to test filesystem
		// presence, so silently exiting 0 would be a regression.
		if filterRequested && len(mounts) == 0 {
			callCtx.Errf("df: no file systems processed\n")
			return builtins.Result{Code: 1}
		}

		mode := resolveUnitMode(f)
		writeOutput(callCtx, mounts, f, mode)
		return builtins.Result{}
	}
}

// resolveUnitMode picks the unit mode based on flag presence. -h beats -H
// beats -k (1024 is the implicit default). -i is orthogonal: it swaps the
// columns from blocks to inodes but the unit mode still applies to the
// inode counts (kept as raw numbers in non-human mode, formatted in
// human mode).
func resolveUnitMode(f *flags) unitMode {
	if *f.human {
		return unitsHuman1024
	}
	if *f.si {
		return unitsHuman1000
	}
	return unitsK
}

// filterMounts applies the -a / -l / -t / -x flags. The order of
// operations is:
//  1. -x removes the given types (always wins over everything else).
//  2. -t restricts to the given types if set; an explicit -t match
//     overrides the default pseudo-FS suppression so e.g. `df -t tmpfs`
//     lists tmpfs mounts even without -a (matching GNU df).
//  3. Otherwise -a includes everything; without -a, pseudo filesystems
//     are dropped.
//  4. -l drops remote (non-local) filesystems.
//
// The result reuses the input slice's backing array; the caller must
// not retain the original slice after this call. diskstats.List always
// returns a fresh slice, so this is safe in the current call sites.
func filterMounts(mounts []diskstats.Mount, f *flags) []diskstats.Mount {
	includeSet := stringSet(*f.includeTypes)
	excludeSet := stringSet(*f.excludeTypes)
	out := mounts[:0]
	for _, m := range mounts {
		if _, ok := excludeSet[m.FSType]; ok {
			continue
		}
		if len(includeSet) > 0 {
			if _, ok := includeSet[m.FSType]; !ok {
				continue
			}
		} else if !*f.all && m.Pseudo {
			continue
		}
		if *f.local && !m.Local {
			continue
		}
		out = append(out, m)
	}
	return out
}

func stringSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	s := make(map[string]struct{}, len(values))
	for _, v := range values {
		// Allow comma-separated lists, matching GNU df.
		for p := range strings.SplitSeq(v, ",") {
			if p == "" {
				continue
			}
			s[p] = struct{}{}
		}
	}
	return s
}

// row holds the formatted column values for a single mount and is shared
// by the printer and the totals accumulator.
type row struct {
	source     string
	fstype     string
	col1       string
	col2       string
	col3       string
	capacity   string
	mountpoint string
}

// writeOutput formats and prints the mount table. The columns depend on
// -P (POSIX) and -i (inodes); -T inserts an FS type column after the
// source.
func writeOutput(callCtx *builtins.CallContext, mounts []diskstats.Mount, f *flags, mode unitMode) {
	posix := *f.posix
	withType := *f.printType
	inodeMode := *f.inodes

	header := buildHeader(posix, withType, inodeMode, mode)

	var totalT, totalU, totalA uint64
	rows := make([]row, 0, len(mounts))
	for _, m := range mounts {
		t, u, a := selectColumns(m, inodeMode)
		// Totals use the raw numbers, not the formatted strings, so
		// human-mode rounding does not propagate into the grand total.
		totalT = saturatingAdd(totalT, t)
		totalU = saturatingAdd(totalU, u)
		totalA = saturatingAdd(totalA, a)
		rows = append(rows, row{
			source:     m.Source,
			fstype:     m.FSType,
			col1:       formatCount(t, mode, inodeMode),
			col2:       formatCount(u, mode, inodeMode),
			col3:       formatCount(a, mode, inodeMode),
			capacity:   percentUsed(u, a),
			mountpoint: m.MountPoint,
		})
	}

	if *f.total {
		rows = append(rows, row{
			source:     "total",
			fstype:     "-",
			col1:       formatCount(totalT, mode, inodeMode),
			col2:       formatCount(totalU, mode, inodeMode),
			col3:       formatCount(totalA, mode, inodeMode),
			capacity:   percentUsed(totalU, totalA),
			mountpoint: "-",
		})
	}

	printRows(callCtx, header, rows, posix, withType)
}

// selectColumns returns the (total, used, available) values that go into
// columns 1/2/3 of the listing. In inode mode they are inode counts; in
// block mode they are byte counts.
func selectColumns(m diskstats.Mount, inodeMode bool) (uint64, uint64, uint64) {
	if inodeMode {
		return m.Inodes, m.InodesUsed, m.InodesFree
	}
	return m.Total, m.Used, m.Free
}

// percentUsed renders the "Capacity" column.
//
// Edge cases:
//   - used + free == 0 → "-" (matches GNU df for empty pseudo filesystems)
//   - rounds up so any non-zero usage shows ≥1%.
//
// Right-shifts numerator and denominator together until `used * 100` fits
// in a uint64. Halving both sides identically preserves whole-percent
// answers. Ceiling is computed as floor-plus-remainder-bump (rather than
// `(num + denom - 1) / denom`) because num can itself sit near MaxUint64.
func percentUsed(used, available uint64) string {
	denom := saturatingAdd(used, available)
	if denom == 0 {
		return "-"
	}
	for used > (^uint64(0))/100 {
		used >>= 1
		denom >>= 1
	}
	num := used * 100
	pct := num / denom
	if num%denom != 0 {
		pct++
	}
	return strconv.FormatUint(pct, 10) + "%"
}

// saturatingAdd returns a + b, clamped to uint64 max on overflow. Used
// for total-row accumulation so a rogue oversized mount cannot wrap the
// running totals to zero.
func saturatingAdd(a, b uint64) uint64 {
	if a > ^uint64(0)-b {
		return ^uint64(0)
	}
	return a + b
}

// formatCount renders a numeric column.
//
// In inode mode the value is an inode count (unit-less). When -h or -H
// is also set, GNU df scales inode counts through the same K/M/G suffix
// machinery, so `df -ih` emits e.g. "4.0M" rather than "4194304". In
// non-human inode mode, the raw integer is printed.
//
// In block mode, unitsK renders the byte count divided by 1024 (1K
// blocks); the human modes call humanBytes.
func formatCount(v uint64, mode unitMode, inodeMode bool) string {
	if inodeMode {
		switch mode {
		case unitsHuman1024:
			return humanBytes(v, 1024)
		case unitsHuman1000:
			return humanBytes(v, 1000)
		}
		return strconv.FormatUint(v, 10)
	}
	switch mode {
	case unitsHuman1024:
		return humanBytes(v, 1024)
	case unitsHuman1000:
		return humanBytes(v, 1000)
	}
	// 1K blocks: round up so a non-zero value never shows as 0. Use
	// floor + remainder bump to avoid wraparound when v is near
	// MaxUint64 (totals saturate to that on overflow).
	q := v / 1024
	if v%1024 != 0 {
		q++
	}
	return strconv.FormatUint(q, 10)
}

// humanBytes formats a byte count as a power-of-base human-readable
// string. base is 1024 for -h or 1000 for -H. Output style matches GNU
// df: one decimal digit when the integer part is < 10, no decimal
// otherwise. Suffixes go up to E (exa); larger sizes are clamped at "E"
// to avoid overflow.
//
// GNU df rounds *up* on every non-integer remainder so that "Used"
// never under-reports. We mirror that with math.Ceil after scaling
// rather than fmt.Sprintf's round-to-nearest. Example: 1,576,960 bytes
// is "1.6M", not "1.5M".
func humanBytes(v uint64, base uint64) string {
	const suffixes = "KMGTPE"
	if v < base {
		return strconv.FormatUint(v, 10)
	}
	// Walk through suffix levels until v fits in 4 digits.
	val := float64(v)
	div := float64(base)
	suffix := byte('K')
	for i := range len(suffixes) {
		if val < div*float64(base) {
			suffix = suffixes[i]
			break
		}
		div *= float64(base)
		suffix = suffixes[len(suffixes)-1]
	}
	scaled := val / div
	if scaled < 10 {
		// One decimal digit, rounded up.
		ceiled := math.Ceil(scaled*10) / 10
		return fmt.Sprintf("%.1f%c", ceiled, suffix)
	}
	// No decimal digit, rounded up.
	return fmt.Sprintf("%.0f%c", math.Ceil(scaled), suffix)
}

// buildHeader returns the column header strings.
func buildHeader(posix, withType, inodeMode bool, mode unitMode) []string {
	first := "Filesystem"
	last := "Mounted on"

	capacity := "Use%"
	if posix {
		capacity = "Capacity"
	}

	if inodeMode {
		cols := []string{first}
		if withType {
			cols = append(cols, "Type")
		}
		cols = append(cols, "Inodes", "IUsed", "IFree", "IUse%", last)
		if posix {
			// POSIX output format for inodes still uses "Capacity".
			cols[len(cols)-2] = "Capacity"
		}
		return cols
	}

	// Size column header. -h / -H always show "Size" (the values are
	// human-suffixed), even when -P is also given — matching GNU df
	// output. The fixed-block POSIX header only applies when the unit
	// mode is itself fixed-block.
	var col1 string
	switch {
	case mode == unitsHuman1024 || mode == unitsHuman1000:
		col1 = "Size"
	case posix:
		col1 = "1024-blocks"
	default:
		col1 = "1K-blocks"
	}

	cols := []string{first}
	if withType {
		cols = append(cols, "Type")
	}
	cols = append(cols, col1, "Used", "Available", capacity, last)
	return cols
}

// printRows emits the header row and each data row.
//
// POSIX format (-P): single-space-separated, no padding beyond a single
// space between fields, with the header printed verbatim.
//
// Default format: hand-aligned. Each column's width is the max of its
// header and the longest data row, capped at a sane upper bound.
func printRows(callCtx *builtins.CallContext, header []string, rows []row, posix, withType bool) {
	if posix {
		callCtx.Out(strings.Join(header, " ") + "\n")
		for _, r := range rows {
			fields := []string{r.source}
			if withType {
				fields = append(fields, r.fstype)
			}
			fields = append(fields, r.col1, r.col2, r.col3, r.capacity, r.mountpoint)
			callCtx.Out(strings.Join(fields, " ") + "\n")
		}
		return
	}

	// Build a 2D table for column-width computation. The header is
	// always present, so len(table) is never zero.
	table := make([][]string, 0, len(rows)+1)
	table = append(table, header)
	for _, r := range rows {
		fields := []string{r.source}
		if withType {
			fields = append(fields, r.fstype)
		}
		fields = append(fields, r.col1, r.col2, r.col3, r.capacity, r.mountpoint)
		table = append(table, fields)
	}

	widths := make([]int, len(table[0]))
	for _, row := range table {
		for i, cell := range row {
			widths[i] = max(widths[i], len(cell))
		}
	}

	// Filesystem (left-aligned) and Mounted on (left-aligned, no
	// trailing pad) frame the row; everything between is right-aligned.
	last := len(widths) - 1
	for _, row := range table {
		var b strings.Builder
		for i, cell := range row {
			if i > 0 {
				b.WriteByte(' ')
			}
			pad := widths[i] - len(cell)
			switch i {
			case 0:
				b.WriteString(cell)
				b.WriteString(strings.Repeat(" ", pad))
			case last:
				b.WriteString(cell)
			default:
				b.WriteString(strings.Repeat(" ", pad))
				b.WriteString(cell)
			}
		}
		b.WriteByte('\n')
		callCtx.Out(b.String())
	}
}

// printHelp emits the help text to stdout (per RULES.md, help is not an
// error; exit 0 with output on stdout).
func printHelp(callCtx *builtins.CallContext, fs *builtins.FlagSet) {
	callCtx.Out("Usage: df [OPTION]...\n")
	callCtx.Out("Show information about the file system on which each FILE resides,\n")
	callCtx.Out("or all file systems by default.\n\n")
	fs.SetOutput(callCtx.Stdout)
	fs.PrintDefaults()
}
