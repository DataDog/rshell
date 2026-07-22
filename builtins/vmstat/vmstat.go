// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package vmstat implements the vmstat builtin command.
//
// vmstat — report virtual memory, swap, IO, and CPU pressure statistics
//
// Usage: vmstat [OPTION]... [delay [count]]
//
// With no arguments, prints a single snapshot averaged since boot. With
// delay (whole seconds), samples repeatedly, printing a since-boot average
// as the first row and true deltas between samples thereafter; count
// limits the number of rows printed (default: run until the shell's
// execution timeout or the caller interrupts the command).
//
// Counter collection is delegated to the internal vmstat package, which
// reads /proc/{stat,meminfo,vmstat,loadavg} on Linux (exempt from the
// AllowedPaths sandbox — same documented exception used by df, ss, and
// ip route: the paths are hardcoded and never derived from user input)
// and sysctl(3) on macOS (hw.memsize, vm.swapusage, vm.loadavg — the same
// darwin toolset already used by df and ss). See
// builtins/internal/vmstat's package doc for the full platform-coverage
// rationale.
//
// # Platform limitations
//
// macOS has no sysctl exposing per-page memory breakdown (buffers/cache/
// active/inactive beyond totals), CPU tick counters, or paging/interrupt/
// context-switch counters without a Mach host_statistics64 call, which
// this implementation intentionally does not make (see the internal
// package doc). On macOS those columns print as "-" when the whole
// counter group is unavailable (procs/swap-rate/io/system/cpu); memory
// and swap totals print normally.
//
// Accepted flags:
//
//	-a, --active
//	    Display active/inactive memory instead of buffers/cache.
//
//	-w, --wide
//	    Use wider field widths (avoids truncating large numbers).
//
//	-S, --unit=k|K|m|M
//	    Scale memory columns: k=1000 bytes, K=1024 (default), m=1e6,
//	    M=2^20.
//
//	-s, --stats
//	    Print a full set of event counters, one label per line, instead
//	    of the column report. Ignores delay/count.
//
//	-h, --help
//	    Print usage to stdout and exit 0.
//
// Rejected flags (intentionally not registered, rejected as unknown by
// pflag with exit 1):
//
//	-d, -p   — disk/partition statistics; not implemented in v1.
//	-f       — fork counts since boot; not implemented in v1.
//	-m       — slab info; not implemented in v1.
//	-t       — timestamp column; not implemented in v1.
//	-n       — single-header mode; not meaningful (every invocation is
//	           already a single process).
//	-V, --version — not meaningful in this shell.
//
// Exit codes:
//
//	0  Success — a report was written.
//	1  Error — unsupported platform, unknown flag, extra operand, or
//	   failure to read counters.
package vmstat

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/procpath"
	ivmstat "github.com/DataDog/rshell/builtins/internal/vmstat"
)

// Cmd is the vmstat builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "vmstat",
	Description: "report virtual memory, swap, IO, and CPU pressure statistics",
	MakeFlags:   makeFlags,
}

// ProcPath is the proc filesystem root passed to ivmstat.Read. It is a
// package-level variable so tests can point it at a synthetic directory
// instead of the real /proc.
//
// Concurrency contract: this variable is written only in tests and is never
// mutated by production code after package initialization. Test code that
// mutates ProcPath must hold a test-package-level mutex for the duration of
// the test to prevent data races between concurrent test goroutines.
var ProcPath = procpath.Default

// flags carries the parsed flag state for one invocation.
type flags struct {
	help   *bool
	active *bool
	wide   *bool
	stats  *bool
	unit   *string
}

func makeFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	f := &flags{
		help:   fs.BoolP("help", "h", false, "print usage and exit"),
		active: fs.BoolP("active", "a", false, "display active and inactive memory instead of buffers and cache"),
		wide:   fs.BoolP("wide", "w", false, "use wider field widths"),
		stats:  fs.BoolP("stats", "s", false, "display a full set of event-counter statistics, one per line"),
		unit:   fs.StringP("unit", "S", "K", "scale memory columns by unit: k|K|m|M (1000|1024|1e6|2^20 bytes)"),
	}

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *f.help {
			printHelp(callCtx, fs)
			return builtins.Result{}
		}

		divisor, err := unitDivisor(*f.unit)
		if err != nil {
			callCtx.Errf("vmstat: %v\n", err)
			callCtx.Errf("Try 'vmstat --help' for more information.\n")
			return builtins.Result{Code: 1}
		}

		// procps vmstat still parses [delay [count]] with -s/--stats (see
		// `vmstat --help`'s "vmstat [options] [delay [count]]" grammar and
		// `vmstat -s 1`, which exits 0), but the stats report ignores them
		// entirely — it never samples more than once. Validate the operands
		// for shape (so a malformed operand is still rejected) without
		// using the resulting delay/count when -s is set.
		delay, count, err := parseSamplingArgs(args)
		if err != nil {
			callCtx.Errf("vmstat: %v\n", err)
			return builtins.Result{Code: 1}
		}

		if *f.stats {
			return runStats(ctx, callCtx, divisor, *f.unit)
		}
		return runSampling(ctx, callCtx, *f.active, *f.wide, divisor, delay, count)
	}
}

// unitDivisor maps the -S argument to a byte divisor.
func unitDivisor(s string) (int64, error) {
	switch s {
	case "k":
		return 1000, nil
	case "K":
		return 1024, nil
	case "m":
		return 1_000_000, nil
	case "M":
		return 1_048_576, nil
	default:
		return 0, fmt.Errorf("invalid unit '%s' (expected k, K, m, or M)", s)
	}
}

// parseSamplingArgs validates the positional [delay [count]] operands.
// delay=0 signals "no sampling requested" (single snapshot); count=0
// signals "unbounded" (sample until ctx is done).
func parseSamplingArgs(args []string) (delay time.Duration, count int, err error) {
	if len(args) == 0 {
		return 0, 1, nil
	}
	if len(args) > 2 {
		return 0, 0, fmt.Errorf("extra operand '%s'", args[2])
	}
	d, convErr := strconv.ParseUint(args[0], 10, 32)
	if convErr != nil || d == 0 {
		return 0, 0, fmt.Errorf("invalid delay '%s'", args[0])
	}
	if len(args) == 2 {
		c, convErr := strconv.ParseUint(args[1], 10, 32)
		if convErr != nil || c == 0 || c > uint64(math.MaxInt) {
			return 0, 0, fmt.Errorf("invalid count '%s'", args[1])
		}
		count = int(c)
	}
	return time.Duration(d) * time.Second, count, nil
}

// runSampling prints the header, an initial since-boot-average row, and
// (when delay > 0) further delta rows spaced delay apart, until count
// rows have been printed (count == 0 means "until ctx is done").
func runSampling(ctx context.Context, callCtx *builtins.CallContext, active, wide bool, divisor int64, delay time.Duration, count int) builtins.Result {
	printHeader(callCtx, active, wide)

	first, err := ivmstat.Read(ctx, ProcPath)
	if err != nil {
		callCtx.Errf("vmstat: %v\n", err)
		return builtins.Result{Code: 1}
	}
	callCtx.Out(formatRow(first, nil, first.Uptime, divisor, active, wide))

	if delay == 0 {
		return builtins.Result{}
	}

	prev := first
	for i := 1; count == 0 || i < count; i++ {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return builtins.Result{Code: 1}
		case <-timer.C:
		}

		cur, err := ivmstat.Read(ctx, ProcPath)
		if err != nil {
			callCtx.Errf("vmstat: %v\n", err)
			return builtins.Result{Code: 1}
		}
		callCtx.Out(formatRow(cur, &prev, delay.Seconds(), divisor, active, wide))
		prev = cur
	}
	return builtins.Result{}
}

// runStats implements -s/--stats: one labeled counter per line. unit is the
// raw -S/--unit argument (k|K|m|M) and labels the memory/swap lines that are
// scaled by divisor, matching procps vmstat's behavior of relabeling those
// lines rather than leaving a stale "K" when a different unit is selected.
func runStats(ctx context.Context, callCtx *builtins.CallContext, divisor int64, unit string) builtins.Result {
	st, err := ivmstat.Read(ctx, ProcPath)
	if err != nil {
		callCtx.Errf("vmstat: %v\n", err)
		return builtins.Result{Code: 1}
	}

	hasMem := st.Partial&ivmstat.FieldMemory != 0
	hasMemDetail := st.Partial&ivmstat.FieldMemoryDetail != 0
	hasUsedMem := hasMem && hasMemDetail
	hasSwap := st.Partial&ivmstat.FieldSwap != 0
	hasProcs := st.Partial&ivmstat.FieldProcs != 0
	hasPaging := st.Partial&ivmstat.FieldPaging != 0
	hasSystem := st.Partial&ivmstat.FieldSystem != 0
	hasCPU := st.Partial&ivmstat.FieldCPU != 0
	hasLoad := st.Partial&ivmstat.FieldLoadAvg != 0

	u := uint64(divisor)
	line := func(ok bool, v uint64, label string) {
		if !ok {
			callCtx.Outf("%12s %s\n", "-", label)
			return
		}
		callCtx.Outf("%12d %s\n", v, label)
	}
	lineFloat := func(ok bool, v float64, label string) {
		if !ok {
			callCtx.Outf("%12s %s\n", "-", label)
			return
		}
		callCtx.Outf("%12.2f %s\n", v, label)
	}

	line(hasMem, st.MemTotal/u, unit+" total memory")
	line(hasUsedMem, subClamp(st.MemTotal, st.MemFree)/u, unit+" used memory")
	line(hasMemDetail, st.MemActive/u, unit+" active memory")
	line(hasMemDetail, st.MemInactive/u, unit+" inactive memory")
	line(hasMemDetail, st.MemFree/u, unit+" free memory")
	line(hasMemDetail, st.MemBuffers/u, unit+" buffer memory")
	line(hasMemDetail, st.MemCached/u, unit+" swap cache")
	line(hasSwap, st.SwapTotal/u, unit+" total swap")
	line(hasSwap, subClamp(st.SwapTotal, st.SwapFree)/u, unit+" used swap")
	line(hasSwap, st.SwapFree/u, unit+" free swap")
	line(hasProcs, st.ProcsRunning, "runnable processes")
	line(hasProcs, st.ProcsBlocked, "processes blocked waiting for I/O")
	line(hasPaging, st.PagesInKB, "K paged in from disk")
	line(hasPaging, st.PagesOutKB, "K paged out to disk")
	line(hasPaging, st.SwapInPages, "pages swapped in")
	line(hasPaging, st.SwapOutPages, "pages swapped out")
	line(hasSystem, st.Interrupts, "interrupts")
	line(hasSystem, st.ContextSwitches, "CPU context switches")
	line(hasCPU, st.CPUUser, "CPU user ticks")
	line(hasCPU, st.CPUNice, "CPU nice ticks")
	line(hasCPU, st.CPUSystem, "CPU system ticks")
	line(hasCPU, st.CPUIdle, "CPU idle ticks")
	line(hasCPU, st.CPUIOWait, "CPU I/O-wait ticks")
	line(hasCPU, st.CPUIRQ, "CPU IRQ ticks")
	line(hasCPU, st.CPUSoftIRQ, "CPU softirq ticks")
	line(hasCPU, st.CPUSteal, "CPU steal ticks")
	lineFloat(hasLoad, st.LoadAvg1, "1 minute load average")
	lineFloat(hasLoad, st.LoadAvg5, "5 minute load average")
	lineFloat(hasLoad, st.LoadAvg15, "15 minute load average")

	return builtins.Result{}
}

// group describes one header/column group (e.g. "memory": swpd, free,
// buff, cache).
type group struct {
	label string
	names []string
	width int
}

func buildGroups(active, wide bool) []group {
	w := func(base int) int {
		if wide {
			return base + 4
		}
		return base
	}
	memNames := []string{"swpd", "free", "buff", "cache"}
	if active {
		memNames = []string{"swpd", "free", "inact", "active"}
	}
	return []group{
		{"procs", []string{"r", "b"}, w(3)},
		{"memory", memNames, w(7)},
		{"swap", []string{"si", "so"}, w(4)},
		{"io", []string{"bi", "bo"}, w(5)},
		{"system", []string{"in", "cs"}, w(4)},
		{"cpu", []string{"us", "sy", "id", "wa", "st"}, w(3)},
	}
}

func printHeader(callCtx *builtins.CallContext, active, wide bool) {
	groups := buildGroups(active, wide)
	l1 := make([]string, 0, len(groups))
	l2 := make([]string, 0, len(groups)*2)
	for _, g := range groups {
		total := g.width*len(g.names) + (len(g.names) - 1)
		l1 = append(l1, groupHeaderCell(g.label, total))
		for _, n := range g.names {
			l2 = append(l2, fmt.Sprintf("%*s", g.width, n))
		}
	}
	callCtx.Out(strings.Join(l1, " ") + "\n")
	callCtx.Out(strings.Join(l2, " ") + "\n")
}

// groupHeaderCell renders one top-level header cell. "procs" has no
// dashes (matches procps: it is not an averaged/rate group); every other
// group is centered within a run of dashes.
func groupHeaderCell(label string, total int) string {
	if label == "procs" {
		if len(label) >= total {
			return label[:total]
		}
		return label + strings.Repeat(" ", total-len(label))
	}
	if len(label) >= total {
		return label[:total]
	}
	pad := total - len(label)
	left := pad / 2
	right := pad - left
	return strings.Repeat("-", left) + label + strings.Repeat("-", right)
}

// formatRow renders one data row. prev == nil means "since-boot average"
// (elapsedSeconds should be cur.Uptime); prev != nil means "delta between
// prev and cur" (elapsedSeconds should be the nominal sample interval).
func formatRow(cur ivmstat.Stats, prev *ivmstat.Stats, elapsedSeconds float64, divisor int64, active, wide bool) string {
	groups := buildGroups(active, wide)
	cells := make([]string, 0, 16)

	// procs: instantaneous counts, not rates.
	w := groups[0].width
	if cur.Partial&ivmstat.FieldProcs != 0 {
		cells = append(cells, fmtUint(cur.ProcsRunning, w), fmtUint(cur.ProcsBlocked, w))
	} else {
		cells = append(cells, dash(w), dash(w))
	}

	// memory: instantaneous sizes, not rates. swpd is backed by the swap
	// field group while the remaining three columns are backed by the
	// memory field group; on macOS the two sysctls can succeed/fail
	// independently, so they are gated separately rather than as one OR'd
	// check (which would render a fabricated "0" for whichever group is
	// actually missing).
	w = groups[1].width
	if cur.Partial&ivmstat.FieldSwap != 0 {
		swpd := subClamp(cur.SwapTotal, cur.SwapFree)
		cells = append(cells, fmtScaled(swpd, divisor, w))
	} else {
		cells = append(cells, dash(w))
	}
	if cur.Partial&ivmstat.FieldMemoryDetail != 0 {
		cells = append(cells, fmtScaled(cur.MemFree, divisor, w))
		if active {
			cells = append(cells, fmtScaled(cur.MemInactive, divisor, w), fmtScaled(cur.MemActive, divisor, w))
		} else {
			cells = append(cells, fmtScaled(cur.MemBuffers, divisor, w), fmtScaled(cur.MemCached, divisor, w))
		}
	} else {
		cells = append(cells, dash(w), dash(w), dash(w))
	}

	// swap: si/so, in KB/sec.
	w = groups[2].width
	if cur.Partial&ivmstat.FieldPaging != 0 && elapsedSeconds > 0 {
		si, so := rateSwap(cur, prev, elapsedSeconds)
		cells = append(cells, fmtUint(si, w), fmtUint(so, w))
	} else {
		cells = append(cells, dash(w), dash(w))
	}

	// io: bi/bo, in KB/sec.
	w = groups[3].width
	if cur.Partial&ivmstat.FieldPaging != 0 && elapsedSeconds > 0 {
		bi, bo := rateIO(cur, prev, elapsedSeconds)
		cells = append(cells, fmtUint(bi, w), fmtUint(bo, w))
	} else {
		cells = append(cells, dash(w), dash(w))
	}

	// system: in/cs, per second.
	w = groups[4].width
	if cur.Partial&ivmstat.FieldSystem != 0 && elapsedSeconds > 0 {
		in, cs := rateSystem(cur, prev, elapsedSeconds)
		cells = append(cells, fmtUint(in, w), fmtUint(cs, w))
	} else {
		cells = append(cells, dash(w), dash(w))
	}

	// cpu: us/sy/id/wa/st, as percentages of ticks elapsed.
	w = groups[5].width
	if cur.Partial&ivmstat.FieldCPU != 0 {
		us, sy, id, wa, st := cpuPercents(cur, prev)
		cells = append(cells, fmtInt(us, w), fmtInt(sy, w), fmtInt(id, w), fmtInt(wa, w), fmtInt(st, w))
	} else {
		cells = append(cells, dash(w), dash(w), dash(w), dash(w), dash(w))
	}

	return strings.Join(cells, " ") + "\n"
}

func fmtUint(v uint64, width int) string {
	return fmt.Sprintf("%*d", width, v)
}

func fmtInt(v int64, width int) string {
	return fmt.Sprintf("%*d", width, v)
}

func fmtScaled(v uint64, divisor int64, width int) string {
	return fmt.Sprintf("%*d", width, v/uint64(divisor))
}

func dash(width int) string {
	return fmt.Sprintf("%*s", width, "-")
}

// subClamp returns a-b, clamped to 0 instead of underflowing when b > a
// (e.g. an adversarial or momentarily-inconsistent kernel counter read).
func subClamp(a, b uint64) uint64 {
	if a >= b {
		return a - b
	}
	return 0
}

// satUint64 converts a non-negative float64 rate to uint64, saturating to
// math.MaxUint64 instead of relying on Go's implementation-defined
// out-of-range float-to-integer conversion (e.g. a huge counter delta over
// a tiny elapsedSeconds).
func satUint64(f float64) uint64 {
	if f <= 0 {
		return 0
	}
	if f >= math.MaxUint64 {
		return math.MaxUint64
	}
	return uint64(f)
}

// rateSwap computes si/so (KB/sec swapped in/out) either since boot
// (prev == nil) or as a delta between two samples.
func rateSwap(cur ivmstat.Stats, prev *ivmstat.Stats, elapsedSeconds float64) (si, so uint64) {
	inPages, outPages := cur.SwapInPages, cur.SwapOutPages
	if prev != nil {
		inPages = subClamp(cur.SwapInPages, prev.SwapInPages)
		outPages = subClamp(cur.SwapOutPages, prev.SwapOutPages)
	}
	kb := cur.PageSize / 1024
	if kb == 0 {
		kb = 1
	}
	// The page-count-to-KB multiplication happens in float64 space (not
	// uint64) so a huge counter delta cannot wrap before the division.
	si = satUint64(float64(inPages) * float64(kb) / elapsedSeconds)
	so = satUint64(float64(outPages) * float64(kb) / elapsedSeconds)
	return si, so
}

// rateIO computes bi/bo (KB/sec paged in/out from/to disk).
func rateIO(cur ivmstat.Stats, prev *ivmstat.Stats, elapsedSeconds float64) (bi, bo uint64) {
	inKB, outKB := cur.PagesInKB, cur.PagesOutKB
	if prev != nil {
		inKB = subClamp(cur.PagesInKB, prev.PagesInKB)
		outKB = subClamp(cur.PagesOutKB, prev.PagesOutKB)
	}
	bi = satUint64(float64(inKB) / elapsedSeconds)
	bo = satUint64(float64(outKB) / elapsedSeconds)
	return bi, bo
}

// rateSystem computes in/cs (interrupts and context switches per second).
func rateSystem(cur ivmstat.Stats, prev *ivmstat.Stats, elapsedSeconds float64) (in, cs uint64) {
	intr, ctxt := cur.Interrupts, cur.ContextSwitches
	if prev != nil {
		intr = subClamp(cur.Interrupts, prev.Interrupts)
		ctxt = subClamp(cur.ContextSwitches, prev.ContextSwitches)
	}
	in = satUint64(float64(intr) / elapsedSeconds)
	cs = satUint64(float64(ctxt) / elapsedSeconds)
	return in, cs
}

// cpuPercents computes us/sy/id/wa/st as percentages of the ticks elapsed
// either since boot (prev == nil) or between two samples. Nice ticks are
// folded into "us" and IRQ/softIRQ ticks are folded into "sy", matching
// procps vmstat's five-column CPU breakdown.
func cpuPercents(cur ivmstat.Stats, prev *ivmstat.Stats) (us, sy, id, wa, st int64) {
	userT := cur.CPUUser + cur.CPUNice
	sysT := cur.CPUSystem + cur.CPUIRQ + cur.CPUSoftIRQ
	idleT := cur.CPUIdle
	waT := cur.CPUIOWait
	stT := cur.CPUSteal
	if prev != nil {
		userT = subClamp(userT, prev.CPUUser+prev.CPUNice)
		sysT = subClamp(sysT, prev.CPUSystem+prev.CPUIRQ+prev.CPUSoftIRQ)
		idleT = subClamp(idleT, prev.CPUIdle)
		waT = subClamp(waT, prev.CPUIOWait)
		stT = subClamp(stT, prev.CPUSteal)
	}
	total := userT + sysT + idleT + waT + stT
	if total == 0 {
		return 0, 0, 100, 0, 0
	}
	us = pct(userT, total)
	sy = pct(sysT, total)
	wa = pct(waT, total)
	st = pct(stT, total)
	id = 100 - us - sy - wa - st
	if id < 0 {
		id = 0
	}
	return us, sy, id, wa, st
}

func pct(v, total uint64) int64 {
	return int64(math.Round(float64(v) * 100 / float64(total)))
}

// printHelp emits the help text to stdout (per RULES.md, help is not an
// error; exit 0 with output on stdout).
func printHelp(callCtx *builtins.CallContext, fs *builtins.FlagSet) {
	callCtx.Out("Usage: vmstat [OPTION]... [delay [count]]\n")
	callCtx.Out("Report virtual memory, swap, IO, and CPU pressure statistics.\n\n")
	callCtx.Out("With no arguments, print one snapshot averaged since boot.\n")
	callCtx.Out("With delay (whole seconds), sample repeatedly; count limits the\n")
	callCtx.Out("number of samples printed (default: until interrupted).\n\n")
	fs.SetOutput(callCtx.Stdout)
	fs.PrintDefaults()
}
