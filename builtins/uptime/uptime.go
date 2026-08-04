// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package uptime implements the uptime builtin command.
//
// uptime — tell how long the system has been running
//
// Usage: uptime [-ps] [--help]
//
// With no options, print the current time, how long the system has been
// running, and the system load averages for the past 1, 5, and 15 minutes.
// User count is intentionally omitted (reading utmp would expose session
// information).
//
// Options:
//
//	-p, --pretty  show uptime in pretty format
//	-s, --since   system up since (YYYY-MM-DD HH:MM:SS)
//	-h, --help    display this help and exit
//
// Data sources (platform-specific, bypass AllowedPaths — see AGENTS.md):
//
//	Linux:   /proc/uptime, /proc/loadavg
//	Darwin:  sysctl kern.boottime, sysctl vm.loadavg
//	Windows: GetTickCount64 (no load average available)
//
// Exit codes:
//
//	0  Success
//	1  Error — platform not supported, kernel read failure, or invalid flag
package uptime

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/sysinfo"
)

// Cmd is the uptime builtin command descriptor using the real system-info provider.
var Cmd = New(sysinfo.Get)

// New returns an uptime Command backed by the given system-info provider.
// Production code uses Cmd (backed by sysinfo.Get); tests pass a fake.
func New(getInfo func() (sysinfo.Info, error)) builtins.Command {
	return builtins.Command{
		Name:        "uptime",
		Description: "tell how long the system has been running",
		MakeFlags: func(fs *builtins.FlagSet) builtins.HandlerFunc {
			return makeFlags(fs, getInfo)
		},
	}
}

func makeFlags(fs *builtins.FlagSet, getInfo func() (sysinfo.Info, error)) builtins.HandlerFunc {
	help := fs.BoolP("help", "h", false, "display this help and exit")
	pretty := fs.BoolP("pretty", "p", false, "show uptime in pretty format")
	since := fs.BoolP("since", "s", false, "system up since, in yyyy-mm-dd HH:MM:SS format")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			callCtx.Outf("Usage: uptime [OPTION]...\n")
			callCtx.Outf("Tell how long the system has been running.\n\n")
			callCtx.Outf("  -p, --pretty  show uptime in pretty format\n")
			callCtx.Outf("  -s, --since   system up since, in yyyy-mm-dd HH:MM:SS format\n")
			callCtx.Outf("  -h, --help    display this help and exit\n")
			return builtins.Result{}
		}

		if len(args) > 0 {
			callCtx.Errf("uptime: extra operand '%s'\n", builtins.SafeOperand(args[0]))
			callCtx.Errf("Try 'uptime --help' for more information.\n")
			return builtins.Result{Code: 1}
		}

		info, err := getInfo()
		if err != nil {
			if err == sysinfo.ErrNotSupported {
				callCtx.Errf("uptime: not supported on %s\n", runtime.GOOS)
			} else {
				callCtx.Errf("uptime: %s\n", err)
			}
			return builtins.Result{Code: 1}
		}

		// -s takes precedence over -p when both are set (matches reference behaviour).
		switch {
		case *since:
			callCtx.Outf("%s\n", time.Unix(info.BootTime, 0).Local().Format("2006-01-02 15:04:05"))
		case *pretty:
			callCtx.Outf("%s\n", formatPretty(info.UptimeSeconds))
		default:
			callCtx.Outf("%s\n", formatDefault(callCtx.Now, info))
		}

		return builtins.Result{}
	}
}

// formatDefault composes the full uptime line:
//
//	HH:MM:SS up <duration>,  load average: 1.23, 1.23, 1.23
//
// The load-average segment is omitted on platforms where it is unavailable.
func formatDefault(now time.Time, info sysinfo.Info) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(" %s up %s", now.Format("15:04:05"), formatDuration(info.UptimeSeconds)))
	if info.LoadAvailable {
		sb.WriteString(fmt.Sprintf(",  load average: %.2f, %.2f, %.2f", info.Load1, info.Load5, info.Load15))
	}
	return sb.String()
}

// formatDuration converts a duration in seconds to the standard uptime
// duration format:
//
//	< 1 hour:  "N min"
//	< 1 day:   "H:MM"
//	= 1 day:   "1 day, H:MM"
//	> 1 day:   "N days, H:MM"
func formatDuration(seconds float64) string {
	total := int64(seconds)
	mins := total / 60
	hours := mins / 60
	days := hours / 24
	remHours := hours % 24
	remMins := mins % 60

	switch {
	case hours == 0:
		return fmt.Sprintf("%d min", mins)
	case days == 0:
		return fmt.Sprintf("%d:%02d", hours, remMins)
	case days == 1:
		return fmt.Sprintf("1 day, %d:%02d", remHours, remMins)
	default:
		return fmt.Sprintf("%d days, %d:%02d", days, remHours, remMins)
	}
}

// formatPretty converts a duration in seconds to the pretty uptime format
// produced by the -p flag.
//
// Shows the highest non-zero unit and the next unit down (if non-zero):
//
//	"up 1 week, 4 days"   (not hours/minutes when weeks are the primary unit)
//	"up 1 day, 2 hours"   (not minutes when days are the primary unit)
//	"up 2 hours, 30 minutes"
//
// Units: decades (10 years), years (365 days), weeks (7 days), days, hours,
// minutes. If the uptime is less than one minute, "up 0 minutes" is returned.
func formatPretty(seconds float64) string {
	total := int64(seconds)
	totalDays := total / 86400
	remSecs := total % 86400
	uphours := int(remSecs / 3600)
	upminutes := int((remSecs % 3600) / 60)

	updecades := int(totalDays / (365 * 10))
	totalDays %= 365 * 10
	upyears := int(totalDays / 365)
	totalDays %= 365
	upweeks := int(totalDays / 7)
	updays := int(totalDays % 7)

	pp := func(n int, unit string) string {
		if n == 1 {
			return "1 " + unit
		}
		return fmt.Sprintf("%d %ss", n, unit)
	}

	var primary, secondary string
	switch {
	case updecades > 0:
		primary = pp(updecades, "decade")
		if upyears > 0 {
			secondary = pp(upyears, "year")
		}
	case upyears > 0:
		primary = pp(upyears, "year")
		if upweeks > 0 {
			secondary = pp(upweeks, "week")
		}
	case upweeks > 0:
		primary = pp(upweeks, "week")
		if updays > 0 {
			secondary = pp(updays, "day")
		}
	case updays > 0:
		primary = pp(updays, "day")
		if uphours > 0 {
			secondary = pp(uphours, "hour")
		}
	case uphours > 0:
		primary = pp(uphours, "hour")
		if upminutes > 0 {
			secondary = pp(upminutes, "minute")
		}
	default:
		primary = pp(upminutes, "minute")
	}

	if secondary != "" {
		return "up " + primary + ", " + secondary
	}
	return "up " + primary
}
