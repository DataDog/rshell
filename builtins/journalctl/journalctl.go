// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package journalctl implements a bounded systemd journal query builtin.
//
// Usage: journalctl (-u UNIT...|-k) [OPTION]...
//
// This is a deliberately restricted journalctl subset. Every log query must
// select one or more exact system units, or the current boot's kernel log.
// Arbitrary field matches, unrestricted journal reads, follow mode, alternate
// files/directories, cursors, and machine/namespace selection are not exposed.
//
// Accepted flags:
//
//	-u, --unit=UNIT       select an exact system unit; may be repeated
//	-k, --dmesg           select kernel messages from the current target boot
//	-b, --boot            restrict a unit query to the current target boot
//	-n, --lines=COUNT     show at most COUNT entries (default 100, maximum 1000)
//	-S, --since=TIME      show entries since an RFC3339 timestamp, local
//	                      YYYY-MM-DD HH:MM:SS timestamp, or lookback duration
//	-o, --output=FORMAT   output format: short (default) or cat
//	--disk-usage          show allocated journal storage and exit
//	--rotate              archive active journal files before returning
//	--vacuum-size=SIZE    remove oldest archives toward allocated SIZE
//	--vacuum-time=AGE     remove archives older than Go duration AGE
//	--dry-run             report cleanup without deleting archives
//	-h, --help            print usage and exit
package journalctl

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/sizeparse"
)

const (
	shortTime      = "Jan 02 15:04:05"
	localSinceTime = "2006-01-02 15:04:05"
)

// Cmd is the journalctl builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "journalctl",
	Description: "query and maintain bounded systemd journals",
	MakeFlags:   makeFlags,
}

type flags struct {
	units      *[]string
	kernel     *bool
	boot       *bool
	lines      *string
	since      *string
	output     *string
	usage      *bool
	rotate     *bool
	vacuumSize *string
	vacuumTime *string
	dryRun     *bool
	help       *bool
}

func makeFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	options := flags{
		units:      fs.StringArrayP("unit", "u", nil, "show logs for exact UNIT (repeatable)"),
		kernel:     fs.BoolP("dmesg", "k", false, "show current-boot kernel messages"),
		boot:       fs.BoolP("boot", "b", false, "show messages from the current boot"),
		lines:      fs.StringP("lines", "n", "100", "show at most COUNT entries (maximum 1000)"),
		since:      fs.StringP("since", "S", "", "show entries newer than TIME or lookback duration"),
		output:     fs.StringP("output", "o", "short", "output format: short or cat"),
		usage:      fs.Bool("disk-usage", false, "show allocated journal storage and exit"),
		rotate:     fs.Bool("rotate", false, "archive active journal files and wait for completion"),
		vacuumSize: fs.String("vacuum-size", "", "remove oldest archives toward allocated SIZE"),
		vacuumTime: fs.String("vacuum-time", "", "remove archives older than Go duration AGE"),
		dryRun:     fs.Bool("dry-run", false, "report cleanup without deleting archives"),
		help:       fs.BoolP("help", "h", false, "print usage and exit"),
	}
	return options.run(fs)
}

func (options flags) run(fs *builtins.FlagSet) builtins.HandlerFunc {
	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *options.help {
			callCtx.Out("Usage: journalctl (-u UNIT...|-k) [OPTION]...\n")
			callCtx.Out("Show a bounded selection of logs from the configured systemd target.\n")
			callCtx.Out("Queries require exact unit scopes or the current-boot kernel scope.\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}
		if len(args) > 0 {
			callCtx.Errf("journalctl: unexpected argument %q; arbitrary journal matches are not supported\n", args[0])
			return builtins.Result{Code: 1}
		}
		if *options.usage {
			return runDiskUsage(ctx, callCtx, fs)
		}
		if *options.rotate || fs.Changed("vacuum-size") || fs.Changed("vacuum-time") || *options.dryRun {
			return options.runMaintenance(ctx, callCtx, fs)
		}
		if len(*options.units) > builtins.MaxJournalQueryUnits {
			callCtx.Errf("journalctl: too many unit scopes (maximum %d)\n", builtins.MaxJournalQueryUnits)
			return builtins.Result{Code: 1}
		}

		units := uniqueUnits(*options.units)
		if *options.kernel && len(units) > 0 {
			callCtx.Errf("journalctl: --dmesg cannot be combined with --unit\n")
			return builtins.Result{Code: 1}
		}
		if !*options.kernel && len(units) == 0 {
			callCtx.Errf("journalctl: an exact --unit or --dmesg scope is required\n")
			return builtins.Result{Code: 1}
		}

		lines, ok := parseLineCount(*options.lines)
		if !ok {
			callCtx.Errf("journalctl: invalid line count %q (must be between 0 and %d)\n", *options.lines, builtins.MaxJournalQueryEntries)
			return builtins.Result{Code: 1}
		}
		if *options.output != "short" && *options.output != "cat" {
			callCtx.Errf("journalctl: unsupported output format %q (supported: short, cat)\n", *options.output)
			return builtins.Result{Code: 1}
		}

		var since time.Time
		if fs.Changed("since") {
			if since, ok = parseSince(*options.since, callCtx.Now); !ok {
				callCtx.Errf("journalctl: invalid --since value %q; use RFC3339, YYYY-MM-DD HH:MM:SS, or a lookback duration such as 15m\n", *options.since)
				return builtins.Result{Code: 1}
			}
		}

		operations := make([]builtins.SystemdOperation, 0, len(units)+1)
		if *options.kernel {
			operations = append(operations, builtins.SystemdOperation{
				Resource: builtins.SystemdResourceJournalKernel,
				Action:   builtins.SystemdActionRead,
			})
		} else {
			for _, unit := range units {
				operations = append(operations, builtins.SystemdOperation{
					Resource: builtins.SystemdUnitResource(unit),
					Action:   builtins.SystemdActionRead,
				})
			}
		}
		if callCtx.AuthorizeSystemd == nil {
			callCtx.Errf("journalctl: systemd authorization capability is not available\n")
			return builtins.Result{Code: 1}
		}
		if err := callCtx.AuthorizeSystemd(operations...); err != nil {
			callCtx.Errf("journalctl: %s\n", err)
			return builtins.Result{Code: 1}
		}
		if callCtx.Systemd == nil || callCtx.Systemd.Journal == nil {
			callCtx.Errf("journalctl: systemd journal capability is not available\n")
			return builtins.Result{Code: 1}
		}

		query := builtins.JournalQuery{
			Units:       units,
			Kernel:      *options.kernel,
			CurrentBoot: *options.kernel || *options.boot,
			Since:       since,
			MaxEntries:  lines,
		}
		err := callCtx.Systemd.Journal.ReadJournal(ctx, query, func(entry builtins.JournalEntry) error {
			return writeEntry(callCtx.Stdout, entry, *options.output)
		})
		if err == nil || builtins.IsBrokenPipe(err) {
			return builtins.Result{}
		}
		if ctx.Err() == nil {
			callCtx.Errf("journalctl: %s\n", err)
		}
		return builtins.Result{Code: 1}
	}
}

func runDiskUsage(ctx context.Context, callCtx *builtins.CallContext, fs *builtins.FlagSet) builtins.Result {
	for _, flagName := range []string{"unit", "dmesg", "boot", "lines", "since", "output", "rotate", "vacuum-size", "vacuum-time", "dry-run"} {
		if fs.Changed(flagName) {
			callCtx.Errf("journalctl: --disk-usage cannot be combined with --%s\n", flagName)
			return builtins.Result{Code: 1}
		}
	}
	if callCtx.AuthorizeSystemd == nil {
		callCtx.Errf("journalctl: systemd authorization capability is not available\n")
		return builtins.Result{Code: 1}
	}
	err := callCtx.AuthorizeSystemd(builtins.SystemdOperation{
		Resource: builtins.SystemdResourceJournalStorage,
		Action:   builtins.SystemdActionRead,
	})
	if err != nil {
		callCtx.Errf("journalctl: %s\n", err)
		return builtins.Result{Code: 1}
	}
	if callCtx.Systemd == nil || callCtx.Systemd.JournalStorage == nil {
		callCtx.Errf("journalctl: systemd journal storage capability is not available\n")
		return builtins.Result{Code: 1}
	}
	usage, err := callCtx.Systemd.JournalStorage.JournalDiskUsage(ctx)
	if err != nil {
		if ctx.Err() == nil {
			callCtx.Errf("journalctl: %s\n", err)
		}
		return builtins.Result{Code: 1}
	}
	callCtx.Outf("Archived and active journals take up %s in the file system.\n", formatUsage(usage.Bytes))
	return builtins.Result{}
}

func (options flags) runMaintenance(ctx context.Context, callCtx *builtins.CallContext, fs *builtins.FlagSet) builtins.Result {
	for _, flagName := range []string{"unit", "dmesg", "boot", "lines", "since", "output", "disk-usage"} {
		if fs.Changed(flagName) {
			callCtx.Errf("journalctl: journal maintenance cannot be combined with --%s\n", flagName)
			return builtins.Result{Code: 1}
		}
	}
	sizeSet := fs.Changed("vacuum-size")
	timeSet := fs.Changed("vacuum-time")
	vacuumSet := sizeSet || timeSet
	if *options.dryRun && *options.rotate {
		callCtx.Errf("journalctl: --dry-run cannot be combined with --rotate\n")
		return builtins.Result{Code: 1}
	}
	if *options.dryRun && !vacuumSet {
		callCtx.Errf("journalctl: --dry-run requires --vacuum-size or --vacuum-time\n")
		return builtins.Result{Code: 1}
	}
	if vacuumSet && callCtx.Now.IsZero() {
		callCtx.Errf("journalctl: journal vacuum requires a runner reference time\n")
		return builtins.Result{Code: 1}
	}

	request := builtins.JournalVacuumRequest{}
	if vacuumSet {
		request.Now = callCtx.Now
		request.DryRun = *options.dryRun
	}
	if sizeSet {
		size, err := sizeparse.Parse(*options.vacuumSize)
		if err != nil || size <= 0 {
			callCtx.Errf("journalctl: invalid --vacuum-size value %q (must be greater than zero)\n", *options.vacuumSize)
			return builtins.Result{Code: 1}
		}
		request.MaxBytes = uint64(size)
	}
	if timeSet {
		age, err := time.ParseDuration(*options.vacuumTime)
		if err != nil || age <= 0 {
			callCtx.Errf("journalctl: invalid --vacuum-time value %q (use a positive Go duration such as 168h)\n", *options.vacuumTime)
			return builtins.Result{Code: 1}
		}
		request.Before = callCtx.Now.Add(-age)
	}

	if callCtx.AuthorizeSystemd == nil {
		callCtx.Errf("journalctl: systemd authorization capability is not available\n")
		return builtins.Result{Code: 1}
	}
	err := callCtx.AuthorizeSystemd(builtins.SystemdOperation{
		Resource: builtins.SystemdResourceJournalStorage,
		Action:   builtins.SystemdActionClean,
	})
	if err != nil {
		callCtx.Errf("journalctl: %s\n", err)
		return builtins.Result{Code: 1}
	}
	if *options.rotate && (callCtx.Systemd == nil || callCtx.Systemd.JournalRotator == nil) {
		callCtx.Errf("journalctl: systemd journal rotation capability is not available\n")
		return builtins.Result{Code: 1}
	}
	if vacuumSet && (callCtx.Systemd == nil || callCtx.Systemd.JournalCleaner == nil) {
		callCtx.Errf("journalctl: systemd journal cleaner capability is not available\n")
		return builtins.Result{Code: 1}
	}
	if *options.rotate {
		if err := callCtx.Systemd.JournalRotator.RotateJournal(ctx); err != nil {
			if ctx.Err() == nil {
				callCtx.Errf("journalctl: %s\n", err)
			}
			return builtins.Result{Code: 1}
		}
		callCtx.Out("Journal rotation completed.\n")
	}
	if !vacuumSet {
		return builtins.Result{}
	}
	result, err := callCtx.Systemd.JournalCleaner.VacuumJournal(ctx, request)
	if err != nil {
		if ctx.Err() == nil {
			callCtx.Errf("journalctl: %s\n", err)
		}
		return builtins.Result{Code: 1}
	}
	verb := "done, freed"
	if request.DryRun {
		verb = "would free"
	}
	noun := "files"
	if result.Files == 1 {
		noun = "file"
	}
	callCtx.Outf("Vacuuming %s %s from %d archived journal %s.\n", verb, formatUsage(result.Bytes), result.Files, noun)
	return builtins.Result{}
}

func formatUsage(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%dB", bytes)
	}
	value := float64(bytes)
	for _, suffix := range []string{"K", "M", "G", "T", "P", "E"} {
		value /= unit
		if value < unit || suffix == "E" {
			return fmt.Sprintf("%.1f%s", value, suffix)
		}
	}
	return fmt.Sprintf("%dB", bytes)
}

func uniqueUnits(units []string) []string {
	unique := make([]string, 0, len(units))
	seen := make(map[string]struct{}, len(units))
	for _, unit := range units {
		if _, exists := seen[unit]; exists {
			continue
		}
		seen[unit] = struct{}{}
		unique = append(unique, unit)
	}
	return unique
}

func parseLineCount(value string) (int, bool) {
	if value == "" || value[0] == '+' {
		return 0, false
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed < 0 || parsed > builtins.MaxJournalQueryEntries {
		return 0, false
	}
	return int(parsed), true
}

func parseSince(value string, now time.Time) (time.Time, bool) {
	if value == "" {
		return time.Time{}, false
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed, true
	}
	if !now.IsZero() {
		if parsed, err := time.ParseInLocation(localSinceTime, value, now.Location()); err == nil {
			return parsed, true
		}
		if lookback, err := time.ParseDuration(value); err == nil && lookback >= 0 {
			return now.Add(-lookback), true
		}
	}
	return time.Time{}, false
}

func writeEntry(writer io.Writer, entry builtins.JournalEntry, output string) error {
	var line strings.Builder
	if output == "short" {
		line.WriteString(entry.Timestamp.Format(shortTime))
		line.WriteByte(' ')
		if entry.Hostname == "" {
			line.WriteByte('-')
		} else {
			appendEscaped(&line, entry.Hostname)
		}
		line.WriteByte(' ')
		if entry.Identifier == "" {
			line.WriteString("journal")
		} else {
			appendEscaped(&line, entry.Identifier)
		}
		if entry.PID != "" {
			line.WriteByte('[')
			appendEscaped(&line, entry.PID)
			line.WriteByte(']')
		}
		line.WriteString(": ")
	}
	appendEscaped(&line, entry.Message)
	line.WriteByte('\n')
	_, err := io.WriteString(writer, line.String())
	return err
}

func appendEscaped(output *strings.Builder, value string) {
	for value != "" {
		r, size := utf8.DecodeRuneInString(value)
		if r == utf8.RuneError && size == 1 {
			appendHexEscape(output, 'x', uint32(value[0]), 2)
			value = value[1:]
			continue
		}
		switch r {
		case '\n':
			output.WriteString("\\n")
		case '\r':
			output.WriteString("\\r")
		case '\t':
			output.WriteString("\\t")
		default:
			if !unicode.IsGraphic(r) {
				if r <= 0xff {
					appendHexEscape(output, 'x', uint32(r), 2)
				} else if r <= 0xffff {
					appendHexEscape(output, 'u', uint32(r), 4)
				} else {
					appendHexEscape(output, 'U', uint32(r), 8)
				}
			} else {
				output.WriteString(value[:size])
			}
		}
		value = value[size:]
	}
}

func appendHexEscape(output *strings.Builder, prefix byte, value uint32, width int) {
	const hex = "0123456789abcdef"
	output.WriteByte('\\')
	output.WriteByte(prefix)
	for shift := (width - 1) * 4; shift >= 0; shift -= 4 {
		output.WriteByte(hex[(value>>shift)&0x0f])
	}
}
